package mcp

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// buildMCPServer 编译 cmd/mcp-server 为临时二进制
func buildMCPServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mcp-server")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// 测试运行于 internal/mcp，项目根为 ../..
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/mcp-server")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mcp-server: %v\n%s", err, out)
	}
	return bin
}

// TestE2E_ProxyToMCPServer 端到端：MCP Proxy → MCPInvoker → 真实 MCP Server（stdio）
// 验证：注册 → 鉴权 → 数据过滤注入 → MCP 协议转发 → 脱敏 → 审计 完整链路
func TestE2E_ProxyToMCPServer(t *testing.T) {
	serverBin := buildMCPServer(t)

	// 1. 构造 Registry 并注册工具（stdio 传输，指向 mcp-server 二进制）
	r := NewMemoryRegistry()
	if err := r.Register(context.Background(), &Tool{
		Name:      "get_weather",
		Endpoint:  serverBin, // stdio 传输：Endpoint 作为启动命令
		Transport: "stdio",
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	// 2. 绑定授权（含数据范围 + 脱敏）
	r.BindToolGrant("tenant-a", "agent-x", map[string]ToolGrant{
		"get_weather": {
			DataScope: map[string]any{"tenant": "tenant-a"},
			Redact:    []string{"temp"},
		},
	})

	// 3. 用 MCPInvoker 作为底层调用器，接入 Proxy
	invoker := NewMCPInvoker()
	proxy := NewMemoryProxy("tenant-a", r)
	proxy.WithInvoker(invoker.Invoke)

	var auditCall *struct {
		tenant, agent, tool string
	}
	proxy.WithAudit(func(tenantID, agentID, toolName string, args, result map[string]any, err error) {
		auditCall = &struct{ tenant, agent, tool string }{tenantID, agentID, toolName}
	})

	// 4. 端到端调用
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := proxy.Invoke(ctx, "agent-x", "get_weather", map[string]any{"city": "Beijing"})
	if err != nil {
		t.Fatalf("e2e invoke: %v", err)
	}

	// 5. 校验：数据范围注入（tenant=tenant-a）已在 MCP Server 参数中
	//   结果返回且 temp 已脱敏
	if result["city"] != "Beijing" {
		t.Fatalf("city = %v, want Beijing", result["city"])
	}
	if result["temp"] != "[REDACTED]" {
		t.Fatalf("temp should be redacted, got %v", result["temp"])
	}
	if result["weather"] != "sunny" {
		t.Fatalf("weather = %v, want sunny", result["weather"])
	}

	// 6. 校验审计已触发
	if auditCall == nil || auditCall.tool != "get_weather" || auditCall.tenant != "tenant-a" || auditCall.agent != "agent-x" {
		t.Fatalf("audit not correct: %+v", auditCall)
	}
}

// TestE2E_MCPServer_Echo 验证 stdio 传输的 MCP 协议握手与 tools/call 直接工作
func TestE2E_MCPServer_Echo(t *testing.T) {
	serverBin := buildMCPServer(t)

	tr, err := NewStdioTransport(context.Background(), serverBin)
	if err != nil {
		t.Fatalf("stdio transport: %v", err)
	}
	defer tr.Close()

	client := NewMCPClient(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := client.CallTool(ctx, "echo", map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("call echo: %v", err)
	}
	if result["received"] != "hello" {
		t.Fatalf("echo result = %v, want received=hello", result)
	}
}
