# Vesta Kubernetes

A self-hosted, open-source Platform-as-a-Service (PaaS) for Kubernetes. Deploy applications via git push, API call, or pre-built image -- without writing Kubernetes manifests.

## Architecture

Vesta consists of four components:

| Component | Language | Description |
|-----------|----------|-------------|
| **Operator** | Go (Kubebuilder) | Watches CRDs and reconciles Deployments, Services, Ingress, HPA, Secrets |
| **API Server** | Go (Gin) | REST API for pipelines, apps, deployments, secrets, auth |
| **Web UI** | React + TypeScript + Tailwind | Dashboard for managing the platform |
| **CLI** | Go (Cobra) | Command-line tool for all operations |

## Project Structure

```
vesta-kubernetes/
├── SPECIFICATIONS.md              # Full product & technical spec
├── operator/                      # Kubernetes operator
│   ├── api/v1alpha1/              # CRD Go types (VestaApp, VestaPipeline, VestaConfig, VestaSecret)
│   ├── controllers/               # Reconciliation controllers
│   ├── config/
│   │   ├── crd/bases/             # CRD YAML schemas
│   │   ├── rbac/                  # RBAC roles and bindings
│   │   ├── manager/               # Operator deployment manifest
│   │   └── samples/               # Example CRD instances
│   ├── main.go                    # Operator entrypoint
│   └── Dockerfile
├── api/                           # REST API server
│   ├── cmd/main.go                # API entrypoint
│   ├── internal/
│   │   ├── handlers/              # Route handlers (apps, deploy, secrets, etc.)
│   │   └── middleware/            # Auth middleware
│   ├── openapi.yaml               # OpenAPI 3.0 specification
│   └── Dockerfile
├── ui/                            # Web UI (React)
│   ├── package.json
│   └── index.html
├── cli/                           # CLI tool
│   ├── cmd/                       # Cobra commands (deploy, apps, pipelines, secrets)
│   └── main.go
└── deploy/
    └── helm/vesta/                # Helm chart
        ├── Chart.yaml
        ├── values.yaml
        ├── crds/                  # CRD installation
        └── templates/             # K8s manifests
```

## Custom Resource Definitions

- **VestaApp** -- Deployed application (image, runtime, secrets, scaling, ingress, custom K8s config)
- **VestaPipeline** -- Groups apps across stages (review, staging, production)
- **VestaConfig** -- Cluster-wide configuration (registry, pod sizes, auth, autoscale defaults)
- **VestaSecret** -- Managed secrets (Opaque, Docker registry, TLS)

## Key Features

- **Secrets-first**: Sensitive config via Kubernetes Secrets, not plain env vars
- **ImagePullSecrets**: Full private registry support at global, pipeline, and app levels
- **Custom K8s config**: Per-app nodeSelector, tolerations, affinity, probes, initContainers, extra resources
- **API-driven deploy**: `POST /api/v1/apps/:id/deploy` with just a tag -- repository and imagePullSecrets come from app config
- **Autoscaling**: CPU, memory, and custom metric-based HPA with configurable behavior
- **Git integration**: Push-to-deploy, PR review apps, branch-per-stage

## Quick Start

```bash
# Install via Helm
helm install vesta ./deploy/helm/vesta -n vesta-system --create-namespace

# Deploy an app via API
curl -X POST https://kubernetes.getvesta.sh/api/v1/apps/my-app/deploy \
  -H "Authorization: Bearer <token>" \
  -d '{"tag": "v1.2.3"}'

# Deploy via CLI
vesta deploy my-app --tag v1.2.3 --token <token>
```

## Development

```bash
# Operator
cd operator && go run .

# API server
cd api && go run ./cmd/main.go

# CLI
cd cli && go build -o vesta . && ./vesta --help

# UI
cd ui && npm install && npm run dev
```

## License

GPL-3.0


DATABASE_URL="postgres://vesta:vesta-dev@localhost:5433/vesta?sslmode=disable" make run-api


# Install CRDs
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/main/deploy/helm/vesta/crds/

# Install the chart from GHCR
helm install vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system --create-namespace \
  --set config.domain=your-domain.com

To upgrade later:
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta -n vesta-system