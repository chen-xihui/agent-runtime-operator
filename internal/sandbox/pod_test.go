package sandbox

import (
	"testing"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
)

func TestBuildSandboxPod_Plain(t *testing.T) {
	sb := &agentv1.Sandbox{}
	sb.Name = "sb-test"
	sb.Spec.AgentRef = "agent-test"
	sb.Spec.Image = "busybox:1.36"
	sb.Spec.RuntimeClass = agentv1.RuntimeGVisor

	pod := buildSandboxPod(sb, "tenant-a", &Config{DefaultImage: "busybox:1.36"})

	if pod.Name != "sb-test" {
		t.Fatalf("pod name = %q, want sb-test", pod.Name)
	}
	if pod.Namespace != "tenant-a" {
		t.Fatalf("pod namespace = %q, want tenant-a", pod.Namespace)
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Fatalf("runtime class = %v, want gvisor", pod.Spec.RuntimeClassName)
	}
	// 默认不注入 Relay
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("containers = %d, want 1 (no relay)", len(pod.Spec.Containers))
	}
	if pod.Spec.Containers[0].Name != "agent" {
		t.Fatalf("container name = %q, want agent", pod.Spec.Containers[0].Name)
	}
	// 默认资源限制（P1-1）
	if pod.Spec.Containers[0].Resources.Limits == nil {
		t.Fatal("expected default resource limits to prevent resource exhaustion")
	}
}

func TestBuildSandboxPod_WithRelay(t *testing.T) {
	sb := &agentv1.Sandbox{}
	sb.Name = "sb-relay"
	sb.Spec.AgentRef = "agent-relay"
	sb.Spec.RuntimeClass = agentv1.RuntimeGVisor
	// Relay 注入需 Sandbox.Spec.EnableRelay 与全局 cfg.EnableRelay 同时为 true
	sb.Spec.EnableRelay = true

	pod := buildSandboxPod(sb, "tenant-a", &Config{
		DefaultImage: "busybox:1.36",
		EnableRelay:  true,
		RelayImage:   "registry.internal/agent-runtime/event-relay:latest",
	})

	// 应有两个容器：agent + event-relay
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("containers = %d, want 2 (agent + relay)", len(pod.Spec.Containers))
	}

	// 检查 relay 容器
	relayC := pod.Spec.Containers[1]
	if relayC.Name != relayContainerName {
		t.Fatalf("relay container name = %q, want %q", relayC.Name, relayContainerName)
	}
	// 共享卷
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].Name != "agent-socket" {
		t.Fatalf("expected shared agent-socket volume, got %+v", pod.Spec.Volumes)
	}
	// Agent 容器也应挂载共享卷
	if len(pod.Spec.Containers[0].VolumeMounts) != 1 {
		t.Fatalf("agent container should mount shared socket volume, got %+v", pod.Spec.Containers[0].VolumeMounts)
	}
	// relay 就绪探测应检查 socket
	if relayC.ReadinessProbe == nil {
		t.Fatal("relay container should have readiness probe checking socket")
	}
}
