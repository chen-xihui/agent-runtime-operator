package sandbox

import (
	"context"
	"testing"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSandbox_SuspendResume(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = agentv1.AddToScheme(scheme)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	c := NewController(cli, scheme, &Config{DefaultImage: "busybox:1.36"})
	ctx := context.Background()

	sb := &agentv1.Sandbox{}
	sb.Name = "sb-suspend"
	sb.Spec.RuntimeClass = agentv1.RuntimeGVisor

	// gVisor Suspend/Resume 降级为无操作，不应报错
	if err := c.Suspend(ctx, sb); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if err := c.Resume(ctx, sb); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Firecracker Suspend/Resume
	sb.Spec.RuntimeClass = agentv1.RuntimeFirecracker
	if err := c.Suspend(ctx, sb); err != nil {
		t.Fatalf("fc suspend: %v", err)
	}
	if err := c.Resume(ctx, sb); err != nil {
		t.Fatalf("fc resume: %v", err)
	}
}
