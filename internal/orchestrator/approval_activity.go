package orchestrator

import (
	"context"
	"fmt"
)

// ApprovalNotifier 审批请求通知器（触发 AGENT_ASK_HUMAN，通知外部工单系统）
// 职责（R-1：I/O 收敛到 Activity）：向事件总线/外部系统发布人工介入请求。
type ApprovalNotifier struct {
	// Notify 实际的通知函数（注入，可测）
	Notify func(ctx context.Context, req ApprovalRequest) error
}

// RequestApprovalActivity 审批请求 Activity（预注册名）
// 触发 AGENT_ASK_HUMAN 事件，通知外部工单系统等待人工审批。
func RequestApprovalActivity(ctx context.Context, req ApprovalRequest) error {
	n := GetApprovalNotifier(ctx)
	if n == nil {
		// 未配置通知器：默认静默通过（审批由外部 Signal 驱动）
		return nil
	}
	if n.Notify != nil {
		if err := n.Notify(ctx, req); err != nil {
			return fmt.Errorf("notify approval request: %w", err)
		}
	}
	return nil
}

// approvalNotifierKey activity context 中保存通知器的 key
type approvalNotifierKey struct{}

// WithApprovalNotifier 将通知器绑定到 activity context（测试/注册用）
func WithApprovalNotifier(ctx context.Context, n *ApprovalNotifier) context.Context {
	return context.WithValue(ctx, approvalNotifierKey{}, n)
}

// GetApprovalNotifier 从 activity context 获取通知器
func GetApprovalNotifier(ctx context.Context) *ApprovalNotifier {
	if n, ok := ctx.Value(approvalNotifierKey{}).(*ApprovalNotifier); ok {
		return n
	}
	return nil
}
