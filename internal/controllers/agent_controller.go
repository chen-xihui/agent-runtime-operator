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
	"github.com/example/agent-runtime-operator/internal/registration"
	"github.com/example/agent-runtime-operator/internal/sandbox"
)

// AgentReconciler 调谐 Agent 资源，创建关联的 Sandbox，
// 并在 Running 时完成 Agent↔MCP Registry / A2A Gateway 的注册联动（M2）。
type AgentReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Sandbox *sandbox.Controller
	// Syncer Agent↔Registry/Gateway 联动同步器
	Syncer *registration.Syncer
}

// +kubebuilder:rbac:groups=agent.runtime.io,resources=agents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agent.runtime.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=toolbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=agent.runtime.io,resources=mcpendpoints,verbs=get;list;watch

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
			RunAsNonRoot: boolPtr(agent.Spec.Security.RunAsNonRoot),
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
		// 联动：读取 ToolBinding/MCPEndpoint CRD 注入 MCP Registry，注册 A2A AgentCard
		if err := r.syncRegistration(ctx, agent); err != nil {
			// 联动失败不阻塞状态机，记录日志并重试
			log.FromContext(ctx).Error(err, "failed to sync agent registration", "agent", agent.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		agent.Status.MCPConnectedTools = agent.Spec.MCP.AllowedTools
		agent.Status.LastHeartbeat = metav1.Now()
	}
	_ = r.Status().Update(ctx, agent)
	return ctrl.Result{}, nil
}

// syncRegistration 完成 Agent↔MCP Registry / A2A Gateway 的注册联动
func (r *AgentReconciler) syncRegistration(ctx context.Context, agent *agentv1.Agent) error {
	if r.Syncer == nil {
		return nil
	}
	tenantID := agent.Namespace

	// 1. 读取 ToolBinding CRD（工具授权唯一来源，R-4）
	tbList := &agentv1.ToolBindingList{}
	if err := r.List(ctx, tbList, client.InNamespace(tenantID)); err != nil {
		return fmt.Errorf("list toolbindings: %w", err)
	}

	// 2. 读取 MCPEndpoint CRD（工具连接信息）
	epList := &agentv1.MCPEndpointList{}
	if err := r.List(ctx, epList, client.InNamespace(tenantID)); err != nil {
		return fmt.Errorf("list mcpendpoints: %w", err)
	}
	endpoints := make(map[string]agentv1.MCPEndpoint, len(epList.Items))
	for _, ep := range epList.Items {
		endpoints[ep.Name] = ep
	}

	// 3. 注入工具授权到 MCP Registry
	if err := r.Syncer.SyncAgentTools(ctx, tenantID, agent.Name, tbList.Items, endpoints); err != nil {
		return fmt.Errorf("sync agent tools: %w", err)
	}

	// 4. 注册 AgentCard 到 A2A Gateway
	if err := r.Syncer.RegisterAgentCard(ctx, tenantID, agent.Name, agent); err != nil {
		return fmt.Errorf("register agent card: %w", err)
	}
	return nil
}

// SetupWithManager 注册控制器
func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1.Agent{}).
		Owns(&agentv1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func boolPtr(b bool) *bool { return &b }
