package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetrics_Observe(t *testing.T) {
	// 各观测函数不应 panic
	ObserveRunStarted("tenant-a", "wf-1")
	ObserveRunDuration("tenant-a", "succeeded", 2*time.Second)
	ObserveEvent("tenant-a", "NODE_SUCCEEDED")
	ObserveSandboxTransition("tenant-a", "Provisioning", "Running")
	SetSandboxActive("gvisor", 3)
	ObserveToolCall("tenant-a", "db.query", "success")
	ObserveMCPError("tenant-a", "db.query")
}

func TestMetrics_Values(t *testing.T) {
	// 验证计数累积
	ObserveToolCall("tenant-a", "code.search", "success")
	ObserveToolCall("tenant-a", "code.search", "success")

	if v := testutil.ToFloat64(toolCalls.WithLabelValues("tenant-a", "code.search", "success")); v != 2 {
		t.Fatalf("toolCalls = %v, want 2", v)
	}

	ObserveSandboxTransition("tenant-a", "Pending", "Provisioning")
	if v := testutil.ToFloat64(sandboxStateTransitions.WithLabelValues("tenant-a", "Pending", "Provisioning")); v != 1 {
		t.Fatalf("sandboxTransitions = %v, want 1", v)
	}
}
