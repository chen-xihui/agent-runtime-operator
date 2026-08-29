package orchestrator

import (
	"strings"
	"testing"

	"github.com/example/agent-runtime-operator/api/v1"
)

func sampleSpec() *v1.WorkflowSpec {
	return &v1.WorkflowSpec{
		Entrypoint: "analyze",
		Nodes: []v1.WorkflowNode{
			{ID: "analyze", Agent: "analyzer", Action: "analyze_repo"},
			{ID: "review", Agent: "reviewer", Action: "review_code", DependsOn: []string{"analyze"}},
			{ID: "comment", Agent: "comment-bot", Action: "post_comment", DependsOn: []string{"review"}},
		},
	}
}

func TestParser_Parse_Valid(t *testing.T) {
	p := NewDefaultParser()
	g, err := p.Parse(sampleSpec())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(g.Nodes))
	}
	// 依赖边: analyze -> review -> comment
	if len(g.Edges["analyze"]) != 1 || g.Edges["analyze"][0] != "review" {
		t.Fatalf("edges[analyze] = %v", g.Edges["analyze"])
	}
	if len(g.Edges["review"]) != 1 || g.Edges["review"][0] != "comment" {
		t.Fatalf("edges[review] = %v", g.Edges["review"])
	}
	if err := p.Validate(g); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestParser_Parse_DuplicateNode(t *testing.T) {
	p := NewDefaultParser()
	spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
		{ID: "a", Agent: "x", Action: "y"},
		{ID: "a", Agent: "z", Action: "w"},
	}}
	if _, err := p.Parse(spec); err == nil || !strings.Contains(err.Error(), "duplicate node id") {
		t.Fatalf("expected duplicate node error, got %v", err)
	}
}

func TestParser_Parse_MissingDep(t *testing.T) {
	p := NewDefaultParser()
	spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
		{ID: "a", Agent: "x", Action: "y", DependsOn: []string{"ghost"}},
	}}
	if _, err := p.Parse(spec); err == nil || !strings.Contains(err.Error(), "missing node") {
		t.Fatalf("expected missing node error, got %v", err)
	}
}

func TestParser_Validate_Cycle(t *testing.T) {
	p := NewDefaultParser()
	spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
		{ID: "a", Agent: "x", Action: "y", DependsOn: []string{"c"}},
		{ID: "b", Agent: "x", Action: "y", DependsOn: []string{"a"}},
		{ID: "c", Agent: "x", Action: "y", DependsOn: []string{"b"}},
	}}
	g, err := p.Parse(spec)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := p.Validate(g); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestParser_Validate_NoEntry(t *testing.T) {
	p := NewDefaultParser()
	spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
		{ID: "a", Agent: "x", Action: "y"},
		{ID: "b", Agent: "x", Action: "y"},
	}}
	g, _ := p.Parse(spec)
	if err := p.Validate(g); err != nil {
		t.Fatalf("should have entry (both no deps): %v", err)
	}
}

func TestParser_Parse_TimeoutAndRetry(t *testing.T) {
	p := NewDefaultParser()
	spec := &v1.WorkflowSpec{Nodes: []v1.WorkflowNode{
		{ID: "a", Agent: "x", Action: "y", Timeout: "5m", Retry: &v1.RetrySpec{Max: 3, Backoff: "exponential"}},
	}}
	g, err := p.Parse(spec)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.Nodes["a"].Timeout.String() != "5m0s" {
		t.Fatalf("timeout = %v, want 5m", g.Nodes["a"].Timeout)
	}
	if g.Nodes["a"].Retry.Max != 3 || g.Nodes["a"].Retry.Backoff != "exponential" {
		t.Fatalf("retry = %+v", g.Nodes["a"].Retry)
	}
}
