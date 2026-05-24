package sqlite

import (
	"fmt"
	"strings"
	"time"

	"vigil/internal/query"
)

type compiledPredicate struct {
	sql  string
	args []any
}

func compileStructuredQuery(expr query.Expression) (compiledPredicate, error) {
	switch typed := expr.(type) {
	case *query.LogicalExpression:
		left, err := compileStructuredQuery(typed.Left)
		if err != nil {
			return compiledPredicate{}, err
		}
		right, err := compileStructuredQuery(typed.Right)
		if err != nil {
			return compiledPredicate{}, err
		}
		return compiledPredicate{
			sql:  fmt.Sprintf("(%s %s %s)", left.sql, sqlLogicalOperator(typed.Op), right.sql),
			args: append(left.args, right.args...),
		}, nil
	case *query.ComparisonExpression:
		return compileComparison(typed)
	default:
		return compiledPredicate{}, fmt.Errorf("unknown structured query expression")
	}
}

func sqlLogicalOperator(op query.LogicalOperator) string {
	if op == query.LogicalOr {
		return "OR"
	}
	return "AND"
}

func compileComparison(expr *query.ComparisonExpression) (compiledPredicate, error) {
	if expr.Field.IsJSONPath() {
		return compileJSONComparison(expr)
	}
	return compileCoreComparison(expr)
}

func compileCoreComparison(expr *query.ComparisonExpression) (compiledPredicate, error) {
	column, isTimestamp, err := coreColumn(expr.Field)
	if err != nil {
		return compiledPredicate{}, err
	}
	if isTimestamp {
		return compileTimestampComparison(column, expr)
	}
	if expr.Value.Kind != query.LiteralString {
		return compiledPredicate{}, &query.QueryError{
			Message:    "core fields require string literals",
			Start:      expr.Value.Start,
			End:        expr.Value.End,
			Suggestion: `Use a quoted string such as level = "error".`,
		}
	}

	value := expr.Value.String
	if expr.Field.Raw == "kind" || expr.Field.Raw == "level" {
		value = strings.ToLower(value)
	}
	if expr.Operator == query.OpContains {
		return compiledPredicate{
			sql:  fmt.Sprintf("lower(%s) LIKE ? ESCAPE '\\'", column),
			args: []any{likePattern(strings.ToLower(value))},
		}, nil
	}
	return compiledPredicate{
		sql:  fmt.Sprintf("%s %s ?", column, sqlOperator(expr.Operator)),
		args: []any{value},
	}, nil
}

func compileTimestampComparison(column string, expr *query.ComparisonExpression) (compiledPredicate, error) {
	if expr.Operator == query.OpContains {
		return compiledPredicate{}, &query.QueryError{
			Message:    "timestamp does not support contains",
			Start:      expr.Field.Start,
			End:        expr.Field.End,
			Suggestion: "Use <, <=, >, or >= with timestamp.",
		}
	}

	value, err := timestampLiteral(expr.Value)
	if err != nil {
		return compiledPredicate{}, err
	}
	return compiledPredicate{
		sql:  fmt.Sprintf("%s %s ?", column, sqlOperator(expr.Operator)),
		args: []any{value},
	}, nil
}

func compileJSONComparison(expr *query.ComparisonExpression) (compiledPredicate, error) {
	path := "$." + expr.Field.Key
	extract := fmt.Sprintf("json_extract(e.%s_json, ?)", expr.Field.Scope)

	if expr.Value.Kind == query.LiteralNull {
		if expr.Operator != query.OpEqual && expr.Operator != query.OpNotEqual {
			return compiledPredicate{}, &query.QueryError{
				Message:    "null only supports = and !=",
				Start:      expr.Value.Start,
				End:        expr.Value.End,
				Suggestion: "Use attrs.field = null or attrs.field != null.",
			}
		}
		if expr.Operator == query.OpEqual {
			return compiledPredicate{sql: extract + " IS NULL", args: []any{path}}, nil
		}
		return compiledPredicate{sql: extract + " IS NOT NULL", args: []any{path}}, nil
	}

	if expr.Operator == query.OpContains {
		if expr.Value.Kind != query.LiteralString {
			return compiledPredicate{}, &query.QueryError{
				Message:    "contains operator requires a string literal",
				Start:      expr.Value.Start,
				End:        expr.Value.End,
				Suggestion: `Use ~= "text".`,
			}
		}
		return compiledPredicate{
			sql:  fmt.Sprintf("lower(COALESCE(CAST(%s AS TEXT), '')) LIKE ? ESCAPE '\\'", extract),
			args: []any{path, likePattern(strings.ToLower(expr.Value.String))},
		}, nil
	}

	value, err := jsonLiteralValue(expr.Value)
	if err != nil {
		return compiledPredicate{}, err
	}
	return compiledPredicate{
		sql:  fmt.Sprintf("%s %s ?", extract, sqlOperator(expr.Operator)),
		args: []any{path, value},
	}, nil
}

func coreColumn(field query.Field) (column string, isTimestamp bool, err error) {
	switch field.Raw {
	case "kind":
		return "e.kind", false, nil
	case "level":
		return "e.level", false, nil
	case "source":
		return "e.source", false, nil
	case "name":
		return "e.name", false, nil
	case "trace_id":
		return "e.trace_id", false, nil
	case "span_id":
		return "e.span_id", false, nil
	case "parent_span_id":
		return "e.parent_span_id", false, nil
	case "timestamp", "ts":
		return "e.ts", true, nil
	default:
		return "", false, &query.QueryError{
			Message:    fmt.Sprintf("unknown field %q", field.Raw),
			Start:      field.Start,
			End:        field.End,
			Suggestion: "Use kind, level, source, name, trace_id, span_id, parent_span_id, timestamp, attrs.foo, or body.foo.",
		}
	}
}

func timestampLiteral(value query.Literal) (string, error) {
	switch value.Kind {
	case query.LiteralTime:
		return value.Time.UTC().Format(time.RFC3339Nano), nil
	case query.LiteralString:
		parsed, err := time.Parse(time.RFC3339Nano, value.String)
		if err != nil {
			return "", &query.QueryError{
				Message:    "timestamp string must be RFC3339",
				Start:      value.Start,
				End:        value.End,
				Suggestion: `Use timestamp > now() - 6h or timestamp > "2026-05-16T12:00:00Z".`,
			}
		}
		return parsed.UTC().Format(time.RFC3339Nano), nil
	default:
		return "", &query.QueryError{
			Message:    "timestamp requires a time value",
			Start:      value.Start,
			End:        value.End,
			Suggestion: `Use timestamp > now() - 6h or timestamp > "2026-05-16T12:00:00Z".`,
		}
	}
}

func jsonLiteralValue(value query.Literal) (any, error) {
	switch value.Kind {
	case query.LiteralString:
		return value.String, nil
	case query.LiteralNumber:
		return value.Number, nil
	case query.LiteralBool:
		if value.Bool {
			return 1, nil
		}
		return 0, nil
	case query.LiteralTime:
		return value.Time.UTC().Format(time.RFC3339Nano), nil
	default:
		return nil, &query.QueryError{
			Message: "unsupported literal for JSON comparison",
			Start:   value.Start,
			End:     value.End,
		}
	}
}

func sqlOperator(op query.Operator) string {
	switch op {
	case query.OpEqual:
		return "="
	case query.OpNotEqual:
		return "!="
	case query.OpLessThan:
		return "<"
	case query.OpLessThanOrEqual:
		return "<="
	case query.OpGreaterThan:
		return ">"
	case query.OpGreaterThanOrEqual:
		return ">="
	default:
		return "="
	}
}

func likePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}
