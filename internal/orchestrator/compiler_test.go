package orchestrator

import (
	"strings"
	"testing"

	"github.com/example/agent-runtime-operator/api/v1"
)

func TestCompiler_Compile(t *testing.T) {
	p := NewDefaultParser()
	c := NewDefaultCompiler(p)

	data, err := c.CompileWorkflow(sampleSpec())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(data.Nodes) != 3 {
		t.Fatalf("compiled nodes = %d, want 3", len(data.Nodes))
	}
	if len(data.Edges["analyze"]) != 1 {
		t.Fatalf("compiled edges[analyze] = %v", data.Edges["analyze"])
	}
}

func TestCompiler_Validate_InvalidBackoff(t *testing.T) {
	p := NewDefaultParser()
	c := NewDefaultCompiler(p)

	spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
		{ID: "a", Agent: "x", Action: "y", Retry: &v1.RetrySpec{Max: 3, Backoff: "jitter"}},
	}}
	g, err := p.Parse(spec)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := c.Validate(g); err == nil || !strings.Contains(err.Error(), "invalid retry backoff") {
		t.Fatalf("expected invalid backoff error, got %v", err)
	}
}

func TestCompiler_Validate_OK(t *testing.T) {
	p := NewDefaultParser()
	c := NewDefaultCompiler(p)

	// fixed 和 exponential 均合法
	for _, backoff := range []string{"fixed", "exponential", "none", ""} {
		spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
			{ID: "a", Agent: "x", Action: "y", Retry: &v1.RetrySpec{Max: 3, Backoff: backoff}},
		}}
		g, _ := p.Parse(spec)
		if err := c.Validate(g); err != nil {
			t.Fatalf("backoff %q should be valid: %v", backoff, err)
		}
	}
}
