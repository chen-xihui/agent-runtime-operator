package controllers

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/eventbus"
	"github.com/example/agent-runtime-operator/internal/orchestrator"
)

var scheme = runtime.NewScheme()

func init() {
	_ = agentv1.AddToScheme(scheme)
}

// fakeEngine mock DAGEngine
type fakeEngine struct {
	executed bool
	cancelled string
	runID    string
	err      error
}

func (f *fakeEngine) Execute(data *orchestrator.ExecutionData, input map[string]interface{}) (string, error) {
	f.executed = true
	if f.err != nil {
		return "", f.err
	}
	if f.runID != "" {
		return f.runID, nil
	}
	return "run-123", nil
}
func (f *fakeEngine) Cancel(runID string) error {
	f.cancelled = runID
	return nil
}
func (f *fakeEngine) OnEvent(ctx context.Context, evt *eventbus.CloudEvent) error { return nil }

func newReconciler(engine orchestrator.DAGEngine, objs ...client.Object) *WorkflowRunReconciler {
	b := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&agentv1.WorkflowRun{}).
		Build()
	p := orchestrator.NewDefaultParser()
	return &WorkflowRunReconciler{
		Client:   b,
		Scheme:   scheme,
		Parser:   p,
		Compiler: orchestrator.NewDefaultCompiler(p),
		Engine:   engine,
	}
}

func validWorkflow() *agentv1.Workflow {
	return &agentv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf-1", Namespace: "tenant-a"},
		Spec: agentv1.WorkflowSpec{
			Entrypoint: "analyze",
			Nodes: []agentv1.WorkflowNode{
				{ID: "analyze", Agent: "analyzer", Action: "analyze_repo"},
				{ID: "review", Agent: "reviewer", Action: "review_code", DependsOn: []string{"analyze"}},
			},
		},
	}
}

func TestWorkflowRun_StartSuccess(t *testing.T) {
	engine := &fakeEngine{runID: "run-abc"}
	r := newReconciler(engine, validWorkflow())

	run := &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "wr-1", Namespace: "tenant-a"},
		Spec:       agentv1.WorkflowRunSpec{WorkflowRef: "wf-1"},
	}
	if err := r.Create(context.Background(), run); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-1", Namespace: "tenant-a"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if !engine.executed {
		t.Fatal("engine.Execute not called")
	}
	got := &agentv1.WorkflowRun{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "wr-1", Namespace: "tenant-a"}, got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != agentv1.PhaseRunRunning {
		t.Fatalf("phase = %q, want RUNNING", got.Status.Phase)
	}
	if got.Status.RunID != "run-abc" {
		t.Fatalf("runID = %q, want run-abc", got.Status.RunID)
	}
}

func TestWorkflowRun_WorkflowNotFound(t *testing.T) {
	engine := &fakeEngine{}
	r := newReconciler(engine) // 无 Workflow 对象

	run := &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "wr-missing", Namespace: "tenant-a"},
		Spec:       agentv1.WorkflowRunSpec{WorkflowRef: "nope"},
	}
	if err := r.Create(context.Background(), run); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-missing", Namespace: "tenant-a"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &agentv1.WorkflowRun{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "wr-missing", Namespace: "tenant-a"}, got)
	if got.Status.Phase != agentv1.PhaseRunFailed {
		t.Fatalf("phase = %q, want FAILED", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Error, "not found") {
		t.Fatalf("error = %q, want 'not found'", got.Status.Error)
	}
}

func TestWorkflowRun_CompileError(t *testing.T) {
	engine := &fakeEngine{}
	// 环依赖的 Workflow
	cycleWF := &agentv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf-cycle", Namespace: "tenant-a"},
		Spec: agentv1.WorkflowSpec{Nodes: []agentv1.WorkflowNode{
			{ID: "a", Agent: "x", Action: "y", DependsOn: []string{"c"}},
			{ID: "b", Agent: "x", Action: "y", DependsOn: []string{"a"}},
			{ID: "c", Agent: "x", Action: "y", DependsOn: []string{"b"}},
		}},
	}
	r := newReconciler(engine, cycleWF)
	run := &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "wr-cycle", Namespace: "tenant-a"},
		Spec:       agentv1.WorkflowRunSpec{WorkflowRef: "wf-cycle"},
	}
	_ = r.Create(context.Background(), run)

	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-cycle", Namespace: "tenant-a"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := &agentv1.WorkflowRun{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "wr-cycle", Namespace: "tenant-a"}, got)
	if got.Status.Phase != agentv1.PhaseRunFailed {
		t.Fatalf("phase = %q, want FAILED", got.Status.Phase)
	}
	if engine.executed {
		t.Fatal("engine should not execute on compile error")
	}
}

func TestWorkflowRun_ExecuteError(t *testing.T) {
	engine := &fakeEngine{err: errors.New("temporal down")}
	r := newReconciler(engine, validWorkflow())
	run := &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "wr-err", Namespace: "tenant-a"},
		Spec:       agentv1.WorkflowRunSpec{WorkflowRef: "wf-1"},
	}
	_ = r.Create(context.Background(), run)

	// 控制器回写 FAILED 且不返回 error（避免无限重试）
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-err", Namespace: "tenant-a"}}); err != nil {
		t.Fatalf("reconcile should not return error after FAILED: %v", err)
	}
	got := &agentv1.WorkflowRun{}
	_ = r.Get(context.Background(), types.NamespacedName{Name: "wr-err", Namespace: "tenant-a"}, got)
	if got.Status.Phase != agentv1.PhaseRunFailed {
		t.Fatalf("phase = %q, want FAILED", got.Status.Phase)
	}
}

func TestWorkflowRun_Cancel(t *testing.T) {
	engine := &fakeEngine{}
	r := newReconciler(engine)
	run := &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: "wr-cancel", Namespace: "tenant-a"},
		Spec:       agentv1.WorkflowRunSpec{WorkflowRef: "wf-1"},
		Status:     agentv1.WorkflowRunStatus{RunID: "run-x", Phase: agentv1.PhaseRunRunning},
	}
	_ = r.Create(context.Background(), run)

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-cancel", Namespace: "tenant-a"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 未取消前，RUNNING 状态应直接返回
	if engine.cancelled != "" {
		t.Fatalf("cancel called unexpectedly: %s", engine.cancelled)
	}

	// 标记取消
	run.Status.Phase = agentv1.PhaseRunCancelled
	_ = r.Status().Update(context.Background(), run)
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "wr-cancel", Namespace: "tenant-a"}}); err != nil {
		t.Fatalf("reconcile cancel: %v", err)
	}
	if engine.cancelled != "run-x" {
		t.Fatalf("cancel called with %q, want run-x", engine.cancelled)
	}
}
