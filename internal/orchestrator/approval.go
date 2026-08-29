package orchestrator

import "time"

// Approval 节点常量（design-doc 4.2.5 Human-in-the-loop）
const (
	// NodeKindApproval 人工审批节点 kind
	NodeKindApproval = "approval"

	// approvalResultSignal 审批结果 Signal 名（外部 Temporal 客户端发送）
	approvalResultSignal = "approval-result"
)

// 审批结果
const (
	ApprovalApproved = "APPROVED"
	ApprovalRejected = "REJECTED"
)

// ApprovalRequest 审批请求（触发 AGENT_ASK_HUMAN 时携带）
type ApprovalRequest struct {
	RunID  string `json:"runId"`
	NodeID string `json:"nodeId"`
	Agent  string `json:"agent"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	TaskID string `json:"taskId,omitempty"`
}

// ApprovalResult 审批结果（通过 Signal 返回）
type ApprovalResult struct {
	NodeID   string `json:"nodeId"`
	Decision string `json:"decision"` // APPROVED / REJECTED
	Approver string `json:"approver,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// approvalTimeout 审批超时（超时未审批按拒绝处理）
const approvalTimeout = 24 * time.Hour
