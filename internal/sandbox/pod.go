package sandbox

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

// relayContainerName Event Relay Sidecar 容器名
const relayContainerName = "event-relay"

// relaySocketPath Relay Sidecar 与 Agent 共享的本地 socket 路径
const relaySocketPath = "/var/run/agent.sock"

// socketDir socket 挂载目录
const socketDir = "/var/run"

// runtimeClassFor 根据沙箱运行时选择 K8s RuntimeClass 名称
func runtimeClassFor(rt agentv1.SandboxRuntime) string {
	switch rt {
	case agentv1.RuntimeGVisor:
		return "gvisor"
	case agentv1.RuntimeFirecracker:
		return "firecracker"
	case agentv1.RuntimeKata:
		return "kata"
	default:
		return ""
	}
}

// buildSandboxPod 构建沙箱对应的 Pod。
// - M1-a：普通 Pod，Agent 容器 + 健康检查
// - M1-b：叠加 gVisor RuntimeClass + Event Relay Sidecar（唯一安全出口，见 design-doc 4.1.4）
func buildSandboxPod(sb *agentv1.Sandbox, tenantNS string, cfg *Config) *corev1.Pod {
	image := sb.Spec.Image
	if image == "" {
		image = cfg.DefaultImage
	}
	if image == "" {
		image = "busybox:1.36"
	}

	labels := map[string]string{
		"app":                    "agent-sandbox",
		"agent.runtime.io/sandbox": sb.Name,
		"agent.runtime.io/agent":   sb.Spec.AgentRef,
		"agent.runtime.io/tenant":  tenantNS,
	}

	// Agent 容器
	cmd, args := agentCommand(sb.Spec.Entrypoint)
	agentContainer := corev1.Container{
		Name:            "agent",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		// M1-a 使用一个常驻命令模拟 Agent，等待信号
		Command: cmd,
		Args:    args,
		Resources: sb.Spec.Resources,
		SecurityContext: restrictedSecurityContext(sb.Spec.RunAsNonRoot),
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"true"},
				},
			},
		},
	}

	containers := []corev1.Container{agentContainer}

	// M1-b：注入 Event Relay Sidecar（唯一安全出口，见 design-doc 4.1.4）
	// 是否注入由 Sandbox.Spec.EnableRelay（per-sandbox 配置）决定；
	// cfg.EnableRelay 作为全局开关，二者都为 true 才注入。
	if sb.Spec.EnableRelay && cfg.EnableRelay {
		relayC := corev1.Container{
			Name:            relayContainerName,
			Image:           cfg.RelayImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Env: []corev1.EnvVar{
				{Name: "TENANT_ID", Value: tenantNS},
				{Name: "AGENT_ID", Value: sb.Spec.AgentRef},
				{Name: "LOCAL_SOCKET", Value: relaySocketPath},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "agent-socket", MountPath: socketDir},
			},
			SecurityContext: restrictedSecurityContext(sb.Spec.RunAsNonRoot),
			// 就绪探测：socket 文件存在即视为 Relay 就绪（真实 relay 监听 socket 后创建）
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"test", "-S", relaySocketPath},
					},
				},
				InitialDelaySeconds: 1,
				PeriodSeconds:       2,
			},
		}
		containers = append(containers, relayC)

		// Agent 容器也需挂载共享 socket 卷以访问 Relay
		agentContainer.VolumeMounts = append(agentContainer.VolumeMounts,
			corev1.VolumeMount{Name: "agent-socket", MountPath: socketDir})
		containers[0] = agentContainer
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sb.Name,
			Namespace: tenantNS,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: runtimeClassPtr(runtimeClassFor(sb.Spec.RuntimeClass)),
			Containers:       containers,
			RestartPolicy:    corev1.RestartPolicyAlways,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: boolPtr(sandboxRunAsNonRoot(sb.Spec.RunAsNonRoot)),
			},
		},
	}

	// 共享 socket 卷（M1-b Relay 需要）
	if sb.Spec.EnableRelay && cfg.EnableRelay {
		pod.Spec.Volumes = []corev1.Volume{
			{Name: "agent-socket", VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			}},
		}
	}

	// 默认资源限制，防止失控 Agent 资源耗尽（P1-1）
	if pod.Spec.Containers[0].Resources.Limits == nil && pod.Spec.Containers[0].Resources.Requests == nil {
		pod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		}
	}

	return pod
}

// restrictedSecurityContext 返回满足 PodSecurity restricted 策略的容器安全上下文
// （design-doc 4.1.5 安全加固：runAsNonRoot + drop ALL capabilities + RuntimeDefault seccomp）
// runAsNonRoot 由调用方根据沙箱/Agent 安全配置决定（默认 true）。
func restrictedSecurityContext(runAsNonRoot *bool) *corev1.SecurityContext {
	all := "ALL"
	rnr := true
	if runAsNonRoot != nil {
		rnr = *runAsNonRoot
	}
	return &corev1.SecurityContext{
		RunAsNonRoot:             boolPtr(rnr),
		ReadOnlyRootFilesystem:   boolPtr(true),
		AllowPrivilegeEscalation: boolPtr(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{corev1.Capability(all)},
		},
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func boolPtr(b bool) *bool { return &b }

// sandboxRunAsNonRoot 计算沙箱是否以非 root 运行（未显式设置时默认 true）
func sandboxRunAsNonRoot(runAsNonRoot *bool) bool {
	if runAsNonRoot != nil {
		return *runAsNonRoot
	}
	return true
}

// agentCommand 根据 Agent 入口生成容器 Command/Args。
// 若提供完整 entrypoint 则直接作为 Command（不追加额外 Args）；
// 否则默认使用 sleep 保持容器常驻（M1-a 验证）。
func agentCommand(entrypoint []string) ([]string, []string) {
	if len(entrypoint) > 0 {
		return entrypoint, nil
	}
	return []string{"sleep", "infinity"}, nil
}

func runtimeClassPtr(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}
