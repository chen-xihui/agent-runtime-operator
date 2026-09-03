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
	// 启用 status subresource（CancelWorkflowRun 经 Status().Update 写 phase）
	b := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&agentv1.WorkflowRun{}).
		Build()
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

func TestSDK_DeleteAgent(t *testing.T) {
	s := testClient(t)
	ctx := context.Background()
	a := &agentv1.Agent{}
	a.Name = "gone"
	a.Namespace = "tenant-sdk"
	a.Spec.Image = "busybox"
	_ = s.client.Create(ctx, a)

	if err := s.DeleteAgent(ctx, "tenant-sdk", "gone"); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if _, err := s.GetAgent(ctx, "tenant-sdk", "gone"); err == nil {
		t.Fatal("agent should be deleted")
	}
}

func TestSDK_WorkflowRunCancelAndEvents(t *testing.T) {
	s := testClient(t)
	ctx := context.Background()

	run := &agentv1.WorkflowRun{}
	run.Name = "run-1"
	run.Namespace = "tenant-sdk"
	run.Spec.WorkflowRef = "wf-1"
	run.Status.Phase = agentv1.PhaseRunRunning
	run.Status.RunID = "run-id-1"
	run.Status.NodeResults = map[string]interface{}{
		"analyze": map[string]interface{}{"state": "SUCCEEDED", "event": "evt-x"},
		"review":  map[string]interface{}{"state": "SUCCEEDED", "event": "evt-y"},
	}
	if err := s.client.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	// 经 Status() 子资源写入 status（否则 Get 拿不到 status，因有 status subresource）
	cur := &agentv1.WorkflowRun{}
	_ = s.client.Get(ctx, client.ObjectKey{Name: "run-1", Namespace: "tenant-sdk"}, cur)
	cur.Status = run.Status
	if err := s.client.Status().Update(ctx, cur); err != nil {
		t.Fatalf("update status: %v", err)
	}

	// 列表
	list, err := s.ListWorkflowRuns(ctx, "tenant-sdk")
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("list runs: n=%d err=%v", len(list.Items), err)
	}

	// 事件派生（2 节点成功，无终态 WORKFLOW_COMPLETED，因为 RUNNING）
	events, err := s.GetWorkflowRunEvents(ctx, "tenant-sdk", "run-1")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	found := map[string]bool{}
	for _, e := range events {
		found[e.Node] = e.Type == "NODE_SUCCEEDED"
	}
	if !found["analyze"] || !found["review"] {
		t.Fatalf("events = %+v", events)
	}

	// cancel → phase CANCELLED
	if err := s.CancelWorkflowRun(ctx, "tenant-sdk", "run-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	got, _ := s.GetWorkflowRun(ctx, "tenant-sdk", "run-1")
	if got.Status.Phase != agentv1.PhaseRunCancelled {
		t.Fatalf("phase after cancel = %q, want CANCELLED", got.Status.Phase)
	}
	// cancel 幂等
	if err := s.CancelWorkflowRun(ctx, "tenant-sdk", "run-1"); err != nil {
		t.Fatalf("cancel idempotent: %v", err)
	}

	// 取消后 events 追加 WORKFLOW_COMPLETED
	events, _ = s.GetWorkflowRunEvents(ctx, "tenant-sdk", "run-1")
	completed := false
	for _, e := range events {
		if e.Type == "WORKFLOW_COMPLETED" && e.State == agentv1.PhaseRunCancelled {
			completed = true
		}
	}
	if !completed {
		t.Fatalf("expected WORKFLOW_COMPLETED, got %+v", events)
	}
}
