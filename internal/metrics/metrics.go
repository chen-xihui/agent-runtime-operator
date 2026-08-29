// Package metrics 提供编排与沙箱可观测性指标（M5 生产化）。
// 基于 Prometheus，暴露到 controller-runtime 的 /metrics 端点。
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// orchestrationRunStarted 编排运行启动计数（按工作流）
	orchestrationRunStarted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_orchestration_runs_started_total",
			Help: "Total number of orchestration runs started.",
		},
		[]string{"tenant", "workflow"},
	)

	// orchestrationRunDuration 编排运行耗时（按结果）
	orchestrationRunDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agent_orchestration_run_duration_seconds",
			Help:    "Duration of orchestration runs.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tenant", "result"},
	)

	// orchestrationEvents 编排事件处理计数（按类型）
	orchestrationEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_orchestration_events_total",
			Help: "Total number of orchestration events processed.",
		},
		[]string{"tenant", "type"},
	)

	// sandboxStateTransitions 沙箱状态迁移计数（按 from/to）
	sandboxStateTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_sandbox_state_transitions_total",
			Help: "Total number of sandbox state transitions.",
		},
		[]string{"tenant", "from", "to"},
	)

	// sandboxActive 当前活跃沙箱数（按运行时）
	sandboxActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agent_sandbox_active",
			Help: "Number of active sandboxes.",
		},
		[]string{"runtime"},
	)

	// toolCalls 工具调用计数（按租户/工具/结果，DLP 审计指标）
	toolCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_tool_calls_total",
			Help: "Total number of tool calls through MCP proxy.",
		},
		[]string{"tenant", "tool", "result"},
	)

	// mcpErrors MCP 工具调用错误（按工具）
	mcpErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_mcp_errors_total",
			Help: "Total number of MCP tool call errors.",
		},
		[]string{"tenant", "tool"},
	)
)

func init() {
	// 注册到 controller-runtime 的全局 metrics registry（暴露于 /metrics）
	metrics.Registry.MustRegister(
		orchestrationRunStarted,
		orchestrationRunDuration,
		orchestrationEvents,
		sandboxStateTransitions,
		sandboxActive,
		toolCalls,
		mcpErrors,
	)
}

// ObserveRunStarted 记录编排运行启动
func ObserveRunStarted(tenant, workflow string) {
	orchestrationRunStarted.WithLabelValues(tenant, workflow).Inc()
}

// ObserveRunDuration 记录编排运行耗时
func ObserveRunDuration(tenant, result string, d time.Duration) {
	orchestrationRunDuration.WithLabelValues(tenant, result).Observe(d.Seconds())
}

// ObserveEvent 记录编排事件处理
func ObserveEvent(tenant, evtType string) {
	orchestrationEvents.WithLabelValues(tenant, evtType).Inc()
}

// ObserveSandboxTransition 记录沙箱状态迁移
func ObserveSandboxTransition(tenant, from, to string) {
	sandboxStateTransitions.WithLabelValues(tenant, from, to).Inc()
}

// SetSandboxActive 设置活跃沙箱数（按运行时）
func SetSandboxActive(runtimeClass string, n int) {
	sandboxActive.WithLabelValues(runtimeClass).Set(float64(n))
}

// ObserveToolCall 记录工具调用（DLP 审计指标）
func ObserveToolCall(tenant, tool, result string) {
	toolCalls.WithLabelValues(tenant, tool, result).Inc()
}

// ObserveMCPError 记录 MCP 工具调用错误
func ObserveMCPError(tenant, tool string) {
	mcpErrors.WithLabelValues(tenant, tool).Inc()
}
