package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/plugin"
)

func pluginReconciler(objs ...*agentv1.Plugin) (*PluginReconciler, *plugin.Registry) {
	b := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&agentv1.Plugin{}).
		Build()
	for _, p := range objs {
		_ = b.Create(context.Background(), p)
	}
	reg := plugin.NewRegistry()
	return &PluginReconciler{Client: b, Scheme: scheme, Registry: reg}, reg
}

func TestPluginReconciler_InstallEnabled(t *testing.T) {
	p := &agentv1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: "code-search"},
		Spec: agentv1.PluginSpec{
			Version: "1.0.0",
			Type:    plugin.TypeTool,
			Tags:    []string{"code"},
			Enabled: boolPtr(true),
		},
	}
	r, reg := pluginReconciler(p)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "code-search"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// 插件已安装到 Registry 且 enabled
	got, err := reg.Get("code-search")
	if err != nil {
		t.Fatalf("get plugin from registry: %v", err)
	}
	if got.State != plugin.StateEnabled {
		t.Fatalf("state = %q, want enabled", got.State)
	}

	// status 回写
	updated := &agentv1.Plugin{}
	_ = r.Get(ctx, types.NamespacedName{Name: "code-search"}, updated)
	if updated.Status.State != plugin.StateEnabled || updated.Status.InstalledVersion != "1.0.0" {
		t.Fatalf("status = %+v", updated.Status)
	}
}

func TestPluginReconciler_Disabled(t *testing.T) {
	p := &agentv1.Plugin{
		ObjectMeta: metav1.ObjectMeta{Name: "hook-h"},
		Spec: agentv1.PluginSpec{
			Version: "1.0.0",
			Type:    plugin.TypeHook,
			Enabled: boolPtr(false),
		},
	}
	r, reg := pluginReconciler(p)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "hook-h"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := reg.Get("hook-h")
	if got.State != plugin.StateDisabled {
		t.Fatalf("state = %q, want disabled", got.State)
	}
}

func TestPluginReconciler_NotFound(t *testing.T) {
	r, _ := pluginReconciler()
	// 插件不存在时 reconcile 不应报错（IgnoreNotFound）
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "ghost"}}); err != nil {
		t.Fatalf("reconcile notfound: %v", err)
	}
}
