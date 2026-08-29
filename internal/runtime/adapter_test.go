package runtime

import (
	"context"
	"testing"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	sb := &agentv1.Sandbox{}

	// gVisor → GVisor 适配器（不支持快照）
	sb.Spec.RuntimeClass = agentv1.RuntimeGVisor
	if got := r.Get(sb.Spec.RuntimeClass); got.Name() != "gvisor" {
		t.Fatalf("gvisor adapter name = %q", got.Name())
	}
	if sc, ok := r.Get(sb.Spec.RuntimeClass).(SuspendCapable); ok && sc.SnapshotSupported() {
		t.Fatal("gvisor should not support snapshot")
	}

	// Firecracker → 支持快照
	sb.Spec.RuntimeClass = agentv1.RuntimeFirecracker
	fc := r.Get(sb.Spec.RuntimeClass)
	if fc.Name() != "firecracker" {
		t.Fatalf("firecracker adapter name = %q", fc.Name())
	}
	if sc, ok := fc.(SuspendCapable); !ok || !sc.SnapshotSupported() {
		t.Fatal("firecracker should support snapshot")
	}
}

func TestAdapter_SuspendResume(t *testing.T) {
	ctx := context.Background()
	sb := &agentv1.Sandbox{}
	sb.Name = "sb-test"
	sb.Spec.RuntimeClass = agentv1.RuntimeGVisor

	r := NewRegistry()
	adapter := r.Get(sb.Spec.RuntimeClass)

	// Suspend/Resume 不应报错（gVisor 降级为无操作）
	if err := adapter.Suspend(ctx, sb); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if err := adapter.Resume(ctx, sb); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Firecracker Suspend/Resume 也不应报错（占位）
	fc := r.Get(agentv1.RuntimeFirecracker)
	if err := fc.Suspend(ctx, sb); err != nil {
		t.Fatalf("fc suspend: %v", err)
	}
	if err := fc.Resume(ctx, sb); err != nil {
		t.Fatalf("fc resume: %v", err)
	}
}

func TestRegistry_UnknownRuntime(t *testing.T) {
	r := NewRegistry()
	// 未知运行时兜底为 gVisor
	adapter := r.Get("unknown")
	if adapter.Name() != "gvisor" {
		t.Fatalf("unknown runtime adapter = %q, want gvisor", adapter.Name())
	}
}
