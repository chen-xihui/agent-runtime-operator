package orchestrator

import (
	"context"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// approvalData 构造含 Approval 节点的 DAG：
//   analyze(agent) -> approve(approval) -> deploy(agent)
func approvalData() *ExecutionData {
	return &ExecutionData{
		Nodes: map[string]*Node{
			"analyze": {ID: "analyze", Agent: "analyzer", Action: "analyze_repo"},
			"approve": {ID: "approve", Agent: "admin", Action: "review", Kind: NodeKindApproval},
			"deploy":  {ID: "deploy", Agent: "deployer", Action: "deploy"},
		},
		Edges: map[string][]string{
			"analyze": {"approve"},
			"approve": {"deploy"},
		},
	}
}

// registerApprovalActivity 注册 RequestApprovalActivity mock
func registerApprovalActivity(testEnv *testsuite.TestWorkflowEnvironment) {
	testEnv.RegisterActivityWithOptions(
		func(ctx context.Context, in ApprovalRequest) error { return nil },
		activity.RegisterOptions{Name: "RequestApprovalActivity"},
	)
}

func TestGenericOrchestratorWorkflow_ApprovalApproved(t *testing.T) {
	testEnv := newTestEnv()
	md := &mockDispatch{accepted: true}
	registerDispatchActivity(testEnv, md)
	registerApprovalActivity(testEnv)

	data := approvalData()
	input := map[string]interface{}{"tenantId": "tenant-a"}

	// 发送 analyze 结果 + 审批通过 Signal + deploy 结果
	testEnv.RegisterDelayedCallback(func() {
		testEnv.SignalWorkflow(nodeResultSignal, NodeResult{NodeID: "analyze", State: "SUCCEEDED"})
	}, 1*time.Second)
	testEnv.RegisterDelayedCallback(func() {
		testEnv.SignalWorkflow(approvalResultSignal, ApprovalResult{NodeID: "approve", Decision: ApprovalApproved, Approver: "alice"})
	}, 2*time.Second)
	testEnv.RegisterDelayedCallback(func() {
		testEnv.SignalWorkflow(nodeResultSignal, NodeResult{NodeID: "deploy", State: "SUCCEEDED"})
	}, 3*time.Second)

	testEnv.ExecuteWorkflow(GenericOrchestratorWorkflow, data, input)
	if err := testEnv.GetWorkflowError(); err != nil {
		t.Fatalf("workflow should succeed after approval: %v", err)
	}
	// analyze + deploy 派发各 1 次，approve 不派发 Agent
	if md.callCount != 2 {
		t.Fatalf("dispatch called %d times, want 2", md.callCount)
	}
}

func TestGenericOrchestratorWorkflow_ApprovalRejected(t *testing.T) {
	testEnv := newTestEnv()
	md := &mockDispatch{accepted: true}
	registerDispatchActivity(testEnv, md)
	registerApprovalActivity(testEnv)

	data := approvalData()
	input := map[string]interface{}{"tenantId": "tenant-a"}

	testEnv.RegisterDelayedCallback(func() {
		testEnv.SignalWorkflow(nodeResultSignal, NodeResult{NodeID: "analyze", State: "SUCCEEDED"})
	}, 1*time.Second)
	testEnv.RegisterDelayedCallback(func() {
		testEnv.SignalWorkflow(approvalResultSignal, ApprovalResult{NodeID: "approve", Decision: ApprovalRejected})
	}, 2*time.Second)

	testEnv.ExecuteWorkflow(GenericOrchestratorWorkflow, data, input)
	// 拒绝且 approve 非 Always → Workflow 失败
	if err := testEnv.GetWorkflowError(); err == nil {
		t.Fatal("expected workflow failure on rejected approval")
	}
}

func TestApprovalNode_ResultMapping(t *testing.T) {
	// 验证审批结果常量
	if ApprovalApproved != "APPROVED" {
		t.Fatalf("approved const = %q", ApprovalApproved)
	}
	if ApprovalRejected != "REJECTED" {
		t.Fatalf("rejected const = %q", ApprovalRejected)
	}
	if NodeKindApproval != "approval" {
		t.Fatalf("kind const = %q", NodeKindApproval)
	}
}
