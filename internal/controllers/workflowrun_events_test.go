package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/eventbus"
)

func nodeEvent(id, runID, nodeID, evtType string) *eventbus.CloudEvent {
	return &eventbus.CloudEvent{
		ID:       id,
		Type:     evtType,
		TenantID: "tenant-a",
		Source:   "workflow/" + runID,
		Subject:  runID + "/" + nodeID,
		Data:     map[string]interface{}{"node": nodeID, "runID": runID},
	}
}

func newEventProcessor(runs ...*agentv1.WorkflowRun) *NodeEventProcessor {
	b := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&agentv1.WorkflowRun{}).
		Build()
	for _, r := range runs {
		_ = b.Create(context.Background(), r)
	}
	return NewNodeEventProcessor(b)
}

func testRun(id, runID string) *agentv1.WorkflowRun {
	return &agentv1.WorkflowRun{
		ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: "tenant-a"},
		Spec:       agentv1.WorkflowRunSpec{WorkflowRef: "wf-1"},
		Status:     agentv1.WorkflowRunStatus{RunID: runID, Phase: agentv1.PhaseRunRunning},
	}
}

func TestNodeEventProcessor_Progress(t *testing.T) {
	r := testRun("wr-1", "run-1")
	p := newEventProcessor(r)
	ctx := context.Background()

	// 节点 started
	if err := p.OnEvent(ctx, nodeEvent("evt-1", "run-1", "analyze", eventbus.EventNodeStarted)); err != nil {
		t.Fatalf("on started: %v", err)
	}
	// 节点 succeeded
	if err := p.OnEvent(ctx, nodeEvent("evt-2", "run-1", "analyze", eventbus.EventNodeSucceeded)); err != nil {
		t.Fatalf("on succeeded: %v", err)
	}

	got := &agentv1.WorkflowRun{}
	_ = p.Get(ctx, types.NamespacedName{Name: "wr-1", Namespace: "tenant-a"}, got)
	if got.Status.EventsCount != 2 {
		t.Fatalf("eventsCount = %d, want 2", got.Status.EventsCount)
	}
	if got.Status.CurrentNode != "analyze" {
		t.Fatalf("currentNode = %q, want analyze", got.Status.CurrentNode)
	}
	res, ok := got.Status.NodeResults["analyze"].(map[string]interface{})
	if !ok || res["state"] != "SUCCEEDED" {
		t.Fatalf("node result = %v", got.Status.NodeResults)
	}
	// 完成判定：单节点成功 → SUCCEEDED
	if got.Status.Phase != agentv1.PhaseRunSucceeded {
		t.Fatalf("phase = %q, want SUCCEEDED", got.Status.Phase)
	}
}

func TestNodeEventProcessor_Idempotency(t *testing.T) {
	r := testRun("wr-2", "run-2")
	p := newEventProcessor(r)
	ctx := context.Background()

	// 同一事件 ID 投递两次，只应处理一次（P1-3）
	if err := p.OnEvent(ctx, nodeEvent("evt-same", "run-2", "review", eventbus.EventNodeSucceeded)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := p.OnEvent(ctx, nodeEvent("evt-same", "run-2", "review", eventbus.EventNodeSucceeded)); err != nil {
		t.Fatalf("second: %v", err)
	}

	got := &agentv1.WorkflowRun{}
	_ = p.Get(ctx, types.NamespacedName{Name: "wr-2", Namespace: "tenant-a"}, got)
	if got.Status.EventsCount != 1 {
		t.Fatalf("eventsCount = %d, want 1 (dedup)", got.Status.EventsCount)
	}
}

func TestNodeEventProcessor_Failure(t *testing.T) {
	r := testRun("wr-3", "run-3")
	p := newEventProcessor(r)
	ctx := context.Background()

	if err := p.OnEvent(ctx, nodeEvent("evt-f1", "run-3", "review", eventbus.EventNodeFailed)); err != nil {
		t.Fatalf("on failed: %v", err)
	}

	got := &agentv1.WorkflowRun{}
	_ = p.Get(ctx, types.NamespacedName{Name: "wr-3", Namespace: "tenant-a"}, got)
	if got.Status.Phase != agentv1.PhaseRunFailed {
		t.Fatalf("phase = %q, want FAILED", got.Status.Phase)
	}
	if res, _ := got.Status.NodeResults["review"].(map[string]interface{}); res["state"] != "FAILED" {
		t.Fatalf("node result = %v", got.Status.NodeResults)
	}
}

func TestNodeEventProcessor_UnknownRunID(t *testing.T) {
	p := newEventProcessor() // 无 WorkflowRun
	ctx := context.Background()

	// runID 不存在，应静默忽略（不报错）
	if err := p.OnEvent(ctx, nodeEvent("evt-x", "run-none", "a", eventbus.EventNodeSucceeded)); err != nil {
		t.Fatalf("should not error for unknown run: %v", err)
	}
}

func TestParseNodeEvent(t *testing.T) {
	evt := nodeEvent("evt-1", "run-1", "analyze", eventbus.EventNodeSucceeded)
	runID, nodeID, ok := parseNodeEvent(evt)
	if !ok || runID != "run-1" || nodeID != "analyze" {
		t.Fatalf("parse = %q %q %v", runID, nodeID, ok)
	}

	// Data 缺失 runID → 解析失败
	evt.Data = map[string]interface{}{"node": "analyze"}
	if _, _, ok := parseNodeEvent(evt); ok {
		t.Fatal("should not parse without runID")
	}
}
