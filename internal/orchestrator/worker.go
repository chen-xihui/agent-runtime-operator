package orchestrator

import (
	"go.temporal.io/sdk/worker"
)

// Worker 编排 Worker：运行 GenericOrchestratorWorkflow 及编排 Activity。
// 平台不自研调度器（ADR-02），DSL 编译为 ExecutionData 由通用 Workflow 解释执行，
// 本 Worker 负责承载该 Workflow 与节点派发 Activity。
type Worker struct {
	worker worker.Worker
}

// NewWorker 创建编排 Worker 并注册 Workflow 与 Activity
func NewWorker(w worker.Worker) *Worker {
	// 注册通用编排 Workflow
	w.RegisterWorkflow(GenericOrchestratorWorkflow)
	// 注册节点派发 Activity 与审批请求 Activity
	w.RegisterActivity(DispatchNodeActivity)
	w.RegisterActivity(RequestApprovalActivity)
	return &Worker{worker: w}
}

// Start 启动 Worker（阻塞）
func (w *Worker) Start() error {
	return w.worker.Run(worker.InterruptCh())
}

// StartAsync 非阻塞启动（供测试/manager 管理）
func (w *Worker) StartAsync() error {
	return w.worker.Start()
}

// Stop 停止 Worker
func (w *Worker) Stop() {
	w.worker.Stop()
}
