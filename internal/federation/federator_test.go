package federation

import (
	"context"
	"testing"
)

func TestRouter_RegisterAndList(t *testing.T) {
	r := NewRouter()
	r.RegisterCluster(&Cluster{Name: "cluster-a", Role: RoleHub})
	r.RegisterCluster(&Cluster{Name: "cluster-b", Role: RoleSpoke, TrustedFrom: []string{"cluster-a"}})

	clusters := r.ListClusters()
	if len(clusters) != 2 || clusters[0] != "cluster-a" || clusters[1] != "cluster-b" {
		t.Fatalf("clusters = %v", clusters)
	}

	// 空名报错
	if err := r.RegisterCluster(&Cluster{Name: "", Role: RoleHub}); err == nil {
		t.Fatal("expected error for empty cluster name")
	}
}

func TestRouter_Allowed_TrustedFrom(t *testing.T) {
	r := NewRouter()
	r.RegisterCluster(&Cluster{Name: "hub", Role: RoleHub})
	r.RegisterCluster(&Cluster{Name: "spoke1", Role: RoleSpoke, TrustedFrom: []string{"hub"}})
	r.RegisterCluster(&Cluster{Name: "spoke2", Role: RoleSpoke, TrustedFrom: []string{"other"}})

	// hub -> spoke1 允许（spoke1 信任 hub）
	if !r.Allowed("hub", "spoke1") {
		t.Fatal("hub should be allowed to delegate to spoke1")
	}
	// hub -> spoke2 不允许（spoke2 不信任 hub，D-4）
	if r.Allowed("hub", "spoke2") {
		t.Fatal("hub should NOT delegate to spoke2")
	}
	// 未知集群
	if r.Allowed("hub", "ghost") {
		t.Fatal("should not allow to unknown cluster")
	}
}

func TestRouter_Route(t *testing.T) {
	r := NewRouter()
	r.RegisterCluster(&Cluster{Name: "hub", Role: RoleHub})
	r.RegisterCluster(&Cluster{Name: "spoke", Role: RoleSpoke, TrustedFrom: []string{"hub"}})

	var routed bool
	r.WithRouteFn(func(ctx context.Context, from, to, agentID string, payload map[string]interface{}) (map[string]interface{}, error) {
		routed = true
		return map[string]interface{}{"forwarded": true, "agent": agentID}, nil
	})

	// 允许的路由
	res, err := r.Route(context.Background(), "hub", "spoke", "agent-remote", map[string]interface{}{"task": "x"})
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if !routed || res["forwarded"] != true || res["agent"] != "agent-remote" {
		t.Fatalf("route result wrong: %+v", res)
	}

	// 不允许的路由
	if _, err := r.Route(context.Background(), "spoke", "hub", "agent-x", nil); err == nil {
		t.Fatal("expected error for disallowed route")
	}

	// 未配置 routeFn
	r2 := NewRouter()
	r2.RegisterCluster(&Cluster{Name: "a"})
	r2.RegisterCluster(&Cluster{Name: "b", TrustedFrom: []string{"a"}})
	if _, err := r2.Route(context.Background(), "a", "b", "x", nil); err == nil {
		t.Fatal("expected error when route fn not configured")
	}
}

func TestRouter_Lookup(t *testing.T) {
	r := NewRouter()
	r.RegisterCluster(&Cluster{Name: "hub", Role: RoleHub})
	r.RegisterCluster(&Cluster{Name: "spoke1", Role: RoleSpoke, TrustedFrom: []string{"hub"}})
	r.RegisterCluster(&Cluster{Name: "spoke2", Role: RoleSpoke, TrustedFrom: []string{"hub"}})
	r.RegisterCluster(&Cluster{Name: "untrusted", Role: RoleSpoke, TrustedFrom: []string{"other"}})

	targets, err := r.Lookup("hub", "code.review")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("lookup targets = %v, want 2", targets)
	}
	// 只包含信任 hub 的集群，确定性排序
	if targets[0] != "spoke1" || targets[1] != "spoke2" {
		t.Fatalf("targets = %v", targets)
	}
}

func TestIsValidClusterName(t *testing.T) {
	cases := map[string]bool{
		"cluster-a": true,
		"a1":        true,
		"":          false,
		"Bad_Name":  false,
		"UPPER":     false,
		tooLongName: false,
	}
	for k, want := range cases {
		if got := IsValidClusterName(k); got != want {
			t.Fatalf("IsValidClusterName(%q) = %v, want %v", k, got, want)
		}
	}
}

// tooLongName 超长名字测试（> 63 字符）
const tooLongName = "this-cluster-name-is-way-too-long-for-the-validation-limit-of-sixty-three-characters-extra"
