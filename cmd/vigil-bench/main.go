package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"vigil/internal/app"
	"vigil/internal/config"
)

const defaultBenchmarkDataDir = "./vigil-bench-data"

type project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type createProjectResponse struct {
	Project   project `json:"project"`
	IngestKey string  `json:"ingest_key"`
}

type eventEnvelope struct {
	SchemaVersion int            `json:"schema_version"`
	ProjectID     string         `json:"project_id"`
	Kind          string         `json:"kind"`
	TS            string         `json:"ts"`
	Source        string         `json:"source"`
	Name          string         `json:"name"`
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	Level         string         `json:"level,omitempty"`
	Attrs         map[string]any `json:"attrs,omitempty"`
	Body          any            `json:"body,omitempty"`
}

type logListResponse struct {
	Total int `json:"total"`
}

type ingestSample struct {
	Duration time.Duration
	Bytes    int
	Err      error
}

type storageSummary struct {
	RawFileCount        int
	RawBytes            int64
	SQLiteBytes         int64
	IndexBytes          int64
	DataDirBytes        int64
	RequestPayloadBytes int64
}

type latencyStats struct {
	Count int
	Avg   time.Duration
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

type queryCase struct {
	Name          string
	Path          string
	ExpectedTotal int
}

type queryResult struct {
	Name          string
	ExpectedTotal int
	ObservedTotal int
	Stats         latencyStats
	Err           error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vigil-bench:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "", "existing Vigil server base URL; empty starts an isolated in-process server")
	dataDir := flag.String("data-dir", "", "data directory for isolated benchmark runs; defaults to ./vigil-bench-data")
	events := flag.Int("events", 5000, "number of synthetic log events to ingest")
	concurrency := flag.Int("concurrency", runtime.GOMAXPROCS(0), "parallel ingest workers")
	queryRuns := flag.Int("query-runs", 25, "timed runs per query")
	warmupRuns := flag.Int("warmup-runs", 3, "untimed warmup runs per query")
	projectName := flag.String("project-name", "", "project name to create for the benchmark")
	waitTimeout := flag.Duration("wait-timeout", 60*time.Second, "maximum time to wait for async indexing to catch up")
	requestTimeout := flag.Duration("request-timeout", 30*time.Second, "HTTP request timeout")
	flag.Parse()

	if *events < 1 {
		return fmt.Errorf("events must be at least 1")
	}
	if *concurrency < 1 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if *queryRuns < 1 {
		return fmt.Errorf("query-runs must be at least 1")
	}
	if *warmupRuns < 0 {
		return fmt.Errorf("warmup-runs cannot be negative")
	}

	baseURL, cleanup, isolatedDataDir, err := resolveServer(*addr, *dataDir)
	if err != nil {
		return err
	}
	defer cleanup()

	client := newHTTPClient(*requestTimeout, *concurrency)
	name := strings.TrimSpace(*projectName)
	if name == "" {
		name = fmt.Sprintf("bench-%s-%d", time.Now().UTC().Format("20060102-150405"), os.Getpid())
	}

	created, err := createProject(client, baseURL, name)
	if err != nil {
		return fmt.Errorf("create benchmark project: %w", err)
	}

	baseTS := time.Now().UTC().Add(-time.Hour)
	from := baseTS.Add(-time.Minute)
	to := baseTS.Add(time.Duration(*events)*time.Millisecond + time.Minute)

	fmt.Printf("Benchmark project: %s (%s)\n", created.Project.Name, created.Project.ID)
	fmt.Printf("Server: %s\n", baseURL)
	if isolatedDataDir != "" {
		fmt.Printf("Data dir: %s\n", isolatedDataDir)
		fmt.Printf("Raw logs dir: %s\n", rawLogsDir(isolatedDataDir, created.Project.ID, baseTS))
		fmt.Printf("SQLite db: %s\n", sqliteDBPath(isolatedDataDir))
	}
	fmt.Printf("\n")
	fmt.Printf("Ingesting %d log events with concurrency %d...\n", *events, *concurrency)

	ingestStarted := time.Now()
	samples, failed := ingestLogs(client, baseURL, created.Project.ID, created.IngestKey, baseTS, *events, *concurrency)
	ingestElapsed := time.Since(ingestStarted)
	ingestStats := summarizeIngest(samples)
	totalBytes := 0
	for _, sample := range samples {
		totalBytes += sample.Bytes
	}

	printIngestSummary(*events, failed, totalBytes, ingestElapsed, ingestStats)
	if failed > 0 {
		printIngestErrors(samples, 5)
		return fmt.Errorf("%d ingest requests failed", failed)
	}

	fmt.Printf("Waiting for async index catch-up...\n")
	indexStarted := time.Now()
	indexedTotal, err := waitForIndexedTotal(client, baseURL, created.Project.ID, from, to, *events, *waitTimeout)
	indexElapsed := time.Since(indexStarted)
	if err != nil {
		return err
	}
	fmt.Printf("Index catch-up: %s, indexed total=%d\n", rounded(indexElapsed), indexedTotal)

	if isolatedDataDir != "" {
		summary, err := collectStorageSummary(isolatedDataDir, totalBytes)
		if err != nil {
			return err
		}
		printStorageSummary(summary)
	}

	queries := buildQueryCases(created.Project.ID, from, to, *events)
	queryResults := runQueryBenchmarks(client, baseURL, queries, *warmupRuns, *queryRuns)
	printQuerySummary(queryResults)

	for _, result := range queryResults {
		if result.Err != nil {
			return fmt.Errorf("query %q failed: %w", result.Name, result.Err)
		}
		if result.ObservedTotal != result.ExpectedTotal {
			return fmt.Errorf("query %q expected total %d, got %d", result.Name, result.ExpectedTotal, result.ObservedTotal)
		}
	}

	return nil
}

func resolveServer(addr, dataDir string) (baseURL string, cleanup func(), isolatedDataDir string, err error) {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	if addr != "" {
		return addr, func() {}, "", nil
	}

	if strings.TrimSpace(dataDir) == "" {
		dataDir = defaultBenchmarkDataDir
	}
	dataDir = filepath.Clean(dataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", nil, "", fmt.Errorf("create benchmark data dir: %w", err)
	}

	cfg := config.Config{
		Addr:            ":0",
		DataDir:         dataDir,
		MaxEventBytes:   1 << 20,
		SegmentMaxBytes: 10 * 1024 * 1024,
	}
	benchmarkApp, err := app.New(context.Background(), cfg)
	if err != nil {
		return "", nil, "", fmt.Errorf("start isolated app: %w", err)
	}
	server := httptest.NewServer(benchmarkApp.Handler)

	cleanup = func() {
		server.Close()
		_ = benchmarkApp.Close()
	}
	return server.URL, cleanup, dataDir, nil
}

func rawLogsDir(dataDir, projectID string, baseTS time.Time) string {
	return filepath.Join(dataDir, "logs", projectID, baseTS.UTC().Format("2006-01-02"))
}

func sqliteDBPath(dataDir string) string {
	return filepath.Join(dataDir, "index", "vigil.db")
}

func collectStorageSummary(dataDir string, requestPayloadBytes int) (storageSummary, error) {
	rawFiles, rawBytes, err := countNDJSONFiles(filepath.Join(dataDir, "logs"))
	if err != nil {
		return storageSummary{}, err
	}

	sqliteBytes, err := fileSize(sqliteDBPath(dataDir))
	if err != nil {
		return storageSummary{}, err
	}

	indexBytes, err := directorySize(filepath.Join(dataDir, "index"))
	if err != nil {
		return storageSummary{}, err
	}

	dataDirBytes, err := directorySize(dataDir)
	if err != nil {
		return storageSummary{}, err
	}

	return storageSummary{
		RawFileCount:        rawFiles,
		RawBytes:            rawBytes,
		SQLiteBytes:         sqliteBytes,
		IndexBytes:          indexBytes,
		DataDirBytes:        dataDirBytes,
		RequestPayloadBytes: int64(requestPayloadBytes),
	}, nil
}

func countNDJSONFiles(root string) (int, int64, error) {
	count := 0
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		total += info.Size()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("measure raw ndjson files: %w", err)
	}
	return count, total, nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("measure directory %s: %w", root, err)
	}
	return total, nil
}

func createProject(client *http.Client, baseURL, name string) (*createProjectResponse, error) {
	var response createProjectResponse
	if err := requestJSON(client, http.MethodPost, baseURL+"/api/projects", "", map[string]string{"name": name}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func newHTTPClient(timeout time.Duration, concurrency int) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        concurrency * 2,
			MaxIdleConnsPerHost: concurrency * 2,
			MaxConnsPerHost:     concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func ingestLogs(client *http.Client, baseURL, projectID, ingestKey string, baseTS time.Time, events, concurrency int) ([]ingestSample, int) {
	jobs := make(chan int)
	results := make(chan ingestSample, events)
	var wg sync.WaitGroup

	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				payload := syntheticLog(projectID, baseTS, index)
				body, err := json.Marshal(payload)
				if err != nil {
					results <- ingestSample{Err: err}
					continue
				}

				started := time.Now()
				err = postJSONBytes(client, baseURL+"/api/ingest", ingestKey, body)
				results <- ingestSample{
					Duration: time.Since(started),
					Bytes:    len(body),
					Err:      err,
				}
			}
		}()
	}

	go func() {
		for index := 0; index < events; index++ {
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	samples := make([]ingestSample, 0, events)
	failed := 0
	for sample := range results {
		samples = append(samples, sample)
		if sample.Err != nil {
			failed++
		}
	}

	return samples, failed
}

func syntheticLog(projectID string, baseTS time.Time, index int) eventEnvelope {
	level := "info"
	if index%20 == 0 {
		level = "error"
	} else if index%10 == 0 {
		level = "warn"
	}

	route := "/api/search"
	flow := "search"
	switch index % 4 {
	case 0:
		route = "/checkout"
		flow = "checkout"
	case 1:
		route = "/login"
		flow = "login"
	case 2:
		route = "/api/projects"
		flow = "projects"
	}

	name := "request.completed"
	if index%50 == 0 {
		name = "auth.failed"
	} else if index%25 == 0 {
		name = "db.query.slow"
	}

	return eventEnvelope{
		SchemaVersion: 1,
		ProjectID:     projectID,
		Kind:          "log",
		TS:            baseTS.Add(time.Duration(index) * time.Millisecond).Format(time.RFC3339Nano),
		Source:        fmt.Sprintf("bench-worker-%02d", index%16),
		Level:         level,
		Name:          name,
		Attrs: map[string]any{
			"route":       route,
			"flow":        flow,
			"region":      []string{"iad", "sfo", "bom", "fra"}[index%4],
			"status":      200 + (index % 5),
			"duration_ms": 20 + (index % 400),
			"customer_id": fmt.Sprintf("cust_%04d", index%1000),
			"benchmark":   true,
		},
		Body: map[string]any{
			"message": fmt.Sprintf("benchmark %s flow request %d completed", flow, index),
			"attempt": 1 + (index % 3),
		},
	}
}

func waitForIndexedTotal(client *http.Client, baseURL, projectID string, from, to time.Time, want int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	path := logsPath(projectID, from, to, url.Values{"limit": []string{"1"}})
	lastTotal := 0

	for {
		total, err := fetchLogTotal(client, baseURL, path)
		if err == nil {
			lastTotal = total
			if total == want {
				return total, nil
			}
		}
		if time.Now().After(deadline) {
			return lastTotal, fmt.Errorf("timed out waiting for indexed logs: got %d, want %d", lastTotal, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func buildQueryCases(projectID string, from, to time.Time, events int) []queryCase {
	errorCount := countWhere(events, func(index int) bool { return index%20 == 0 })
	checkoutCount := countWhere(events, func(index int) bool { return index%4 == 0 })
	authFailedCount := countWhere(events, func(index int) bool { return index%50 == 0 })

	return []queryCase{
		{
			Name:          "all recent logs",
			Path:          logsPath(projectID, from, to, url.Values{"limit": []string{"100"}}),
			ExpectedTotal: events,
		},
		{
			Name:          "level=error",
			Path:          logsPath(projectID, from, to, url.Values{"limit": []string{"100"}, "level": []string{"error"}}),
			ExpectedTotal: errorCount,
		},
		{
			Name:          "q=checkout",
			Path:          logsPath(projectID, from, to, url.Values{"limit": []string{"100"}, "q": []string{"checkout"}}),
			ExpectedTotal: checkoutCount,
		},
		{
			Name:          "name=auth.failed",
			Path:          logsPath(projectID, from, to, url.Values{"limit": []string{"100"}, "name": []string{"auth.failed"}}),
			ExpectedTotal: authFailedCount,
		},
	}
}

func runQueryBenchmarks(client *http.Client, baseURL string, queries []queryCase, warmupRuns, queryRuns int) []queryResult {
	results := make([]queryResult, 0, len(queries))
	for _, query := range queries {
		for i := 0; i < warmupRuns; i++ {
			_, _ = fetchLogTotal(client, baseURL, query.Path)
		}

		durations := make([]time.Duration, 0, queryRuns)
		observedTotal := 0
		var queryErr error
		for i := 0; i < queryRuns; i++ {
			started := time.Now()
			total, err := fetchLogTotal(client, baseURL, query.Path)
			elapsed := time.Since(started)
			if err != nil {
				queryErr = err
				break
			}
			observedTotal = total
			durations = append(durations, elapsed)
		}

		results = append(results, queryResult{
			Name:          query.Name,
			ExpectedTotal: query.ExpectedTotal,
			ObservedTotal: observedTotal,
			Stats:         summarizeDurations(durations),
			Err:           queryErr,
		})
	}
	return results
}

func logsPath(projectID string, from, to time.Time, extra url.Values) string {
	values := url.Values{}
	values.Set("project_id", projectID)
	values.Set("from", from.UTC().Format(time.RFC3339Nano))
	values.Set("to", to.UTC().Format(time.RFC3339Nano))
	for key, items := range extra {
		for _, item := range items {
			values.Add(key, item)
		}
	}
	return "/api/logs?" + values.Encode()
}

func countWhere(events int, fn func(int) bool) int {
	count := 0
	for i := 0; i < events; i++ {
		if fn(i) {
			count++
		}
	}
	return count
}

func fetchLogTotal(client *http.Client, baseURL, path string) (int, error) {
	var response logListResponse
	if err := requestJSON(client, http.MethodGet, baseURL+path, "", nil, &response); err != nil {
		return 0, err
	}
	return response.Total, nil
}

func requestJSON(client *http.Client, method, targetURL, bearer string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func postJSONBytes(client *http.Client, targetURL, bearer string, data []byte) error {
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return readErr
		}
		return responseError(resp.StatusCode, body)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func responseError(status int, body []byte) error {
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &failure); err == nil && strings.TrimSpace(failure.Error) != "" {
		return fmt.Errorf("%s", failure.Error)
	}
	return fmt.Errorf("unexpected status %d: %s", status, strings.TrimSpace(string(body)))
}

func summarizeIngest(samples []ingestSample) latencyStats {
	durations := make([]time.Duration, 0, len(samples))
	for _, sample := range samples {
		if sample.Err == nil {
			durations = append(durations, sample.Duration)
		}
	}
	return summarizeDurations(durations)
}

func summarizeDurations(durations []time.Duration) latencyStats {
	if len(durations) == 0 {
		return latencyStats{}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })

	var total time.Duration
	for _, duration := range durations {
		total += duration
	}

	return latencyStats{
		Count: len(durations),
		Avg:   total / time.Duration(len(durations)),
		P50:   percentile(durations, 0.50),
		P95:   percentile(durations, 0.95),
		P99:   percentile(durations, 0.99),
		Max:   durations[len(durations)-1],
	}
}

func percentile(sorted []time.Duration, pct float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * pct)
	return sorted[index]
}

func printIngestSummary(events, failed, totalBytes int, elapsed time.Duration, stats latencyStats) {
	successful := events - failed
	eventsPerSecond := float64(successful) / elapsed.Seconds()
	mbPerSecond := (float64(totalBytes) / 1024 / 1024) / elapsed.Seconds()
	fmt.Printf("Ingest summary:\n")
	fmt.Printf("  successful: %d/%d\n", successful, events)
	fmt.Printf("  elapsed:    %s\n", rounded(elapsed))
	fmt.Printf("  throughput: %.1f events/s, %.2f MiB/s\n", eventsPerSecond, mbPerSecond)
	fmt.Printf("  latency:    avg=%s p50=%s p95=%s p99=%s max=%s\n", rounded(stats.Avg), rounded(stats.P50), rounded(stats.P95), rounded(stats.P99), rounded(stats.Max))
}

func printIngestErrors(samples []ingestSample, limit int) {
	printed := 0
	fmt.Printf("  sampled failures:\n")
	for _, sample := range samples {
		if sample.Err == nil {
			continue
		}
		fmt.Printf("    - %s\n", sample.Err)
		printed++
		if printed >= limit {
			return
		}
	}
}

func printStorageSummary(summary storageSummary) {
	fmt.Printf("Storage summary:\n")
	fmt.Printf("  request payload: %s\n", byteSummary(summary.RequestPayloadBytes))
	fmt.Printf("  raw ndjson:      %d files, %s\n", summary.RawFileCount, byteSummary(summary.RawBytes))
	fmt.Printf("  sqlite db:       %s\n", byteSummary(summary.SQLiteBytes))
	fmt.Printf("  sqlite index:    %s\n", byteSummary(summary.IndexBytes))
	fmt.Printf("  data dir total:  %s\n", byteSummary(summary.DataDirBytes))
}

func printQuerySummary(results []queryResult) {
	fmt.Printf("Query summary:\n")
	for _, result := range results {
		status := "ok"
		if result.Err != nil {
			status = result.Err.Error()
		} else if result.ObservedTotal != result.ExpectedTotal {
			status = fmt.Sprintf("total mismatch: want %d", result.ExpectedTotal)
		}

		fmt.Printf("  %-18s total=%-6d avg=%-8s p50=%-8s p95=%-8s p99=%-8s max=%-8s %s\n",
			result.Name,
			result.ObservedTotal,
			rounded(result.Stats.Avg),
			rounded(result.Stats.P50),
			rounded(result.Stats.P95),
			rounded(result.Stats.P99),
			rounded(result.Stats.Max),
			status,
		)
	}
}

func rounded(duration time.Duration) string {
	if duration >= time.Second {
		return duration.Round(time.Millisecond).String()
	}
	if duration >= time.Millisecond {
		return duration.Round(100 * time.Microsecond).String()
	}
	return duration.Round(time.Microsecond).String()
}

func byteSummary(bytes int64) string {
	return fmt.Sprintf("%d bytes (%.2f MiB)", bytes, float64(bytes)/1024/1024)
}
