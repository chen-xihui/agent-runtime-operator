// Package admission 实现准入控制（design-doc 3.3/7.1）。
// 提供纯函数校验与默认值注入，供 controller-runtime webhook 复用，并可纯本地单测。
//
// 注意：webhook 是单对象视图，跨对象/跨租户语义（如 R-4 工具授权、D-4 跨租户委派）
// 需在 Webhook 内结合 client 做二级查询，或由控制器最终保证；此处覆盖可静态判定的校验。
package admission

import (
	"fmt"
	"strings"

	"github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/orchestrator"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ValidateAgent 校验 Agent spec，返回聚合错误（空表示通过）
func ValidateAgent(a *v1.Agent) error {
	var errs []error
	add := func(f string, err error) { errs = append(errs, fmt.Errorf("%s: %w", f, err)) }

	if a.Spec.Image == "" {
		add("spec.image", fmt.Errorf("image is required"))
	}
	if err := validateRuntimeClass(a.Spec.Runtime.Class); err != nil {
		add("spec.runtime.class", err)
	}
	if a.Spec.Runtime.Resources.Requests != nil && a.Spec.Runtime.Resources.Limits != nil {
		// 资源 request/limit 一致性：request 不应超过 limit
		for r, req := range a.Spec.Runtime.Resources.Requests {
			if lim, ok := a.Spec.Runtime.Resources.Limits[r]; ok && req.Cmp(lim) > 0 {
				add("spec.runtime.resources", fmt.Errorf("request %s exceeds limit for %s", req.String(), r))
			}
		}
	}
	// MCP 引用：allowedTools / endpoints 内容为工具/端点名，不能含非法字符
	for _, t := range a.Spec.MCP.AllowedTools {
		if !validRefName(t) {
			add("spec.mcp.allowedTools", fmt.Errorf("invalid tool reference %q", t))
		}
	}
	for _, e := range a.Spec.MCP.Endpoints {
		if !validRefName(e) {
			add("spec.mcp.endpoints", fmt.Errorf("invalid endpoint reference %q", e))
		}
	}
	if a.Spec.MCP.WhitelistDomains != nil {
		for _, d := range a.Spec.MCP.WhitelistDomains {
			if strings.TrimSpace(d) == "" {
				add("spec.mcp.whitelistDomains", fmt.Errorf("empty domain"))
			}
		}
	}
	switch a.Spec.Security.NetworkPolicy {
	case "", "deny-all", "allow-same-namespace", "egress-dns":
	default:
		add("spec.security.networkPolicy", fmt.Errorf("unsupported networkPolicy %q", a.Spec.Security.NetworkPolicy))
	}
	if a.Spec.Security.MaxLifetimeMin < 0 {
		add("spec.security.maxLifetimeMin", fmt.Errorf("must be >= 0"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("agent %q invalid: %w", a.Name, errs[0])
	}
	return nil
}

// DefaultAgent 注入默认值
func DefaultAgent(a *v1.Agent) {
	if a.Spec.Runtime.Class == "" {
		a.Spec.Runtime.Class = v1.RuntimeGVisor
	}
	if !a.Spec.Security.RunAsNonRoot {
		// runAsNonRoot 无显式设置时默认 true（安全默认值）
		a.Spec.Security.RunAsNonRoot = true
	}
}

// ValidateSandbox 校验 Sandbox spec
func ValidateSandbox(sb *v1.Sandbox) error {
	var errs []error
	if sb.Spec.AgentRef == "" {
		errs = append(errs, fmt.Errorf("spec.agentRef: required"))
	}
	if err := validateRuntimeClass(sb.Spec.RuntimeClass); err != nil {
		errs = append(errs, fmt.Errorf("spec.runtimeClass: %w", err))
	}
	if sb.Spec.Suspend != nil && !*sb.Spec.Suspend {
		// 显式 false 正常（运行态），不报错
	}
	if len(errs) > 0 {
		return fmt.Errorf("sandbox %q invalid: %w", sb.Name, errs[0])
	}
	return nil
}

// ValidateWorkflow 复用 orchestrator 的 DAG 校验（entrypoint/缺失依赖/环/必填 agent-action）
func ValidateWorkflow(wf *v1.Workflow) error {
	if wf.Spec.Entrypoint == "" {
		return fmt.Errorf("workflow %q invalid: spec.entrypoint is required", wf.Name)
	}
	p := orchestrator.NewDefaultParser().WithEntrypoints(wf.Spec.Entrypoint)
	g, err := p.Parse(&wf.Spec)
	if err != nil {
		return fmt.Errorf("workflow %q invalid: %w", wf.Name, err)
	}
	if err := p.Validate(g); err != nil {
		return fmt.Errorf("workflow %q invalid: %w", wf.Name, err)
	}
	// 显式校验 entrypoint 必须是已声明节点
	if _, ok := g.Nodes[wf.Spec.Entrypoint]; !ok {
		return fmt.Errorf("workflow %q invalid: spec.entrypoint %q not in nodes", wf.Name, wf.Spec.Entrypoint)
	}
	return nil
}

// ValidateWorkflowRun 校验 WorkflowRun spec
func ValidateWorkflowRun(run *v1.WorkflowRun) error {
	if run.Spec.WorkflowRef == "" {
		return fmt.Errorf("workflowrun %q invalid: spec.workflowRef is required", run.Name)
	}
	return nil
}

// ValidateTenant 校验 Tenant spec（配额一致性）
func ValidateTenant(t *v1.Tenant) error {
	var errs []error
	q := t.Spec.Quota
	if q.MaxSandboxes < 0 {
		errs = append(errs, fmt.Errorf("spec.quota.maxSandboxes must be >= 0"))
	}
	if q.MaxAgents < 0 {
		errs = append(errs, fmt.Errorf("spec.quota.maxAgents must be >= 0"))
	}
	for f, v := range map[string]string{"spec.quota.maxCpu": q.MaxCPU, "spec.quota.maxMemory": q.MaxMemory} {
		if v == "" {
			continue
		}
		if _, err := resource.ParseQuantity(v); err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid quantity %q: %v", f, v, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("tenant %q invalid: %w", t.Name, errs[0])
	}
	return nil
}

// ValidateMCPEndpoint 校验 MCPEndpoint spec
func ValidateMCPEndpoint(ep *v1.MCPEndpoint) error {
	if ep.Spec.Address == "" {
		return fmt.Errorf("mcpendpoint %q invalid: spec.address is required", ep.Name)
	}
	switch ep.Spec.Transport {
	case "", "stdio", "streamable-http":
	default:
		return fmt.Errorf("mcpendpoint %q invalid: unsupported transport %q", ep.Name, ep.Spec.Transport)
	}
	return nil
}

// ValidateToolBinding 校验 ToolBinding spec（R-4 权限唯一来源）
func ValidateToolBinding(tb *v1.ToolBinding) error {
	if len(tb.Spec.Tools) == 0 {
		return fmt.Errorf("toolbinding %q invalid: spec.tools must not be empty", tb.Name)
	}
	seen := map[string]bool{}
	for _, g := range tb.Spec.Tools {
		if !validRefName(g.Name) {
			return fmt.Errorf("toolbinding %q invalid: invalid tool name %q", tb.Name, g.Name)
		}
		if seen[g.Name] {
			return fmt.Errorf("toolbinding %q invalid: duplicate tool grant %q", tb.Name, g.Name)
		}
		seen[g.Name] = true
		if g.RateLimit.RPS < 0 || g.RateLimit.Burst < 0 || g.RateLimit.Monthly < 0 {
			return fmt.Errorf("toolbinding %q invalid: rate limit must be >= 0", tb.Name)
		}
	}
	return nil
}

// validateRuntimeClass 校验合法运行时
func validateRuntimeClass(rc v1.SandboxRuntime) error {
	switch rc {
	case v1.RuntimeGVisor, v1.RuntimeFirecracker, v1.RuntimeKata:
		return nil
	case "":
		return nil // 允许空，由 DefaultAgent 注入默认
	default:
		return fmt.Errorf("unsupported runtime class %q (allowed: gvisor, firecracker, kata)", rc)
	}
}

// validRefName 校验对象/工具引用为合法 DNS-1035/1123 子域
func validRefName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		if !(r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.') {
			return false
		}
	}
	return true
}
