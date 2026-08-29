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
	runtimepkg "github.com/example/agent-runtime-operator/internal/runtime"
)

// Config 沙箱控制器的可调参数
type Config struct {
	DefaultImage string // 默认 Agent 沙箱镜像（M1-a 使用普通镜像）
	EnableRelay  bool   // 是否注入 Event Relay Sidecar（M1-b）
	RelayImage   string // Event Relay Sidecar 镜像
}

// Controller 负责沙箱 Pod 的创建与回收，以及 Suspend/Resume 运维（M4）
type Controller struct {
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
	cfg      *Config
	runtimes *runtimepkg.Registry // 运行时适配器注册表（按 RuntimeClass 挂起/恢复）
}

// NewController 创建沙箱控制器
func NewController(c client.Client, s *runtime.Scheme, cfg *Config) *Controller {
	return &Controller{client: c, scheme: s, cfg: cfg, runtimes: runtimepkg.NewRegistry()}
}

// Suspend 挂起沙箱（Firecracker 快照 Suspend；gVisor/普通 Pod 降级）
func (c *Controller) Suspend(ctx context.Context, sb *agentv1.Sandbox) error {
	return c.runtimes.Get(sb.Spec.RuntimeClass).Suspend(ctx, sb)
}

// Resume 恢复沙箱（Firecracker 快照 Resume；gVisor/普通 Pod 降级）
func (c *Controller) Resume(ctx context.Context, sb *agentv1.Sandbox) error {
	return c.runtimes.Get(sb.Spec.RuntimeClass).Resume(ctx, sb)
}

// PodState 表示沙箱 Pod 的就绪状态
type PodState struct {
	// PodReady Pod 整体已 Ready
	PodReady bool
	// RelayReady Event Relay Sidecar 容器已就绪（仅 EnableRelay 时有效）
	RelayReady bool
}

// EnsurePod 确保沙箱对应的 Pod 存在，返回 Pod 就绪状态
func (c *Controller) EnsurePod(ctx context.Context, sb *agentv1.Sandbox, tenantNS string) (*PodState, error) {
	pod := &corev1.Pod{}
	err := c.client.Get(ctx, types.NamespacedName{Name: sb.Name, Namespace: tenantNS}, pod)
	if err == nil {
		return c.podState(pod, sb.Spec.EnableRelay), nil
	}
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}

	// Pod 不存在，创建
	pod = buildSandboxPod(sb, tenantNS, c.cfg)
	if err := ctrl.SetControllerReference(sb, pod, c.scheme); err != nil {
		return nil, fmt.Errorf("set owner ref: %w", err)
	}
	if err := c.client.Create(ctx, pod); err != nil {
		return nil, err
	}
	return &PodState{}, nil
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

// podState 计算 Pod 及 Event Relay Sidecar 容器的就绪状态
func (c *Controller) podState(pod *corev1.Pod, enableRelay bool) *PodState {
	state := &PodState{}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			state.PodReady = cond.Status == corev1.ConditionTrue
		}
	}
	// 检查 Event Relay 容器就绪（S2：relayReady 作为 Provisioning→Running 前置条件）
	if enableRelay {
		state.RelayReady = containerReady(pod, relayContainerName)
	}
	return state
}

// containerReady 判断指定容器是否处于 Ready 状态
func containerReady(pod *corev1.Pod, name string) bool {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == name {
			return cs.Ready
		}
	}
	return false
}
