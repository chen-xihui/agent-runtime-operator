package main

import (
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
	"github.com/example/agent-runtime-operator/internal/mcp"
	"github.com/example/agent-runtime-operator/internal/registration"
	"github.com/example/agent-runtime-operator/internal/sandbox"
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

	if err = (&controllers.AgentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Sandbox: sandbox.NewController(mgr.GetClient(), mgr.GetScheme(), sandboxCfg),
		Syncer:  syncer,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Agent")
		os.Exit(1)
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
