package query

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPageSize    = 50
	MaxPageSize        = 100
	DefaultWindow      = 7 * 24 * time.Hour
	DefaultPollingSecs = 5
	DefaultTimeFormat  = time.RFC3339
)

type RangeFilters struct {
	ProjectID      string
	From           time.Time
	To             time.Time
	Page           int
	Limit          int
	RequestedLimit int
	LimitCapped    bool
}

type LogFilters struct {
	RangeFilters
	Kind            string
	Level           string
	Name            string
	TraceID         string
	SpanID          string
	ParentSpanID    string
	Query           string
	StructuredQuery string
	Structured      *StructuredQuery
}

func ParseLogFilters(values url.Values, now time.Time) (LogFilters, error) {
	ranges, err := ParseRangeFilters(values, now)
	if err != nil {
		return LogFilters{}, err
	}
	structuredRaw := strings.TrimSpace(values.Get("query"))
	structured, err := ParseStructuredQuery(structuredRaw, now)
	if err != nil {
		return LogFilters{}, err
	}
	return LogFilters{
		RangeFilters:    ranges,
		Kind:            strings.ToLower(strings.TrimSpace(values.Get("kind"))),
		Level:           strings.ToLower(strings.TrimSpace(values.Get("level"))),
		Name:            strings.TrimSpace(values.Get("name")),
		TraceID:         strings.TrimSpace(values.Get("trace_id")),
		SpanID:          strings.TrimSpace(values.Get("span_id")),
		ParentSpanID:    strings.TrimSpace(values.Get("parent_span_id")),
		Query:           strings.TrimSpace(values.Get("q")),
		StructuredQuery: structuredRaw,
		Structured:      structured,
	}, nil
}

func ParseRangeFilters(values url.Values, now time.Time) (RangeFilters, error) {
	page := parseInt(values.Get("page"), 1)
	if page < 1 {
		page = 1
	}

	limit := parseInt(values.Get("limit"), DefaultPageSize)
	if limit < 1 {
		limit = DefaultPageSize
	}
	requestedLimit := limit
	limitCapped := false
	if limit > MaxPageSize {
		limit = MaxPageSize
		limitCapped = true
	}

	to := now.UTC()
	if raw := strings.TrimSpace(values.Get("to")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return RangeFilters{}, fmt.Errorf("invalid to timestamp")
		}
		to = parsed.UTC()
	}

	from := to.Add(-DefaultWindow)
	if raw := strings.TrimSpace(values.Get("from")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return RangeFilters{}, fmt.Errorf("invalid from timestamp")
		}
		from = parsed.UTC()
	}

	if from.After(to) {
		return RangeFilters{}, fmt.Errorf("from must be before to")
	}

	return RangeFilters{
		ProjectID:      strings.TrimSpace(values.Get("project_id")),
		From:           from,
		To:             to,
		Page:           page,
		Limit:          limit,
		RequestedLimit: requestedLimit,
		LimitCapped:    limitCapped,
	}, nil
}

func parseInt(raw string, fallback int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}
