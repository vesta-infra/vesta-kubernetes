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
- **Two-factor authentication** -- passkeys and authenticator apps, single-use recovery codes, and an optional policy requiring 2FA for admins
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
  --set operator.image.tag=0.6.3 \
  --set api.image.tag=0.6.3 \
  --set ui.image.tag=0.6.3
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

### Two-factor authentication

Users enrol under Settings -> General -> Two-Factor Authentication. Two factor types are
supported and either satisfies a login:

- **Passkeys** (WebAuthn) -- Touch ID, Windows Hello, or a hardware security key. Requires
  the UI to be served over HTTPS; browsers refuse the API outside a secure context, with
  `localhost` the one exception.
- **Authenticator apps** (TOTP) -- a six-digit code from 1Password, Authy, Google
  Authenticator and the like. Requires `VESTA_ENCRYPTION_KEY`; see below.

Enrolling the first factor of either kind also issues ten single-use recovery codes, shown
once. Adding a second factor later does not reissue them, so codes already written down
stay valid. They can be regenerated from Settings, which invalidates the previous set.

**Requiring 2FA for admins.** An admin can turn this on under Settings -> Roles -> Two-Factor
Policy; it takes effect without a redeploy. Admins who already exist and hold no factor are
not locked out -- their next sign-in stops at an enrollment screen instead, reachable only
by the enrollment endpoints. While the policy is on, an admin cannot remove their last
factor, and recovery codes do not count as one.

**Changing your factors takes a fresh confirmation.** A session proves who signed in, not
who is at the keyboard now, so anything that could weaken the account asks for a password
or a passkey again first: removing an authenticator app, removing a passkey, regenerating
recovery codes, and adding a factor to an account that already has one. Regeneration is
included because codes lifted from a stolen session would otherwise outlive the password
change the victim makes afterwards, and addition because someone who cannot take your
factor away can otherwise register their own beside it and keep access indefinitely.

Enrolling the *first* factor is exempt. There is nothing to prove possession of yet, and
requiring it would make mandatory enrollment at login impossible -- that user has no factor
by definition, which is why they are being asked for one.

The confirmation produces a single-use grant that expires in five minutes and is sent in
an `X-Vesta-Reauth` header. One grant authorises exactly one change. Anti-lockout is
checked before the grant is spent, so a removal the policy forbids does not cost a
confirmation that would have to be repeated.

**Locked-out users.** An admin can clear another user's factors from Settings -> Users ->
Reset 2FA. This is the escape hatch for someone who has lost both their device and their
recovery codes; without it such an account is unreachable. The reset is atomic, clears the
lockout timer along with the factors, is audit-logged as `mfa_admin_reset`, and requires
the admin to confirm their own identity first. Admins cannot reset themselves this way --
that would sidestep the anti-lockout rule -- and under a mandatory policy the reset user is
asked to enrol again at their next sign-in rather than being let through without a factor.

**Login becomes a two-step exchange.** `POST /auth/login` no longer always returns a
session. When the account holds a factor it returns `{"mfaRequired": true, "token": ...}`,
where the token reaches only the verification endpoints and expires in five minutes.
Exchange it at `POST /auth/mfa/verify` (which accepts either a TOTP code or a recovery
code -- it tells them apart by shape) or through the WebAuthn assertion endpoints. Scripts
using API keys (`vst_...`) are unaffected: they are separate credentials and never carry a
second factor.

Failed verifications are rate-limited per user in Postgres -- five failures in fifteen
minutes locks the account for fifteen, ten locks it for an hour -- so the counter holds
across API replicas and restarts.

**`VESTA_ENCRYPTION_KEY`.** TOTP secrets have to be readable to generate a comparison code,
so they are stored encrypted with AES-256-GCM rather than hashed. The Helm chart generates
the key on first install and reuses it across upgrades. Rotating or losing it does not sign
anyone out, but it does make every enrolled authenticator app unverifiable -- those users
fall back to recovery codes and re-enrol. Passkeys need no key and are unaffected. If the
key is absent the API still starts and passkeys still work; authenticator-app enrollment
reports itself unavailable.

**Passkeys behind a proxy.** The relying party is derived from the browser's `Origin`
header, not the API's own `Host`, because the UI and API sit on different hostnames in the
default chart and the dev proxy rewrites `Host`. Set `VESTA_ALLOWED_ORIGINS` to pin which
browser origins may complete a ceremony; left unset, any syntactically valid domain is
accepted.

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
 