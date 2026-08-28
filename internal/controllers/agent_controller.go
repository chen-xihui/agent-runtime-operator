package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/sandbox"
)

// AgentReconciler 调谐 Agent 资源，创建关联的 Sandbox
type AgentReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Sandbox *sandbox.Controller
}

// +kubebuilder:rbac:groups=agent.runtime.io,resources=agents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agent.runtime.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete

// Reconcile 调谐逻辑
func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	agent := &agentv1.Agent{}
	if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 已有关联 Sandbox？
	if agent.Status.SandboxRef != "" {
		return r.syncStatus(ctx, agent, agent.Status.SandboxRef)
	}

	// 创建 Sandbox
	sbName := fmt.Sprintf("sb-%s", agent.Name)
	sb := &agentv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sbName,
			Namespace: req.Namespace,
			Labels: map[string]string{
				"agent.runtime.io/agent":  agent.Name,
				"agent.runtime.io/tenant": req.Namespace,
			},
		},
		Spec: agentv1.SandboxSpec{
			AgentRef:     agent.Name,
			RuntimeClass: agent.Spec.Runtime.Class,
			Resources:    agent.Spec.Runtime.Resources,
			Image:        agent.Spec.Image,
			Entrypoint:   agent.Spec.Entrypoint,
			EnableRelay:  agent.Spec.A2A.Enabled || agent.Spec.MCP.AllowedTools != nil,
		},
	}
	if err := ctrl.SetControllerReference(agent, sb, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, sb); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	// 回写 Agent status
	agent.Status.SandboxRef = sbName
	if err := r.Status().Update(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentReconciler) syncStatus(ctx context.Context, agent *agentv1.Agent, sbName string) (ctrl.Result, error) {
	sb := &agentv1.Sandbox{}
	if err := r.Get(ctx, types.NamespacedName{Name: sbName, Namespace: agent.Namespace}, sb); err != nil {
		if apierrors.IsNotFound(err) {
			// Sandbox 被删除，清空引用以便重建
			agent.Status.SandboxRef = ""
			_ = r.Status().Update(ctx, agent)
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}
	agent.Status.Phase = sb.Status.Phase
	if sb.Status.Phase == agentv1.PhaseRunning {
		agent.Status.MCPConnectedTools = agent.Spec.MCP.AllowedTools
		agent.Status.LastHeartbeat = metav1.Now()
	}
	_ = r.Status().Update(ctx, agent)
	return ctrl.Result{}, nil
}

// SetupWithManager 注册控制器
func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1.Agent{}).
		Owns(&agentv1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
