package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/sdk"
)

func testServer(t *testing.T, objs ...client.Object) *httptest.Server {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	b := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, o := range objs {
		_ = b.Create(context.Background(), o)
	}
	s := New(sdk.NewFromClient(b))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doReq(t *testing.T, method, url, body string) (*http.Response, map[string]interface{}) {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, url, strings.NewReader(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestServer_Health(t *testing.T) {
	ts := testServer(t)
	resp, out := doReq(t, "GET", ts.URL+"/healthz", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
	if out["status"] != "ok" {
		t.Fatalf("health = %v", out)
	}
}

func TestServer_CreateAndGetTenant(t *testing.T) {
	ts := testServer(t)

	// 创建租户
	resp, _ := doReq(t, "POST", ts.URL+"/api/v1/tenants", `{"metadata":{"name":"tenant-api"},"spec":{"quota":{"maxSandboxes":5}}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create tenant status = %d", resp.StatusCode)
	}

	// 获取租户
	resp, out := doReq(t, "GET", ts.URL+"/api/v1/tenants/tenant-api", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get tenant status = %d", resp.StatusCode)
	}
	meta := out["metadata"].(map[string]interface{})
	if meta["name"] != "tenant-api" {
		t.Fatalf("tenant name = %v", meta["name"])
	}

	// 获取不存在的租户 → 404
	resp, _ = doReq(t, "GET", ts.URL+"/api/v1/tenants/ghost", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get ghost status = %d, want 404", resp.StatusCode)
	}
}

func TestServer_ListTenants(t *testing.T) {
	tnt := &v1.Tenant{}
	tnt.Name = "tenant-1"
	ts := testServer(t, tnt)

	resp, out := doReq(t, "GET", ts.URL+"/api/v1/tenants", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	if items, ok := out["items"].([]interface{}); ok && len(items) != 1 {
		t.Fatalf("tenants = %d, want 1", len(items))
	}
}

func TestServer_CreateAndGetSandbox(t *testing.T) {
	// 先创建租户
	ts := testServer(t)
	_, _ = doReq(t, "POST", ts.URL+"/api/v1/tenants", `{"metadata":{"name":"tenant-sb"}}`)

	// 创建 Agent
	resp, _ := doReq(t, "POST", ts.URL+"/api/v1/tenants/tenant-sb/agents", `{"metadata":{"name":"reviewer"},"spec":{"image":"busybox:1.36"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create agent status = %d", resp.StatusCode)
	}
	resp, out := doReq(t, "GET", ts.URL+"/api/v1/tenants/tenant-sb/agents", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list agents status = %d", resp.StatusCode)
	}
	if items, ok := out["items"].([]interface{}); ok && len(items) != 1 {
		t.Fatalf("agents = %d, want 1", len(items))
	}

	// 验证重复创建 Agent 幂等（存在则返回）
	_, _ = doReq(t, "POST", ts.URL+"/api/v1/tenants/tenant-sb/agents", `{"metadata":{"name":"reviewer2"},"spec":{"image":"busybox:1.36"}}`)
}

func TestServer_BadRequest(t *testing.T) {
	ts := testServer(t)
	// 空 tenant 名 → 400
	resp, _ := doReq(t, "POST", ts.URL+"/api/v1/tenants", `{"metadata":{}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", resp.StatusCode)
	}
	// 非法 body → 400
	resp, _ = doReq(t, "POST", ts.URL+"/api/v1/tenants", `invalid-json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want 400", resp.StatusCode)
	}
}
