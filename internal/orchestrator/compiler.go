package orchestrator

import (
	"fmt"
	"strings"

	"github.com/example/agent-runtime-operator/api/v1"
)

// DefaultCompiler 默认编译器：将 Graph 编译为执行数据（ExecutionData）
// 负责编译期校验（C1：RetrySpec.backoff 合法性；节点引用完整性）。
type DefaultCompiler struct {
	parser Parser
}

// NewDefaultCompiler 创建编译器
func NewDefaultCompiler(p Parser) *DefaultCompiler {
	return &DefaultCompiler{parser: p}
}

// Compile 将 Graph 编译为执行数据
func (c *DefaultCompiler) Compile(g *Graph) (*ExecutionData, error) {
	if err := c.Validate(g); err != nil {
		return nil, err
	}
	data := &ExecutionData{
		Nodes: make(map[string]*Node, len(g.Nodes)),
		Edges: make(map[string][]string, len(g.Edges)),
	}
	for id, n := range g.Nodes {
		cp := *n
		data.Nodes[id] = &cp
	}
	for from, tos := range g.Edges {
		deps := make([]string, len(tos))
		copy(deps, tos)
		data.Edges[from] = deps
	}
	return data, nil
}

// Validate 编译期校验
func (c *DefaultCompiler) Validate(g *Graph) error {
	if g == nil || len(g.Nodes) == 0 {
		return fmt.Errorf("empty graph")
	}
	for id, n := range g.Nodes {
		// 节点必须引用存在的 agent/action（已在 parser 校验，此处兜底）
		if n == nil || n.Agent == "" || n.Action == "" {
			return fmt.Errorf("node %q: agent and action required", id)
		}
		// C1：RetrySpec.backoff 合法性
		if n.Retry.Max > 0 {
			if err := validateBackoff(n.Retry.Backoff, n.Retry.Max); err != nil {
				return fmt.Errorf("node %q: %w", id, err)
			}
		}
	}
	return nil
}

// validateBackoff 校验重试退避策略（C1）
// none 忽略次数；fixed/exponential 需要 max>0 才有效。
func validateBackoff(backoff string, max int) error {
	switch strings.ToLower(backoff) {
	case "none", "":
		return nil
	case "fixed", "exponential":
		if max < 1 {
			return fmt.Errorf("backoff %q requires retry.max >= 1", backoff)
		}
		return nil
	default:
		return fmt.Errorf("invalid retry backoff %q (allowed: none|fixed|exponential)", backoff)
	}
}

// CompileWorkflow 便捷方法：从 WorkflowSpec 直接编译为执行数据
func (c *DefaultCompiler) CompileWorkflow(spec *v1.WorkflowSpec) (*ExecutionData, error) {
	g, err := c.parser.Parse(spec)
	if err != nil {
		return nil, err
	}
	return c.Compile(g)
}
