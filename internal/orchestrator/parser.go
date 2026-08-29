package orchestrator

import (
	"fmt"
	"time"

	"github.com/example/agent-runtime-operator/api/v1"
)

// DefaultParser 默认 DSL 解析器：将 WorkflowSpec 解析为可执行 Graph
type DefaultParser struct {
	// entrypoint 覆盖入口节点（用于多入口/事件触发场景，缺省为无依赖节点）
	entrypoints []string
}

// NewDefaultParser 创建默认解析器
func NewDefaultParser() *DefaultParser {
	return &DefaultParser{}
}

// WithEntrypoints 显式指定入口节点（可选，缺省为 dependsOn 为空的节点）
func (p *DefaultParser) WithEntrypoints(ids ...string) *DefaultParser {
	p.entrypoints = ids
	return p
}

// Parse 将 Workflow CR 解析为 Graph
func (p *DefaultParser) Parse(spec *v1.WorkflowSpec) (*Graph, error) {
	if spec == nil {
		return nil, fmt.Errorf("workflow spec is nil")
	}
	if len(spec.Nodes) == 0 {
		return nil, fmt.Errorf("workflow has no nodes")
	}

	g := &Graph{
		Nodes: make(map[string]*Node, len(spec.Nodes)),
		Edges: make(map[string][]string),
	}

	// 解析节点
	for i := range spec.Nodes {
		wn := &spec.Nodes[i]
		node, err := p.parseNode(wn)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", wn.ID, err)
		}
		if _, dup := g.Nodes[node.ID]; dup {
			return nil, fmt.Errorf("duplicate node id %q", node.ID)
		}
		g.Nodes[node.ID] = node
	}

	// 解析依赖边
	for i := range spec.Nodes {
		wn := &spec.Nodes[i]
		for _, dep := range wn.DependsOn {
			if _, ok := g.Nodes[dep]; !ok {
				return nil, fmt.Errorf("node %q depends on missing node %q", wn.ID, dep)
			}
			// 依赖边: 被依赖节点 -> 依赖它的节点（出边）
			g.Edges[dep] = append(g.Edges[dep], wn.ID)
		}
	}

	return g, nil
}

func (p *DefaultParser) parseNode(wn *v1.WorkflowNode) (*Node, error) {
	if wn.ID == "" {
		return nil, fmt.Errorf("node id is required")
	}
	if wn.Agent == "" {
		return nil, fmt.Errorf("node %q: agent is required", wn.ID)
	}
	if wn.Action == "" {
		return nil, fmt.Errorf("node %q: action is required", wn.ID)
	}

	node := &Node{
		ID:        wn.ID,
		Agent:     wn.Agent,
		Action:    wn.Action,
		Condition: wn.Condition,
		Always:    wn.Always,
		Kind:      wn.Kind,
	}
	if wn.Retry != nil {
		node.Retry = *wn.Retry
	}
	// 解析 timeout（Go duration 语义）
	if wn.Timeout != "" {
		d, err := time.ParseDuration(wn.Timeout)
		if err != nil {
			return nil, fmt.Errorf("node %q: invalid timeout %q: %v", wn.ID, wn.Timeout, err)
		}
		node.Timeout = d
	}
	return node, nil
}

// Validate 校验 DAG：无入口、缺失依赖、环
func (p *DefaultParser) Validate(g *Graph) error {
	if g == nil || len(g.Nodes) == 0 {
		return fmt.Errorf("empty graph")
	}

	// 1. 校验所有依赖边指向存在且无环
	indegree := make(map[string]int, len(g.Nodes))
	for id := range g.Nodes {
		indegree[id] = 0
	}
	for from, tos := range g.Edges {
		for _, to := range tos {
			if _, ok := g.Nodes[to]; !ok {
				return fmt.Errorf("edge from %q references missing node %q", from, to)
			}
			indegree[to]++
		}
	}

	// 2. 环检测（拓扑排序，Kahn 算法）
	queue := []string{}
	for id, d := range indegree {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, to := range g.Edges[cur] {
			indegree[to]--
			if indegree[to] == 0 {
				queue = append(queue, to)
			}
		}
	}
	if visited != len(g.Nodes) {
		return fmt.Errorf("graph contains a cycle")
	}

	// 3. 校验存在入口（无依赖节点 或 显式 entrypoint 有效）
	if !p.hasValidEntry(g) {
		return fmt.Errorf("no valid entrypoint node")
	}
	return nil
}

// hasValidEntry 校验是否存在有效入口
func (p *DefaultParser) hasValidEntry(g *Graph) bool {
	// 显式 entrypoint
	if len(p.entrypoints) > 0 {
		for _, e := range p.entrypoints {
			if _, ok := g.Nodes[e]; ok {
				return true
			}
		}
		return false
	}
	// 缺省：无出边的节点作为候选入口（被依赖的节点）
	hasDep := func(id string) bool {
		for _, tos := range g.Edges {
			for _, to := range tos {
				if to == id {
					return true
				}
			}
		}
		return false
	}
	for id := range g.Nodes {
		if !hasDep(id) {
			return true
		}
	}
	return false
}
