package admission

import (
	"fmt"

	"github.com/example/agent-runtime-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SetupWebhookWithManager 将校验/默认值 webhook 注册到 manager 的 webhook server。
// 需 manager 配置 WebhookServer（含 TLS certs；集群部署时经 cert-manager/K8s CA 注入）。
// enableMutating 是否注册 mutating(defaulting) webhook（Agent 默认值注入）。
func SetupWebhookWithManager(mgr ctrl.Manager, enableMutating bool) error {
	for _, reg := range []struct {
		obj       client.Object
		validator admission.CustomValidator
		name      string
	}{
		{&v1.Agent{}, AgentValidator{}, "agent"},
		{&v1.Workflow{}, WorkflowValidator{}, "workflow"},
		{&v1.WorkflowRun{}, WorkflowRunValidator{}, "workflowrun"},
		{&v1.Tenant{}, TenantValidator{}, "tenant"},
		{&v1.MCPEndpoint{}, MCPEndpointValidator{}, "mcpendpoint"},
		{&v1.ToolBinding{}, ToolBindingValidator{}, "toolbinding"},
	} {
		if err := ctrl.NewWebhookManagedBy(mgr).
			For(reg.obj).
			WithValidator(reg.validator).
			Complete(); err != nil {
			return fmt.Errorf("%s validating webhook: %w", reg.name, err)
		}
	}

	if enableMutating {
		if err := ctrl.NewWebhookManagedBy(mgr).
			For(&v1.Agent{}).
			WithDefaulter(AgentDefaulter{}).
			Complete(); err != nil {
			return fmt.Errorf("agent defaulter webhook: %w", err)
		}
	}
	return nil
}
