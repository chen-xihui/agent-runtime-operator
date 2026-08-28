package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// TenantReconciler 调谐 Tenant 资源，负责创建/回收租户 Namespace 与资源配额
type TenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=agent.runtime.io,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete

// Reconcile 调谐逻辑
func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	tenant := &agentv1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 租户命名空间与 Tenant 同名
	ns := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: tenant.Name}, ns)
	if err == nil {
		r.updateStatus(tenant)
		return ctrl.Result{}, nil
	}
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	// Namespace 不存在，创建（含租户标签，用于 NetworkPolicy 默认 Deny-All）
	ns = &corev1.Namespace{}
	ns.Name = tenant.Name
	ns.Labels = map[string]string{
		"agent.runtime.io/tenant": tenant.Name,
		"pod-security.kubernetes.io/enforce": "restricted",
	}
	for k, v := range tenant.Spec.Labels {
		ns.Labels[k] = v
	}
	if err := r.Create(ctx, ns); err != nil {
		return ctrl.Result{}, err
	}

	// 创建资源配额
	if tenant.Spec.Quota.MaxCPU != "" || tenant.Spec.Quota.MaxMemory != "" || tenant.Spec.Quota.MaxSandboxes > 0 {
		rq := buildResourceQuota(tenant)
		if err := r.Create(ctx, rq); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
	}

	r.updateStatus(tenant)
	return ctrl.Result{}, nil
}

func buildResourceQuota(tenant *agentv1.Tenant) *corev1.ResourceQuota {
	q := tenant.Spec.Quota
	rq := &corev1.ResourceQuota{}
	rq.Name = "tenant-quota"
	rq.Namespace = tenant.Name
	if q.MaxCPU != "" {
		rq.Spec.Hard["limits.cpu"] = parseQuantity(q.MaxCPU)
	}
	if q.MaxMemory != "" {
		rq.Spec.Hard["limits.memory"] = parseQuantity(q.MaxMemory)
	}
	return rq
}

func parseQuantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return resource.Quantity{}
	}
	return q
}

func (r *TenantReconciler) updateStatus(tenant *agentv1.Tenant) {
	tenant.Status.Namespace = tenant.Name
	tenant.Status.Phase = "Active"
	_ = r.Status().Update(context.Background(), tenant)
}

// SetupWithManager 注册控制器
func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1.Tenant{}).
		Complete(r)
}
