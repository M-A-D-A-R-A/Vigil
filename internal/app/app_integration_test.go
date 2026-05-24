package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

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

func TestBrowserSafeIngestKeys(t *testing.T) {
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

	projectID, privateKey := createProjectForTest(t, server.URL, "browser-app")
	createBrowser := map[string]any{}
	postJSON(t, server.URL+"/api/projects/"+projectID+"/browser-keys", map[string]any{
		"name":            "web",
		"allowed_origins": []string{"http://localhost:3000", "https://app.example.test/"},
	}, &createBrowser)

	browserKey := createBrowser["browser_ingest_key"].(string)
	if !strings.HasPrefix(browserKey, "vigil_browser_") {
		t.Fatalf("expected browser key prefix, got %q", browserKey)
	}
	keyRecord := createBrowser["key"].(map[string]any)
	keyID := keyRecord["id"].(string)
	if _, ok := keyRecord["key_hash"]; ok {
		t.Fatal("browser key response must not expose key hash")
	}

	listed := map[string]any{}
	getJSON(t, server.URL+"/api/projects/"+projectID+"/browser-keys", &listed)
	keys := listed["browser_keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("expected one listed browser key, got %v", keys)
	}
	if _, ok := keys[0].(map[string]any)["browser_ingest_key"]; ok {
		t.Fatal("listed browser keys must not expose plaintext key")
	}

	preflight := newRequest(t, http.MethodOptions, server.URL+"/api/browser/ingest", nil)
	preflight.Header.Set("Origin", "http://localhost:3000")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	preflightResp := doRequest(t, preflight)
	preflightResp.Body.Close()
	if preflightResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected allowed preflight 204, got %d", preflightResp.StatusCode)
	}
	if got := preflightResp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("expected CORS allow origin, got %q", got)
	}

	blockedPreflight := newRequest(t, http.MethodOptions, server.URL+"/api/browser/ingest", nil)
	blockedPreflight.Header.Set("Origin", "https://evil.example")
	blockedResp := doRequest(t, blockedPreflight)
	blockedResp.Body.Close()
	if blockedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected blocked preflight 403, got %d", blockedResp.StatusCode)
	}

	payload := map[string]any{
		"schema_version": 1,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "browser",
		"level":          "error",
		"name":           "frontend.error",
		"attrs":          map[string]any{"path": "/checkout"},
		"body":           map[string]any{"message": "client exploded"},
	}
	if status := postBrowserIngestStatus(t, server.URL, browserKey, "http://localhost:3000", payload); status != http.StatusAccepted {
		t.Fatalf("expected browser ingest 202, got %d", status)
	}

	waitFor(t, 2*time.Second, func() bool {
		result := map[string]any{}
		getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&source=browser", &result)
		return int(result["total"].(float64)) == 1
	})

	if status := postBrowserIngestStatus(t, server.URL, privateKey, "http://localhost:3000", payload); status != http.StatusUnauthorized {
		t.Fatalf("expected private key rejected by browser endpoint with 401, got %d", status)
	}
	serverPayload := map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "browser",
		"name":           "native.rejected",
	}
	if status := postNativeIngestStatus(t, server.URL, browserKey, serverPayload); status != http.StatusUnauthorized {
		t.Fatalf("expected browser key rejected by native ingest with 401, got %d", status)
	}
	if status := postBrowserIngestStatus(t, server.URL, browserKey, "https://evil.example", payload); status != http.StatusForbidden {
		t.Fatalf("expected blocked origin 403, got %d", status)
	}

	rotated := map[string]any{}
	postJSON(t, server.URL+"/api/projects/"+projectID+"/browser-keys/"+keyID+"/rotate", nil, &rotated)
	rotatedKey := rotated["browser_ingest_key"].(string)
	if rotatedKey == browserKey {
		t.Fatal("expected rotated browser key to change")
	}
	if status := postBrowserIngestStatus(t, server.URL, browserKey, "http://localhost:3000", payload); status != http.StatusUnauthorized {
		t.Fatalf("expected old browser key rejected after rotate with 401, got %d", status)
	}
	if status := postBrowserIngestStatus(t, server.URL, rotatedKey, "http://localhost:3000", payload); status != http.StatusAccepted {
		t.Fatalf("expected rotated browser key accepted, got %d", status)
	}

	revoked := map[string]any{}
	postJSON(t, server.URL+"/api/projects/"+projectID+"/browser-keys/"+keyID+"/revoke", nil, &revoked)
	if revoked["key"].(map[string]any)["status"] != "revoked" {
		t.Fatalf("expected revoked status, got %v", revoked)
	}
	if status := postBrowserIngestStatus(t, server.URL, rotatedKey, "http://localhost:3000", payload); status != http.StatusUnauthorized {
		t.Fatalf("expected revoked browser key rejected with 401, got %d", status)
	}
}

func TestOTLPHTTPIngestEndpoints(t *testing.T) {
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
	postJSON(t, server.URL+"/api/projects", map[string]string{"name": "otlp-app"}, &createResp)
	project := createResp["project"].(map[string]any)
	projectID := project["id"].(string)
	key := createResp["ingest_key"].(string)

	baseTS := time.Now().UTC().Add(-time.Minute)
	logTraceID := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	logSpanID := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	postOTLPProto(t, server.URL+"/v1/logs", key, &logsv1.LogsData{
		ResourceLogs: []*logsv1.ResourceLogs{{
			Resource: otelResource("checkout-api"),
			ScopeLogs: []*logsv1.ScopeLogs{{
				Scope: &commonv1.InstrumentationScope{Name: "test-logger", Version: "1.0.0"},
				LogRecords: []*logsv1.LogRecord{{
					TimeUnixNano:   uint64(baseTS.UnixNano()),
					SeverityText:   "ERROR",
					SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR,
					EventName:      "payment.failed",
					TraceId:        logTraceID,
					SpanId:         logSpanID,
					Body:           stringAny("payment failed"),
					Attributes: []*commonv1.KeyValue{
						stringKV("route", "/checkout"),
						intKV("status", 502),
					},
				}},
			}},
		}},
	})

	traceID := []byte{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}
	spanID := []byte{8, 7, 6, 5, 4, 3, 2, 1}
	postOTLPProto(t, server.URL+"/v1/traces", key, &tracev1.TracesData{
		ResourceSpans: []*tracev1.ResourceSpans{{
			Resource: otelResource("checkout-api"),
			ScopeSpans: []*tracev1.ScopeSpans{{
				Scope: &commonv1.InstrumentationScope{Name: "test-tracer", Version: "1.0.0"},
				Spans: []*tracev1.Span{{
					TraceId:           traceID,
					SpanId:            spanID,
					Name:              "POST /checkout",
					Kind:              tracev1.Span_SPAN_KIND_SERVER,
					StartTimeUnixNano: uint64(baseTS.Add(time.Second).UnixNano()),
					EndTimeUnixNano:   uint64(baseTS.Add(150 * time.Millisecond).Add(time.Second).UnixNano()),
					Attributes: []*commonv1.KeyValue{
						stringKV("http.route", "/checkout"),
					},
					Status: &tracev1.Status{
						Code:    tracev1.Status_STATUS_CODE_ERROR,
						Message: "upstream payment failed",
					},
				}},
			}},
		}},
	})

	postOTLPProto(t, server.URL+"/v1/metrics", key, &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: otelResource("checkout-api"),
			ScopeMetrics: []*metricsv1.ScopeMetrics{{
				Scope: &commonv1.InstrumentationScope{Name: "test-meter", Version: "1.0.0"},
				Metrics: []*metricsv1.Metric{{
					Name: "checkout.requests",
					Unit: "1",
					Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
						DataPoints: []*metricsv1.NumberDataPoint{{
							TimeUnixNano: uint64(baseTS.Add(2 * time.Second).UnixNano()),
							Attributes: []*commonv1.KeyValue{
								stringKV("route", "/checkout"),
							},
							Value: &metricsv1.NumberDataPoint_AsInt{AsInt: 42},
						}},
					}},
				}},
			}},
		}},
	})

	waitFor(t, 2*time.Second, func() bool {
		result := map[string]any{}
		getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&kind=metric", &result)
		return int(result["total"].(float64)) == 1
	})

	logs := map[string]any{}
	getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&kind=log", &logs)
	if int(logs["total"].(float64)) != 1 {
		t.Fatalf("expected 1 OTLP log event, got %v", logs["total"])
	}
	logEvent := logs["events"].([]any)[0].(map[string]any)
	if logEvent["source"] != "checkout-api" {
		t.Fatalf("expected service.name source, got %v", logEvent["source"])
	}
	if logEvent["level"] != "error" {
		t.Fatalf("expected error level, got %v", logEvent["level"])
	}
	if logEvent["trace_id"] != "000102030405060708090a0b0c0d0e0f" {
		t.Fatalf("expected preserved log trace id, got %v", logEvent["trace_id"])
	}
	logBody := logEvent["body"].(map[string]any)
	if logBody["message"] != "payment failed" {
		t.Fatalf("expected OTLP log body message, got %v", logBody)
	}
	logAttrs := logEvent["attrs"].(map[string]any)
	if logAttrs["route"] != "/checkout" {
		t.Fatalf("expected promoted log attrs, got %v", logAttrs)
	}

	traces := map[string]any{}
	getJSON(t, server.URL+"/api/traces?project_id="+projectID, &traces)
	if int(traces["total"].(float64)) != 2 {
		t.Fatalf("expected log and span trace summaries, got %v", traces["total"])
	}

	metrics := map[string]any{}
	getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&kind=metric", &metrics)
	metricEvent := metrics["events"].([]any)[0].(map[string]any)
	if metricEvent["name"] != "checkout.requests" {
		t.Fatalf("expected metric name, got %v", metricEvent["name"])
	}
	metricBody := metricEvent["body"].(map[string]any)
	if metricBody["value"] != float64(42) {
		t.Fatalf("expected metric value 42, got %v", metricBody)
	}

	stats := map[string]any{}
	getJSON(t, server.URL+"/api/stats?project_id="+projectID, &stats)
	if int(stats["total_events"].(float64)) != 3 {
		t.Fatalf("expected 3 total OTLP-backed events, got %v", stats["total_events"])
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

func TestLogTailStreamsMatchingEvents(t *testing.T) {
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

	projectID, key := createProjectForTest(t, server.URL, "tail-app")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/logs/tail?project_id="+projectID+"&level=error", nil)
	if err != nil {
		t.Fatalf("new tail request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tail request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from tail endpoint, got %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", contentType)
	}

	postIngest(t, server.URL, key, map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "tail-test",
		"level":          "info",
		"name":           "tail.skip",
		"body":           map[string]any{"message": "skip"},
	})
	postIngest(t, server.URL, key, map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "tail-test",
		"level":          "error",
		"name":           "tail.match",
		"body":           map[string]any{"message": "match"},
	})

	payload := readSSEPayload(t, bufio.NewReader(resp.Body), "log")
	if got := payload["name"]; got != "tail.match" {
		t.Fatalf("expected tail.match event, got %v", got)
	}
	if got := payload["level"]; got != "error" {
		t.Fatalf("expected error level, got %v", got)
	}
}

func TestLogTailBackfillsAfterCursor(t *testing.T) {
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

	projectID, key := createProjectForTest(t, server.URL, "tail-catchup-app")
	first := postIngestResult(t, server.URL, key, map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "log",
		"ts":             time.Now().UTC().Add(-time.Second).Format(time.RFC3339),
		"source":         "tail-test",
		"level":          "error",
		"name":           "tail.first",
		"body":           map[string]any{"message": "first"},
	})
	postIngestResult(t, server.URL, key, map[string]any{
		"schema_version": 1,
		"project_id":     projectID,
		"kind":           "log",
		"ts":             time.Now().UTC().Format(time.RFC3339),
		"source":         "tail-test",
		"level":          "error",
		"name":           "tail.second",
		"body":           map[string]any{"message": "second"},
	})

	waitFor(t, 2*time.Second, func() bool {
		result := map[string]any{}
		getJSON(t, server.URL+"/api/logs?project_id="+projectID+"&level=error", &result)
		return int(result["total"].(float64)) == 2
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/logs/tail?project_id="+projectID+"&level=error&after="+first["event_id"].(string), nil)
	if err != nil {
		t.Fatalf("new tail request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("tail request: %v", err)
	}
	defer resp.Body.Close()

	payload := readSSEPayload(t, bufio.NewReader(resp.Body), "log")
	if got := payload["name"]; got != "tail.second" {
		t.Fatalf("expected tail.second catchup event, got %v", got)
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
	postIngestResult(t, baseURL, key, payload)
}

func postIngestResult(t *testing.T, baseURL, key string, payload map[string]any) map[string]any {
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
	result := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode ingest response: %v", err)
	}
	return result
}

func postNativeIngestStatus(t *testing.T, baseURL, key string, payload map[string]any) int {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := newRequest(t, http.MethodPost, baseURL+"/api/ingest", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp := doRequest(t, req)
	defer resp.Body.Close()
	return resp.StatusCode
}

func postBrowserIngestStatus(t *testing.T, baseURL, key, origin string, payload map[string]any) int {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := newRequest(t, http.MethodPost, baseURL+"/api/browser/ingest", body)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp := doRequest(t, req)
	defer resp.Body.Close()
	return resp.StatusCode
}

func postOTLPProto(t *testing.T, requestURL, key string, payload proto.Message) {
	t.Helper()
	body, err := proto.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal OTLP payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new OTLP request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OTLP request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from OTLP ingest, got %d", resp.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse OTLP response content type: %v", err)
	}
	if mediaType != "application/x-protobuf" {
		t.Fatalf("expected protobuf response, got %q", resp.Header.Get("Content-Type"))
	}
}

func newRequest(t *testing.T, method, requestURL string, body []byte) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, requestURL, reader)
	if err != nil {
		t.Fatalf("new %s request %s: %v", method, requestURL, err)
	}
	return req
}

func doRequest(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	return resp
}

func otelResource(serviceName string) *resourcev1.Resource {
	return &resourcev1.Resource{
		Attributes: []*commonv1.KeyValue{
			stringKV("service.name", serviceName),
		},
	}
}

func stringKV(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{
		Key: key,
		Value: &commonv1.AnyValue{
			Value: &commonv1.AnyValue_StringValue{StringValue: value},
		},
	}
}

func intKV(key string, value int64) *commonv1.KeyValue {
	return &commonv1.KeyValue{
		Key: key,
		Value: &commonv1.AnyValue{
			Value: &commonv1.AnyValue_IntValue{IntValue: value},
		},
	}
}

func stringAny(value string) *commonv1.AnyValue {
	return &commonv1.AnyValue{
		Value: &commonv1.AnyValue_StringValue{StringValue: value},
	}
}

func readSSEPayload(t *testing.T, reader *bufio.Reader, wantEvent string) map[string]any {
	t.Helper()

	currentEvent := ""
	currentData := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE line: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if currentEvent == wantEvent && currentData != "" {
				payload := map[string]any{}
				if err := json.Unmarshal([]byte(currentData), &payload); err != nil {
					t.Fatalf("decode SSE payload: %v", err)
				}
				return payload
			}
			currentEvent = ""
			currentData = ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			currentData = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
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
