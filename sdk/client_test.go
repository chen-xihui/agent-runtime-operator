package sdk

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

func testClient(t *testing.T, objs ...client.Object) *Client {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = agentv1.AddToScheme(scheme)
	b := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, o := range objs {
		_ = b.Create(context.Background(), o)
	}
	return NewFromClient(b)
}

func TestSDK_TenantLifecycle(t *testing.T) {
	s := testClient(t)
	ctx := context.Background()

	tnt := &agentv1.Tenant{}
	tnt.Name = "tenant-sdk"
	tnt.Spec.Quota.MaxSandboxes = 5
	if err := s.CreateTenant(ctx, tnt); err != nil {
		t.Fatalf("create tenant: %v", err)
	}

	got, err := s.GetTenant(ctx, "tenant-sdk")
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if got.Spec.Quota.MaxSandboxes != 5 {
		t.Fatalf("maxSandboxes = %d, want 5", got.Spec.Quota.MaxSandboxes)
	}

	list, _ := s.ListTenants(ctx)
	if len(list.Items) != 1 {
		t.Fatalf("tenants = %d, want 1", len(list.Items))
	}
}

func TestSDK_AgentAndSandbox(t *testing.T) {
	s := testClient(t)
	ctx := context.Background()

	// 创建 Agent（自动触发 Sandbox 调谐）
	agent := &agentv1.Agent{}
	agent.Name = "reviewer"
	agent.Spec.Image = "busybox:1.36"
	agent.Spec.Security.RunAsNonRoot = false
	if err := s.CreateAgent(ctx, "tenant-sdk", agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	got, err := s.GetAgent(ctx, "tenant-sdk", "reviewer")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if got.Name != "reviewer" || got.Namespace != "tenant-sdk" {
		t.Fatalf("agent = %s/%s", got.Namespace, got.Name)
	}

	// Suspend/Resume Sandbox
	sb := &agentv1.Sandbox{}
	sb.Name = "sb-reviewer"
	sb.Namespace = "tenant-sdk"
	_ = s.client.Create(ctx, sb)

	if err := s.SuspendSandbox(ctx, "tenant-sdk", "sb-reviewer"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	sb2, _ := s.GetSandbox(ctx, "tenant-sdk", "sb-reviewer")
	if sb2.Spec.Suspend == nil || !*sb2.Spec.Suspend {
		t.Fatal("suspend should be true")
	}
	if err := s.ResumeSandbox(ctx, "tenant-sdk", "sb-reviewer"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	sb3, _ := s.GetSandbox(ctx, "tenant-sdk", "sb-reviewer")
	if sb3.Spec.Suspend == nil || *sb3.Spec.Suspend {
		t.Fatal("suspend should be false after resume")
	}
}

func TestSDK_Workflow(t *testing.T) {
	s := testClient(t)
	ctx := context.Background()

	wf := &agentv1.Workflow{}
	wf.Name = "wf-1"
	wf.Spec.Entrypoint = "analyze"
	wf.Spec.Nodes = []agentv1.WorkflowNode{
		{ID: "analyze", Agent: "analyzer", Action: "analyze_repo"},
	}
	if err := s.CreateWorkflow(ctx, "tenant-sdk", wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	run, err := s.CreateWorkflowRun(ctx, "tenant-sdk", "wf-1", map[string]interface{}{"input": "x"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.Spec.WorkflowRef != "wf-1" {
		t.Fatalf("workflowRef = %q, want wf-1", run.Spec.WorkflowRef)
	}
	got, err := s.GetWorkflowRun(ctx, "tenant-sdk", run.Name)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Spec.Input["input"] != "x" {
		t.Fatalf("input = %v", got.Spec.Input)
	}
}
