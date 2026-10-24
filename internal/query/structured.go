package query

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type QueryError struct {
	Message    string `json:"message"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Suggestion string `json:"suggestion,omitempty"`
}

func (e *QueryError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type StructuredQuery struct {
	Raw  string
	Expr Expression
}

type Expression interface {
	exprNode()
}

type LogicalOperator string

const (
	LogicalAnd LogicalOperator = "&&"
	LogicalOr  LogicalOperator = "||"
)

type LogicalExpression struct {
	Op    LogicalOperator
	Left  Expression
	Right Expression
}

func (*LogicalExpression) exprNode() {}

type Operator string

const (
	OpEqual              Operator = "="
	OpNotEqual           Operator = "!="
	OpLessThan           Operator = "<"
	OpLessThanOrEqual    Operator = "<="
	OpGreaterThan        Operator = ">"
	OpGreaterThanOrEqual Operator = ">="
	OpContains           Operator = "~="
)

type ComparisonExpression struct {
	Field    Field
	Operator Operator
	Value    Literal
}

func (*ComparisonExpression) exprNode() {}

type Field struct {
	Raw   string
	Scope string
	Key   string
	Start int
	End   int
}

func (f Field) IsJSONPath() bool {
	return f.Scope == "attrs" || f.Scope == "body"
}

type LiteralKind string

const (
	LiteralString LiteralKind = "string"
	LiteralNumber LiteralKind = "number"
	LiteralBool   LiteralKind = "bool"
	LiteralNull   LiteralKind = "null"
	LiteralTime   LiteralKind = "time"
)

type Literal struct {
	Kind   LiteralKind
	Raw    string
	String string
	Number float64
	Bool   bool
	Time   time.Time
	Start  int
	End    int
}

func ParseStructuredQuery(raw string, now time.Time) (*StructuredQuery, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parser := newStructuredParser(raw, now.UTC())
	expr, err := parser.parseExpression()
	if err != nil {
		return nil, err
	}
	if parser.current.kind != tokenEOF {
		return nil, parser.errAt(parser.current.start, parser.current.end, "unexpected token", "Use && or || to join comparisons.")
	}
	return &StructuredQuery{Raw: raw, Expr: expr}, nil
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenIdent
	tokenString
	tokenNumber
	tokenBool
	tokenNull
	tokenDuration
	tokenNow
	tokenOp
	tokenAnd
	tokenOr
	tokenLParen
	tokenRParen
	tokenMinus
)

type token struct {
	kind  tokenKind
	raw   string
	start int
	end   int
}

type structuredParser struct {
	input   string
	now     time.Time
	current token
}

func newStructuredParser(input string, now time.Time) *structuredParser {
	p := &structuredParser{input: input, now: now}
	p.current = p.scan(0)
	return p
}

func (p *structuredParser) parseExpression() (Expression, error) {
	return p.parseOr()
}

func (p *structuredParser) parseOr() (Expression, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.current.kind == tokenOr {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &LogicalExpression{Op: LogicalOr, Left: left, Right: right}
	}
	return left, nil
}

func (p *structuredParser) parseAnd() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.current.kind == tokenAnd {
		p.next()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &LogicalExpression{Op: LogicalAnd, Left: left, Right: right}
	}
	return left, nil
}

func (p *structuredParser) parsePrimary() (Expression, error) {
	if p.current.kind == tokenLParen {
		p.next()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if p.current.kind != tokenRParen {
			return nil, p.errAt(p.current.start, p.current.end, "expected closing parenthesis", "Add ) to close the grouped expression.")
		}
		p.next()
		return expr, nil
	}
	return p.parseComparison()
}

func (p *structuredParser) parseComparison() (Expression, error) {
	if p.current.kind != tokenIdent {
		return nil, p.errAt(p.current.start, p.current.end, "expected field name", "Start with a field such as level, source, attrs.route, or timestamp.")
	}
	field, err := p.parseField(p.current)
	if err != nil {
		return nil, err
	}
	p.next()

	if p.current.kind != tokenOp {
		return nil, p.errAt(p.current.start, p.current.end, "expected comparison operator", "Use =, !=, <, <=, >, >=, or ~=.")
	}
	op := Operator(p.current.raw)
	p.next()

	value, err := p.parseLiteral()
	if err != nil {
		return nil, err
	}
	if err := validateComparison(field, op, value); err != nil {
		return nil, err
	}

	return &ComparisonExpression{Field: field, Operator: op, Value: value}, nil
}

func (p *structuredParser) parseField(tok token) (Field, error) {
	raw := tok.raw
	if isCoreField(raw) {
		return Field{Raw: raw, Start: tok.start, End: tok.end}, nil
	}

	parts := strings.Split(raw, ".")
	if len(parts) == 2 && (parts[0] == "attrs" || parts[0] == "body") && parts[1] != "" {
		return Field{Raw: raw, Scope: parts[0], Key: parts[1], Start: tok.start, End: tok.end}, nil
	}
	if strings.HasPrefix(raw, "attrs.") || strings.HasPrefix(raw, "body.") {
		return Field{}, p.errAt(tok.start, tok.end, "only one-level JSON paths are supported", "Use fields like attrs.route or body.message.")
	}
	return Field{}, p.errAt(tok.start, tok.end, fmt.Sprintf("unknown field %q", raw), "Use kind, level, source, name, trace_id, span_id, parent_span_id, timestamp, attrs.foo, or body.foo.")
}

func (p *structuredParser) parseLiteral() (Literal, error) {
	tok := p.current
	switch tok.kind {
	case tokenString:
		value, err := strconv.Unquote(tok.raw)
		if err != nil {
			return Literal{}, p.errAt(tok.start, tok.end, "invalid string literal", "Use a double-quoted string.")
		}
		p.next()
		return Literal{Kind: LiteralString, Raw: tok.raw, String: value, Start: tok.start, End: tok.end}, nil
	case tokenNumber:
		value, err := strconv.ParseFloat(tok.raw, 64)
		if err != nil {
			return Literal{}, p.errAt(tok.start, tok.end, "invalid number literal", "")
		}
		p.next()
		return Literal{Kind: LiteralNumber, Raw: tok.raw, Number: value, Start: tok.start, End: tok.end}, nil
	case tokenBool:
		p.next()
		return Literal{Kind: LiteralBool, Raw: tok.raw, Bool: tok.raw == "true", Start: tok.start, End: tok.end}, nil
	case tokenNull:
		p.next()
		return Literal{Kind: LiteralNull, Raw: tok.raw, Start: tok.start, End: tok.end}, nil
	case tokenNow:
		return p.parseRelativeTime()
	default:
		return Literal{}, p.errAt(tok.start, tok.end, "expected literal value", "Use a string, number, boolean, null, or now() - duration.")
	}
}

func (p *structuredParser) parseRelativeTime() (Literal, error) {
	start := p.current.start
	p.next()
	if p.current.kind != tokenLParen {
		return Literal{}, p.errAt(p.current.start, p.current.end, "expected ( after now", "Use now() - 6h.")
	}
	p.next()
	if p.current.kind != tokenRParen {
		return Literal{}, p.errAt(p.current.start, p.current.end, "expected ) after now(", "Use now() - 6h.")
	}
	p.next()
	if p.current.kind != tokenMinus {
		return Literal{}, p.errAt(p.current.start, p.current.end, "expected - after now()", "Use now() - 6h.")
	}
	p.next()
	if p.current.kind != tokenDuration {
		return Literal{}, p.errAt(p.current.start, p.current.end, "expected duration after now() -", "Use durations like 5m, 6h, or 30s.")
	}
	duration, err := time.ParseDuration(p.current.raw)
	if err != nil {
		return Literal{}, p.errAt(p.current.start, p.current.end, "invalid duration", "Use durations like 5m, 6h, or 30s.")
	}
	end := p.current.end
	p.next()
	return Literal{Kind: LiteralTime, Raw: p.input[start:end], Time: p.now.Add(-duration).UTC(), Start: start, End: end}, nil
}

func (p *structuredParser) next() {
	p.current = p.scan(p.current.end)
}

func (p *structuredParser) scan(pos int) token {
	for pos < len(p.input) && unicode.IsSpace(rune(p.input[pos])) {
		pos++
	}
	if pos >= len(p.input) {
		return token{kind: tokenEOF, start: pos, end: pos}
	}

	start := pos
	switch p.input[pos] {
	case '(':
		return token{kind: tokenLParen, raw: "(", start: start, end: pos + 1}
	case ')':
		return token{kind: tokenRParen, raw: ")", start: start, end: pos + 1}
	case '-':
		return token{kind: tokenMinus, raw: "-", start: start, end: pos + 1}
	case '"':
		pos++
		escaped := false
		for pos < len(p.input) {
			ch := p.input[pos]
			if escaped {
				escaped = false
				pos++
				continue
			}
			if ch == '\\' {
				escaped = true
				pos++
				continue
			}
			if ch == '"' {
				return token{kind: tokenString, raw: p.input[start : pos+1], start: start, end: pos + 1}
			}
			pos++
		}
		return token{kind: tokenString, raw: p.input[start:], start: start, end: len(p.input)}
	}

	if pos+1 < len(p.input) {
		two := p.input[pos : pos+2]
		switch two {
		case "&&":
			return token{kind: tokenAnd, raw: two, start: start, end: pos + 2}
		case "||":
			return token{kind: tokenOr, raw: two, start: start, end: pos + 2}
		case "!=", "<=", ">=", "~=":
			return token{kind: tokenOp, raw: two, start: start, end: pos + 2}
		}
	}
	if p.input[pos] == '=' || p.input[pos] == '<' || p.input[pos] == '>' {
		return token{kind: tokenOp, raw: p.input[pos : pos+1], start: start, end: pos + 1}
	}

	if isDigit(p.input[pos]) {
		pos++
		for pos < len(p.input) && (isDigit(p.input[pos]) || p.input[pos] == '.') {
			pos++
		}
		for pos < len(p.input) && isAlpha(p.input[pos]) {
			pos++
		}
		raw := p.input[start:pos]
		if hasAlpha(raw) {
			return token{kind: tokenDuration, raw: raw, start: start, end: pos}
		}
		return token{kind: tokenNumber, raw: raw, start: start, end: pos}
	}

	if isIdentStart(p.input[pos]) {
		pos++
		for pos < len(p.input) && isIdentPart(p.input[pos]) {
			pos++
		}
		raw := p.input[start:pos]
		switch raw {
		case "true", "false":
			return token{kind: tokenBool, raw: raw, start: start, end: pos}
		case "null":
			return token{kind: tokenNull, raw: raw, start: start, end: pos}
		case "now":
			return token{kind: tokenNow, raw: raw, start: start, end: pos}
		default:
			return token{kind: tokenIdent, raw: raw, start: start, end: pos}
		}
	}

	return token{kind: tokenEOF, raw: p.input[pos : pos+1], start: start, end: pos + 1}
}

func (p *structuredParser) errAt(start, end int, message, suggestion string) *QueryError {
	if end < start {
		end = start
	}
	return &QueryError{Message: message, Start: start, End: end, Suggestion: suggestion}
}

func validateComparison(field Field, op Operator, value Literal) error {
	if op == OpContains && value.Kind != LiteralString {
		return &QueryError{Message: "contains operator requires a string literal", Start: value.Start, End: value.End, Suggestion: `Use ~= "text".`}
	}
	if (field.Raw == "timestamp" || field.Raw == "ts") && op == OpContains {
		return &QueryError{Message: "timestamp does not support contains", Start: field.Start, End: field.End, Suggestion: "Use <, <=, >, or >= with timestamp."}
	}
	return nil
}

func isCoreField(raw string) bool {
	switch raw {
	case "kind", "level", "source", "name", "trace_id", "span_id", "parent_span_id", "timestamp", "ts":
		return true
	default:
		return false
	}
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func hasAlpha(raw string) bool {
	for i := 0; i < len(raw); i++ {
		if isAlpha(raw[i]) {
			return true
		}
	}
	return false
}

func isIdentStart(ch byte) bool {
	return isAlpha(ch) || ch == '_'
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch) || ch == '.'
}
