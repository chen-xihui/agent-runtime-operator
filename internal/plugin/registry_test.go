package plugin

import (
	"context"
	"errors"
	"testing"
)

func handler(name string) PluginHandler {
	return func(ctx context.Context, args map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"plugin": name}, nil
	}
}

func TestRegistry_InstallAndGet(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()

	err := r.Install(ctx, Manifest{Name: "code-search", Version: "1.0.0", Type: TypeTool}, handler("code-search"))
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	p, err := r.Get("code-search")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.State != StateEnabled {
		t.Fatalf("state = %q, want enabled", p.State)
	}

	// 重复安装同版本 → ErrPluginExists
	if err := r.Install(ctx, Manifest{Name: "code-search", Version: "1.0.0"}, nil); !errors.Is(err, ErrPluginExists) {
		t.Fatalf("expected ErrPluginExists, got %v", err)
	}
}

func TestRegistry_VersionUpgrade(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()

	_ = r.Install(ctx, Manifest{Name: "plugin-a", Version: "1.0.0"}, handler("a"))
	// 升级到 1.2.0 应成功
	if err := r.Install(ctx, Manifest{Name: "plugin-a", Version: "1.2.0"}, handler("a2")); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	// 降级到 1.1.0 应冲突
	if err := r.Install(ctx, Manifest{Name: "plugin-a", Version: "1.1.0"}, nil); !errors.Is(err, ErrPluginConflict) {
		t.Fatalf("expected conflict on downgrade, got %v", err)
	}

	vers := r.Versions("plugin-a")
	if len(vers) != 2 {
		t.Fatalf("versions = %v, want 2", vers)
	}
}

func TestRegistry_EnableDisableCall(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	_ = r.Install(ctx, Manifest{Name: "hook-h", Version: "1.0.0", Type: TypeHook}, handler("h"))

	// 启用时调用成功
	res, err := r.Call(ctx, "hook-h", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res["plugin"] != "h" {
		t.Fatalf("result = %v", res)
	}

	// 禁用后调用失败
	_ = r.Disable(ctx, "hook-h")
	if _, err := r.Call(ctx, "hook-h", nil); !errors.Is(err, ErrPluginDisabled) {
		t.Fatalf("expected ErrPluginDisabled, got %v", err)
	}

	// 重新启用
	_ = r.Enable(ctx, "hook-h")
	if _, err := r.Call(ctx, "hook-h", nil); err != nil {
		t.Fatalf("call after enable: %v", err)
	}
}

func TestRegistry_DiscoverAndList(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	_ = r.Install(ctx, Manifest{Name: "tool-db", Version: "1.0.0", Type: TypeTool, Tags: []string{"db"}}, handler("db"))
	_ = r.Install(ctx, Manifest{Name: "tool-code", Version: "1.0.0", Type: TypeTool, Tags: []string{"code"}}, handler("code"))
	_ = r.Install(ctx, Manifest{Name: "skill-x", Version: "1.0.0", Type: TypeSkill, Tags: []string{"db"}}, handler("x"))

	// 按类型过滤
	tools := r.List(TypeTool)
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	if tools[0].Name != "tool-code" || tools[1].Name != "tool-db" {
		t.Fatalf("tools order = %v", tools)
	}

	// 按标签发现
	dbPlugins := r.Discover([]string{"db"}, TypeTool)
	if len(dbPlugins) != 1 || dbPlugins[0].Name != "tool-db" {
		t.Fatalf("discover db = %+v", dbPlugins)
	}
}

func TestAgentPlugins_MountAndCall(t *testing.T) {
	r := NewRegistry()
	ctx := context.Background()
	_ = r.Install(ctx, Manifest{Name: "code-search", Version: "1.0.0", Type: TypeTool}, handler("cs"))

	ap := NewAgentPlugins("agent-x", r)

	// 挂载
	if err := ap.Mount(ctx, "code-search"); err != nil {
		t.Fatalf("mount: %v", err)
	}
	if !ap.HasPlugin("code-search") {
		t.Fatal("should have code-search")
	}
	// 幂等挂载
	if err := ap.Mount(ctx, "code-search"); err != nil {
		t.Fatalf("re-mount: %v", err)
	}

	// 调用
	res, err := ap.Call(ctx, "code-search", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res["plugin"] != "cs" {
		t.Fatalf("result = %v", res)
	}

	// 卸载
	if err := ap.Unmount(ctx, "code-search"); err != nil {
		t.Fatalf("unmount: %v", err)
	}
	if ap.HasPlugin("code-search") {
		t.Fatal("should not have code-search after unmount")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct{ a, b string; want int }{
		{"1.0.0", "1.0.0", 0},
		{"1.2.0", "1.1.0", 1},
		{"1.0.1", "1.0.0", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.0", "1.0.1", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Fatalf("compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
