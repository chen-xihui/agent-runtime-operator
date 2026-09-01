package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBuildVMConfig(t *testing.T) {
	cfg := BuildVMConfig(2, 256)
	if cfg.VCPUCount != 2 || cfg.MemSizeMib != 256 {
		t.Fatalf("cfg = %+v", cfg)
	}
	// 默认值
	cfg = BuildVMConfig(0, 0)
	if cfg.VCPUCount != 1 || cfg.MemSizeMib != 128 {
		t.Fatalf("default cfg = %+v", cfg)
	}
}

func TestBuildBootSource(t *testing.T) {
	boot := BuildBootSource("/opt/vmlinux")
	if boot.KernelImagePath != "/opt/vmlinux" || boot.BootArgs == "" {
		t.Fatalf("boot = %+v", boot)
	}
}

func TestKVMEnabled(t *testing.T) {
	// 非 KVM 环境返回 false（测试环境通常无 /dev/kvm），只验证不 panic
	_ = KVMEnabled()
}

// fakeFirecrackerAPI 模拟 Firecracker API server
func fakeFirecrackerAPI(t *testing.T) (*httptest.Server, *sync.Map) {
	var mu sync.Mutex
	calls := &sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls.Store(r.URL.Path, true)
		mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			if r.URL.Path == "/vm" {
				_ = json.NewEncoder(w).Encode(VMState{State: "Running"})
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}

func TestVMManager_Lifecycle(t *testing.T) {
	srv, calls := fakeFirecrackerAPI(t)
	ctx := context.Background()

	m := NewVMManager()
	m.WithAPIClient(srv.Client()) // 用 httptest server 的 client（HTTP）
	m.WithBaseURL(srv.URL)        // 指向 mock server

	cfg := BuildVMConfig(1, 128)
	boot := BuildBootSource("/opt/vmlinux")
	drive := Drive{DriveID: "rootfs", PathOnHost: "/opt/rootfs.ext4", IsRootDevice: true}

	// 启动 VM（请求应打到 mock server）
	if err := m.StartVM(ctx, "sb-vm", srv.URL, cfg, boot, drive); err != nil {
		t.Fatalf("start vm: %v", err)
	}
	// 验证关键 API 被调用
	for _, path := range []string{"/machine-config", "/boot-source", "/drives/rootfs", "/actions"} {
		if _, ok := calls.Load(path); !ok {
			t.Fatalf("expected API call to %q", path)
		}
	}

	// 获取状态
	st, err := m.State(ctx, "sb-vm")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if st.State != "Running" {
		t.Fatalf("state = %q, want Running", st.State)
	}

	// 停止 VM
	if err := m.StopVM(ctx, "sb-vm"); err != nil {
		t.Fatalf("stop vm: %v", err)
	}
	// 停止后 VM 移除 → 再获取状态应报错
	if _, err := m.State(ctx, "sb-vm"); err == nil {
		t.Fatal("expected error after stop")
	}
}

func TestVMManager_NotFound(t *testing.T) {
	m := NewVMManager()
	if err := m.StopVM(context.Background(), "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

// 验证 Firecracker 适配器的 KVM 检查 + Snapshot 能力
func TestFirecracker_Adapter(t *testing.T) {
	f := NewFirecracker()
	if !f.SnapshotSupported() {
		t.Fatal("firecracker should support snapshot")
	}
	if !f.KVMOK() {
		// 测试环境可能无 KVM，不视为失败，仅提示
		t.Log("KVM not available in this environment (expected on non-KVM host)")
	}
	// SuspendCapable 接口断言
	var _ SuspendCapable = f
}
