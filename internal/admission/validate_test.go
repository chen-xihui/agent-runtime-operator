package admission

import (
	"strings"
	"testing"

	"github.com/example/agent-runtime-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func mName(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}

func sampleAgent() *v1.Agent {
	return &v1.Agent{
		ObjectMeta: mName("agent-1"),
		Spec: v1.AgentSpec{
			Image:   "busybox:1.36",
			Runtime: v1.RuntimeSpec{Class: v1.RuntimeGVisor},
			MCP:     v1.MCPSpec{AllowedTools: []string{"db.query"}, Endpoints: []string{"mcp-db"}},
			Security: v1.SecuritySpec{
				NetworkPolicy: "deny-all",
			},
		},
	}
}

func TestValidateAgent_OK(t *testing.T) {
	if err := ValidateAgent(sampleAgent()); err != nil {
		t.Fatalf("valid agent rejected: %v", err)
	}
}

func TestValidateAgent_MissingImage(t *testing.T) {
	a := sampleAgent()
	a.Spec.Image = ""
	if err := ValidateAgent(a); err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected missing image error, got %v", err)
	}
}

func TestValidateAgent_BadRuntime(t *testing.T) {
	a := sampleAgent()
	a.Spec.Runtime.Class = "weird-runtime"
	if err := ValidateAgent(a); err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("expected runtime error, got %v", err)
	}
}

func TestValidateAgent_BadNetworkPolicy(t *testing.T) {
	a := sampleAgent()
	a.Spec.Security.NetworkPolicy = "open-internet"
	if err := ValidateAgent(a); err == nil || !strings.Contains(err.Error(), "networkPolicy") {
		t.Fatalf("expected networkPolicy error, got %v", err)
	}
}

func TestValidateAgent_RequestExceedsLimit(t *testing.T) {
	a := sampleAgent()
	a.Spec.Runtime.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
	}
	if err := ValidateAgent(a); err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected request>limit error, got %v", err)
	}
}

func TestDefaultAgent(t *testing.T) {
	a := &v1.Agent{}
	a.Name = "a"
	DefaultAgent(a)
	if a.Spec.Runtime.Class != v1.RuntimeGVisor {
		t.Fatalf("runtime class default = %q, want gvisor", a.Spec.Runtime.Class)
	}
	if !a.Spec.Security.RunAsNonRoot {
		t.Fatal("runAsNonRoot default should be true")
	}
}

func TestValidateWorkflow_OK(t *testing.T) {
	wf := &v1.Workflow{
		ObjectMeta: mName("wf"),
		Spec: v1.WorkflowSpec{
			Entrypoint: "a",
			Nodes: []v1.WorkflowNode{
				{ID: "a", Agent: "ag1", Action: "go"},
				{ID: "b", Agent: "ag2", Action: "review", DependsOn: []string{"a"}},
			},
		},
	}
	if err := ValidateWorkflow(wf); err != nil {
		t.Fatalf("valid workflow rejected: %v", err)
	}
}

func TestValidateWorkflow_Cycle(t *testing.T) {
	wf := &v1.Workflow{
		ObjectMeta: mName("wf"),
		Spec: v1.WorkflowSpec{
			Entrypoint: "a",
			Nodes: []v1.WorkflowNode{
				{ID: "a", Agent: "ag1", Action: "go", DependsOn: []string{"b"}},
				{ID: "b", Agent: "ag2", Action: "review", DependsOn: []string{"a"}},
			},
		},
	}
	if err := ValidateWorkflow(wf); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidateWorkflow_EntrypointNotInNodes(t *testing.T) {
	wf := &v1.Workflow{
		ObjectMeta: mName("wf"),
		Spec: v1.WorkflowSpec{
			Entrypoint: "ghost",
			Nodes:      []v1.WorkflowNode{{ID: "a", Agent: "ag1", Action: "go"}},
		},
	}
	if err := ValidateWorkflow(wf); err == nil {
		t.Fatal("expected entrypoint-not-in-nodes error")
	}
}

func TestValidateWorkflow_MissingDependency(t *testing.T) {
	wf := &v1.Workflow{
		ObjectMeta: mName("wf"),
		Spec: v1.WorkflowSpec{
			Entrypoint: "a",
			Nodes: []v1.WorkflowNode{
				{ID: "a", Agent: "ag1", Action: "go"},
				{ID: "b", Agent: "ag2", Action: "review", DependsOn: []string{"ghost"}},
			},
		},
	}
	if err := ValidateWorkflow(wf); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestValidateWorkflowRun(t *testing.T) {
	run := &v1.WorkflowRun{ObjectMeta: mName("r"), Spec: v1.WorkflowRunSpec{WorkflowRef: "wf"}}
	if err := ValidateWorkflowRun(run); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}
	run.Spec.WorkflowRef = ""
	if err := ValidateWorkflowRun(run); err == nil {
		t.Fatal("expected missing workflowRef error")
	}
}

func TestValidateTenant_Quota(t *testing.T) {
	tok := &v1.Tenant{ObjectMeta: mName("t"), Spec: v1.TenantSpec{Quota: v1.QuotaSpec{
		MaxSandboxes: 5, MaxAgents: 2, MaxCPU: "2", MaxMemory: "4Gi",
	}}}
	if err := ValidateTenant(tok); err != nil {
		t.Fatalf("valid tenant rejected: %v", err)
	}
	// 非法 quantity
	tok.Spec.Quota.MaxMemory = "not-a-quantity"
	if err := ValidateTenant(tok); err == nil {
		t.Fatal("expected invalid quantity error")
	}
}

func TestValidateToolBinding(t *testing.T) {
	ok := &v1.ToolBinding{
		ObjectMeta: mName("tb"),
		Spec:       v1.ToolBindingSpec{Tools: []v1.ToolGrant{{Name: "db.query"}}},
	}
	if err := ValidateToolBinding(ok); err != nil {
		t.Fatalf("valid toolbinding rejected: %v", err)
	}
	dup := ok.DeepCopy()
	dup.Spec.Tools = []v1.ToolGrant{{Name: "db.query"}, {Name: "db.query"}}
	if err := ValidateToolBinding(dup); err == nil {
		t.Fatal("expected duplicate tool error")
	}
}

func TestValidateMCPEndpoint(t *testing.T) {
	ok := &v1.MCPEndpoint{ObjectMeta: mName("ep"), Spec: v1.MCPEndpointSpec{Address: "mcp:50051", Transport: "streamable-http"}}
	if err := ValidateMCPEndpoint(ok); err != nil {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
	ok.Spec.Transport = "carrier-pigeon"
	if err := ValidateMCPEndpoint(ok); err == nil {
		t.Fatal("expected unsupported transport error")
	}
}
