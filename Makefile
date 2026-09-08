.PHONY: all build operator api cli sync-crds check-crds helm-lint helm-upgrade-safety check-upgrader-rbac cli-release cli-install ui install crds run-operator run-api run-ui docker-build docker-push help

# Must match .github/workflows/release.yaml and the image repositories in
# deploy/helm/vesta/values.yaml. These disagreed for a long time -- the Makefile pushed to
# ghcr.io/vesta-kubernetes/{operator,api,ui} while everything else read
# ghcr.io/vesta-infra/kubernetes-{operator,api,ui} -- so local pushes went nowhere useful.
REGISTRY ?= ghcr.io/vesta-infra
IMAGE_PREFIX ?= kubernetes-
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# ── CLI release ──────────────────────────────────────────────────────────
# CLI_VERSION drops the leading "v" so archive names match the release tag body.
CLI_VERSION ?= $(patsubst v%,%,$(VERSION))
CLI_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
CLI_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
CLI_DIST ?= dist/cli
CLI_INSTALL_DIR ?= /usr/local/bin
CLI_PKG := kubernetes.getvesta.sh/cli/cmd
CLI_LDFLAGS := -s -w \
	-X $(CLI_PKG).version=$(CLI_VERSION) \
	-X $(CLI_PKG).commit=$(CLI_COMMIT) \
	-X $(CLI_PKG).date=$(CLI_DATE)
CONTROLLER_GEN_VERSION ?= v0.19.0
CLI_PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

all: build ## Build all components

build: operator api cli ## Build operator, API, and CLI binaries

operator: ## Build the operator binary
	cd operator && go build -o bin/manager .

api: ## Build the API server binary
	cd api && go build -o bin/vesta-api ./cmd/main.go

cli: ## Build the CLI binary
	cd cli && go build -ldflags '$(CLI_LDFLAGS)' -o bin/vesta .

cli-install: cli ## Build the CLI and install it into $(CLI_INSTALL_DIR)
	install -d $(CLI_INSTALL_DIR)
	install -m 0755 cli/bin/vesta $(CLI_INSTALL_DIR)/vesta
	@echo "Installed $(CLI_INSTALL_DIR)/vesta ($(CLI_VERSION))"

cli-release: ## Cross-compile CLI release archives + checksums into dist/
	@rm -rf $(CLI_DIST) && mkdir -p $(CLI_DIST)
	@set -e; for platform in $(CLI_PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  building $$os/$$arch"; \
		( cd cli && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags '$(CLI_LDFLAGS)' -o ../$(CLI_DIST)/vesta$$ext . ); \
		name="vesta_$(CLI_VERSION)_$${os}_$${arch}"; \
		if [ "$$os" = "windows" ]; then \
			( cd $(CLI_DIST) && zip -q "$$name.zip" "vesta$$ext" ); \
		else \
			( cd $(CLI_DIST) && tar -czf "$$name.tar.gz" "vesta$$ext" ); \
		fi; \
		rm -f $(CLI_DIST)/vesta$$ext; \
	done
	@cd $(CLI_DIST) && (sha256sum vesta_* 2>/dev/null || shasum -a 256 vesta_*) > checksums.txt
	@echo "Release artifacts in $(CLI_DIST)/:" && ls -1 $(CLI_DIST)

ui: ## Build the UI
	cd ui && npm install && npm run build

# ── Run locally ──────────────────────────────────────────────────────────

run-operator: ## Run the operator locally (requires kubeconfig)
	cd operator && go run . --metrics-bind-address=:8080 --health-probe-bind-address=:8081

run-api: ## Run the API server locally (sources api/.env when present)
	cd api && set -a && . ./.env 2>/dev/null; set +a; go run ./cmd/main.go

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
	docker build -t $(REGISTRY)/$(IMAGE_PREFIX)operator:$(VERSION) --build-arg APP_VERSION=$(VERSION) operator/
	docker build -t $(REGISTRY)/$(IMAGE_PREFIX)api:$(VERSION) --build-arg APP_VERSION=$(VERSION) api/
	docker build -t $(REGISTRY)/$(IMAGE_PREFIX)ui:$(VERSION) --build-arg APP_VERSION=$(VERSION) ui/

docker-push: docker-build ## Push all Docker images
	docker push $(REGISTRY)/$(IMAGE_PREFIX)operator:$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_PREFIX)api:$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_PREFIX)ui:$(VERSION)

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

generate: ## Generate CRD manifests from Go types, then sync them into the chart
	cd operator && GOFLAGS=-mod=mod go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) \
		crd paths="./api/..." output:crd:dir=config/crd/bases
	@echo ""
	@echo "Generated into operator/config/crd/bases only."
	@echo "The chart's CRDs are deliberately more permissive: many Go fields lack"
	@echo "omitempty, so generation marks them required and the API server then rejects"
	@echo "objects earlier releases stored. Run 'make sync-crds' to copy them across,"
	@echo "then 'make check-crds' -- and expect to add omitempty until it passes."

sync-crds: ## Copy generated CRDs into the chart (then run check-crds)
	cp operator/config/crd/bases/*.yaml deploy/helm/vesta/crds/

check-crds: ## Fail if the chart's CRDs would reject data an earlier release accepted
	./hack/check-crd-compat.sh $(CHART_BASELINE)

helm-lint: ## Lint and render the chart the way CI does
	helm lint deploy/helm/vesta
	helm template vesta deploy/helm/vesta -n vesta-system >/dev/null
	helm template vesta deploy/helm/vesta -n vesta-system --set postgres.enabled=true >/dev/null

# CHART_BASELINE is the last release whose rendered resources this chart must still
# cover. Bump it only when you have confirmed the upgrade path from that version.
CHART_BASELINE ?= 0.6.3

helm-upgrade-safety: ## Fail if this chart drops a resource an earlier release rendered
	./hack/check-no-dropped-resources.sh $(CHART_BASELINE)

check-upgrader-rbac: ## Fail if self-update cannot apply everything the chart renders
	./hack/check-upgrader-rbac.sh

clean: ## Clean build artifacts
	rm -rf operator/bin api/bin cli/bin ui/dist dist
