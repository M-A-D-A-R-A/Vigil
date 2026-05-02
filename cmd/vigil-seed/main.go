package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type listProjectsResponse struct {
	Projects []project `json:"projects"`
}

type regenerateKeyResponse struct {
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

func main() {
	addr := flag.String("addr", "http://localhost:8080", "Vigil server base URL")
	projectID := flag.String("project-id", "", "Project ID to seed")
	projectName := flag.String("project-name", "", "Project name to seed when project ID is not provided")
	ingestKey := flag.String("ingest-key", "", "Existing ingest key to use instead of rotating one")
	flag.Parse()

	baseURL := strings.TrimRight(strings.TrimSpace(*addr), "/")
	if baseURL == "" {
		exitf("addr is required")
	}

	client := &http.Client{Timeout: 15 * time.Second}

	targetProject, err := resolveProject(client, baseURL, strings.TrimSpace(*projectID), strings.TrimSpace(*projectName))
	if err != nil {
		exitf("%v", err)
	}

	key := strings.TrimSpace(*ingestKey)
	rotated := false
	if key == "" {
		result, err := regenerateKey(client, baseURL, targetProject.ID)
		if err != nil {
			exitf("regenerate ingest key: %v", err)
		}
		key = result.IngestKey
		rotated = true
	}

	events := sampleEvents(targetProject.ID, time.Now().UTC())
	counts := map[string]int{}
	for _, event := range events {
		if err := ingestEvent(client, baseURL, key, event); err != nil {
			exitf("ingest %s/%s: %v", event.Kind, event.Name, err)
		}
		counts[event.Kind]++
	}

	fmt.Printf("Seeded %d events into %s (%s)\n", len(events), targetProject.Name, targetProject.ID)
	fmt.Printf("  logs:   %d\n", counts["log"])
	fmt.Printf("  traces: %d\n", counts["trace"])
	fmt.Printf("  metric: %d\n", counts["metric"])
	if rotated {
		fmt.Printf("Rotated ingest key for %s\n", targetProject.ID)
		fmt.Printf("New ingest key: %s\n", key)
	}
	fmt.Printf("Open logs:   %s/projects/%s/logs\n", baseURL, targetProject.ID)
	fmt.Printf("Open traces: %s/projects/%s/traces\n", baseURL, targetProject.ID)
	fmt.Printf("Open stats:  %s/projects/%s/stats\n", baseURL, targetProject.ID)
}

func resolveProject(client *http.Client, baseURL, wantedID, wantedName string) (project, error) {
	var response listProjectsResponse
	if err := requestJSON(client, http.MethodGet, baseURL+"/api/projects", "", nil, &response); err != nil {
		return project{}, fmt.Errorf("list projects: %w", err)
	}
	if len(response.Projects) == 0 {
		return project{}, fmt.Errorf("no projects found; create one in Vigil first")
	}

	if wantedID != "" {
		for _, item := range response.Projects {
			if item.ID == wantedID {
				return item, nil
			}
		}
		return project{}, fmt.Errorf("project %s not found", wantedID)
	}

	if wantedName != "" {
		for _, item := range response.Projects {
			if strings.EqualFold(item.Name, wantedName) {
				return item, nil
			}
		}
		return project{}, fmt.Errorf("project named %q not found", wantedName)
	}

	return response.Projects[0], nil
}

func regenerateKey(client *http.Client, baseURL, projectID string) (*regenerateKeyResponse, error) {
	var response regenerateKeyResponse
	if err := requestJSON(client, http.MethodPost, baseURL+"/api/projects/"+projectID+"/keys/regenerate", "", nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func ingestEvent(client *http.Client, baseURL, ingestKey string, payload eventEnvelope) error {
	return requestJSON(client, http.MethodPost, baseURL+"/api/ingest", ingestKey, payload, nil)
}

func requestJSON(client *http.Client, method, url, bearer string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, body)
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
		var failure struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &failure); err == nil && strings.TrimSpace(failure.Error) != "" {
			return fmt.Errorf("%s", failure.Error)
		}
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	if out == nil || len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, out); err != nil {
		return err
	}
	return nil
}

func sampleEvents(projectID string, now time.Time) []eventEnvelope {
	at := func(offset time.Duration) string {
		return now.Add(offset).UTC().Format(time.RFC3339)
	}

	return []eventEnvelope{
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-4*time.Minute - 40*time.Second),
			Source:        "api-gateway",
			Level:         "info",
			Name:          "request.received",
			Attrs: map[string]any{
				"route":  "/api/search",
				"method": "GET",
				"region": "bom1",
			},
			Body: map[string]any{
				"message": "Search request accepted",
				"status":  202,
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-4*time.Minute - 10*time.Second),
			Source:        "api-gateway",
			Level:         "warn",
			Name:          "cache.miss",
			Attrs: map[string]any{
				"route":     "/api/search",
				"cache_key": "search:trending",
			},
			Body: map[string]any{
				"message": "Cache miss forced a backing store read",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-3*time.Minute - 35*time.Second),
			Source:        "worker-sync",
			Level:         "info",
			Name:          "job.started",
			Attrs: map[string]any{
				"queue":   "sync",
				"job_id":  "job_1842",
				"attempt": 1,
			},
			Body: map[string]any{
				"message": "Project sync worker started",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-3*time.Minute - 5*time.Second),
			Source:        "worker-sync",
			Level:         "error",
			Name:          "db.query.failed",
			Attrs: map[string]any{
				"query":     "select reports by account_id",
				"retriable": true,
			},
			Body: map[string]any{
				"message": "Primary read replica timed out",
				"timeout": "850ms",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-2*time.Minute - 45*time.Second),
			Source:        "agent-runner",
			Level:         "info",
			Name:          "llm.response.received",
			Attrs: map[string]any{
				"model":             "gpt-5.4-mini",
				"prompt_tokens":     842,
				"completion_tokens": 231,
				"cost_usd":          0.0142,
			},
			Body: map[string]any{
				"message": "Generated summary for the incident timeline",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-2*time.Minute - 15*time.Second),
			Source:        "edge-auth",
			Level:         "warn",
			Name:          "auth.rate_limited",
			Attrs: map[string]any{
				"tenant":      "acme",
				"limit":       120,
				"window_secs": 60,
			},
			Body: map[string]any{
				"message": "Burst traffic crossed the login protection threshold",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "log",
			TS:            at(-90 * time.Second),
			Source:        "queue-consumer",
			Level:         "error",
			Name:          "deadletter.appended",
			Attrs: map[string]any{
				"queue":   "email",
				"message": "msg_991",
			},
			Body: map[string]any{
				"message": "Event moved to dead letter queue after max retries",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "trace",
			TS:            at(-4*time.Minute - 25*time.Second),
			Source:        "api-gateway",
			TraceID:       "trace_checkout_1001",
			SpanID:        "span_checkout_root",
			Level:         "info",
			Name:          "checkout.request.received",
			Attrs: map[string]any{
				"route":    "/checkout",
				"customer": "cust_184",
			},
			Body: map[string]any{
				"message": "Checkout started",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "trace",
			TS:            at(-4*time.Minute - 5*time.Second),
			Source:        "payment-service",
			TraceID:       "trace_checkout_1001",
			SpanID:        "span_payment_auth",
			Level:         "info",
			Name:          "payment.authorized",
			Attrs: map[string]any{
				"provider": "stripe",
				"amount":   72.50,
			},
			Body: map[string]any{
				"message": "Card authorization completed",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "trace",
			TS:            at(-3*time.Minute - 50*time.Second),
			Source:        "email-service",
			TraceID:       "trace_checkout_1001",
			SpanID:        "span_receipt_email",
			Level:         "info",
			Name:          "receipt.queued",
			Attrs: map[string]any{
				"template": "purchase_receipt",
			},
			Body: map[string]any{
				"message": "Receipt email queued",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "trace",
			TS:            at(-2*time.Minute - 55*time.Second),
			Source:        "scheduler",
			TraceID:       "trace_backup_1002",
			SpanID:        "span_backup_root",
			Level:         "info",
			Name:          "backup.started",
			Attrs: map[string]any{
				"target": "s3://nightly-backups",
			},
			Body: map[string]any{
				"message": "Nightly backup kicked off",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "trace",
			TS:            at(-2*time.Minute - 25*time.Second),
			Source:        "storage-worker",
			TraceID:       "trace_backup_1002",
			SpanID:        "span_chunk_upload",
			Level:         "warn",
			Name:          "backup.chunk.retried",
			Attrs: map[string]any{
				"chunk":   17,
				"attempt": 2,
			},
			Body: map[string]any{
				"message": "Chunk upload retried after transient network error",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "trace",
			TS:            at(-2*time.Minute + 5*time.Second),
			Source:        "storage-worker",
			TraceID:       "trace_backup_1002",
			SpanID:        "span_backup_finalize",
			Level:         "info",
			Name:          "backup.completed",
			Attrs: map[string]any{
				"size_mb": 184,
			},
			Body: map[string]any{
				"message": "Backup completed successfully",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "metric",
			TS:            at(-105 * time.Second),
			Source:        "api-gateway",
			Name:          "requests.per_minute",
			Attrs: map[string]any{
				"route":  "/api/search",
				"tenant": "acme",
			},
			Body: map[string]any{
				"value":   182,
				"message": "Current per-minute request volume",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "metric",
			TS:            at(-75 * time.Second),
			Source:        "api-gateway",
			Name:          "latency.p95_ms",
			Attrs: map[string]any{
				"route": "/api/search",
			},
			Body: map[string]any{
				"value":   248,
				"message": "P95 latency for the last one-minute window",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "metric",
			TS:            at(-50 * time.Second),
			Source:        "agent-runner",
			Name:          "model.usage",
			Attrs: map[string]any{
				"model":        "gpt-5.4-mini",
				"total_tokens": 1536,
				"cost_usd":     0.0218,
			},
			Body: map[string]any{
				"message": "Observed model usage in the last batch",
			},
		},
		{
			SchemaVersion: 1,
			ProjectID:     projectID,
			Kind:          "metric",
			TS:            at(-20 * time.Second),
			Source:        "queue-consumer",
			Name:          "queue.depth",
			Attrs: map[string]any{
				"queue": "email",
			},
			Body: map[string]any{
				"value":   14,
				"message": "Current queue depth",
			},
		},
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
