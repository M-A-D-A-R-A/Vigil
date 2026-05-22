package otlp

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"vigil/internal/event"
)

func LogsToEnvelopes(req *logsv1.LogsData, now time.Time) ([]event.Envelope, error) {
	var envelopes []event.Envelope
	for _, resourceLogs := range req.GetResourceLogs() {
		resourceAttrs := keyValuesToMap(resourceLogs.GetResource().GetAttributes())
		source := sourceFromResource(resourceAttrs)
		for _, scopeLogs := range resourceLogs.GetScopeLogs() {
			scopeInfo := scopeToMap(scopeLogs.GetScope())
			for _, record := range scopeLogs.GetLogRecords() {
				attrs := keyValuesToMap(record.GetAttributes())
				attrs["otel"] = map[string]any{
					"signal":                   "logs",
					"resource":                 resourceAttrs,
					"resource_schema_url":      resourceLogs.GetSchemaUrl(),
					"scope":                    scopeInfo,
					"scope_schema_url":         scopeLogs.GetSchemaUrl(),
					"attributes":               keyValuesToMap(record.GetAttributes()),
					"severity_number":          int32(record.GetSeverityNumber()),
					"severity_text":            record.GetSeverityText(),
					"flags":                    record.GetFlags(),
					"dropped_attributes_count": record.GetDroppedAttributesCount(),
				}

				envelopes = append(envelopes, event.Envelope{
					SchemaVersion: event.SchemaVersion,
					Kind:          event.KindLog,
					TS:            timestampFromUnixNanos(firstNonZero(record.GetTimeUnixNano(), record.GetObservedTimeUnixNano()), now),
					Source:        source,
					TraceID:       hexID(record.GetTraceId(), 16),
					SpanID:        hexID(record.GetSpanId(), 8),
					Level:         logLevel(record.GetSeverityText(), record.GetSeverityNumber()),
					Name:          logName(record),
					Attrs:         mustJSON(attrs, "{}"),
					Body:          logBody(record.GetBody()),
				})
			}
		}
	}
	return envelopes, nil
}

func TracesToEnvelopes(req *tracev1.TracesData, now time.Time) ([]event.Envelope, error) {
	var envelopes []event.Envelope
	for _, resourceSpans := range req.GetResourceSpans() {
		resourceAttrs := keyValuesToMap(resourceSpans.GetResource().GetAttributes())
		source := sourceFromResource(resourceAttrs)
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			scopeInfo := scopeToMap(scopeSpans.GetScope())
			for _, span := range scopeSpans.GetSpans() {
				attrs := keyValuesToMap(span.GetAttributes())
				attrs["otel"] = map[string]any{
					"signal":                   "traces",
					"resource":                 resourceAttrs,
					"resource_schema_url":      resourceSpans.GetSchemaUrl(),
					"scope":                    scopeInfo,
					"scope_schema_url":         scopeSpans.GetSchemaUrl(),
					"attributes":               keyValuesToMap(span.GetAttributes()),
					"kind":                     span.GetKind().String(),
					"trace_state":              span.GetTraceState(),
					"parent_span_id":           hexID(span.GetParentSpanId(), 8),
					"flags":                    span.GetFlags(),
					"dropped_attributes_count": span.GetDroppedAttributesCount(),
					"dropped_events_count":     span.GetDroppedEventsCount(),
					"dropped_links_count":      span.GetDroppedLinksCount(),
					"events":                   spanEventsToSlice(span.GetEvents()),
					"links":                    spanLinksToSlice(span.GetLinks()),
					"status":                   spanStatusToMap(span.GetStatus()),
				}

				envelopes = append(envelopes, event.Envelope{
					SchemaVersion: event.SchemaVersion,
					Kind:          event.KindTrace,
					TS:            timestampFromUnixNanos(firstNonZero(span.GetStartTimeUnixNano(), span.GetEndTimeUnixNano()), now),
					Source:        source,
					TraceID:       hexID(span.GetTraceId(), 16),
					SpanID:        hexID(span.GetSpanId(), 8),
					Level:         spanLevel(span.GetStatus()),
					Name:          nonEmpty(span.GetName(), "otel.span"),
					Attrs:         mustJSON(attrs, "{}"),
					Body:          mustJSON(spanBody(span), "null"),
				})
			}
		}
	}
	return envelopes, nil
}

func MetricsToEnvelopes(req *metricsv1.MetricsData, now time.Time) ([]event.Envelope, error) {
	var envelopes []event.Envelope
	for _, resourceMetrics := range req.GetResourceMetrics() {
		resourceAttrs := keyValuesToMap(resourceMetrics.GetResource().GetAttributes())
		source := sourceFromResource(resourceAttrs)
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			scopeInfo := scopeToMap(scopeMetrics.GetScope())
			for _, metric := range scopeMetrics.GetMetrics() {
				baseOTel := map[string]any{
					"signal":              "metrics",
					"resource":            resourceAttrs,
					"resource_schema_url": resourceMetrics.GetSchemaUrl(),
					"scope":               scopeInfo,
					"scope_schema_url":    scopeMetrics.GetSchemaUrl(),
					"metric": map[string]any{
						"name":        metric.GetName(),
						"description": metric.GetDescription(),
						"unit":        metric.GetUnit(),
						"metadata":    keyValuesToMap(metric.GetMetadata()),
					},
				}

				for _, converted := range metricPoints(metric, baseOTel, now) {
					converted.Source = source
					envelopes = append(envelopes, converted)
				}
			}
		}
	}
	return envelopes, nil
}

func metricPoints(metric *metricsv1.Metric, baseOTel map[string]any, now time.Time) []event.Envelope {
	switch data := metric.GetData().(type) {
	case *metricsv1.Metric_Gauge:
		return numberPointEvents(metric, "gauge", data.Gauge.GetDataPoints(), baseOTel, now)
	case *metricsv1.Metric_Sum:
		return numberPointEvents(metric, "sum", data.Sum.GetDataPoints(), withMetricData(baseOTel, map[string]any{
			"aggregation_temporality": data.Sum.GetAggregationTemporality().String(),
			"is_monotonic":            data.Sum.GetIsMonotonic(),
		}), now)
	case *metricsv1.Metric_Histogram:
		events := make([]event.Envelope, 0, len(data.Histogram.GetDataPoints()))
		for _, point := range data.Histogram.GetDataPoints() {
			events = append(events, metricEvent(metric, point.GetTimeUnixNano(), keyValuesToMap(point.GetAttributes()), withMetricData(baseOTel, map[string]any{
				"type":                    "histogram",
				"aggregation_temporality": data.Histogram.GetAggregationTemporality().String(),
			}), map[string]any{
				"type":            "histogram",
				"count":           point.GetCount(),
				"sum":             point.GetSum(),
				"min":             point.GetMin(),
				"max":             point.GetMax(),
				"bucket_counts":   point.GetBucketCounts(),
				"explicit_bounds": point.GetExplicitBounds(),
			}, now))
		}
		return events
	case *metricsv1.Metric_Summary:
		events := make([]event.Envelope, 0, len(data.Summary.GetDataPoints()))
		for _, point := range data.Summary.GetDataPoints() {
			quantiles := make([]map[string]any, 0, len(point.GetQuantileValues()))
			for _, quantile := range point.GetQuantileValues() {
				quantiles = append(quantiles, map[string]any{
					"quantile": quantile.GetQuantile(),
					"value":    quantile.GetValue(),
				})
			}
			events = append(events, metricEvent(metric, point.GetTimeUnixNano(), keyValuesToMap(point.GetAttributes()), withMetricData(baseOTel, map[string]any{"type": "summary"}), map[string]any{
				"type":      "summary",
				"count":     point.GetCount(),
				"sum":       point.GetSum(),
				"quantiles": quantiles,
			}, now))
		}
		return events
	case *metricsv1.Metric_ExponentialHistogram:
		events := make([]event.Envelope, 0, len(data.ExponentialHistogram.GetDataPoints()))
		for _, point := range data.ExponentialHistogram.GetDataPoints() {
			events = append(events, metricEvent(metric, point.GetTimeUnixNano(), keyValuesToMap(point.GetAttributes()), withMetricData(baseOTel, map[string]any{
				"type":                    "exponential_histogram",
				"aggregation_temporality": data.ExponentialHistogram.GetAggregationTemporality().String(),
			}), map[string]any{
				"type":           "exponential_histogram",
				"count":          point.GetCount(),
				"sum":            point.GetSum(),
				"min":            point.GetMin(),
				"max":            point.GetMax(),
				"scale":          point.GetScale(),
				"zero_count":     point.GetZeroCount(),
				"zero_threshold": point.GetZeroThreshold(),
			}, now))
		}
		return events
	default:
		return []event.Envelope{{
			SchemaVersion: event.SchemaVersion,
			Kind:          event.KindMetric,
			TS:            now.UTC().Format(time.RFC3339Nano),
			Name:          nonEmpty(metric.GetName(), "otel.metric"),
			Attrs:         mustJSON(map[string]any{"otel": withMetricData(baseOTel, map[string]any{"type": "unknown"})}, "{}"),
			Body:          mustJSON(map[string]any{"type": "unknown"}, "null"),
		}}
	}
}

func numberPointEvents(metric *metricsv1.Metric, dataType string, points []*metricsv1.NumberDataPoint, baseOTel map[string]any, now time.Time) []event.Envelope {
	events := make([]event.Envelope, 0, len(points))
	for _, point := range points {
		value := any(point.GetAsInt())
		if _, ok := point.GetValue().(*metricsv1.NumberDataPoint_AsDouble); ok {
			value = point.GetAsDouble()
		}
		events = append(events, metricEvent(metric, point.GetTimeUnixNano(), keyValuesToMap(point.GetAttributes()), withMetricData(baseOTel, map[string]any{"type": dataType}), map[string]any{
			"type":  dataType,
			"value": value,
		}, now))
	}
	return events
}

func metricEvent(metric *metricsv1.Metric, unixNanos uint64, attrs map[string]any, otel map[string]any, body map[string]any, now time.Time) event.Envelope {
	attrs["otel"] = otel
	return event.Envelope{
		SchemaVersion: event.SchemaVersion,
		Kind:          event.KindMetric,
		TS:            timestampFromUnixNanos(unixNanos, now),
		Name:          nonEmpty(metric.GetName(), "otel.metric"),
		Attrs:         mustJSON(attrs, "{}"),
		Body:          mustJSON(body, "null"),
	}
}

func withMetricData(base map[string]any, extra map[string]any) map[string]any {
	next := cloneMap(base)
	metricInfo, _ := next["metric"].(map[string]any)
	if metricInfo == nil {
		metricInfo = map[string]any{}
	}
	metricInfo = cloneMap(metricInfo)
	for key, value := range extra {
		metricInfo[key] = value
	}
	next["metric"] = metricInfo
	return next
}

func sourceFromResource(attrs map[string]any) string {
	for _, key := range []string{"service.name", "service.namespace", "host.name", "telemetry.sdk.name"} {
		if value, ok := attrs[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "otel"
}

func scopeToMap(scope *commonv1.InstrumentationScope) map[string]any {
	if scope == nil {
		return map[string]any{}
	}
	return map[string]any{
		"name":                     scope.GetName(),
		"version":                  scope.GetVersion(),
		"attributes":               keyValuesToMap(scope.GetAttributes()),
		"dropped_attributes_count": scope.GetDroppedAttributesCount(),
	}
}

func keyValuesToMap(values []*commonv1.KeyValue) map[string]any {
	result := map[string]any{}
	for _, kv := range values {
		key := strings.TrimSpace(kv.GetKey())
		if key == "" {
			continue
		}
		result[key] = anyValue(kv.GetValue())
	}
	return result
}

func anyValue(value *commonv1.AnyValue) any {
	if value == nil {
		return nil
	}
	switch typed := value.GetValue().(type) {
	case *commonv1.AnyValue_StringValue:
		return typed.StringValue
	case *commonv1.AnyValue_BoolValue:
		return typed.BoolValue
	case *commonv1.AnyValue_IntValue:
		return typed.IntValue
	case *commonv1.AnyValue_DoubleValue:
		return typed.DoubleValue
	case *commonv1.AnyValue_ArrayValue:
		items := []any{}
		if typed.ArrayValue != nil {
			for _, item := range typed.ArrayValue.GetValues() {
				items = append(items, anyValue(item))
			}
		}
		return items
	case *commonv1.AnyValue_KvlistValue:
		if typed.KvlistValue == nil {
			return map[string]any{}
		}
		return keyValuesToMap(typed.KvlistValue.GetValues())
	case *commonv1.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(typed.BytesValue)
	default:
		return nil
	}
}

func logName(record *logsv1.LogRecord) string {
	if name := strings.TrimSpace(record.GetEventName()); name != "" {
		return name
	}
	if attrs := keyValuesToMap(record.GetAttributes()); attrs != nil {
		if value, ok := attrs["event.name"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "otel.log"
}

func logLevel(text string, severity logsv1.SeverityNumber) string {
	cleaned := strings.ToLower(strings.TrimSpace(text))
	if cleaned != "" {
		for _, prefix := range []string{"trace", "debug", "info", "warn", "warning", "error", "fatal"} {
			if strings.HasPrefix(cleaned, prefix) {
				if prefix == "warning" {
					return "warn"
				}
				return prefix
			}
		}
		return cleaned
	}

	switch {
	case severity >= logsv1.SeverityNumber_SEVERITY_NUMBER_FATAL:
		return "fatal"
	case severity >= logsv1.SeverityNumber_SEVERITY_NUMBER_ERROR:
		return "error"
	case severity >= logsv1.SeverityNumber_SEVERITY_NUMBER_WARN:
		return "warn"
	case severity >= logsv1.SeverityNumber_SEVERITY_NUMBER_INFO:
		return "info"
	case severity >= logsv1.SeverityNumber_SEVERITY_NUMBER_DEBUG:
		return "debug"
	case severity >= logsv1.SeverityNumber_SEVERITY_NUMBER_TRACE:
		return "trace"
	default:
		return "info"
	}
}

func logBody(value *commonv1.AnyValue) json.RawMessage {
	converted := anyValue(value)
	if message, ok := converted.(string); ok {
		return mustJSON(map[string]any{"message": message}, "null")
	}
	return mustJSON(map[string]any{"value": converted}, "null")
}

func spanLevel(status *tracev1.Status) string {
	if status != nil && status.GetCode() == tracev1.Status_STATUS_CODE_ERROR {
		return "error"
	}
	return "info"
}

func spanBody(span *tracev1.Span) map[string]any {
	durationMS := float64(0)
	if span.GetStartTimeUnixNano() > 0 && span.GetEndTimeUnixNano() >= span.GetStartTimeUnixNano() {
		durationMS = float64(span.GetEndTimeUnixNano()-span.GetStartTimeUnixNano()) / float64(time.Millisecond)
	}
	return map[string]any{
		"message":     span.GetStatus().GetMessage(),
		"duration_ms": durationMS,
		"status":      spanStatusToMap(span.GetStatus()),
	}
}

func spanStatusToMap(status *tracev1.Status) map[string]any {
	if status == nil {
		return map[string]any{"code": tracev1.Status_STATUS_CODE_UNSET.String()}
	}
	return map[string]any{
		"code":    status.GetCode().String(),
		"message": status.GetMessage(),
	}
}

func spanEventsToSlice(events []*tracev1.Span_Event) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		result = append(result, map[string]any{
			"name":                     ev.GetName(),
			"ts":                       timestampFromUnixNanos(ev.GetTimeUnixNano(), time.Time{}),
			"attributes":               keyValuesToMap(ev.GetAttributes()),
			"dropped_attributes_count": ev.GetDroppedAttributesCount(),
		})
	}
	return result
}

func spanLinksToSlice(links []*tracev1.Span_Link) []map[string]any {
	result := make([]map[string]any, 0, len(links))
	for _, link := range links {
		result = append(result, map[string]any{
			"trace_id":                 hexID(link.GetTraceId(), 16),
			"span_id":                  hexID(link.GetSpanId(), 8),
			"trace_state":              link.GetTraceState(),
			"attributes":               keyValuesToMap(link.GetAttributes()),
			"dropped_attributes_count": link.GetDroppedAttributesCount(),
		})
	}
	return result
}

func timestampFromUnixNanos(unixNanos uint64, fallback time.Time) string {
	if unixNanos > 0 {
		return time.Unix(0, int64(unixNanos)).UTC().Format(time.RFC3339Nano)
	}
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	return fallback.UTC().Format(time.RFC3339Nano)
}

func hexID(value []byte, expectedLen int) string {
	if len(value) != expectedLen {
		return ""
	}
	allZero := true
	for _, b := range value {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return ""
	}
	return hex.EncodeToString(value)
}

func mustJSON(value any, fallback string) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(fallback)
	}
	return raw
}

func firstNonZero(values ...uint64) uint64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func nonEmpty(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func cloneMap(source map[string]any) map[string]any {
	next := make(map[string]any, len(source))
	for key, value := range source {
		next[key] = value
	}
	return next
}
