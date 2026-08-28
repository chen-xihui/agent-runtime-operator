// Package orchestrator 提供事件驱动 Multi-Agent 编排引擎接口。
// 见 design-doc 4.2 / core-interface 2。
//
// 编排职责收敛（ADR-02 / 评审 P0-1）：平台只做 DSL→执行数据，不自研调度器。
// DSL 在创建 WorkflowRun 时解析为 DAG 数据，交由 Temporal 预注册的
// GenericOrchestratorWorkflow 按数据解释执行；重试/补偿/确定性重放全部由 Temporal 承担。
package orchestrator

import (
	"context"
	"time"

	"github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/eventbus"
)

// Graph 由 DSL 解析得到的可执行 DAG
type Graph struct {
	Nodes map[string]*Node       `json:"nodes"`
	Edges map[string][]string    `json:"edges"` // 依赖边: nodeID -> 依赖它的节点
}

type Node struct {
	ID        string
	Agent     string
	Action    string
	Retry     v1.RetrySpec
	Condition string
	Always    bool
	Timeout   time.Duration
	Kind      string
}

// Parser 将 Workflow CR 解析为 Graph
type Parser interface {
	Parse(spec *v1.WorkflowSpec) (*Graph, error)
	Validate(g *Graph) error // 检测环、缺失依赖、无入口
}

// Compiler 将 DSL/Graph 转换为执行数据
type Compiler interface {
	// Compile 将 Graph 转换为可供 GenericOrchestratorWorkflow 执行的 DAG 数据
	Compile(g *Graph) (*ExecutionData, error)
	// Validate 编译期校验（Temporal 侧也可二次校验）
	Validate(g *Graph) error
}

// ExecutionData 通用编排 Workflow 的输入数据（数据驱动）
type ExecutionData struct {
	Nodes map[string]*Node       `json:"nodes"`
	Edges map[string][]string    `json:"edges"`
	Retry v1.RetrySpec           `json:"retry,omitempty"`
}

// DAGEngine 执行 DAG（底层委托 Temporal GenericOrchestratorWorkflow）
type DAGEngine interface {
	// Execute 以执行数据启动通用编排 Workflow，返回 runID
	Execute(data *ExecutionData, input map[string]interface{}) (runID string, err error)
	// Cancel 取消运行
	Cancel(runID string) error
	// OnEvent 处理事件总线投递的节点结果事件（幂等去重后推进）
	// 幂等键: evt.ID + runID + nodeID，避免 at-least-once 重复推进（P1-3）
	OnEvent(ctx context.Context, evt *eventbus.CloudEvent) error
}

// ConditionEvaluator condition 表达式求值器（R-2），采用 CEL，运行于确定性上下文
type ConditionEvaluator interface {
	// Compile 静态编译校验 CEL 表达式，非法时在提交阶段即报错
	Compile(expr string) error
	// Eval 求值: 绑定 nodes.<id>.result / input / env
	Eval(ctx context.Context, expr string, bindings map[string]any) (bool, error)
}
