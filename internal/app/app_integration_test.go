package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"vigil/internal/config"
)

func TestProjectIngestAndQueries(t *testing.T) {
	cfg := config.Config{
		Addr:            ":0",
		DataDir:         t.TempDir(),
		MaxEventBytes:   1 << 20,
		SegmentMaxBytes: 10 * 1024 * 1024,
	}

	app, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	defer app.Close()

	server := httptest.NewServer(app.Handler)
	defer server.Close()

	createResp := map[string]any{}
	postJSON(t, server.URL+"/api/projects", map[string]string{"name": "integration-app"}, &createResp)

	project := createResp["project"].(map[string]any)
	projectID := project["id"].(string)
	key := createResp["ingest_key"].(string)

	payload := map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "trace",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "integration-test",
		"trace_id":       "trace-123",
		"level":          "info",
		"name":           "request.completed",
		"attrs": map[string]any{
			"route":        "/hello",
			"total_tokens": 12,
			"cost_usd":     0.5,
		},
		"body": map[string]any{"message": "ok"},
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new ingest request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ingest request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 from ingest, got %d", resp.StatusCode)
	}

	waitFor(t, 2*time.Second, func() bool {
		result := map[string]any{}
		getJSON(t, server.URL+"/api/logs?project_id="+projectID, &result)
		return int(result["total"].(float64)) == 1
	})

	logs := map[string]any{}
	getJSON(t, server.URL+"/api/logs?project_id="+projectID, &logs)
	if int(logs["total"].(float64)) != 1 {
		t.Fatalf("expected 1 log event, got %v", logs["total"])
	}

	traces := map[string]any{}
	getJSON(t, server.URL+"/api/traces?project_id="+projectID, &traces)
	if int(traces["total"].(float64)) != 1 {
		t.Fatalf("expected 1 trace, got %v", traces["total"])
	}

	stats := map[string]any{}
	getJSON(t, server.URL+"/api/stats?project_id="+projectID, &stats)
	if int(stats["total_events"].(float64)) != 1 {
		t.Fatalf("expected 1 total event in stats, got %v", stats["total_events"])
	}

	regenerated := map[string]any{}
	postJSON(t, server.URL+"/api/projects/"+projectID+"/keys/regenerate", nil, &regenerated)

	reqOld, err := http.NewRequest(http.MethodPost, server.URL+"/api/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new old-key request: %v", err)
	}
	reqOld.Header.Set("Authorization", "Bearer "+key)
	reqOld.Header.Set("Content-Type", "application/json")
	respOld, err := http.DefaultClient.Do(reqOld)
	if err != nil {
		t.Fatalf("old-key request: %v", err)
	}
	respOld.Body.Close()
	if respOld.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for old key, got %d", respOld.StatusCode)
	}
}

func TestWarningsAndRetentionFlow(t *testing.T) {
	cfg := config.Config{
		Addr:            ":0",
		DataDir:         t.TempDir(),
		MaxEventBytes:   1 << 20,
		SegmentMaxBytes: 10 * 1024 * 1024,
		Retention: config.RetentionConfig{
			Enabled:       true,
			Days:          3,
			SweepInterval: time.Hour,
			DryRun:        false,
		},
	}

	app, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	defer app.Close()

	server := httptest.NewServer(app.Handler)
	defer server.Close()

	createResp := map[string]any{}
	postJSON(t, server.URL+"/api/projects", map[string]string{"name": "retention-app"}, &createResp)
	project := createResp["project"].(map[string]any)
	projectID := project["id"].(string)
	key := createResp["ingest_key"].(string)

	postIngest(t, server.URL, key, map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "log",
		"ts":             time.Now().UTC().AddDate(0, 0, -10).Format(time.RFC3339),
		"source":         "integration-test",
		"level":          "error",
		"name":           "old.event",
		"body":           map[string]any{"message": "old"},
	})

	for i := 0; i < 3; i++ {
		postIngest(t, server.URL, key, map[string]any{
			"schema_version": 1,
			"project_id":     projectID,
			"kind":           "trace",
			"ts":             time.Now().UTC().Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"source":         "integration-test",
			"trace_id":       "trace-live-" + string(rune('A'+i)),
			"level":          "info",
			"name":           "live.event",
			"body":           map[string]any{"message": "live"},
		})
	}

	waitFor(t, 2*time.Second, func() bool {
		result := map[string]any{}
		getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&from="+url.QueryEscape(time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339))+"&to="+url.QueryEscape(time.Now().UTC().Add(time.Hour).Format(time.RFC3339)), &result)
		return int(result["total"].(float64)) == 4
	})

	logs := map[string]any{}
	getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&limit=1&from="+url.QueryEscape(time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339))+"&to="+url.QueryEscape(time.Now().UTC().Add(time.Hour).Format(time.RFC3339)), &logs)
	warnings, ok := logs["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatal("expected logs warnings for partial results")
	}

	capped := map[string]any{}
	getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&limit=999&from="+url.QueryEscape(time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339))+"&to="+url.QueryEscape(time.Now().UTC().Add(time.Hour).Format(time.RFC3339)), &capped)
	capWarnings, ok := capped["warnings"].([]any)
	if !ok || len(capWarnings) == 0 {
		t.Fatal("expected logs warnings for capped results")
	}

	if err := app.RunRetentionNow(context.Background()); err != nil {
		t.Fatalf("run retention: %v", err)
	}

	postRetention := map[string]any{}
	getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&from="+url.QueryEscape(time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339))+"&to="+url.QueryEscape(time.Now().UTC().Add(time.Hour).Format(time.RFC3339)), &postRetention)
	if int(postRetention["total"].(float64)) != 3 {
		t.Fatalf("expected retention to prune old event, got total %v", postRetention["total"])
	}

	health := map[string]any{}
	getJSON(t, server.URL+"/api/health", &health)
	retentionStatus, ok := health["retention"].(map[string]any)
	if !ok {
		t.Fatal("expected retention status in health response")
	}
	if retentionStatus["enabled"] != true {
		t.Fatalf("expected retention enabled, got %v", retentionStatus["enabled"])
	}
	if retentionStatus["last_success_at"] == "" {
		t.Fatal("expected retention success timestamp")
	}
}

func TestProjectLogFiltersAgainstSeededProject(t *testing.T) {
	cfg := config.Config{
		Addr:            ":0",
		DataDir:         t.TempDir(),
		MaxEventBytes:   1 << 20,
		SegmentMaxBytes: 10 * 1024 * 1024,
	}

	app, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	defer app.Close()

	server := httptest.NewServer(app.Handler)
	defer server.Close()

	projectID, key := createProjectForTest(t, server.URL, "filters-app")
	baseTS := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)

	events := []map[string]any{
		{
			"schema_version": 1,
			"project_id":     projectID,
			"kind":           "log",
			"ts":             baseTS.Add(-5 * time.Minute).Format(time.RFC3339),
			"source":         "api",
			"level":          "info",
			"name":           "agent.run.started",
			"attrs": map[string]any{
				"route": "/chat",
				"team":  "alpha",
			},
			"body": map[string]any{"message": "Agent booted for customer alpha"},
		},
		{
			"schema_version": 1,
			"project_id":     projectID,
			"kind":           "log",
			"ts":             baseTS.Add(-4 * time.Minute).Format(time.RFC3339),
			"source":         "worker",
			"level":          "error",
			"name":           "agent.run.failed",
			"attrs": map[string]any{
				"job": "cleanup",
			},
			"body": map[string]any{"message": "Invoice sync failed after cleanup retry"},
		},
		{
			"schema_version": 1,
			"project_id":     projectID,
			"kind":           "trace",
			"ts":             baseTS.Add(-3 * time.Minute).Format(time.RFC3339),
			"source":         "api",
			"trace_id":       "trace-123",
			"span_id":        "span-root",
			"level":          "info",
			"name":           "request.completed",
			"body":           map[string]any{"message": "Request completed"},
		},
		{
			"schema_version": 1,
			"project_id":     projectID,
			"kind":           "metric",
			"ts":             baseTS.Add(-2 * time.Minute).Format(time.RFC3339),
			"source":         "worker",
			"level":          "warn",
			"name":           "queue.depth",
			"attrs": map[string]any{
				"value": 7,
				"unit":  "count",
			},
			"body": map[string]any{"message": "Queue depth snapshot"},
		},
		{
			"schema_version": 1,
			"project_id":     projectID,
			"kind":           "log",
			"ts":             baseTS.Add(-1 * time.Minute).Format(time.RFC3339),
			"source":         "mailer",
			"level":          "warn",
			"name":           "billing.invoice.sent",
			"attrs": map[string]any{
				"channel": "email",
			},
			"body": map[string]any{"message": "Invoice delivered to customer"},
		},
	}

	for _, payload := range events {
		postIngest(t, server.URL, key, payload)
	}

	from := url.QueryEscape(baseTS.Add(-10 * time.Minute).Format(time.RFC3339))
	to := url.QueryEscape(baseTS.Add(1 * time.Minute).Format(time.RFC3339))
	baseLogsURL := server.URL + "/api/logs?project_id=" + projectID + "&from=" + from + "&to=" + to

	waitFor(t, 2*time.Second, func() bool {
		result := map[string]any{}
		getJSON(t, baseLogsURL, &result)
		return int(result["total"].(float64)) == 5
	})

	assertLogTotal(t, baseLogsURL+"&kind=log", 3)
	assertLogTotal(t, baseLogsURL+"&kind=trace", 1)
	assertLogTotal(t, baseLogsURL+"&level=error", 1)
	assertLogTotal(t, baseLogsURL+"&name=agent.run.failed", 1)
	assertLogTotal(t, baseLogsURL+"&name=cleanup", 0)
	assertLogTotal(t, baseLogsURL+"&q=cleanup", 1)
	assertLogTotal(t, baseLogsURL+"&q=mailer", 1)
	assertLogTotal(t, baseLogsURL+"&q=invoice", 2)
	assertLogTotal(t, baseLogsURL+"&query="+url.QueryEscape(`level = "error" && source = "worker"`), 1)
	assertLogTotal(t, baseLogsURL+"&query="+url.QueryEscape(`attrs.route = "/chat" || attrs.value >= 7`), 2)
	assertLogTotal(t, baseLogsURL+"&q=invoice&query="+url.QueryEscape(`body.message ~= "customer"`), 1)

	narrowFrom := url.QueryEscape(baseTS.Add(-150 * time.Second).Format(time.RFC3339))
	narrowTo := url.QueryEscape(baseTS.Add(1 * time.Minute).Format(time.RFC3339))
	assertLogTotal(t, server.URL+"/api/logs?project_id="+projectID+"&from="+narrowFrom+"&to="+narrowTo, 2)

	invalidQuery := map[string]any{}
	resp, err := http.Get(baseLogsURL + "&query=" + url.QueryEscape(`level = error`))
	if err != nil {
		t.Fatalf("invalid query request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid query status 400, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&invalidQuery); err != nil {
		t.Fatalf("decode invalid query response: %v", err)
	}
	if invalidQuery["error"] != "invalid query" {
		t.Fatalf("expected invalid query error, got %v", invalidQuery)
	}
	if _, ok := invalidQuery["query_error"].(map[string]any); !ok {
		t.Fatalf("expected query_error object, got %v", invalidQuery)
	}

	pageOne := map[string]any{}
	getJSON(t, baseLogsURL+"&limit=2&page=1", &pageOne)
	if int(pageOne["total"].(float64)) != 5 {
		t.Fatalf("expected total 5 for paginated logs, got %v", pageOne["total"])
	}
	eventsPageOne, ok := pageOne["events"].([]any)
	if !ok || len(eventsPageOne) != 2 {
		t.Fatalf("expected 2 events on page 1, got %v", pageOne["events"])
	}
	firstEvent := eventsPageOne[0].(map[string]any)
	if got := firstEvent["name"]; got != "billing.invoice.sent" {
		t.Fatalf("expected newest page-1 event billing.invoice.sent, got %v", got)
	}

	pageTwo := map[string]any{}
	getJSON(t, baseLogsURL+"&limit=2&page=2", &pageTwo)
	eventsPageTwo, ok := pageTwo["events"].([]any)
	if !ok || len(eventsPageTwo) != 2 {
		t.Fatalf("expected 2 events on page 2, got %v", pageTwo["events"])
	}
	firstPageTwoEvent := eventsPageTwo[0].(map[string]any)
	if got := firstPageTwoEvent["name"]; got != "request.completed" {
		t.Fatalf("expected page-2 first event request.completed, got %v", got)
	}
}

func createProjectForTest(t *testing.T, baseURL, name string) (string, string) {
	t.Helper()
	createResp := map[string]any{}
	postJSON(t, baseURL+"/api/projects", map[string]string{"name": name}, &createResp)
	project := createResp["project"].(map[string]any)
	return project["id"].(string), createResp["ingest_key"].(string)
}

func assertLogTotal(t *testing.T, requestURL string, want int) {
	t.Helper()
	result := map[string]any{}
	getJSON(t, requestURL, &result)
	got := int(result["total"].(float64))
	if got != want {
		t.Fatalf("GET %s expected total %d, got %d", requestURL, want, got)
	}
}

func postIngest(t *testing.T, baseURL, key string, payload map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/ingest", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new ingest request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ingest request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 from ingest, got %d", resp.StatusCode)
	}
}

func postJSON(t *testing.T, url string, payload any, target any) {
	t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	} else {
		body = []byte("{}")
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		t.Fatalf("POST %s returned status %d", url, resp.StatusCode)
	}
	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("decode response from %s: %v", url, err)
		}
	}
}

func getJSON(t *testing.T, url string, target any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		t.Fatalf("GET %s returned status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode response from %s: %v", url, err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
