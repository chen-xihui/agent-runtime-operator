// Agent Runtime 平台 REST API Server 入口。
// 暴露租户/Agent/Sandbox/Workflow 管理接口（对接开放 SDK），供外部系统集成。
package main

import (
	"flag"
	"log"
	"net/http"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/example/agent-runtime-operator/internal/apiserver"
	"github.com/example/agent-runtime-operator/sdk"
)

func main() {
	var addr, kubeconfig string
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address.")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig (default: in-cluster).")
	flag.Parse()

	// 构建 k8s client config
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatalf("load kubeconfig: %v", err)
	}

	client, err := sdk.New(cfg)
	if err != nil {
		log.Fatalf("create sdk client: %v", err)
	}

	server := apiserver.New(client)
	log.Printf("agent-runtime api-server listening on %s", addr)
	if err := http.ListenAndServe(addr, server.Handler()); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
