package query

import (
	"net/url"
	"testing"
	"time"
)

func TestParseRangeFiltersDefaults(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	filters, err := ParseRangeFilters(url.Values{}, now)
	if err != nil {
		t.Fatalf("ParseRangeFilters returned error: %v", err)
	}
	if filters.Page != 1 {
		t.Fatalf("expected page 1, got %d", filters.Page)
	}
	if filters.Limit != DefaultPageSize {
		t.Fatalf("expected default page size %d, got %d", DefaultPageSize, filters.Limit)
	}
	if filters.To.Sub(filters.From) != DefaultWindow {
		t.Fatalf("expected default time window %s, got %s", DefaultWindow, filters.To.Sub(filters.From))
	}
}

func TestParseRangeFiltersRejectsInvalidTime(t *testing.T) {
	_, err := ParseRangeFilters(url.Values{"from": []string{"not-a-time"}}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected invalid timestamp error")
	}
}

func TestParseRangeFiltersTracksLimitCapping(t *testing.T) {
	filters, err := ParseRangeFilters(url.Values{"limit": []string{"999"}}, time.Now().UTC())
	if err != nil {
		t.Fatalf("ParseRangeFilters returned error: %v", err)
	}
	if !filters.LimitCapped {
		t.Fatal("expected limit to be marked as capped")
	}
	if filters.RequestedLimit != 999 {
		t.Fatalf("expected requested limit 999, got %d", filters.RequestedLimit)
	}
	if filters.Limit != MaxPageSize {
		t.Fatalf("expected capped limit %d, got %d", MaxPageSize, filters.Limit)
	}
}
