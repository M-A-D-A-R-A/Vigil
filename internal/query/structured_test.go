package query

import (
	"errors"
	"testing"
	"time"
)

func TestParseStructuredQueryExamples(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	cases := []string{
		`level = "error" && timestamp > now() - 6h`,
		`source = "api" && attrs.route = "/login"`,
		`name ~= "checkout"`,
		`trace_id = "trace_123"`,
	}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			parsed, err := ParseStructuredQuery(raw, now)
			if err != nil {
				t.Fatalf("ParseStructuredQuery returned error: %v", err)
			}
			if parsed == nil || parsed.Expr == nil {
				t.Fatal("expected parsed expression")
			}
		})
	}
}

func TestParseStructuredQueryPrecedence(t *testing.T) {
	parsed, err := ParseStructuredQuery(`level = "error" || level = "warn" && source = "api"`, time.Now().UTC())
	if err != nil {
		t.Fatalf("ParseStructuredQuery returned error: %v", err)
	}
	root, ok := parsed.Expr.(*LogicalExpression)
	if !ok || root.Op != LogicalOr {
		t.Fatalf("expected root OR expression, got %#v", parsed.Expr)
	}
	right, ok := root.Right.(*LogicalExpression)
	if !ok || right.Op != LogicalAnd {
		t.Fatalf("expected AND on right side, got %#v", root.Right)
	}
}

func TestParseStructuredQueryLiterals(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	cases := []string{
		`attrs.status >= 500`,
		`attrs.cached = true`,
		`body.error = null`,
		`timestamp > now() - 15m`,
	}

	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseStructuredQuery(raw, now); err != nil {
				t.Fatalf("ParseStructuredQuery returned error: %v", err)
			}
		})
	}
}

func TestParseStructuredQueryErrorSpans(t *testing.T) {
	_, err := ParseStructuredQuery(`level = error`, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error")
	}
	var queryErr *QueryError
	if !errors.As(err, &queryErr) {
		t.Fatalf("expected QueryError, got %T", err)
	}
	if queryErr.Start != 8 || queryErr.End != 13 {
		t.Fatalf("expected span 8..13, got %d..%d", queryErr.Start, queryErr.End)
	}
	if queryErr.Suggestion == "" {
		t.Fatal("expected suggestion")
	}
}

func TestParseStructuredQueryRejectsDeepJSONPath(t *testing.T) {
	_, err := ParseStructuredQuery(`attrs.http.route = "/login"`, time.Now().UTC())
	if err == nil {
		t.Fatal("expected error")
	}
	var queryErr *QueryError
	if !errors.As(err, &queryErr) {
		t.Fatalf("expected QueryError, got %T", err)
	}
	if queryErr.Start != 0 {
		t.Fatalf("expected error at start of field, got %d", queryErr.Start)
	}
}
