package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vigil/internal/app"
	"vigil/internal/config"
)

func TestInitDefaultsServerCreatesProjectStoresConfigAndWritesEnv(t *testing.T) {
	server := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	var out bytes.Buffer

	err := Runner{
		Out:              &out,
		Err:              &bytes.Buffer{},
		DefaultServerURL: server.URL,
		Getenv: func(key string) string {
			if key == configEnvKey {
				return configPath
			}
			return ""
		},
	}.Run(context.Background(), []string{"init", "-project", "cli-app"})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	cfg := readConfig(t, configPath)
	if cfg.ServerURL != server.URL {
		t.Fatalf("expected server URL %s, got %s", server.URL, cfg.ServerURL)
	}
	if cfg.ActiveProjectName != "cli-app" {
		t.Fatalf("expected active project cli-app, got %s", cfg.ActiveProjectName)
	}
	if cfg.ActiveProjectID == "" {
		t.Fatal("expected active project ID")
	}
	if cfg.IngestKey == "" {
		t.Fatal("expected stored ingest key")
	}
	if !strings.Contains(out.String(), "Ingest command:") {
		t.Fatalf("expected init to print ingest command, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), cfg.ActiveProjectID) {
		t.Fatalf("expected init output to include active project ID, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Env file: .env") {
		t.Fatalf("expected init output to include env file, got:\n%s", out.String())
	}

	env := readFile(t, filepath.Join(projectDir, ".env"))
	for _, want := range []string{
		"VIGIL_BASE_URL=" + server.URL,
		"VIGIL_PROJECT_ID=" + cfg.ActiveProjectID,
		"VIGIL_INGEST_KEY=" + cfg.IngestKey,
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("expected .env to contain %q, got:\n%s", want, env)
		}
	}
	if strings.Contains(env, "VIGIL_RETENTION_ENABLED") || strings.Contains(env, "VIGIL_DATA_DIR") {
		t.Fatalf("expected app .env to omit server runtime settings, got:\n%s", env)
	}

	gitignore := readFile(t, filepath.Join(projectDir, ".gitignore"))
	if !strings.Contains(gitignore, ".env\n") {
		t.Fatalf("expected .gitignore to ignore .env, got:\n%s", gitignore)
	}
}

func TestInitReplacesManagedEnvBlockAndPreservesExistingContent(t *testing.T) {
	server := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	envPath := filepath.Join(projectDir, ".env")
	if err := os.WriteFile(envPath, []byte(`APP_MODE=dev

# BEGIN VIGIL
VIGIL_BASE_URL=http://old.example
VIGIL_PROJECT_ID=proj_old
VIGIL_INGEST_KEY=vigil_old
# END VIGIL
AFTER=yes
`), 0o644); err != nil {
		t.Fatalf("seed .env: %v", err)
	}

	if err := testRunner(configPath, &bytes.Buffer{}).Run(context.Background(), []string{"init", "-server", server.URL, "-project", "cli-app"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	cfg := readConfig(t, configPath)
	env := readFile(t, envPath)
	for _, want := range []string{
		"APP_MODE=dev\n",
		"AFTER=yes\n",
		"VIGIL_BASE_URL=" + server.URL,
		"VIGIL_PROJECT_ID=" + cfg.ActiveProjectID,
		"VIGIL_INGEST_KEY=" + cfg.IngestKey,
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("expected .env to contain %q, got:\n%s", want, env)
		}
	}
	for _, old := range []string{"http://old.example", "proj_old", "vigil_old"} {
		if strings.Contains(env, old) {
			t.Fatalf("expected .env to replace old managed value %q, got:\n%s", old, env)
		}
	}
}

func TestIngestCommandRequiresStoredKey(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, configPath, Config{
		ServerURL:         "http://localhost:8080",
		ActiveProjectID:   "proj_123",
		ActiveProjectName: "demo",
	})

	err := testRunner(configPath, nil).Run(context.Background(), []string{"ingest-command"})
	if err == nil || !strings.Contains(err.Error(), "no ingest key stored") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}

func TestUseExistingProjectAndRotateKey(t *testing.T) {
	server := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Chdir(t.TempDir())

	runner := testRunner(configPath, &bytes.Buffer{})
	if err := runner.Run(context.Background(), []string{"init", "-server", server.URL, "-project", "first"}); err != nil {
		t.Fatalf("init first: %v", err)
	}
	if err := runner.Run(context.Background(), []string{"init", "-server", server.URL, "-project", "second"}); err != nil {
		t.Fatalf("init second: %v", err)
	}

	var out bytes.Buffer
	runner = testRunner(configPath, &out)
	if err := runner.Run(context.Background(), []string{"use", "-server", server.URL, "-regenerate-key", "first"}); err != nil {
		t.Fatalf("use first: %v", err)
	}

	cfg := readConfig(t, configPath)
	if cfg.ActiveProjectName != "first" {
		t.Fatalf("expected active project first, got %s", cfg.ActiveProjectName)
	}
	if cfg.IngestKey == "" {
		t.Fatal("expected key generated by use -regenerate-key")
	}
	if !strings.Contains(out.String(), "Ingest key: stored") {
		t.Fatalf("expected use output to make key state obvious, got:\n%s", out.String())
	}

	out.Reset()
	runner = testRunner(configPath, &out)
	if err := runner.Run(context.Background(), []string{"projects", "-server", server.URL}); err != nil {
		t.Fatalf("projects: %v", err)
	}
	if !strings.Contains(out.String(), "* "+cfg.ActiveProjectID) {
		t.Fatalf("expected projects output to mark active project, got:\n%s", out.String())
	}
}

func TestKeyRotateUpdatesStoredKey(t *testing.T) {
	server := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Chdir(t.TempDir())

	runner := testRunner(configPath, &bytes.Buffer{})
	if err := runner.Run(context.Background(), []string{"init", "-server", server.URL, "-project", "cli-app"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	before := readConfig(t, configPath).IngestKey

	var out bytes.Buffer
	runner = testRunner(configPath, &out)
	if err := runner.Run(context.Background(), []string{"key", "rotate"}); err != nil {
		t.Fatalf("key rotate: %v", err)
	}
	after := readConfig(t, configPath).IngestKey
	if after == "" || after == before {
		t.Fatalf("expected rotated key to change, before=%q after=%q", before, after)
	}
	if !strings.Contains(out.String(), "curl -X POST") {
		t.Fatalf("expected rotate to print ingest command, got:\n%s", out.String())
	}

	out.Reset()
	runner = testRunner(configPath, &out)
	if err := runner.Run(context.Background(), []string{"ingest-command"}); err != nil {
		t.Fatalf("ingest-command: %v", err)
	}
	if !strings.Contains(out.String(), `"source": "vigil"`) {
		t.Fatalf("expected ingest command to use vigil source, got:\n%s", out.String())
	}
}

func TestLogsListsActiveProjectEventsWithFiltersAndJSON(t *testing.T) {
	server := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Chdir(t.TempDir())

	runner := testRunner(configPath, &bytes.Buffer{})
	if err := runner.Run(context.Background(), []string{"init", "-server", server.URL, "-project", "cli-logs"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := readConfig(t, configPath)
	now := time.Now().UTC()
	ingestTestLog(t, server.URL, cfg, now.Add(-2*time.Minute), "info", "request.ok", map[string]any{"route": "/health"}, map[string]any{"message": "healthy"})
	ingestTestLog(t, server.URL, cfg, now.Add(-time.Minute), "error", "request.failed", map[string]any{"route": "/checkout"}, map[string]any{"message": "checkout failed"})
	waitForLogs(t, runner, "request.failed")

	var out bytes.Buffer
	runner = testRunner(configPath, &out)
	if err := runner.Run(context.Background(), []string{"logs", "-errors", "-q", "checkout", "-limit", "5"}); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out.String(), "request.failed") || !strings.Contains(out.String(), "checkout failed") {
		t.Fatalf("expected filtered error log, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "request.ok") {
		t.Fatalf("did not expect info log in filtered output, got:\n%s", out.String())
	}

	out.Reset()
	if err := runner.Run(context.Background(), []string{"logs", "-json", "-query", `attrs.route = "/checkout"`}); err != nil {
		t.Fatalf("logs json: %v", err)
	}
	var response logListResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("parse logs JSON: %v\n%s", err, out.String())
	}
	if response.Total != 1 || len(response.Events) != 1 || response.Events[0].Name != "request.failed" {
		t.Fatalf("expected one checkout log, got total=%d events=%v", response.Total, response.Events)
	}
}

func TestLogsStatsAndFieldDiscovery(t *testing.T) {
	server := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Chdir(t.TempDir())

	runner := testRunner(configPath, &bytes.Buffer{})
	if err := runner.Run(context.Background(), []string{"init", "-server", server.URL, "-project", "cli-fields"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg := readConfig(t, configPath)
	now := time.Now().UTC()
	ingestTestLog(t, server.URL, cfg, now.Add(-2*time.Minute), "info", "worker.started", map[string]any{"queue": "email"}, map[string]any{"message": "started", "job_id": "job_1"})
	ingestTestLog(t, server.URL, cfg, now.Add(-time.Minute), "error", "worker.failed", map[string]any{"queue": "email", "attempt": 2}, map[string]any{"message": "failed", "job_id": "job_1"})
	waitForLogs(t, runner, "worker.failed")

	var out bytes.Buffer
	runner = testRunner(configPath, &out)
	if err := runner.Run(context.Background(), []string{"logs", "-stats"}); err != nil {
		t.Fatalf("logs stats: %v", err)
	}
	for _, want := range []string{"Total events: 2", "log", "error"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected stats output to contain %q, got:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := runner.Run(context.Background(), []string{"logs", "-fields"}); err != nil {
		t.Fatalf("logs fields: %v", err)
	}
	for _, want := range []string{"attrs.queue", "attrs.attempt", "body.message", "body.job_id"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected fields output to contain %q, got:\n%s", want, out.String())
		}
	}
}

func TestLogsRequiresActiveProject(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, configPath, Config{ServerURL: "http://localhost:8080"})

	err := testRunner(configPath, nil).Run(context.Background(), []string{"logs"})
	if err == nil || !strings.Contains(err.Error(), "no active project") {
		t.Fatalf("expected missing active project error, got %v", err)
	}
}

func testRunner(configPath string, out *bytes.Buffer) Runner {
	if out == nil {
		out = &bytes.Buffer{}
	}
	return Runner{
		Out: out,
		Err: &bytes.Buffer{},
		Getenv: func(key string) string {
			if key == configEnvKey {
				return configPath
			}
			return ""
		},
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	vigilApp, err := app.New(context.Background(), config.Config{
		Addr:            ":0",
		DataDir:         t.TempDir(),
		MaxEventBytes:   1 << 20,
		SegmentMaxBytes: 10 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	t.Cleanup(func() {
		if err := vigilApp.Close(); err != nil {
			t.Fatalf("close app: %v", err)
		}
	})

	server := httptest.NewServer(vigilApp.Handler)
	t.Cleanup(server.Close)
	return server
}

func ingestTestLog(t *testing.T, serverURL string, cfg Config, ts time.Time, level, name string, attrs map[string]any, body map[string]any) {
	t.Helper()
	payload := map[string]any{
		"schema_version": 1,
		"project_id":     cfg.ActiveProjectID,
		"kind":           "log",
		"ts":             ts.UTC().Format(time.RFC3339),
		"source":         "cli-test",
		"level":          level,
		"name":           name,
		"attrs":          attrs,
		"body":           body,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal log: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/ingest", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("new ingest request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.IngestKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ingest log: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected accepted ingest, got %d", resp.StatusCode)
	}
}

func waitForLogs(t *testing.T, runner Runner, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var out bytes.Buffer
		runner.Out = &out
		if err := runner.Run(context.Background(), []string{"logs", "-since", "5m"}); err != nil {
			t.Fatalf("logs while waiting: %v", err)
		}
		last = out.String()
		if strings.Contains(last, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in logs output:\n%s", want, last)
}

func readConfig(t *testing.T, path string) Config {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(content, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func writeConfig(t *testing.T, path string, cfg Config) {
	t.Helper()
	content, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
