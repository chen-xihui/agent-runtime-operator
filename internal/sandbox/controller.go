// Package sandbox 提供沙箱生命周期管理：根据 RuntimeClass 创建普通 Pod / gVisor / Firecracker 沙箱，
// 并可注入 Event Relay Sidecar（M1-b，见 design-doc 4.1.4 / core-interface 4.2）。
package sandbox

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// Config 沙箱控制器的可调参数
type Config struct {
	DefaultImage string // 默认 Agent 沙箱镜像（M1-a 使用普通镜像）
	EnableRelay  bool   // 是否注入 Event Relay Sidecar（M1-b）
	RelayImage   string // Event Relay Sidecar 镜像
}

// Controller 负责沙箱 Pod 的创建与回收
type Controller struct {
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
	cfg      *Config
}

// NewController 创建沙箱控制器
func NewController(c client.Client, s *runtime.Scheme, cfg *Config) *Controller {
	return &Controller{client: c, scheme: s, cfg: cfg}
}

// EnsurePod 确保沙箱对应的 Pod 存在，返回 Pod 是否就绪
func (c *Controller) EnsurePod(ctx context.Context, sb *agentv1.Sandbox, tenantNS string) (bool, error) {
	pod := &corev1.Pod{}
	err := c.client.Get(ctx, types.NamespacedName{Name: sb.Name, Namespace: tenantNS}, pod)
	if err == nil {
		return c.podReady(pod), nil
	}
	if client.IgnoreNotFound(err) != nil {
		return false, err
	}

	// Pod 不存在，创建
	pod = buildSandboxPod(sb, tenantNS, c.cfg)
	if err := ctrl.SetControllerReference(sb, pod, c.scheme); err != nil {
		return false, fmt.Errorf("set owner ref: %w", err)
	}
	if err := c.client.Create(ctx, pod); err != nil {
		return false, err
	}
	return false, nil
}

// DestroyPod 删除沙箱对应的 Pod
func (c *Controller) DestroyPod(ctx context.Context, sb *agentv1.Sandbox, tenantNS string) error {
	pod := &corev1.Pod{}
	err := c.client.Get(ctx, types.NamespacedName{Name: sb.Name, Namespace: tenantNS}, pod)
	if err != nil {
		return client.IgnoreNotFound(err)
	}
	return c.client.Delete(ctx, pod)
}

// podReady 判断 Pod 是否已 Ready
func (c *Controller) podReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}
