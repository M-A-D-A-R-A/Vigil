package event

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SchemaVersion     = 1
	DefaultMaxPayload = 1 << 20
)

type Kind string

const (
	KindLog    Kind = "log"
	KindTrace  Kind = "trace"
	KindMetric Kind = "metric"
)

type Envelope struct {
	SchemaVersion int             `json:"schema_version"`
	ProjectID     string          `json:"project_id"`
	Kind          Kind            `json:"kind"`
	TS            string          `json:"ts"`
	Source        string          `json:"source"`
	TraceID       string          `json:"trace_id,omitempty"`
	SpanID        string          `json:"span_id,omitempty"`
	ParentSpanID  string          `json:"parent_span_id,omitempty"`
	Level         string          `json:"level,omitempty"`
	Name          string          `json:"name"`
	Attrs         json.RawMessage `json:"attrs,omitempty"`
	Body          json.RawMessage `json:"body,omitempty"`
}

type StoredEvent struct {
	EventID    string `json:"event_id"`
	ReceivedAt string `json:"received_at"`
	Envelope
}

func ParseAndNormalize(payload []byte, projectID string, now time.Time) (*StoredEvent, error) {
	return parseAndNormalize(payload, projectID, now, true)
}

func ParseAndNormalizeForProject(payload []byte, projectID string, now time.Time) (*StoredEvent, error) {
	return parseAndNormalize(payload, projectID, now, false)
}

func parseAndNormalize(payload []byte, projectID string, now time.Time, requireProjectID bool) (*StoredEvent, error) {
	if len(payload) == 0 {
		return nil, errors.New("payload is required")
	}

	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()

	var env Envelope
	if err := decoder.Decode(&env); err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}
	if !requireProjectID && strings.TrimSpace(env.ProjectID) == "" {
		env.ProjectID = projectID
	}

	return NormalizeEnvelope(env, projectID, now)
}

func NormalizeEnvelope(env Envelope, projectID string, now time.Time) (*StoredEvent, error) {
	if err := validateEnvelope(&env, projectID); err != nil {
		return nil, err
	}

	eventID, err := randomToken("evt_", 12)
	if err != nil {
		return nil, fmt.Errorf("generate event id: %w", err)
	}

	stored := &StoredEvent{
		EventID:    eventID,
		ReceivedAt: now.UTC().Format(time.RFC3339Nano),
		Envelope: Envelope{
			SchemaVersion: SchemaVersion,
			ProjectID:     strings.TrimSpace(env.ProjectID),
			Kind:          Kind(strings.ToLower(strings.TrimSpace(string(env.Kind)))),
			TS:            normalizeTimestamp(env.TS),
			Source:        strings.TrimSpace(env.Source),
			TraceID:       strings.TrimSpace(env.TraceID),
			SpanID:        strings.TrimSpace(env.SpanID),
			ParentSpanID:  strings.TrimSpace(env.ParentSpanID),
			Level:         strings.ToLower(strings.TrimSpace(env.Level)),
			Name:          strings.TrimSpace(env.Name),
			Attrs:         normalizeAttrs(env.Attrs),
			Body:          normalizeBody(env.Body),
		},
	}

	return stored, nil
}

func validateEnvelope(env *Envelope, projectID string) error {
	if env.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", env.SchemaVersion)
	}
	if strings.TrimSpace(env.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	if strings.TrimSpace(env.ProjectID) != projectID {
		return errors.New("project_id does not match authenticated project")
	}

	switch Kind(strings.ToLower(strings.TrimSpace(string(env.Kind)))) {
	case KindLog, KindTrace, KindMetric:
	default:
		return fmt.Errorf("unknown kind %q", env.Kind)
	}

	if strings.TrimSpace(env.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(env.Name) == "" {
		return errors.New("name is required")
	}
	if _, err := time.Parse(time.RFC3339, env.TS); err != nil {
		return errors.New("ts must be a valid RFC3339 timestamp")
	}

	if len(env.Attrs) > 0 {
		var attrs map[string]any
		if err := json.Unmarshal(env.Attrs, &attrs); err != nil {
			return errors.New("attrs must be a JSON object")
		}
	}

	if len(env.Body) > 0 {
		var body any
		if err := json.Unmarshal(env.Body, &body); err != nil {
			return errors.New("body must be valid JSON")
		}
	}

	return nil
}

func normalizeTimestamp(raw string) string {
	ts, _ := time.Parse(time.RFC3339, raw)
	return ts.UTC().Format(time.RFC3339Nano)
}

func normalizeAttrs(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	return compactJSON(raw, "{}")
}

func normalizeBody(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return compactJSON(raw, "null")
}

func compactJSON(raw json.RawMessage, fallback string) json.RawMessage {
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		return json.RawMessage(fallback)
	}
	return json.RawMessage(compacted.String())
}

func SearchText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func TimestampDay(ts string) string {
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Now().UTC().Format("2006-01-02")
	}
	return parsed.UTC().Format("2006-01-02")
}

func ExtractUsageTotals(ev *StoredEvent) (tokens float64, cost float64) {
	attrs, _ := decodeMap(ev.Attrs)
	body, _ := decodeMap(ev.Body)

	tokens = lookupNumber(attrs,
		"total_tokens",
		"token_count",
		"tokens",
	)
	if tokens == 0 {
		tokens = lookupNumber(attrs, "prompt_tokens") + lookupNumber(attrs, "completion_tokens")
	}
	if tokens == 0 {
		tokens = lookupNumber(body,
			"total_tokens",
			"token_count",
			"tokens",
		)
		if tokens == 0 {
			tokens = lookupNumber(body, "prompt_tokens") + lookupNumber(body, "completion_tokens")
		}
	}

	cost = lookupNumber(attrs, "cost_usd", "usd_cost", "cost")
	if cost == 0 {
		cost = lookupNumber(body, "cost_usd", "usd_cost", "cost")
	}

	return tokens, cost
}

func decodeMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}, err
	}
	return value, nil
}

func lookupNumber(data map[string]any, keys ...string) float64 {
	for _, key := range keys {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return typed
		case int:
			return float64(typed)
		}
	}
	return 0
}

func randomToken(prefix string, bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
