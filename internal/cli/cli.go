package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vigil/internal/event"
)

const (
	defaultServerURL = "http://localhost:8080"
	configEnvKey     = "VIGIL_CONFIG_PATH"
	appEnvBegin      = "# BEGIN VIGIL"
	appEnvEnd        = "# END VIGIL"
)

type Runner struct {
	Out              io.Writer
	Err              io.Writer
	Client           *http.Client
	Getenv           func(string) string
	DefaultServerURL string
}

type Config struct {
	ServerURL         string `json:"server_url"`
	ActiveProjectID   string `json:"active_project_id"`
	ActiveProjectName string `json:"active_project_name"`
	IngestKey         string `json:"ingest_key,omitempty"`
	UpdatedAt         string `json:"updated_at"`
}

type project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type projectCreateResponse struct {
	Project   project `json:"project"`
	IngestKey string  `json:"ingest_key"`
}

type logListResponse struct {
	Events   []event.StoredEvent `json:"events"`
	Page     int                 `json:"page"`
	Limit    int                 `json:"limit"`
	Total    int                 `json:"total"`
	Warnings []resultWarning     `json:"warnings,omitempty"`
}

type resultWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type statsSummaryResponse struct {
	TotalEvents int            `json:"total_events"`
	ByKind      []countByValue `json:"by_kind"`
	ByLevel     []countByValue `json:"by_level"`
	TokenTotal  float64        `json:"token_total"`
	CostTotal   float64        `json:"cost_total"`
	Volume      []dailyVolume  `json:"volume"`
}

type countByValue struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type dailyVolume struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

type logsOptions struct {
	ServerURL      string
	Project        string
	From           string
	To             string
	Since          time.Duration
	Level          string
	ErrorsOnly     bool
	Name           string
	Query          string
	Structured     string
	Page           int
	Limit          int
	JSON           bool
	Stats          bool
	Fields         bool
	Tail           bool
	IncludeDetails bool
}

func (r Runner) Run(ctx context.Context, args []string) error {
	r = r.withDefaults()
	if len(args) == 0 {
		return r.usage()
	}

	switch args[0] {
	case "init":
		return r.runInit(ctx, args[1:])
	case "status":
		return r.runStatus(ctx, args[1:])
	case "projects":
		return r.runProjects(ctx, args[1:])
	case "use":
		return r.runUse(ctx, args[1:])
	case "key":
		return r.runKey(ctx, args[1:])
	case "ingest-command":
		return r.runIngestCommand(ctx, args[1:])
	case "logs":
		return r.runLogs(ctx, args[1:])
	case "help", "-h", "--help":
		return r.usage()
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usageText())
	}
}

func (r Runner) withDefaults() Runner {
	if r.Out == nil {
		r.Out = io.Discard
	}
	if r.Err == nil {
		r.Err = io.Discard
	}
	if r.Client == nil {
		r.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if r.Getenv == nil {
		r.Getenv = os.Getenv
	}
	if strings.TrimSpace(r.DefaultServerURL) == "" {
		r.DefaultServerURL = defaultServerURL
	}
	return r
}

func (r Runner) usage() error {
	_, _ = fmt.Fprint(r.Out, usageText())
	return nil
}

func usageText() string {
	return `vigil manages local Vigil setup.

Usage:
  vigil init [-server URL] [-project NAME] [-env-file PATH] [-regenerate-key]
  vigil status
  vigil projects [-server URL]
  vigil use [-server URL] [-regenerate-key] PROJECT_ID_OR_NAME
  vigil key rotate
  vigil ingest-command
  vigil logs [-server URL] [-project ID_OR_NAME] [-since DURATION] [-level LEVEL] [-errors] [-q TEXT] [-query EXPR] [-json] [-stats] [-fields] [-tail]

Environment:
  VIGIL_CONFIG_PATH  Override the local CLI config path.
`
}

func (r Runner) runInit(ctx context.Context, args []string) error {
	cfgPath, cfg, err := r.load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	serverFlag := fs.String("server", firstNonEmpty(cfg.ServerURL, r.DefaultServerURL), "Vigil server URL")
	projectFlag := fs.String("project", defaultProjectName(), "project name to create or select")
	envFileFlag := fs.String("env-file", ".env", "app env file to update with Vigil SDK settings")
	regenerate := fs.Bool("regenerate-key", false, "rotate and store a fresh ingest key when selecting an existing project")
	if err := fs.Parse(args); err != nil {
		return err
	}

	serverURL, err := normalizeServerURL(*serverFlag)
	if err != nil {
		return err
	}
	projectName := strings.TrimSpace(*projectFlag)
	if projectName == "" {
		return errors.New("project name is required")
	}
	envFile := strings.TrimSpace(*envFileFlag)
	if envFile == "" {
		return errors.New("env file path is required")
	}
	if err := r.checkHealth(ctx, serverURL); err != nil {
		return err
	}

	projects, err := r.listProjects(ctx, serverURL)
	if err != nil {
		return err
	}

	var selected project
	var ingestKey string
	if existing, ok := findProject(projects, projectName); ok {
		selected = existing
		if *regenerate || cfg.ServerURL != serverURL || cfg.ActiveProjectID != existing.ID || cfg.IngestKey == "" {
			rotated, err := r.regenerateKey(ctx, serverURL, existing.ID)
			if err != nil {
				return err
			}
			selected = rotated.Project
			ingestKey = rotated.IngestKey
		} else if cfg.ActiveProjectID == existing.ID {
			ingestKey = cfg.IngestKey
		}
	} else {
		created, err := r.createProject(ctx, serverURL, projectName)
		if err != nil {
			return err
		}
		selected = created.Project
		ingestKey = created.IngestKey
	}

	cfg = Config{
		ServerURL:         serverURL,
		ActiveProjectID:   selected.ID,
		ActiveProjectName: selected.Name,
		IngestKey:         ingestKey,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveConfig(cfgPath, cfg); err != nil {
		return err
	}
	if err := writeAppEnv(envFile, cfg); err != nil {
		return err
	}
	if isDefaultEnvFile(envFile) {
		if err := ensureGitignoreIgnoresDotEnv(); err != nil {
			return err
		}
	}

	printActiveSummary(r.Out, cfgPath, cfg)
	_, _ = fmt.Fprintf(r.Out, "Env file: %s\n", envFile)
	if cfg.IngestKey == "" {
		_, _ = fmt.Fprintf(r.Out, "\nNo ingest key is stored for this existing project. Run `vigil key rotate` to create one.\n")
		return nil
	}
	_, _ = fmt.Fprintln(r.Out, "\nIngest command:")
	printIngestCommand(r.Out, cfg)
	return nil
}

func (r Runner) runStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfgPath, cfg, err := r.load()
	if err != nil {
		return err
	}
	printActiveSummary(r.Out, cfgPath, cfg)
	if cfg.ServerURL == "" {
		return nil
	}
	if err := r.checkHealth(ctx, cfg.ServerURL); err != nil {
		_, _ = fmt.Fprintf(r.Out, "Server health: unavailable (%v)\n", err)
		return nil
	}
	_, _ = fmt.Fprintln(r.Out, "Server health: ok")
	return nil
}

func (r Runner) runProjects(ctx context.Context, args []string) error {
	cfgPath, cfg, err := r.load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	serverFlag := fs.String("server", firstNonEmpty(cfg.ServerURL, r.DefaultServerURL), "Vigil server URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	serverURL, err := normalizeServerURL(*serverFlag)
	if err != nil {
		return err
	}

	projects, err := r.listProjects(ctx, serverURL)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.Out, "Projects from %s\n", serverURL)
	if len(projects) == 0 {
		_, _ = fmt.Fprintln(r.Out, "  none")
		return nil
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})
	for _, project := range projects {
		marker := " "
		if project.ID == cfg.ActiveProjectID {
			marker = "*"
		}
		_, _ = fmt.Fprintf(r.Out, "%s %s  %s  %s\n", marker, project.ID, project.Name, project.Status)
	}
	_ = cfgPath
	return nil
}

func (r Runner) runUse(ctx context.Context, args []string) error {
	cfgPath, cfg, err := r.load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("use", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	serverFlag := fs.String("server", firstNonEmpty(cfg.ServerURL, r.DefaultServerURL), "Vigil server URL")
	regenerate := fs.Bool("regenerate-key", false, "rotate and store a fresh ingest key for the selected project")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("use requires PROJECT_ID_OR_NAME")
	}
	serverURL, err := normalizeServerURL(*serverFlag)
	if err != nil {
		return err
	}

	projects, err := r.listProjects(ctx, serverURL)
	if err != nil {
		return err
	}
	selected, ok := findProject(projects, fs.Arg(0))
	if !ok {
		return fmt.Errorf("project %q not found", fs.Arg(0))
	}

	ingestKey := ""
	if selected.ID == cfg.ActiveProjectID {
		ingestKey = cfg.IngestKey
	}
	if *regenerate {
		rotated, err := r.regenerateKey(ctx, serverURL, selected.ID)
		if err != nil {
			return err
		}
		selected = rotated.Project
		ingestKey = rotated.IngestKey
	}

	cfg = Config{
		ServerURL:         serverURL,
		ActiveProjectID:   selected.ID,
		ActiveProjectName: selected.Name,
		IngestKey:         ingestKey,
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveConfig(cfgPath, cfg); err != nil {
		return err
	}
	printActiveSummary(r.Out, cfgPath, cfg)
	if cfg.IngestKey == "" {
		_, _ = fmt.Fprintln(r.Out, "No ingest key stored. Run `vigil key rotate` to create one.")
	}
	return nil
}

func (r Runner) runKey(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("key requires a subcommand: rotate")
	}
	switch args[0] {
	case "rotate":
		return r.runKeyRotate(ctx, args[1:])
	default:
		return fmt.Errorf("unknown key subcommand %q", args[0])
	}
}

func (r Runner) runKeyRotate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key rotate", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfgPath, cfg, err := r.load()
	if err != nil {
		return err
	}
	if cfg.ServerURL == "" || cfg.ActiveProjectID == "" {
		return errors.New("no active project; run `vigil init` first")
	}

	rotated, err := r.regenerateKey(ctx, cfg.ServerURL, cfg.ActiveProjectID)
	if err != nil {
		return err
	}
	cfg.ActiveProjectID = rotated.Project.ID
	cfg.ActiveProjectName = rotated.Project.Name
	cfg.IngestKey = rotated.IngestKey
	cfg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := saveConfig(cfgPath, cfg); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.Out, "Rotated ingest key for %s (%s).\n\n", cfg.ActiveProjectName, cfg.ActiveProjectID)
	printIngestCommand(r.Out, cfg)
	return nil
}

func (r Runner) runIngestCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest-command", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, cfg, err := r.load()
	if err != nil {
		return err
	}
	if cfg.ServerURL == "" || cfg.ActiveProjectID == "" {
		return errors.New("no active project; run `vigil init` first")
	}
	if cfg.IngestKey == "" {
		return errors.New("no ingest key stored; run `vigil key rotate`")
	}
	_ = ctx
	printIngestCommand(r.Out, cfg)
	return nil
}

func (r Runner) runLogs(ctx context.Context, args []string) error {
	_, cfg, err := r.load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(r.Err)
	opts := logsOptions{}
	fs.StringVar(&opts.ServerURL, "server", firstNonEmpty(cfg.ServerURL, r.DefaultServerURL), "Vigil server URL")
	fs.StringVar(&opts.Project, "project", firstNonEmpty(cfg.ActiveProjectID, cfg.ActiveProjectName), "project ID or name")
	fs.DurationVar(&opts.Since, "since", time.Hour, "relative time window such as 15m, 1h, or 24h")
	fs.StringVar(&opts.From, "from", "", "RFC3339 window start; overrides -since")
	fs.StringVar(&opts.To, "to", "", "RFC3339 window end; defaults to now")
	fs.StringVar(&opts.Level, "level", "", "log level filter")
	fs.BoolVar(&opts.ErrorsOnly, "errors", false, "shortcut for -level error")
	fs.StringVar(&opts.Name, "name", "", "exact event name filter")
	fs.StringVar(&opts.Query, "q", "", "full-text search")
	fs.StringVar(&opts.Structured, "query", "", "structured query expression")
	fs.IntVar(&opts.Page, "page", 1, "result page")
	fs.IntVar(&opts.Limit, "limit", 50, "maximum logs to return")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON")
	fs.BoolVar(&opts.Stats, "stats", false, "print stats for the selected window")
	fs.BoolVar(&opts.Fields, "fields", false, "discover top-level attrs/body fields in matching logs")
	fs.BoolVar(&opts.Tail, "tail", false, "stream matching logs with Server-Sent Events")
	fs.BoolVar(&opts.IncludeDetails, "details", false, "include attrs/body JSON in text log output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("logs does not accept positional arguments")
	}

	opts.ServerURL, err = normalizeServerURL(opts.ServerURL)
	if err != nil {
		return err
	}
	projectID, err := r.resolveProjectID(ctx, opts.ServerURL, opts.Project, cfg)
	if err != nil {
		return err
	}
	values, err := logsQueryValues(opts, projectID, time.Now().UTC())
	if err != nil {
		return err
	}

	switch {
	case opts.Stats:
		stats, err := r.fetchLogStats(ctx, opts.ServerURL, values)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writePrettyJSON(r.Out, stats)
		}
		printLogStats(r.Out, stats)
		return nil
	case opts.Fields:
		logs, err := r.fetchLogs(ctx, opts.ServerURL, values)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writePrettyJSON(r.Out, discoverLogFields(logs.Events))
		}
		printLogFields(r.Out, discoverLogFields(logs.Events))
		return nil
	case opts.Tail:
		return r.tailLogs(ctx, opts.ServerURL, values, opts)
	default:
		logs, err := r.fetchLogs(ctx, opts.ServerURL, values)
		if err != nil {
			return err
		}
		if opts.JSON {
			return writePrettyJSON(r.Out, logs)
		}
		printLogs(r.Out, logs, opts)
		return nil
	}
}

func (r Runner) load() (string, Config, error) {
	cfgPath, err := configPath(r.Getenv)
	if err != nil {
		return "", Config{}, err
	}
	cfg, err := loadConfig(cfgPath)
	return cfgPath, cfg, err
}

func configPath(getenv func(string) string) (string, error) {
	if override := strings.TrimSpace(getenv(configEnvKey)); override != "" {
		return override, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "vigil", "config.json"), nil
}

func loadConfig(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.ServerURL != "" {
		serverURL, err := normalizeServerURL(cfg.ServerURL)
		if err != nil {
			return Config{}, err
		}
		cfg.ServerURL = serverURL
	}
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	content, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func writeAppEnv(path string, cfg Config) error {
	block := appEnvBlock(cfg)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		content = nil
	} else if err != nil {
		return fmt.Errorf("read app env: %w", err)
	}

	updated := mergeAppEnvBlock(string(content), block)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create app env dir: %w", err)
	}

	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return fmt.Errorf("write app env: %w", err)
	}
	return nil
}

func appEnvBlock(cfg Config) string {
	return fmt.Sprintf(`%s
VIGIL_BASE_URL=%s
VIGIL_PROJECT_ID=%s
VIGIL_INGEST_KEY=%s
%s
`, appEnvBegin, cfg.ServerURL, cfg.ActiveProjectID, cfg.IngestKey, appEnvEnd)
}

func mergeAppEnvBlock(content, block string) string {
	start := strings.Index(content, appEnvBegin)
	end := strings.Index(content, appEnvEnd)
	if start >= 0 && end >= start {
		end += len(appEnvEnd)
		if end < len(content) && content[end] == '\r' {
			end++
		}
		if end < len(content) && content[end] == '\n' {
			end++
		}
		return content[:start] + block + content[end:]
	}

	if content == "" {
		return block
	}
	separator := "\n\n"
	if strings.HasSuffix(content, "\n\n") {
		separator = ""
	} else if strings.HasSuffix(content, "\n") {
		separator = "\n"
	}
	return content + separator + block
}

func isDefaultEnvFile(path string) bool {
	return filepath.Clean(path) == ".env"
}

func ensureGitignoreIgnoresDotEnv() error {
	const path = ".gitignore"
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte(".env\n"), 0o644)
	}
	if err != nil {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == ".env" {
			return nil
		}
	}

	updated := string(content)
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += ".env\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

func (r Runner) resolveProjectID(ctx context.Context, serverURL, wanted string, cfg Config) (string, error) {
	wanted = strings.TrimSpace(wanted)
	if wanted == "" {
		return "", errors.New("no active project; run `vigil init` first or pass -project")
	}
	if wanted == cfg.ActiveProjectID || wanted == cfg.ActiveProjectName {
		if cfg.ActiveProjectID == "" {
			return "", errors.New("no active project; run `vigil init` first or pass -project")
		}
		return cfg.ActiveProjectID, nil
	}

	projects, err := r.listProjects(ctx, serverURL)
	if err != nil {
		return "", err
	}
	selected, ok := findProject(projects, wanted)
	if !ok {
		return "", fmt.Errorf("project %q not found", wanted)
	}
	return selected.ID, nil
}

func logsQueryValues(opts logsOptions, projectID string, now time.Time) (url.Values, error) {
	values := url.Values{}
	values.Set("project_id", projectID)

	to := now.UTC()
	if strings.TrimSpace(opts.To) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(opts.To))
		if err != nil {
			return nil, fmt.Errorf("invalid to timestamp")
		}
		to = parsed.UTC()
	}
	from := to.Add(-opts.Since)
	if opts.Since <= 0 {
		return nil, errors.New("since must be greater than 0")
	}
	if strings.TrimSpace(opts.From) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(opts.From))
		if err != nil {
			return nil, fmt.Errorf("invalid from timestamp")
		}
		from = parsed.UTC()
	}
	if from.After(to) {
		return nil, errors.New("from must be before to")
	}
	values.Set("from", from.Format(time.RFC3339))
	values.Set("to", to.Format(time.RFC3339))

	level := strings.ToLower(strings.TrimSpace(opts.Level))
	if opts.ErrorsOnly {
		level = "error"
	}
	if level != "" {
		values.Set("level", level)
	}
	if name := strings.TrimSpace(opts.Name); name != "" {
		values.Set("name", name)
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		values.Set("q", q)
	}
	if structured := strings.TrimSpace(opts.Structured); structured != "" {
		values.Set("query", structured)
	}
	if opts.Page > 1 {
		values.Set("page", fmt.Sprint(opts.Page))
	}
	if opts.Limit > 0 {
		values.Set("limit", fmt.Sprint(opts.Limit))
	}
	return values, nil
}

func (r Runner) fetchLogs(ctx context.Context, serverURL string, values url.Values) (*logListResponse, error) {
	var response logListResponse
	if err := r.getJSON(ctx, serverURL+"/api/logs?"+values.Encode(), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (r Runner) fetchLogStats(ctx context.Context, serverURL string, values url.Values) (*statsSummaryResponse, error) {
	statsValues := url.Values{}
	for _, key := range []string{"project_id", "from", "to"} {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			statsValues.Set(key, value)
		}
	}
	var response statsSummaryResponse
	if err := r.getJSON(ctx, serverURL+"/api/stats?"+statsValues.Encode(), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (r Runner) tailLogs(ctx context.Context, serverURL string, values url.Values, opts logsOptions) error {
	requestURL := serverURL + "/api/logs/tail?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	client := *r.Client
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("GET %s returned %d", requestURL, resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventName string
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := printSSELogEvent(r.Out, eventName, strings.Join(dataLines, "\n"), opts); err != nil {
				return err
			}
			eventName = ""
			dataLines = nil
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return scanner.Err()
}

func printSSELogEvent(w io.Writer, eventName, data string, opts logsOptions) error {
	if strings.TrimSpace(data) == "" || eventName == "ping" {
		return nil
	}
	if eventName == "error" {
		return fmt.Errorf("tail stream error: %s", data)
	}
	var ev event.StoredEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return fmt.Errorf("decode tail event: %w", err)
	}
	if opts.JSON {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(w, string(line))
		return nil
	}
	printLogEvent(w, ev, opts)
	return nil
}

func (r Runner) checkHealth(ctx context.Context, serverURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/health", nil)
	if err != nil {
		return err
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", serverURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}
	return nil
}

func (r Runner) listProjects(ctx context.Context, serverURL string) ([]project, error) {
	var response struct {
		Projects []project `json:"projects"`
	}
	if err := r.getJSON(ctx, serverURL+"/api/projects", &response); err != nil {
		return nil, err
	}
	return response.Projects, nil
}

func (r Runner) createProject(ctx context.Context, serverURL, name string) (projectCreateResponse, error) {
	var response projectCreateResponse
	err := r.postJSON(ctx, serverURL+"/api/projects", map[string]string{"name": name}, &response)
	return response, err
}

func (r Runner) regenerateKey(ctx context.Context, serverURL, projectID string) (projectCreateResponse, error) {
	var response projectCreateResponse
	err := r.postJSON(ctx, serverURL+"/api/projects/"+url.PathEscape(projectID)+"/keys/regenerate", map[string]any{}, &response)
	return response, err
}

func (r Runner) getJSON(ctx context.Context, requestURL string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return responseError(http.MethodGet, requestURL, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", requestURL, err)
	}
	return nil
}

func (r Runner) postJSON(ctx context.Context, requestURL string, payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return responseError(http.MethodPost, requestURL, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", requestURL, err)
	}
	return nil
}

func responseError(method string, requestURL string, resp *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if len(body) > 0 {
		_ = json.Unmarshal(body, &payload)
	}
	if payload.Error != "" {
		return fmt.Errorf("%s %s returned %d: %s", method, requestURL, resp.StatusCode, payload.Error)
	}
	return fmt.Errorf("%s %s returned %d", method, requestURL, resp.StatusCode)
}

func findProject(projects []project, wanted string) (project, bool) {
	wanted = strings.TrimSpace(wanted)
	for _, project := range projects {
		if project.ID == wanted || project.Name == wanted {
			return project, true
		}
	}
	return project{}, false
}

func normalizeServerURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", errors.New("server URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid server URL %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("server URL must use http or https")
	}
	return raw, nil
}

func defaultProjectName() string {
	wd, err := os.Getwd()
	if err != nil {
		return "default"
	}
	name := strings.TrimSpace(filepath.Base(wd))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "default"
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func writePrettyJSON(w io.Writer, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, string(content))
	return nil
}

func printLogs(w io.Writer, logs *logListResponse, opts logsOptions) {
	if len(logs.Events) == 0 {
		_, _ = fmt.Fprintln(w, "No matching logs.")
		return
	}
	for _, ev := range logs.Events {
		printLogEvent(w, ev, opts)
	}
	_, _ = fmt.Fprintf(w, "\nShowing %d of %d matching logs (page %d, limit %d).\n", len(logs.Events), logs.Total, logs.Page, logs.Limit)
	for _, warning := range logs.Warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", warning.Message)
	}
}

func printLogEvent(w io.Writer, ev event.StoredEvent, opts logsOptions) {
	level := strings.ToUpper(strings.TrimSpace(ev.Level))
	if level == "" {
		level = "-"
	}
	message := eventMessage(ev)
	_, _ = fmt.Fprintf(w, "%s  %-5s  %-18s  %-28s  %s\n", compactTimestamp(ev.TS), level, ev.Source, ev.Name, message)
	if opts.IncludeDetails {
		if attrs := compactRawJSON(ev.Attrs); attrs != "{}" {
			_, _ = fmt.Fprintf(w, "  attrs: %s\n", attrs)
		}
		if body := compactRawJSON(ev.Body); body != "" && body != "null" {
			_, _ = fmt.Fprintf(w, "  body:  %s\n", body)
		}
	}
}

func eventMessage(ev event.StoredEvent) string {
	if len(ev.Body) == 0 {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(ev.Body, &object); err == nil {
		if message, ok := object["message"].(string); ok {
			return message
		}
		if errorValue, ok := object["error"].(string); ok {
			return errorValue
		}
	}
	var text string
	if err := json.Unmarshal(ev.Body, &text); err == nil {
		return text
	}
	body := compactRawJSON(ev.Body)
	if body == "null" {
		return ""
	}
	return body
}

func compactTimestamp(raw string) string {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("2006-01-02 15:04:05")
}

func compactRawJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return string(raw)
	}
	return out.String()
}

func printLogStats(w io.Writer, stats *statsSummaryResponse) {
	_, _ = fmt.Fprintf(w, "Total events: %d\n", stats.TotalEvents)
	if len(stats.ByKind) > 0 {
		_, _ = fmt.Fprintln(w, "\nBy kind:")
		printCounts(w, stats.ByKind)
	}
	if len(stats.ByLevel) > 0 {
		_, _ = fmt.Fprintln(w, "\nBy level:")
		printCounts(w, stats.ByLevel)
	}
	if stats.TokenTotal != 0 || stats.CostTotal != 0 {
		_, _ = fmt.Fprintf(w, "\nTokens: %.0f\nCost: %.4f\n", stats.TokenTotal, stats.CostTotal)
	}
}

func printCounts(w io.Writer, counts []countByValue) {
	for _, item := range counts {
		label := item.Label
		if label == "" {
			label = "(empty)"
		}
		_, _ = fmt.Fprintf(w, "  %-18s %d\n", label, item.Count)
	}
}

type fieldCount struct {
	Field string `json:"field"`
	Count int    `json:"count"`
}

func discoverLogFields(events []event.StoredEvent) []fieldCount {
	counts := map[string]int{}
	for _, ev := range events {
		addJSONFieldCounts(counts, "attrs", ev.Attrs)
		addJSONFieldCounts(counts, "body", ev.Body)
	}
	fields := make([]fieldCount, 0, len(counts))
	for field, count := range counts {
		fields = append(fields, fieldCount{Field: field, Count: count})
	}
	sort.Slice(fields, func(i, j int) bool {
		if fields[i].Count == fields[j].Count {
			return fields[i].Field < fields[j].Field
		}
		return fields[i].Count > fields[j].Count
	})
	return fields
}

func addJSONFieldCounts(counts map[string]int, prefix string, raw json.RawMessage) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return
	}
	for key := range object {
		key = strings.TrimSpace(key)
		if key != "" {
			counts[prefix+"."+key]++
		}
	}
}

func printLogFields(w io.Writer, fields []fieldCount) {
	if len(fields) == 0 {
		_, _ = fmt.Fprintln(w, "No fields found in matching logs.")
		return
	}
	for _, field := range fields {
		_, _ = fmt.Fprintf(w, "%-32s %d\n", field.Field, field.Count)
	}
}

func printActiveSummary(w io.Writer, cfgPath string, cfg Config) {
	_, _ = fmt.Fprintf(w, "Config: %s\n", cfgPath)
	_, _ = fmt.Fprintf(w, "Server: %s\n", firstNonEmpty(cfg.ServerURL, "not set"))
	if cfg.ActiveProjectID == "" {
		_, _ = fmt.Fprintln(w, "Active project: not set")
		_, _ = fmt.Fprintln(w, "Ingest key: not stored")
		return
	}
	_, _ = fmt.Fprintf(w, "Active project: %s (%s)\n", cfg.ActiveProjectName, cfg.ActiveProjectID)
	_, _ = fmt.Fprintf(w, "Open: %s/projects/%s/logs\n", cfg.ServerURL, cfg.ActiveProjectID)
	if cfg.IngestKey == "" {
		_, _ = fmt.Fprintln(w, "Ingest key: not stored")
		return
	}
	_, _ = fmt.Fprintln(w, "Ingest key: stored")
}

func printIngestCommand(w io.Writer, cfg Config) {
	_, _ = fmt.Fprintf(w, `curl -X POST %s/api/ingest \
  -H "Authorization: Bearer %s" \
  -H "Content-Type: application/json" \
  -d '{
    "schema_version": 1,
    "project_id": "%s",
    "kind": "log",
    "ts": "%s",
    "source": "vigil",
    "level": "info",
    "name": "hello.vigil",
    "attrs": { "route": "/first-run" },
    "body": { "message": "hello from vigil" }
  }'
`, cfg.ServerURL, cfg.IngestKey, cfg.ActiveProjectID, time.Now().UTC().Format(time.RFC3339))
}
