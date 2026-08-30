package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	agentv1 "github.com/example/agent-runtime-operator/api/v1"
	"github.com/example/agent-runtime-operator/internal/a2a"
	"github.com/example/agent-runtime-operator/internal/controllers"
	"github.com/example/agent-runtime-operator/internal/eventbus"
	"github.com/example/agent-runtime-operator/internal/mcp"
	"github.com/example/agent-runtime-operator/internal/orchestrator"
	"github.com/example/agent-runtime-operator/internal/plugin"
	"github.com/example/agent-runtime-operator/internal/registration"
	"github.com/example/agent-runtime-operator/internal/sandbox"
	"go.temporal.io/sdk/client"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(agentv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var sandboxImage string
	var enableRelay bool
	var relayImage string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&sandboxImage, "sandbox-image", "busybox:1.36", "Default agent sandbox image.")
	flag.BoolVar(&enableRelay, "enable-relay", false, "Inject Event Relay sidecar into sandbox (M1-b).")
	flag.StringVar(&relayImage, "relay-image", "registry.internal/agent-runtime/event-relay:latest",
		"Event Relay sidecar image (M1-b).")
	var temporalAddr string
	var temporalTaskQueue string
	flag.StringVar(&temporalAddr, "temporal-address", "", "Temporal server address (e.g. 127.0.0.1:7233). Enables orchestration.")
	flag.StringVar(&temporalTaskQueue, "temporal-task-queue", "agent-orchestration", "Temporal task queue.")
	var natsURL string
	flag.StringVar(&natsURL, "nats-url", "", "NATS server URL (e.g. nats://127.0.0.1:4222). Enables event-driven orchestration.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "a9b7c6d5.agent.runtime.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	sandboxCfg := &sandbox.Config{
		DefaultImage:  sandboxImage,
		EnableRelay:   enableRelay,
		RelayImage:    relayImage,
	}

	if err = (&controllers.SandboxReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Sandbox: sandbox.NewController(mgr.GetClient(), mgr.GetScheme(), sandboxCfg),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Sandbox")
		os.Exit(1)
	}

	if err = (&controllers.TenantReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Tenant")
		os.Exit(1)
	}

	// M2 协议层：构造 MCP Registry / A2A Gateway / 联动同步器
	mcpRegistry := mcp.NewMemoryRegistry()
	a2aGateway := a2a.NewMemoryGateway()
	syncer := registration.NewSyncer(mcpRegistry, a2aGateway)

	// M5 插件市场：构造 PluginRegistry
	pluginRegistry := plugin.NewRegistry()

	if err = (&controllers.AgentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Sandbox: sandbox.NewController(mgr.GetClient(), mgr.GetScheme(), sandboxCfg),
		Syncer:  syncer,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Agent")
		os.Exit(1)
	}

	// M5 插件市场：Plugin 控制器（CRD → PluginRegistry）
	if err = (&controllers.PluginReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Registry: pluginRegistry,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Plugin")
		os.Exit(1)
	}

	// M3 编排：WorkflowRun 控制器（依赖 Temporal，可选注册）
	if temporalAddr != "" {
		parser := orchestrator.NewDefaultParser()
		compiler := orchestrator.NewDefaultCompiler(parser)

		tClient, err := client.Dial(client.Options{HostPort: temporalAddr})
		if err != nil {
			setupLog.Error(err, "unable to dial temporal", "address", temporalAddr)
			os.Exit(1)
		}
		engine := orchestrator.NewTemporalEngine(tClient, temporalTaskQueue)

		// 节点事件处理器（幂等推进 WorkflowRun 状态，P1-3）
		nodeEvents := controllers.NewNodeEventProcessor(mgr.GetClient())

		if err = (&controllers.WorkflowRunReconciler{
			Client:     mgr.GetClient(),
			Scheme:     mgr.GetScheme(),
			Parser:     parser,
			Compiler:   compiler,
			Engine:     engine,
			NodeEvents: nodeEvents,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "WorkflowRun")
			os.Exit(1)
		}

		// 事件驱动推进：订阅 NATS 事件总线（若配置），转发给节点事件处理器
		if natsURL != "" {
			natsBus, err := eventbus.NewNatsBus(eventbus.NatsConfig{URL: natsURL, SubjectPrefix: "agent-runtime"})
			if err != nil {
				setupLog.Error(err, "unable to connect nats", "url", natsURL)
				os.Exit(1)
			}
			if _, err := natsBus.SubscribeAll(context.Background(), nodeEvents.OnEvent); err != nil {
				setupLog.Error(err, "unable to subscribe node events")
				os.Exit(1)
			}
			setupLog.Info("event-driven orchestration enabled", "nats", natsURL)
		}
		setupLog.Info("workflowrun controller enabled", "temporal", temporalAddr)
	} else {
		setupLog.Info("workflowrun controller disabled (--temporal-address not set)")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
