# Vesta Kubernetes

A self-hosted, open-source Platform-as-a-Service (PaaS) for Kubernetes. Deploy applications via git push, API call, or pre-built image -- without writing Kubernetes manifests.

## Architecture

Vesta consists of four components:

| Component | Language | Description |
|-----------|----------|-------------|
| **Operator** | Go (Kubebuilder) | Watches CRDs and reconciles Deployments, Services, Ingress, HPA, Secrets |
| **API Server** | Go (Gin) | REST API for projects, apps, deployments, secrets, auth, notifications |
| **Web UI** | React + TypeScript + Tailwind | Dashboard for managing the platform |
| **CLI** | Go (Cobra) | Command-line tool for all operations |

## Key Features

- **Zero-manifest deploys** -- deploy from a pre-built image, git push, or API call
- **Projects & environments** -- organize apps into projects with per-environment config (staging, production, etc.)
- **Per-environment pod sizes** -- choose resource presets (small/medium/large/xlarge) per app per environment
- **Health checks** -- configurable HTTP, TCP, or exec liveness & readiness probes
- **Autoscaling** -- CPU, memory, and custom metric-based HPA with configurable behavior
- **Secrets management** -- Opaque, Docker registry, and TLS secrets with per-app bindings
- **Private registries** -- ImagePullSecrets at project, app, and environment levels
- **Notifications** -- Slack, Discord, Google Chat, webhooks (HMAC-SHA256), and email (SMTP)
- **Forgot password** -- email-based password reset (when an email channel is configured)
- **Ingress** -- automatic ingress with optional TLS via cert-manager

## Installation

### Prerequisites

- Kubernetes cluster (v1.27+)
- Helm 3
- PostgreSQL database (for the API server)
- (Optional) cert-manager for TLS
- (Optional) metrics-server for autoscaling

### 1. Install CRDs

```bash
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/main/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/main/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaprojects.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/main/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaconfigs.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/main/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaenvironments.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/main/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestasecrets.yaml
```

### 2. Create the database secret

```bash
kubectl create namespace vesta-system

kubectl create secret generic vesta-db-secret \
  -n vesta-system \
  --from-literal=DATABASE_URL="postgres://user:password@db-host:5432/vesta?sslmode=disable"
```

### 3. Install with Helm

```bash
helm install vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system --create-namespace \
  --set api.database.existingSecret=vesta-db-secret
```

### 4. Upgrade

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system \
  --set api.database.existingSecret=vesta-db-secret
```

To pin specific image versions:

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system \
  --set api.database.existingSecret=vesta-db-secret \
  --set operator.image.tag=0.1.17 \
  --set api.image.tag=0.1.17 \
  --set ui.image.tag=0.1.17
```

### Optional: Metrics Server (for autoscaling)

```bash
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
```

## Helm Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `operator.image.tag` | Operator image tag | Chart appVersion |
| `api.image.tag` | API server image tag | Chart appVersion |
| `ui.image.tag` | UI image tag | Chart appVersion |
| `api.database.existingSecret` | Name of secret containing `DATABASE_URL` | `""` |
| `api.database.url` | Inline database URL (if not using a secret) | `""` |
| `api.ingress.enabled` | Enable API ingress | `false` |
| `api.ingress.host` | API ingress hostname | `kubernetes.getvesta.sh` |
| `config.domain` | Default domain for app ingresses | `apps.getvesta.sh` |
| `config.clusterIssuer` | cert-manager ClusterIssuer for TLS | `letsencrypt-prod` |
| `ui.enabled` | Deploy the web UI | `true` |

## Usage

### Deploy an app via API

```bash
curl -X POST https://<api-host>/api/v1/apps/my-app/deploy \
  -H "Authorization: Bearer <token>" \
  -d '{"tag": "v1.2.3", "environment": "production"}'
```

### Deploy via CLI

```bash
vesta deploy my-app --tag v1.2.3 --env production
```

## Project Structure

```
vesta-kubernetes/
├── operator/          # Kubernetes operator (Go/Kubebuilder)
├── api/               # REST API server (Go/Gin)
├── ui/                # Web dashboard (React/TypeScript/Tailwind)
├── cli/               # CLI tool (Go/Cobra)
└── deploy/helm/vesta/ # Helm chart
```

## Development

```bash
# Start PostgreSQL
docker compose up postgres

# Operator
cd operator && go run .

# API server
DATABASE_URL="postgres://vesta:vesta-dev@localhost:5433/vesta?sslmode=disable" make run-api

# UI
cd ui && npm install && npm run dev

# CLI
cd cli && go build -o vesta . && ./vesta --help
```

## License

GPL-3.0
