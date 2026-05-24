package redact

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"vigil/internal/event"
)

const Replacement = "[REDACTED]"

type Policy struct {
	Enabled      bool `json:"enabled"`
	RedactEmails bool `json:"redact_emails"`
	MaxDepth     int  `json:"max_depth"`
	MaxStringLen int  `json:"max_string_length"`
}

type Stats struct {
	Enabled        bool   `json:"enabled"`
	RedactEmails   bool   `json:"redact_emails"`
	FieldsRedacted uint64 `json:"fields_redacted"`
	ValuesRedacted uint64 `json:"values_redacted"`
	EmailsRedacted uint64 `json:"emails_redacted"`
}

type Redactor struct {
	policy Policy
	mu     sync.Mutex
	stats  Stats
}

type Result struct {
	Fields uint64
	Values uint64
	Emails uint64
}

func DefaultPolicy() Policy {
	return Policy{
		Enabled:      true,
		RedactEmails: true,
		MaxDepth:     8,
		MaxStringLen: 8192,
	}
}

func New(policy Policy) *Redactor {
	if !policy.Enabled && !policy.RedactEmails && policy.MaxDepth == 0 && policy.MaxStringLen == 0 {
		policy = DefaultPolicy()
	}
	if policy.MaxDepth <= 0 {
		policy.MaxDepth = DefaultPolicy().MaxDepth
	}
	if policy.MaxStringLen <= 0 {
		policy.MaxStringLen = DefaultPolicy().MaxStringLen
	}
	return &Redactor{
		policy: policy,
		stats: Stats{
			Enabled:      policy.Enabled,
			RedactEmails: policy.RedactEmails,
		},
	}
}

func (r *Redactor) ApplyEvent(ev *event.StoredEvent) Result {
	if r == nil || !r.policy.Enabled || ev == nil {
		return Result{}
	}

	var result Result
	ev.Attrs, result = r.applyRaw(ev.Attrs, json.RawMessage("{}"), result)
	ev.Body, result = r.applyRaw(ev.Body, json.RawMessage("null"), result)
	r.record(result)
	return result
}

func (r *Redactor) Stats() Stats {
	if r == nil {
		return Stats{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

func (r *Redactor) applyRaw(raw json.RawMessage, fallback json.RawMessage, result Result) (json.RawMessage, Result) {
	if len(raw) == 0 {
		return fallback, result
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw, result
	}
	value, result = r.redactValue("", value, 0, result)
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw, result
	}
	return encoded, result
}

func (r *Redactor) redactValue(key string, value any, depth int, result Result) (any, Result) {
	if depth > r.policy.MaxDepth {
		return value, result
	}
	if isSensitiveKey(key) {
		result.Fields++
		return Replacement, result
	}

	switch typed := value.(type) {
	case map[string]any:
		for childKey, childValue := range typed {
			typed[childKey], result = r.redactValue(childKey, childValue, depth+1, result)
		}
		return typed, result
	case []any:
		for i, childValue := range typed {
			typed[i], result = r.redactValue("", childValue, depth+1, result)
		}
		return typed, result
	case string:
		return r.redactString(typed, result)
	default:
		return value, result
	}
}

func (r *Redactor) redactString(value string, result Result) (string, Result) {
	inspection := value
	if len(inspection) > r.policy.MaxStringLen {
		inspection = inspection[:r.policy.MaxStringLen]
	}

	redacted := value
	changed := false
	for _, pattern := range valuePatterns {
		next := pattern.ReplaceAllString(redacted, Replacement)
		if next != redacted {
			changed = true
			redacted = next
		}
	}
	if looksHighEntropySecret(inspection) {
		changed = true
		redacted = Replacement
	}
	if changed {
		result.Values++
	}
	if r.policy.RedactEmails {
		next := emailPattern.ReplaceAllString(redacted, Replacement)
		if next != redacted {
			result.Emails++
			redacted = next
		}
	}
	return redacted, result
}

func (r *Redactor) record(result Result) {
	if result.Fields == 0 && result.Values == 0 && result.Emails == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.FieldsRedacted += result.Fields
	r.stats.ValuesRedacted += result.Values
	r.stats.EmailsRedacted += result.Emails
}

func isSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	if normalized == "" {
		return false
	}
	if sensitiveKeys[normalized] {
		return true
	}
	return strings.HasSuffix(normalized, "token") ||
		strings.HasSuffix(normalized, "secret") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "cookie")
}

func normalizeKey(key string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(key) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func looksHighEntropySecret(value string) bool {
	if len(value) < 40 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}

	classes := 0
	if lowercasePattern.MatchString(value) {
		classes++
	}
	if uppercasePattern.MatchString(value) {
		classes++
	}
	if digitPattern.MatchString(value) {
		classes++
	}
	if secretSymbolPattern.MatchString(value) {
		classes++
	}
	return classes >= 3
}

var sensitiveKeys = map[string]bool{
	"password":      true,
	"passwd":        true,
	"pwd":           true,
	"token":         true,
	"secret":        true,
	"apikey":        true,
	"xapikey":       true,
	"authorization": true,
	"cookie":        true,
	"setcookie":     true,
	"accesstoken":   true,
	"refreshtoken":  true,
	"idtoken":       true,
	"clientsecret":  true,
	"privatekey":    true,
}

var valuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{20,}\b`),
	regexp.MustCompile(`://[^/\s:@]+:[^@\s/]+@`),
}

var emailPattern = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
var lowercasePattern = regexp.MustCompile(`[a-z]`)
var uppercasePattern = regexp.MustCompile(`[A-Z]`)
var digitPattern = regexp.MustCompile(`[0-9]`)
var secretSymbolPattern = regexp.MustCompile(`[_./+=-]`)
