# Makefile —— agent-runtime-operator

# 镜像仓库前缀
IMG ?= registry.internal/agent-runtime/operator:latest
# 构建产物
BIN_DIR := bin

GO ?= go
KUBECTL ?= kubectl

# 为二进制注入版本信息
VERSION ?= 0.1.0
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all
all: build

.PHONY: build
build: ## 编译所有二进制（operator + event-relay）
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/operator ./cmd/operator
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/event-relay ./cmd/relay

.PHONY: build-relay
build-relay: ## 仅编译 Event Relay Sidecar
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/event-relay ./cmd/relay

.PHONY: run
run: ## 本地运行 Operator（连当前 kubeconfig 集群）
	$(GO) run ./cmd/operator

.PHONY: vet
vet: ## 运行 go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## 格式化代码
	$(GO) fmt ./...

.PHONY: test
test: ## 运行单元测试
	$(GO) test ./... -count=1

.PHONY: tidy
tidy: ## 整理 go.mod / go.sum
	$(GO) mod tidy

.PHONY: manifests
manifests: ## 生成 CRD 清单（需 controller-gen）
	controller-gen rbac:roleName=manager-role crd webhook paths="./..." \
		output:crd:artifacts:config=config/crd

.PHONY: generate
generate: ## 生成 DeepCopy 等代码（需 controller-gen）
	controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: install
install: manifests ## 安装 CRD 到集群
	$(KUBECTL) apply -f config/crd

.PHONY: deploy
deploy: ## 部署 Operator 与 CRD
	$(KUBECTL) apply -f config/crd
	$(KUBECTL) apply -f config/manager

.PHONY: install-runtimes
install-runtimes: ## 注册沙箱运行时 RuntimeClass
	$(KUBECTL) apply -f config/runtimes/gvisor.yaml
	$(KUBECTL) apply -f config/runtimes/firecracker.yaml

HELM ?= helm
CHART := charts/agent-infra

.PHONY: helm-lint
helm-lint: ## lint Helm chart（需 helm）
	$(HELM) lint $(CHART)

.PHONY: helm-template
helm-template: ## 渲染 Helm chart 校验模板（无需集群）
	$(HELM) template agent-infra $(CHART) --namespace agent-runtime-system > /tmp/agent-infra-render.yaml
	@echo "rendered $(shell grep -c '^kind:' /tmp/agent-infra-render.yaml) resources"

.PHONY: docker-build
docker-build: ## 构建 Operator 镜像
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## 推送 Operator 镜像
	docker push $(IMG)

.PHONY: clean
clean: ## 清理构建产物
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
