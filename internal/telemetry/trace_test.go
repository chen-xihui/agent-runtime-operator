package telemetry

import (
	"strings"
	"testing"
)

func TestNewTraceContext(t *testing.T) {
	tc := NewTraceContext()
	if len(tc.TraceID) != 32 {
		t.Fatalf("traceID len = %d, want 32", len(tc.TraceID))
	}
	if len(tc.SpanID) != 16 {
		t.Fatalf("spanID len = %d, want 16", len(tc.SpanID))
	}
	// traceparent 格式：00-traceID-spanID-flags
	tp := tc.Traceparent()
	if !strings.HasPrefix(tp, "00-") || len(tp) != 55 {
		t.Fatalf("traceparent = %q (len=%d)", tp, len(tp))
	}
}

func TestParseTraceparent(t *testing.T) {
	tc := NewTraceContext()
	tp := tc.Traceparent()

	parsed := ParseTraceparent(tp)
	if parsed == nil {
		t.Fatal("failed to parse valid traceparent")
	}
	if parsed.TraceID != tc.TraceID || parsed.SpanID != tc.SpanID {
		t.Fatalf("parsed mismatch: %+v vs %+v", parsed, tc)
	}

	// 非法格式
	if ParseTraceparent("invalid") != nil {
		t.Fatal("should not parse invalid traceparent")
	}
	if ParseTraceparent("") != nil {
		t.Fatal("should not parse empty traceparent")
	}
}

func TestWithChild(t *testing.T) {
	parent := NewTraceContext()
	child := parent.WithChild()

	// 子 span 保留 traceID，spanID 不同
	if child.TraceID != parent.TraceID {
		t.Fatalf("child traceID = %q, want %q", child.TraceID, parent.TraceID)
	}
	if child.SpanID == parent.SpanID {
		t.Fatal("child spanID should differ from parent")
	}
}

func TestParseOrNew(t *testing.T) {
	// 合法 traceparent 保留
	tc := NewTraceContext()
	parsed := ParseOrNew(tc.Traceparent())
	if parsed.TraceID != tc.TraceID {
		t.Fatalf("ParseOrNew should keep valid trace, got %q", parsed.TraceID)
	}

	// 非法则生成新 trace
	parsed = ParseOrNew("garbage")
	if parsed == nil {
		t.Fatal("ParseOrNew should return non-nil for invalid input")
	}
	if len(parsed.TraceID) != 32 {
		t.Fatalf("new traceID len = %d, want 32", len(parsed.TraceID))
	}
}
