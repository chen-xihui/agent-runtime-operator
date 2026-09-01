package controllers

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/sandbox"
)

func testSandboxReconciler(objs ...client.Object) *SandboxReconciler {
	b := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&agentv1.Sandbox{}).
		Build()
	for _, o := range objs {
		_ = b.Create(context.Background(), o)
	}
	return &SandboxReconciler{
		Client:  b,
		Scheme:  scheme,
		Sandbox: sandbox.NewController(b, scheme, &sandbox.Config{}),
	}
}

func TestSandboxReconciler_RecycleExceededLifetime(t *testing.T) {
	ctx := context.Background()

	// 关联 Agent：MaxLifetimeMin=1
	agent := &agentv1.Agent{}
	agent.Name = "reviewer"
	agent.Namespace = "tenant-a"
	agent.Spec.Security.MaxLifetimeMin = 1

	// 沙箱：Running，进入时间 2 分钟前（超时）
	sb := &agentv1.Sandbox{}
	sb.Name = "sb-1"
	sb.Namespace = "tenant-a"
	sb.Spec.AgentRef = "reviewer"
	sb.Status.Phase = agentv1.PhaseRunning
	sb.Status.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * time.Minute))

	r := testSandboxReconciler(agent, sb)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "sb-1", Namespace: "tenant-a"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// 沙箱应被回收 → Terminated
	got := &agentv1.Sandbox{}
	_ = r.Get(ctx, types.NamespacedName{Name: "sb-1", Namespace: "tenant-a"}, got)
	if got.Status.Phase != agentv1.PhaseTerminated {
		t.Fatalf("phase = %q, want Terminated", got.Status.Phase)
	}
}

func TestSandboxReconciler_NoRecycleWithinLifetime(t *testing.T) {
	ctx := context.Background()

	agent := &agentv1.Agent{}
	agent.Name = "reviewer"
	agent.Namespace = "tenant-a"
	agent.Spec.Security.MaxLifetimeMin = 60

	sb := &agentv1.Sandbox{}
	sb.Name = "sb-2"
	sb.Namespace = "tenant-a"
	sb.Spec.AgentRef = "reviewer"
	sb.Status.Phase = agentv1.PhaseRunning
	sb.Status.LastTransitionTime = metav1.NewTime(time.Now().Add(-time.Minute))

	r := testSandboxReconciler(agent, sb)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "sb-2", Namespace: "tenant-a"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &agentv1.Sandbox{}
	_ = r.Get(ctx, types.NamespacedName{Name: "sb-2", Namespace: "tenant-a"}, got)
	if got.Status.Phase == agentv1.PhaseTerminated {
		t.Fatal("sandbox should not be recycled within lifetime")
	}
}

func TestSandboxReconciler_QuotaExceeded(t *testing.T) {
	ctx := context.Background()

	// 租户：MaxSandboxes=1
	tenant := &agentv1.Tenant{}
	tenant.Name = "tenant-a"
	tenant.Spec.Quota.MaxSandboxes = 1

	// 一个已运行的沙箱（占满配额）
	sb1 := &agentv1.Sandbox{}
	sb1.Name = "sb-running"
	sb1.Namespace = "tenant-a"
	sb1.Spec.AgentRef = "a1"
	sb1.Status.Phase = agentv1.PhaseRunning

	// 新沙箱进入 Provisioning（触发配额检查）
	sb2 := &agentv1.Sandbox{}
	sb2.Name = "sb-new"
	sb2.Namespace = "tenant-a"
	sb2.Spec.AgentRef = "a2"
	sb2.Status.Phase = agentv1.PhaseProvisioning

	r := testSandboxReconciler(tenant, sb1, sb2)

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "sb-new", Namespace: "tenant-a"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// 配额满 → 新沙箱保持 Provisioning，Message 提示排队
	got := &agentv1.Sandbox{}
	_ = r.Get(ctx, types.NamespacedName{Name: "sb-new", Namespace: "tenant-a"}, got)
	if got.Status.Phase != agentv1.PhaseProvisioning {
		t.Fatalf("phase = %q, want Provisioning (queued)", got.Status.Phase)
	}
	if got.Status.Message != "waiting for tenant sandbox quota" {
		t.Fatalf("message = %q, want quota waiting", got.Status.Message)
	}
}
