.PHONY: all build operator api cli ui install crds run-operator run-api run-ui docker-build docker-push help

REGISTRY ?= ghcr.io/vesta-kubernetes
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

all: build ## Build all components

build: operator api cli ## Build operator, API, and CLI binaries

operator: ## Build the operator binary
	cd operator && go build -o bin/manager .

api: ## Build the API server binary
	cd api && go build -o bin/vesta-api ./cmd/main.go

cli: ## Build the CLI binary
	cd cli && go build -o bin/vesta .

ui: ## Build the UI
	cd ui && npm install && npm run build

# ── Run locally ──────────────────────────────────────────────────────────

run-operator: ## Run the operator locally (requires kubeconfig)
	cd operator && go run . --metrics-bind-address=:8080 --health-probe-bind-address=:8081

run-api: ## Run the API server locally
	cd api && go run ./cmd/main.go

run-ui: ## Run the UI dev server
	cd ui && npm run dev

# ── Kubernetes ───────────────────────────────────────────────────────────

crds: ## Install CRDs into the cluster
	kubectl apply -f operator/config/crd/bases/

samples: ## Apply sample CRD instances
	kubectl apply -f operator/config/samples/

rbac: ## Install RBAC roles
	kubectl apply -f operator/config/rbac/

install: crds rbac ## Install CRDs and RBAC into the cluster

uninstall: ## Remove CRDs from the cluster
	kubectl delete -f operator/config/crd/bases/ --ignore-not-found

# ── Docker ───────────────────────────────────────────────────────────────

docker-build: ## Build all Docker images
	docker build -t $(REGISTRY)/operator:$(VERSION) operator/
	docker build -t $(REGISTRY)/api:$(VERSION) api/

docker-push: docker-build ## Push all Docker images
	docker push $(REGISTRY)/operator:$(VERSION)
	docker push $(REGISTRY)/api:$(VERSION)

# ── Helm ─────────────────────────────────────────────────────────────────

helm-install: ## Install Vesta via Helm
	helm install vesta deploy/helm/vesta -n vesta-system --create-namespace

helm-upgrade: ## Upgrade Vesta via Helm
	helm upgrade vesta deploy/helm/vesta -n vesta-system

helm-uninstall: ## Uninstall Vesta via Helm
	helm uninstall vesta -n vesta-system

helm-template: ## Render Helm templates locally
	helm template vesta deploy/helm/vesta -n vesta-system

# ── Code quality ─────────────────────────────────────────────────────────

lint: ## Run linters
	cd operator && go vet ./...
	cd api && go vet ./...
	cd cli && go vet ./...

test: ## Run tests
	cd operator && go test ./... -v
	cd api && go test ./... -v
	cd cli && go test ./... -v

fmt: ## Format Go code
	cd operator && go fmt ./...
	cd api && go fmt ./...
	cd cli && go fmt ./...

generate: ## Generate CRD manifests from Go types
	cd operator && controller-gen crd paths="./api/..." output:crd:dir=config/crd/bases

clean: ## Clean build artifacts
	rm -rf operator/bin api/bin cli/bin ui/dist
