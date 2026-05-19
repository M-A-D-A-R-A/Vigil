package query

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vigil/internal/event"
)

func MatchLogEvent(filters LogFilters, ev *event.StoredEvent) bool {
	if ev == nil || ev.Kind != event.KindLog {
		return false
	}
	if filters.ProjectID != "" && ev.ProjectID != filters.ProjectID {
		return false
	}
	if ev.TS < filters.From.Format(time.RFC3339Nano) || ev.TS > filters.To.Format(time.RFC3339Nano) {
		return false
	}
	if filters.Kind != "" && string(ev.Kind) != filters.Kind {
		return false
	}
	if filters.Level != "" && ev.Level != filters.Level {
		return false
	}
	if filters.Name != "" && ev.Name != filters.Name {
		return false
	}
	if filters.Query != "" && !matchesTextQuery(filters.Query, ev) {
		return false
	}
	if filters.Structured != nil && !matchExpression(filters.Structured.Expr, ev) {
		return false
	}
	return true
}

func matchesTextQuery(raw string, ev *event.StoredEvent) bool {
	needle := strings.ToLower(strings.TrimSpace(raw))
	if needle == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		ev.Name,
		ev.Source,
		ev.Level,
		string(ev.Attrs),
		string(ev.Body),
	}, " "))
	return strings.Contains(haystack, needle)
}

func matchExpression(expr Expression, ev *event.StoredEvent) bool {
	switch typed := expr.(type) {
	case *LogicalExpression:
		if typed.Op == LogicalOr {
			return matchExpression(typed.Left, ev) || matchExpression(typed.Right, ev)
		}
		return matchExpression(typed.Left, ev) && matchExpression(typed.Right, ev)
	case *ComparisonExpression:
		return matchComparison(typed, ev)
	default:
		return false
	}
}

func matchComparison(expr *ComparisonExpression, ev *event.StoredEvent) bool {
	if expr.Field.IsJSONPath() {
		value, ok := jsonPathValue(ev, expr.Field)
		if expr.Value.Kind == LiteralNull {
			if expr.Operator == OpEqual {
				return !ok || value == nil
			}
			if expr.Operator == OpNotEqual {
				return ok && value != nil
			}
			return false
		}
		if !ok || value == nil {
			return false
		}
		return compareValues(value, expr.Operator, expr.Value)
	}

	value, ok := coreFieldValue(ev, expr.Field.Raw)
	if !ok {
		return false
	}
	return compareValues(value, expr.Operator, expr.Value)
}

func coreFieldValue(ev *event.StoredEvent, field string) (any, bool) {
	switch field {
	case "kind":
		return string(ev.Kind), true
	case "level":
		return ev.Level, true
	case "source":
		return ev.Source, true
	case "name":
		return ev.Name, true
	case "trace_id":
		return ev.TraceID, true
	case "span_id":
		return ev.SpanID, true
	case "timestamp", "ts":
		parsed, err := time.Parse(time.RFC3339Nano, ev.TS)
		return parsed, err == nil
	default:
		return nil, false
	}
}

func jsonPathValue(ev *event.StoredEvent, field Field) (any, bool) {
	var raw json.RawMessage
	if field.Scope == "attrs" {
		raw = ev.Attrs
	} else {
		raw = ev.Body
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}

	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false
	}
	value, ok := object[field.Key]
	return value, ok
}

func compareValues(value any, op Operator, literal Literal) bool {
	if op == OpContains {
		if literal.Kind != LiteralString {
			return false
		}
		return strings.Contains(strings.ToLower(fmt.Sprint(value)), strings.ToLower(literal.String))
	}

	switch literal.Kind {
	case LiteralString:
		if leftTime, ok := timeValue(value); ok {
			rightTime, err := time.Parse(time.RFC3339Nano, literal.String)
			if err == nil {
				return compareOrderedTime(leftTime, op, rightTime)
			}
		}
		return compareOrderedStrings(fmt.Sprint(value), op, literal.String)
	case LiteralNumber:
		number, ok := numericValue(value)
		return ok && compareOrderedFloat(number, op, literal.Number)
	case LiteralBool:
		boolValue, ok := boolValue(value)
		return ok && compareBool(boolValue, op, literal.Bool)
	case LiteralTime:
		timeValue, ok := timeValue(value)
		return ok && compareOrderedTime(timeValue, op, literal.Time)
	default:
		return false
	}
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) (bool, bool) {
	typed, ok := value.(bool)
	return typed, ok
}

func timeValue(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, true
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		return parsed, err == nil
	default:
		return time.Time{}, false
	}
}

func compareOrderedStrings(left string, op Operator, right string) bool {
	switch op {
	case OpEqual:
		return left == right
	case OpNotEqual:
		return left != right
	case OpLessThan:
		return left < right
	case OpLessThanOrEqual:
		return left <= right
	case OpGreaterThan:
		return left > right
	case OpGreaterThanOrEqual:
		return left >= right
	default:
		return false
	}
}

func compareOrderedFloat(left float64, op Operator, right float64) bool {
	switch op {
	case OpEqual:
		return left == right
	case OpNotEqual:
		return left != right
	case OpLessThan:
		return left < right
	case OpLessThanOrEqual:
		return left <= right
	case OpGreaterThan:
		return left > right
	case OpGreaterThanOrEqual:
		return left >= right
	default:
		return false
	}
}

func compareBool(left bool, op Operator, right bool) bool {
	switch op {
	case OpEqual:
		return left == right
	case OpNotEqual:
		return left != right
	default:
		return false
	}
}

func compareOrderedTime(left time.Time, op Operator, right time.Time) bool {
	switch op {
	case OpEqual:
		return left.Equal(right)
	case OpNotEqual:
		return !left.Equal(right)
	case OpLessThan:
		return left.Before(right)
	case OpLessThanOrEqual:
		return left.Before(right) || left.Equal(right)
	case OpGreaterThan:
		return left.After(right)
	case OpGreaterThanOrEqual:
		return left.After(right) || left.Equal(right)
	default:
		return false
	}
}
