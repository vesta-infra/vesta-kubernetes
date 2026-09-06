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
- **Project transfer** -- move a whole project to another Vesta instance in an encrypted bundle only that instance can open
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

### 1. Install / Update CRDs

> **Important:** Helm does not update CRDs on `helm upgrade`. You must re-apply CRDs manually when upgrading to pick up schema changes (e.g., healthCheck, per-environment resources).

```bash
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaprojects.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaconfigs.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaenvironments.yaml
kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestasecrets.yaml
```


```bash
helm install -n vesta-system  vesta oci://ghcr.io/vesta-infra/charts/vesta --create-namespace \
  -n vesta-system   
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
helm install -n vesta-system vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system   \
  --set api.database.existingSecret=vesta-db-secret \
  --set config.ingressClassName=traefik
```

### 4. Upgrade

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system \
   --reuse-values \
   --set ui.ingress.tls=false
  # --set ui.ingress.host=k8.getvesta.sh \
  # --set ui.ingress.enabled=true \
  # --set ui.ingress.clusterIssuer=letsencrypt-prod
```

To pin specific image versions:

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system \
   --reuse-values \
    --set config.ingressClassName=traefik \
  --set operator.image.tag=0.3.44 \
  --set api.image.tag=0.3.44 \
  --set ui.image.tag=0.3.44
```

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system \
   --reuse-values \
  --set operator.image.tag=0.6.2 \
  --set api.image.tag=0.6.2 \
  --set ui.image.tag=0.6.2
```


```bash
  kubectl apply -f https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/deploy/helm/vesta/crds/kubernetes.getvesta.sh_vestaapps.yaml
```

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system \
  --set api.database.existingSecret=vesta-db-secret \
  --set operator.image.tag=0.5.22 \
  --set api.image.tag=0.5.22 \
  --set ui.image.tag=0.5.22 
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
| `ui.ingress.enabled` | Enable UI ingress | `false` |
| `ui.ingress.host` | UI ingress hostname | `ui.getvesta.sh` |

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

### Copying env vars and secrets

Every env var and revealed-secret panel has a **Copy as .env** button that puts the whole
set on the clipboard in `KEY=value` form, ready to paste into another app or an `.env`
file. Values containing spaces, `#` or newlines are quoted and escaped so the output
pastes back through **Import .env** unchanged.

Secrets expose the button only once values are revealed, so it carries the same
admin-or-project-owner gate and 30-second auto-hide as the reveal itself.

### Moving a project to another Vesta instance

A project can be exported as an encrypted bundle carrying its apps, configuration, env
vars, secrets, shared secrets and the registry credentials its images need.

Each Vesta installation holds an X25519 keypair, generated on first use and stored as the
`vesta-instance-identity` Secret in `vesta-system`. A bundle is sealed to one instance's
public key, so the file is inert to everyone else -- including whoever carries it. Nothing
about its contents is readable from the outside: only the recipient fingerprint is
cleartext, so an instance can tell a misdirected bundle from a corrupted one.

**On the target instance**, copy the public key from Settings -> Instance Identity:

```
vesta1:pub:MCowBQYDK2VuAyEA9k3...
```

**On the source instance**, from the project page or the CLI:

```bash
vesta project export acme \
  --recipient-key @target.pub \
  --out acme.bundle.json \
  --api-url https://vesta.staging.example.com --token "$SOURCE_TOKEN"
```

**On the target instance:**

```bash
vesta project import \
  --file acme.bundle.json \
  --api-url https://vesta.prod.example.com --token "$TARGET_TOKEN"
```

Notes:

- Export requires admin or project owner -- the bundle holds every secret in the project,
  so it is gated exactly like revealing one. Import requires admin, because it can create
  instance-level registry credentials.
- An existing project is never overwritten. If the name is taken, import fails and you
  re-run with `--as <new-name>`; app specs are rewritten to the new project name.
- Import is not atomic. Kubernetes has no transaction, so a failure partway through
  reports what was created and leaves it in place rather than deleting more than it made.
- Losing `vesta-instance-identity` makes every bundle previously sealed for that instance
  permanently unreadable. Back it up alongside your database.

## Installing the CLI

### Install script (macOS & Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/install.sh | sh
```

The script picks the right build for your OS/arch, verifies the SHA-256 checksum, and
installs to `/usr/local/bin`. Override either with environment variables:

```bash
curl -fsSL https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/install.sh \
  | VESTA_VERSION=0.6.2 VESTA_INSTALL_DIR="$HOME/.local/bin" sh
```

### Manual download

Grab an archive from the [releases page](https://github.com/vesta-infra/vesta-kubernetes/releases)
-- `vesta_<version>_<os>_<arch>.tar.gz` for macOS/Linux, `.zip` for Windows -- verify it
against `checksums.txt`, extract, and put `vesta` on your `PATH`.

### From source

```bash
make cli-install                          # builds and installs to /usr/local/bin
make cli-install CLI_INSTALL_DIR=~/.local/bin
```

Confirm the install:

```bash
vesta version
```

### Cutting a CLI release

Pushing a `v*` tag runs [`.github/workflows/release-cli.yaml`](.github/workflows/release-cli.yaml),
which cross-compiles the CLI for darwin/linux (amd64 + arm64) and windows/amd64 and
attaches the archives plus `checksums.txt` to the GitHub release. To produce the same
artifacts locally:

```bash
make cli-release            # writes dist/cli/
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


```
kubectl apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: vesta-ui
  namespace: vesta-system
spec:
  rules:
    - host: k8.getvesta.sh
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: vesta-ui
                port:
                  number: 80
EOF
```

## License

GPL-3.0
 