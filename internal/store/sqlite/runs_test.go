package sqlite

import (
	"testing"
	"time"

	"vigil/internal/event"
)

func TestRunsReadModelAggregatesLifecycleAndSteps(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	runID := "run_agent_1"

	events := []*event.StoredEvent{
		testRunEvent("evt_run_start", event.KindRun, runID, "", baseTS, "run.started",
			`{"run":{"repo":"/repo","branch":"main","worktree":"/tmp/wt","model":"deepseek"},"status":"running","prompt":"fix the bug"}`),
		testRunEvent("evt_step_tool", event.KindRunStep, runID, "tool_call", baseTS.Add(time.Second), "bash",
			`{"status":"success","duration_ms":12.5}`),
		testRunEvent("evt_step_decision", event.KindRunStep, runID, "decision", baseTS.Add(2*time.Second), "assistant.decision",
			`{"status":"success","total_tokens":120,"cost_usd":0.002}`),
		testRunEvent("evt_step_error", event.KindRunStep, runID, "tool_result", baseTS.Add(3*time.Second), "bash",
			`{"status":"error","duration_ms":5}`),
		testRunEvent("evt_run_done", event.KindRun, runID, "", baseTS.Add(4*time.Second), "run.completed",
			`{"status":"succeeded","outcome":"patch applied"}`),
	}

	if _, _, err := store.UpsertEvents(events); err != nil {
		t.Fatalf("upsert run events: %v", err)
	}

	runs, err := store.ListRuns(testRangeFilters(baseTS))
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if runs.Total != 1 {
		t.Fatalf("expected 1 run, got %d", runs.Total)
	}

	run := runs.Runs[0]
	if run.RunID != runID {
		t.Fatalf("expected run id %s, got %s", runID, run.RunID)
	}
	if run.Status != "succeeded" {
		t.Fatalf("expected run status succeeded, got %q", run.Status)
	}
	if run.Source != "pi" {
		t.Fatalf("expected source pi, got %q", run.Source)
	}
	if run.Repo != "/repo" || run.Branch != "main" || run.Worktree != "/tmp/wt" {
		t.Fatalf("expected run git metadata, got %+v", run)
	}
	if run.Model != "deepseek" {
		t.Fatalf("expected model deepseek, got %q", run.Model)
	}
	if run.PromptSummary != "fix the bug" {
		t.Fatalf("expected prompt summary, got %q", run.PromptSummary)
	}
	if run.StepCount != 3 {
		t.Fatalf("expected 3 steps, got %d", run.StepCount)
	}
	if run.ErrorCount != 1 {
		t.Fatalf("expected 1 error step, got %d", run.ErrorCount)
	}
	if run.TotalTokens != 120 {
		t.Fatalf("expected 120 tokens, got %v", run.TotalTokens)
	}
	if run.TotalCostUSD != 0.002 {
		t.Fatalf("expected 0.002 cost, got %v", run.TotalCostUSD)
	}
	if run.EndedAt == "" {
		t.Fatal("expected ended_at to be set after completion")
	}

	detail, err := store.GetRun(runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if len(detail.Steps) != 3 {
		t.Fatalf("expected 3 run steps, got %d", len(detail.Steps))
	}
	if detail.Steps[0].StepType != "tool_call" || detail.Steps[1].StepType != "decision" || detail.Steps[2].StepType != "tool_result" {
		t.Fatalf("expected ordered steps, got %+v", detail.Steps)
	}
	if detail.Steps[0].Name != "bash" || detail.Steps[0].DurationMS != 12.5 {
		t.Fatalf("expected tool step details, got %+v", detail.Steps[0])
	}
}

func TestResetReadModelsClearsRuns(t *testing.T) {
	store := openTestStore(t)
	baseTS := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	runID := "run_reset"

	events := []*event.StoredEvent{
		testRunEvent("evt_reset_start", event.KindRun, runID, "", baseTS, "run.started", `{"status":"running"}`),
		testRunEvent("evt_reset_step", event.KindRunStep, runID, "tool_call", baseTS.Add(time.Second), "read", `{"status":"success"}`),
	}
	if _, _, err := store.UpsertEvents(events); err != nil {
		t.Fatalf("upsert run events: %v", err)
	}

	if err := store.ResetReadModels(baseTS); err != nil {
		t.Fatalf("reset read models: %v", err)
	}

	runs, err := store.ListRuns(testRangeFilters(baseTS))
	if err != nil {
		t.Fatalf("list runs after reset: %v", err)
	}
	if runs.Total != 0 {
		t.Fatalf("expected runs cleared after reset, got %d", runs.Total)
	}
	if _, err := store.GetRun(runID); err == nil {
		t.Fatal("expected run lookup to fail after reset")
	}
}

func testRunEvent(id string, kind event.Kind, runID, stepType string, ts time.Time, name, attrs string) *event.StoredEvent {
	return &event.StoredEvent{
		EventID:    id,
		ReceivedAt: ts.Add(time.Millisecond).UTC().Format(time.RFC3339Nano),
		Envelope: event.Envelope{
			SchemaVersion: event.SchemaVersion,
			ProjectID:     "proj_1",
			Kind:          kind,
			RunID:         runID,
			StepType:      stepType,
			TS:            ts.UTC().Format(time.RFC3339Nano),
			Source:        "pi",
			Name:          name,
			Attrs:         []byte(attrs),
			Body:          []byte(`null`),
		},
	}
}
