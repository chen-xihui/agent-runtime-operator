package orchestrator

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ===================== 数据契约 =====================

// NodeResult 节点执行结果（通过 Signal 返回 Workflow）
type NodeResult struct {
	NodeID  string                 `json:"nodeId"`
	State   string                 `json:"state"` // SUCCEEDED / FAILED / SKIPPED
	Output  map[string]interface{} `json:"output,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Attempt int                    `json:"attempt,omitempty"`
}

// DispatchInput Activity 派发输入
type DispatchInput struct {
	TenantID string                 `json:"tenantId"`
	RunID    string                 `json:"runId"`
	NodeID   string                 `json:"nodeId"`
	Agent    string                 `json:"agent"`
	Action   string                 `json:"action"`
	Input    map[string]interface{} `json:"input,omitempty"`
	// NodeResults 前序节点结果（用于条件求值，R-2）
	NodeResults map[string]interface{} `json:"nodeResults,omitempty"`
	// Condition 条件表达式（CEL，R-2）；为空表示无条件
	Condition string `json:"condition,omitempty"`
	Attempt   int    `json:"attempt,omitempty"`
}

// DispatchOutput Activity 派发输出
type DispatchOutput struct {
	Accepted bool `json:"accepted"`
}

// 节点结果 Signal 名
const nodeResultSignal = "node-result"

// ===================== 通用编排 Workflow =====================

// GenericOrchestratorWorkflow 通用编排 Workflow（数据驱动，预注册）。
// 职责收敛（ADR-02 / P0-1）：平台不自研调度器，本 Workflow 按 ExecutionData 解释执行。
// 确定性约束（R-1）：
//   - Workflow 内仅用 workflow.Await / Sleep / ExecuteActivity / Signal
//   - 禁止直接 NATS I/O；I/O 全部收敛到 Activity
//   - 条件表达式求值（CEL）在 Activity 内完成（确定性输入），不在 Workflow 内做非确定性计算
func GenericOrchestratorWorkflow(ctx workflow.Context, data *ExecutionData, input map[string]interface{}) error {
	logger := workflow.GetLogger(ctx)

	if data == nil || len(data.Nodes) == 0 {
		return temporal.NewNonRetryableApplicationError("empty execution data", "Orchestration", nil)
	}

	// 依赖计数
	pendingDeps := make(map[string]int, len(data.Nodes))
	dependents := make(map[string][]string, len(data.Nodes))
	for id := range data.Nodes {
		pendingDeps[id] = 0
	}
	for from, tos := range data.Edges {
		for _, to := range tos {
			pendingDeps[to]++
			dependents[from] = append(dependents[from], to)
		}
	}

	// 节点结果汇总
	results := make(map[string]*NodeResult)

	// 初始化就绪队列：无依赖的节点
	readyQueue := []string{}
	for id, deps := range pendingDeps {
		if deps == 0 {
			readyQueue = append(readyQueue, id)
		}
	}

	// 结果 Signal 通道（Workflow 生命周期内创建一次，确定性）
	resultCh := workflow.GetSignalChannel(ctx, nodeResultSignal)

	for len(readyQueue) > 0 {
		cur := readyQueue[0]
		readyQueue = readyQueue[1:]
		node := data.Nodes[cur]

		// 构造前序节点结果（确定性输入，供条件求值）
		nodeResultsForInput := buildNodeResultsView(results)

		// 派发节点（Activity；内部完成条件求值 + 发起 Agent 任务）
		dispatchErr := workflow.ExecuteActivity(
			workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Second,
					BackoffCoefficient: 2.0,
					MaximumAttempts:    3,
				},
			}),
			"DispatchNodeActivity",
			DispatchInput{
				TenantID:    getTenant(input),
				RunID:       workflow.GetInfo(ctx).WorkflowExecution.ID,
				NodeID:      cur,
				Agent:       node.Agent,
				Action:      node.Action,
				Input:       input,
				NodeResults: nodeResultsForInput,
				Condition:   node.Condition,
				Attempt:     0,
			},
		).Get(ctx, nil)
		if dispatchErr != nil {
			// 派发失败：Always 补偿节点标记失败继续，否则终止
			results[cur] = &NodeResult{NodeID: cur, State: "FAILED", Error: dispatchErr.Error()}
			if !node.Always {
				return temporal.NewNonRetryableApplicationError(
					"node dispatch failed: "+cur, "Orchestration", dispatchErr)
			}
			fanOut(cur, dependents, pendingDeps, &readyQueue)
			continue
		}

		// 确定性等待该节点结果 Signal（过滤非当前节点信号）
		for {
			var res NodeResult
			resultCh.Receive(ctx, &res)
			if res.NodeID != cur {
				// 非当前节点信号，暂存（简化为丢弃，生产用 pending buffer）
				continue
			}
			results[cur] = &res

			// 重试（指数退避）：失败且未超 max → 重派发
			if res.State == "FAILED" && shouldRetry(node, res.Attempt) {
				nextAttempt := res.Attempt + 1
				dispatchErr := workflow.ExecuteActivity(
					workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
						StartToCloseTimeout: time.Minute,
						RetryPolicy: &temporal.RetryPolicy{
							InitialInterval:    time.Second,
							BackoffCoefficient: 2.0,
							MaximumAttempts:    3,
						},
					}),
					"DispatchNodeActivity",
					DispatchInput{
						TenantID:    getTenant(input),
						RunID:       workflow.GetInfo(ctx).WorkflowExecution.ID,
						NodeID:      cur,
						Agent:       node.Agent,
						Action:      node.Action,
						Input:       input,
						NodeResults: nodeResultsForInput,
						Condition:   node.Condition,
						Attempt:     nextAttempt,
						},
				).Get(ctx, nil)
				if dispatchErr == nil {
					continue // 等待重试结果
				}
			}
			break
		}

		fanOut(cur, dependents, pendingDeps, &readyQueue)
	}

	logger.Info("orchestration completed", "nodeCount", len(data.Nodes))
	return nil
}

// buildNodeResultsView 构建前序节点结果视图（确定性输入，供条件求值）
func buildNodeResultsView(results map[string]*NodeResult) map[string]interface{} {
	out := make(map[string]interface{}, len(results))
	for id, res := range results {
		out[id] = map[string]interface{}{
			"state":  res.State,
			"result": res.Output,
		}
	}
	return out
}

// fanOut 节点完成时减少依赖计数并唤醒就绪节点
func fanOut(done string, dependents map[string][]string, pendingDeps map[string]int, readyQueue *[]string) {
	for _, next := range dependents[done] {
		pendingDeps[next]--
		if pendingDeps[next] <= 0 {
			*readyQueue = append(*readyQueue, next)
		}
	}
}

// shouldRetry 判断是否重试
func shouldRetry(node *Node, attempt int) bool {
	return node.Retry.Max > 0 && attempt < node.Retry.Max
}

// getTenant 从输入提取租户
func getTenant(input map[string]interface{}) string {
	if v, ok := input["tenantId"].(string); ok {
		return v
	}
	return ""
}
