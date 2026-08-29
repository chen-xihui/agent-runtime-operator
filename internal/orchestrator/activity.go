package orchestrator

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/temporal"
)

// NodeDispatcher 节点派发器（Activity 实现）。
// 职责（R-1：I/O 收敛到 Activity）：
//   - 条件节点求值（CEL，确定性输入）→ 不满足则跳过（NODE_SKIPPED）
//   - 发起 Agent 任务（派发 NODE_STARTED 到事件总线 / Agent 沙箱）
//   - 节点结果经 Signal 返回 Workflow
type NodeDispatcher struct {
	// Condition 条件求值器（R-2 CEL）
	Condition ConditionEvaluator
	// Dispatch 实际的 Agent 任务派发函数（注入，可测）
	Dispatch func(ctx context.Context, in DispatchInput) error
	// EventSink 可选：派发事件（NODE_STARTED）到外部
	EventSink func(ctx context.Context, in DispatchInput, evtType string) error
}

// DispatchNodeActivity 节点派发 Activity（预注册名）
func DispatchNodeActivity(ctx context.Context, in DispatchInput) (DispatchOutput, error) {
	d := GetNodeDispatcher(ctx)
	if d == nil {
		return DispatchOutput{}, temporal.NewNonRetryableApplicationError(
			"node dispatcher not configured", "Orchestration", nil)
	}

	// 1. 条件求值（若有条件）：不满足 → 跳过（通过 Signal NODE_SKIPPED）
	if d.Condition != nil {
		skip, err := d.Condition.Eval(ctx, buildConditionExpr(in), map[string]interface{}{
			"nodes": in.NodeResults,
			"input": in.Input,
		})
		if err != nil {
			return DispatchOutput{}, fmt.Errorf("condition eval: %w", err)
		}
		if skip {
			// 标记跳过：通过 EventSink 发 NODE_SKIPPED（Workflow 等待该 Signal）
			if d.EventSink != nil {
				_ = d.EventSink(ctx, in, "NODE_SKIPPED")
			}
			return DispatchOutput{Accepted: false}, nil
		}
	}

	// 2. 派发 NODE_STARTED 事件
	if d.EventSink != nil {
		if err := d.EventSink(ctx, in, "NODE_STARTED"); err != nil {
			return DispatchOutput{}, fmt.Errorf("emit NODE_STARTED: %w", err)
		}
	}

	// 3. 发起 Agent 任务（实际派发到 Agent 沙箱）
	if d.Dispatch != nil {
		if err := d.Dispatch(ctx, in); err != nil {
			return DispatchOutput{}, fmt.Errorf("dispatch to agent: %w", err)
		}
	}
	return DispatchOutput{Accepted: true}, nil
}

// buildConditionExpr 构造条件表达式（来自节点定义，经 DispatchInput 携带）
func buildConditionExpr(in DispatchInput) string {
	return in.Condition
}

// nodeDispatcherKey activity 上下文中保存 dispatcher 的 key
type nodeDispatcherKey struct{}

// WithNodeDispatcher 将 dispatcher 绑定到 activity context（测试/注册用）
func WithNodeDispatcher(ctx context.Context, d *NodeDispatcher) context.Context {
	return context.WithValue(ctx, nodeDispatcherKey{}, d)
}

// GetNodeDispatcher 从 activity context 获取 dispatcher
func GetNodeDispatcher(ctx context.Context) *NodeDispatcher {
	if d, ok := ctx.Value(nodeDispatcherKey{}).(*NodeDispatcher); ok {
		return d
	}
	return nil
}
