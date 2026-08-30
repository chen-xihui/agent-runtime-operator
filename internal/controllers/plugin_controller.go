package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/plugin"
)

// PluginReconciler 调谐 Plugin 资源，把插件 CRD 同步到 PluginRegistry（M5 插件市场）。
// 职责：安装/启用/禁用插件到注册中心，回写 status。
type PluginReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *plugin.Registry
}

// +kubebuilder:rbac:groups=agent.runtime.io,resources=plugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent.runtime.io,resources=plugins/status,verbs=get;update;patch

// Reconcile 调谐逻辑
func (r *PluginReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	p := &agentv1.Plugin{}
	if err := r.Get(ctx, req.NamespacedName, p); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 组装插件 Manifest
	manifest := plugin.Manifest{
		Name:            p.Name,
		Version:         p.Spec.Version,
		Type:            p.Spec.Type,
		Description:     p.Spec.Description,
		Author:          p.Spec.Author,
		Tags:            p.Spec.Tags,
		RequiresAgents:  p.Spec.RequiresAgents,
	}

	// 安装到注册中心（升级覆盖旧版本）
	if err := r.Registry.Install(ctx, manifest, nil); err != nil {
		logger.Error(err, "failed to install plugin", "plugin", p.Name)
		r.updateStatus(ctx, p, "failed", p.Spec.Version, err.Error())
		return ctrl.Result{}, err
	}

	// 启用/禁用
	if p.Spec.Enabled != nil && !*p.Spec.Enabled {
		_ = r.Registry.Disable(ctx, p.Name)
		r.updateStatus(ctx, p, plugin.StateDisabled, p.Spec.Version, "")
	} else {
		_ = r.Registry.Enable(ctx, p.Name)
		r.updateStatus(ctx, p, plugin.StateEnabled, p.Spec.Version, "")
	}
	return ctrl.Result{}, nil
}

func (r *PluginReconciler) updateStatus(ctx context.Context, p *agentv1.Plugin, state, version, msg string) {
	if p.Status.State == state && p.Status.InstalledVersion == version && p.Status.Message == msg {
		return
	}
	p.Status.State = state
	p.Status.InstalledVersion = version
	p.Status.Message = msg
	_ = r.Status().Update(ctx, p)
}

// SetupWithManager 注册控制器
func (r *PluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agentv1.Plugin{}).
		Complete(r)
}
