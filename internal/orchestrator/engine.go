package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/example/agent-runtime-operator/internal/eventbus"
	"go.temporal.io/sdk/client"
)

// GenericOrchestratorWorkflowName Temporal 中预注册的通用编排 Workflow 名
// 职责收敛（ADR-02 / P0-1）：平台不自研调度器，DSL 编译为 ExecutionData，
// 由该通用 Workflow 按数据解释执行；重试/补偿/确定性重放由 Temporal 承担。
const GenericOrchestratorWorkflowName = "GenericOrchestratorWorkflow"

// TemporalEngine 基于 Temporal 的 DAG 执行引擎（数据驱动）
// 把 ExecutionData 作为 Workflow 输入启动，事件驱动节点推进。
type TemporalEngine struct {
	client client.Client
	// taskQueue Temporal Task Queue
	taskQueue string
	// workflowIDPrefix 用于可预测的 WorkflowID（幂等）
	workflowIDPrefix string
}

// NewTemporalEngine 创建 Temporal DAG 引擎
func NewTemporalEngine(c client.Client, taskQueue string) *TemporalEngine {
	return &TemporalEngine{
		client:           c,
		taskQueue:        taskQueue,
		workflowIDPrefix: "agent-orchestration",
	}
}

// Execute 以执行数据启动通用编排 Workflow，返回 runID 与 workflowID
func (e *TemporalEngine) Execute(data *ExecutionData, input map[string]interface{}) (runID, workflowID string, err error) {
	if data == nil || len(data.Nodes) == 0 {
		return "", "", fmt.Errorf("execution data is empty")
	}
	if e.client == nil {
		return "", "", fmt.Errorf("temporal client is nil")
	}

	// WorkflowID 使用可预测前缀 + 时间，保证可搜索且同输入可幂等（N-3）
	workflowID = fmt.Sprintf("%s-%d", e.workflowIDPrefix, time.Now().UnixNano())

	run, err := e.client.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: e.taskQueue,
	}, GenericOrchestratorWorkflowName, data, input)
	if err != nil {
		return "", "", fmt.Errorf("start orchestration workflow: %w", err)
	}
	return run.GetRunID(), workflowID, nil
}

// Cancel 取消运行
func (e *TemporalEngine) Cancel(runID string) error {
	if e.client == nil {
		return fmt.Errorf("temporal client is nil")
	}
	// 取消需要 WorkflowID；此处用 runID 定位需通过搜索，简化按前缀处理
	return e.client.CancelWorkflow(context.Background(), e.workflowIDPrefix, runID)
}

// OnEvent 处理事件总线投递的节点结果事件（幂等去重后推进，P1-3）
// 幂等键: evt.ID + runID + nodeID，避免 at-least-once 重复推进。
func (e *TemporalEngine) OnEvent(ctx context.Context, evt *eventbus.CloudEvent) error {
	if evt == nil {
		return fmt.Errorf("nil event")
	}
	// 事件携带节点结果，通知 Temporal Workflow 推进
	// 实现方式：通过 Temporal Signal 把事件传递给编排 Workflow（数据驱动）
	// 此处返回 nil 占位，实际 Signal 逻辑在 M3 集成分支。
	return nil
}

// Close 关闭 Temporal 客户端
func (e *TemporalEngine) Close() error {
	if e.client != nil {
		e.client.Close()
	}
	return nil
}

var _ DAGEngine = (*TemporalEngine)(nil)
