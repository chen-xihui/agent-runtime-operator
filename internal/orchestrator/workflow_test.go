package orchestrator

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// mockDispatch 记录派发调用并返回 mock 结果
type mockDispatch struct {
	mu        sync.Mutex
	accepted  bool
	callCount int
}

func (m *mockDispatch) Execute(ctx context.Context, in DispatchInput) (DispatchOutput, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return DispatchOutput{Accepted: m.accepted}, nil
}

// registerDispatchActivity 注册 DispatchNodeActivity（mock 实现）
func registerDispatchActivity(testEnv *testsuite.TestWorkflowEnvironment, md *mockDispatch) {
	testEnv.RegisterActivityWithOptions(md.Execute, activity.RegisterOptions{
		Name: "DispatchNodeActivity",
	})
}

// newTestEnv 创建 Temporal 测试环境
func newTestEnv() *testsuite.TestWorkflowEnvironment {
	suite := &testsuite.WorkflowTestSuite{}
	return suite.NewTestWorkflowEnvironment()
}

// sendNodeResults 注册延时回调，在虚拟时间推进时按序发送节点结果 Signal（确定性）
func sendNodeResults(testEnv *testsuite.TestWorkflowEnvironment, results []NodeResult) {
	// 每个 Signal 用递增的虚拟时间延迟触发
	for i, res := range results {
		delay := time.Duration(i+1) * time.Second
		testEnv.RegisterDelayedCallback(func() {
			testEnv.SignalWorkflow(nodeResultSignal, res)
		}, delay)
	}
}

// 构造顺序 DAG：analyze -> review -> comment
func sequentialData() *ExecutionData {
	return &ExecutionData{
		Nodes: map[string]*Node{
			"analyze": {ID: "analyze", Agent: "analyzer", Action: "analyze_repo"},
			"review":  {ID: "review", Agent: "reviewer", Action: "review_code"},
			"comment": {ID: "comment", Agent: "comment-bot", Action: "post_comment"},
		},
		Edges: map[string][]string{
			"analyze": {"review"},
			"review":  {"comment"},
		},
	}
}

func TestGenericOrchestratorWorkflow_Sequential(t *testing.T) {
	testEnv := newTestEnv()
	md := &mockDispatch{accepted: true}
	registerDispatchActivity(testEnv, md)
	data := sequentialData()
	input := map[string]interface{}{"tenantId": "tenant-a"}

	// 顺序发送节点成功结果
	sendNodeResults(testEnv, []NodeResult{
		{NodeID: "analyze", State: "SUCCEEDED", Output: map[string]interface{}{"ok": true}},
		{NodeID: "review", State: "SUCCEEDED"},
		{NodeID: "comment", State: "SUCCEEDED"},
	})

	testEnv.ExecuteWorkflow(GenericOrchestratorWorkflow, data, input)
	if err := testEnv.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if md.callCount != 3 {
		t.Fatalf("dispatch called %d times, want 3", md.callCount)
	}
}

func TestGenericOrchestratorWorkflow_NodeFailedThenRetry(t *testing.T) {
	testEnv := newTestEnv()
	md := &mockDispatch{accepted: true}
	registerDispatchActivity(testEnv, md)
	data := sequentialData()
	input := map[string]interface{}{"tenantId": "tenant-a"}
	// analyze 节点带重试
	data.Nodes["analyze"].Retry = agentv1.RetrySpec{Max: 2, Backoff: "fixed"}

	// analyze 第一次失败（attempt=1），重试后成功
	sendNodeResults(testEnv, []NodeResult{
		{NodeID: "analyze", State: "FAILED", Error: "boom", Attempt: 1},
		{NodeID: "analyze", State: "SUCCEEDED", Attempt: 2},
		{NodeID: "review", State: "SUCCEEDED"},
		{NodeID: "comment", State: "SUCCEEDED"},
	})

	testEnv.ExecuteWorkflow(GenericOrchestratorWorkflow, data, input)
	if err := testEnv.GetWorkflowError(); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	// analyze 派发 2 次（初始 + 重试），review/comment 各 1 次
	if md.callCount != 4 {
		t.Fatalf("dispatch called %d times, want 4", md.callCount)
	}
}

// 单元级验证 shouldRetry 逻辑（R-1 确定性重试判定）
func TestShouldRetry(t *testing.T) {
	node := &Node{Retry: agentv1.RetrySpec{Max: 3, Backoff: "fixed"}}
	if !shouldRetry(node, 1) {
		t.Fatal("attempt 1 < max 3 should retry")
	}
	if !shouldRetry(node, 2) {
		t.Fatal("attempt 2 < max 3 should retry")
	}
	if shouldRetry(node, 3) {
		t.Fatal("attempt 3 >= max 3 should not retry")
	}
	// max=0 不重试
	if shouldRetry(&Node{}, 1) {
		t.Fatal("max=0 should not retry")
	}
}

func TestGenericOrchestratorWorkflow_EmptyData(t *testing.T) {
	testEnv := newTestEnv()
	testEnv.ExecuteWorkflow(GenericOrchestratorWorkflow, &ExecutionData{}, nil)
	if err := testEnv.GetWorkflowError(); err == nil {
		t.Fatal("expected error for empty data")
	}
}
