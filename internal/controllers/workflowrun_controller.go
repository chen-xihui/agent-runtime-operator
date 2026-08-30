package controllers

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/eventbus"
	"github.com/example/agent-runtime-operator/internal/metrics"
	"github.com/example/agent-runtime-operator/internal/orchestrator"
)

// WorkflowRunReconciler 调谐 WorkflowRun 资源。
// 职责（design-doc 4.2.4 / ADR-02）：
//   - 创建时解析引用的 Workflow → 编译为 ExecutionData → 委托 Temporal 执行
//   - 状态回写（R-5）：status 为只读低频快照，实际以 Temporal 为事实状态源
//   - 取消：标记删除时调用引擎 Cancel
type WorkflowRunReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Parser   orchestrator.Parser
	Compiler orchestrator.Compiler
	Engine   orchestrator.DAGEngine
	// NodeEvents 节点事件处理器（幂等推进 WorkflowRun 状态，P1-3）
	NodeEvents *NodeEventProcessor
}

// EventHandler 返回节点事件处理函数（供事件总线订阅接入）
func (r *WorkflowRunReconciler) EventHandler() func(ctx context.Context, evt *eventbus.CloudEvent) error {
	if r.NodeEvents == nil {
		return func(ctx context.Context, evt *eventbus.CloudEvent) error { return nil }
	}
	return r.NodeEvents.OnEvent
}

// +kubebuilder:rbac:groups=agent.runtime.io,resources=workflowruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=workflowruns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agent.runtime.io,resources=workflowruns/finalizers,verbs=update
// +kubebuilder:rbac:groups=agent.runtime.io,resources=workflows,verbs=get;list;watch

// Reconcile 调谐逻辑
func (r *WorkflowRunReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	run := &agentv1.WorkflowRun{}
	if err := r.Get(ctx, req.NamespacedName, run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 终止：调用引擎取消
	if run.Status.Phase == agentv1.PhaseRunCancelled || !run.DeletionTimestamp.IsZero() {
		if run.Status.RunID != "" && r.Engine != nil {
			_ = r.Engine.Cancel(run.Status.RunID)
		}
		return ctrl.Result{}, nil
	}

	// 已启动（有 runID 且状态 RUNNING/终态）→ 仅低频刷新（R-5），此处简化直接返回
	if run.Status.RunID != "" && (run.Status.Phase == agentv1.PhaseRunRunning ||
		run.Status.Phase == agentv1.PhaseRunSucceeded ||
		run.Status.Phase == agentv1.PhaseRunFailed) {
		return ctrl.Result{}, nil
	}

	// 解析引用的 Workflow CR
	workflow := &agentv1.Workflow{}
	if err := r.Get(ctx, client.ObjectKey{Name: run.Spec.WorkflowRef, Namespace: run.Namespace}, workflow); err != nil {
		if apierrors.IsNotFound(err) {
			r.updateStatus(ctx, run, agentv1.PhaseRunFailed, "", "", fmt.Sprintf("referenced workflow %q not found", run.Spec.WorkflowRef))
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 编译 Workflow → ExecutionData（Parser + Compiler）
	data, err := r.compile(workflow)
	if err != nil {
		r.updateStatus(ctx, run, agentv1.PhaseRunFailed, "", "", fmt.Sprintf("compile workflow: %v", err))
		return ctrl.Result{}, nil
	}

	// 委托 Temporal 执行
	if r.Engine == nil {
		err = fmt.Errorf("orchestration engine is not configured")
		r.updateStatus(ctx, run, agentv1.PhaseRunFailed, "", "", err.Error())
		return ctrl.Result{}, nil
	}
	runID, workflowID, err := r.Engine.Execute(data, run.Spec.Input)
	if err != nil {
		r.updateStatus(ctx, run, agentv1.PhaseRunFailed, "", "", fmt.Sprintf("execute workflow: %v", err))
		return ctrl.Result{}, nil
	}

	// 回写 runID + workflowID + RUNNING
	r.updateRunStatus(ctx, run, agentv1.PhaseRunRunning, runID, workflowID)
	logger.Info("workflow run started", "run", run.Name, "workflow", workflow.Name, "runID", runID, "workflowID", workflowID)

	// 可观测性指标（M5）
	metrics.ObserveRunStarted(run.Namespace, workflow.Name)
	return ctrl.Result{}, nil
}

// compile 解析并编译 Workflow 为执行数据
func (r *WorkflowRunReconciler) compile(w *agentv1.Workflow) (*orchestrator.ExecutionData, error) {
	if r.Parser == nil {
		return nil, fmt.Errorf("parser is not configured")
	}
	if r.Compiler == nil {
		return nil, fmt.Errorf("compiler is not configured")
	}
	g, err := r.Parser.Parse(&w.Spec)
	if err != nil {
		return nil, err
	}
	if err := r.Parser.Validate(g); err != nil {
		return nil, err
	}
	return r.Compiler.Compile(g)
}

// updateRunStatus 回写 runID + workflowID（R-5 低频快照）
func (r *WorkflowRunReconciler) updateRunStatus(ctx context.Context, run *agentv1.WorkflowRun, phase, runID, workflowID string) {
	changed := false
	if run.Status.Phase != phase {
		run.Status.Phase = phase
		changed = true
	}
	if runID != "" && run.Status.RunID != runID {
		run.Status.RunID = runID
		changed = true
	}
	if workflowID != "" && run.Status.WorkflowID != workflowID {
		run.Status.WorkflowID = workflowID
		changed = true
	}
	if !changed {
		return
	}
	if err := r.Status().Update(ctx, run); err != nil {
		log.FromContext(ctx).Error(err, "failed to update workflowrun status", "run", run.Name)
	}
}

// updateStatus 更新 WorkflowRun 状态（R-5 低频快照）
func (r *WorkflowRunReconciler) updateStatus(ctx context.Context, run *agentv1.WorkflowRun, phase, runID, node string, errMsg string) {
	changed := false
	if run.Status.Phase != phase {
		run.Status.Phase = phase
		changed = true
	}
	if runID != "" && run.Status.RunID != runID {
		run.Status.RunID = runID
		changed = true
	}
	if node != "" && run.Status.CurrentNode != node {
		run.Status.CurrentNode = node
		changed = true
	}
	if errMsg != "" && run.Status.Error != errMsg {
		run.Status.Error = errMsg
		changed = true
	}
	if !changed {
		return
	}
	if err := r.Status().Update(ctx, run); err != nil {
		log.FromContext(ctx).Error(err, "failed to update workflowrun status", "run", run.Name)
	}
}

// SetupWithManager 注册控制器
func (r *WorkflowRunReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1.WorkflowRun{}).
		Complete(r)
}
