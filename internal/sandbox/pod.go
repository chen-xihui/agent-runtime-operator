package sandbox

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

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
	agentContainer := corev1.Container{
		Name:  "agent",
		Image: image,
		// M1-a 使用一个常驻命令模拟 Agent，等待信号
		Command: sb.Spec.Entrypoint,
		Args:    []string{"sleep", "infinity"},
		Resources: sb.Spec.Resources,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:       boolPtr(true),
			ReadOnlyRootFilesystem: boolPtr(true),
			AllowPrivilegeEscalation: boolPtr(false),
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"true"},
				},
			},
		},
	}

	containers := []corev1.Container{agentContainer}

	// M1-b：注入 Event Relay Sidecar（唯一安全出口）
	if cfg.EnableRelay {
		containers = append(containers, corev1.Container{
			Name:  "event-relay",
			Image: cfg.RelayImage,
			Env: []corev1.EnvVar{
				{Name: "TENANT_ID", Value: tenantNS},
				{Name: "AGENT_ID", Value: sb.Spec.AgentRef},
				{Name: "LOCAL_SOCKET", Value: "/var/run/agent.sock"},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "agent-socket", MountPath: "/var/run"},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"test", "-S", "/var/run/agent.sock"},
					},
				},
			},
		})
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
				RunAsNonRoot: boolPtr(true),
			},
		},
	}

	// 共享 socket 卷（M1-b Relay 需要）
	if cfg.EnableRelay {
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

func boolPtr(b bool) *bool { return &b }

func runtimeClassPtr(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}
