package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/metrics"
	"github.com/example/agent-runtime-operator/internal/sandbox"
)

// SandboxReconciler 调谐 Sandbox 资源，负责创建/回收沙箱 Pod
type SandboxReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Sandbox *sandbox.Controller
}

// +kubebuilder:rbac:groups=agent.runtime.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=sandboxes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=agent.runtime.io,resources=sandboxes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile 调谐逻辑
func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	sb := &agentv1.Sandbox{}
	if err := r.Get(ctx, req.NamespacedName, sb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	tenantNS := req.Namespace

	// M4：Suspend/Resume 运维（Firecracker 快照 Suspend/Resume，gVisor/普通 Pod 降级）
	if wantSuspend(sb.Spec.Suspend) && sb.Status.Phase == agentv1.PhaseRunning {
		logger.Info("suspending sandbox", "sandbox", sb.Name, "runtime", sb.Status.RuntimeClass)
		if err := r.Sandbox.Suspend(ctx, sb); err != nil {
			return ctrl.Result{}, err
		}
		r.setPhase(sb, agentv1.PhaseSuspended, "suspended by request")
		return ctrl.Result{}, nil
	}
	if !wantSuspend(sb.Spec.Suspend) && sb.Status.Phase == agentv1.PhaseSuspended {
		logger.Info("resuming sandbox", "sandbox", sb.Name)
		if err := r.Sandbox.Resume(ctx, sb); err != nil {
			return ctrl.Result{}, err
		}
		r.setPhase(sb, agentv1.PhaseRunning, "resumed by request")
		return ctrl.Result{}, nil
	}

	switch sb.Status.Phase {
	case agentv1.PhaseTerminated:
		// 已终止，确保 Pod 已清理
		if err := r.Sandbox.DestroyPod(ctx, sb, tenantNS); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	case agentv1.PhaseTerminating, "":
		// 进入 Provisioning
		r.setPhase(sb, agentv1.PhaseProvisioning, "")
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil

	case agentv1.PhaseProvisioning:
		state, err := r.Sandbox.EnsurePod(ctx, sb, tenantNS)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !state.PodReady {
			logger.Info("sandbox pod not ready, waiting", "sandbox", sb.Name)
			r.setRelayReady(sb, false)
			return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
		}
		// Pod 已 Ready。若启用 Relay，需等待 relayReady=true 才能进入 Running（S2）
		if sb.Spec.EnableRelay {
			r.setRelayReady(sb, state.RelayReady)
			if !state.RelayReady {
				r.setPhase(sb, agentv1.PhaseProvisioning, "waiting for event relay ready")
				logger.Info("event relay not ready, waiting", "sandbox", sb.Name)
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
		}
		r.setPhase(sb, agentv1.PhaseRunning, "")
		return ctrl.Result{}, nil

	case agentv1.PhaseRunning:
		// 确保 Pod 仍在运行；若被删除则回退 Provisioning
		state, err := r.Sandbox.EnsurePod(ctx, sb, tenantNS)
		if err != nil {
			if apierrors.IsNotFound(err) {
				r.setPhase(sb, agentv1.PhaseProvisioning, "pod lost, re-provisioning")
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		// Relay 掉线时回退 Provisioning（保持 S2 前置条件）
		if sb.Spec.EnableRelay && !state.RelayReady {
			r.setRelayReady(sb, false)
			r.setPhase(sb, agentv1.PhaseProvisioning, "relay lost, re-provisioning")
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	case agentv1.PhaseFailed:
		// 可重试：回退 Provisioning
		r.setPhase(sb, agentv1.PhaseProvisioning, "retry after failure")
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *SandboxReconciler) setPhase(sb *agentv1.Sandbox, phase, msg string) {
	if sb.Status.Phase == phase {
		return
	}
	// 可观测性指标（M5）：记录状态迁移
	metrics.ObserveSandboxTransition(sb.Namespace, sb.Status.Phase, phase)

	sb.Status.Phase = phase
	sb.Status.Message = msg
	sb.Status.LastTransitionTime = metav1.Now()
	_ = r.Status().Update(context.Background(), sb)
}

func (r *SandboxReconciler) setRelayReady(sb *agentv1.Sandbox, ready bool) {
	if sb.Status.RelayReady == ready {
		return
	}
	sb.Status.RelayReady = ready
	_ = r.Status().Update(context.Background(), sb)
}

// wantSuspend 判断沙箱是否期望挂起（spec.suspend 为 true）
func wantSuspend(suspend *bool) bool {
	return suspend != nil && *suspend
}

// SetupWithManager 注册控制器
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
