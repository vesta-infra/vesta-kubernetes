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
- **One-command install and upgrade** -- installer script, `vesta install`, or a single `helm install`; CRDs upgrade themselves
- **Two-factor authentication** -- passkeys and authenticator apps, single-use recovery codes, and an optional policy requiring 2FA for admins
- **Private registries** -- ImagePullSecrets at project, app, and environment levels
- **Notifications** -- Slack, Discord, Google Chat, webhooks (HMAC-SHA256), and email (SMTP)
- **Forgot password** -- email-based password reset (when an email channel is configured)
- **Ingress** -- automatic ingress with optional TLS, from certificate providers you manage in the UI
- **SSL providers** -- create Let's Encrypt, ZeroSSL, Buypass, Google Trust Services, custom ACME, private-CA or self-signed issuers from Settings, with HTTP-01 or DNS-01 validation, and pick one per app or per environment

## Installation

Four ways in, in rough order of how quickly they get you to a running Vesta.

### 1. One command

```bash
curl -fsSL https://get.vesta.sh | sh
```

(or, until that domain is live:)

```bash
curl -fsSL https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/install-vesta.sh | sh
```

Checks prerequisites, installs Vesta with a bundled PostgreSQL, waits for rollout, and
prints the URL. Re-running it is also the upgrade path. Configure it with the environment
variables in [Installer variables](#installer-variables):

```bash
VESTA_VERSION=0.7.0 \
VESTA_NAMESPACE=vesta-system \
VESTA_DATABASE_URL="postgres://user:pass@db:5432/vesta?sslmode=disable" \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/install-vesta.sh)"
```

Set `VESTA_DRY_RUN=1` to print the helm command it would run and exit.

### 1b. With the CLI

If you already have the CLI (see [Installing the CLI](#installing-the-cli)):

```bash
vesta install --postgres
vesta install --database-url "postgres://user:pass@db:5432/vesta?sslmode=disable"
```

It wraps the same helm command shown below — there is nothing it does that you could not
do by hand; it just removes the flags you would otherwise have to remember. Add
`--dry-run` to print the command instead of running it.

### 2. Helm, with a bundled database

```bash
helm install vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system --create-namespace \
  --set postgres.enabled=true
```

That is the whole command — CRDs are applied by the chart, image tags come from the chart
version, and the namespace is created for you. Pin the release with `--version 0.7.0`.

The bundled database is a single-replica StatefulSet with a PVC. Fine for evaluation and
small installs; it is still a database you have to back up.

### 3. Helm, with your own database

```bash
kubectl create secret generic vesta-db-secret -n vesta-system \
  --from-literal=DATABASE_URL="postgres://user:pass@db-host:5432/vesta?sslmode=disable"

helm install vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system --create-namespace \
  --set api.database.existingSecret=vesta-db-secret
```

Or pass the URL directly with `--set-string api.database.url=...`, which the chart puts
into a Secret it manages.

### 4. From a checkout

```bash
git clone https://github.com/vesta-infra/vesta-kubernetes
cd vesta-kubernetes
helm install vesta deploy/helm/vesta -n vesta-system --create-namespace \
  --set postgres.enabled=true \
  --set api.image.tag=0.7.0 --set ui.image.tag=0.7.0 --set operator.image.tag=0.7.0
```

Image tags are needed here and only here: the in-repo `Chart.yaml` carries a placeholder
`appVersion`, which CI replaces at package time. Charts pulled from the registry do not
need them.

Once installed, open the UI and create the first admin account at `/setup`:

```bash
kubectl port-forward -n vesta-system svc/vesta-ui 8080:80
```

### Prerequisites

| | |
|---|---|
| Kubernetes | 1.27 or newer |
| Helm | 3 |
| PostgreSQL | unless using `postgres.enabled=true` |
| cert-manager | optional, for TLS |
| metrics-server | optional, for autoscaling |

### Installer variables

Read by `install-vesta.sh`.

| Variable | Default | Meaning |
|---|---|---|
| `VESTA_VERSION` | latest published | Chart version to install |
| `VESTA_NAMESPACE` | `vesta-system` | Namespace to install into |
| `VESTA_RELEASE` | `vesta` | Helm release name |
| `VESTA_DATABASE_URL` | *unset* | External Postgres. Unset deploys the bundled one |
| `VESTA_INGRESS_CLASS` | *unset* | Ingress class for app routing |
| `VESTA_DRY_RUN` | *unset* | `1` prints the helm command and exits |

The CLI installer (`install.sh`) is a different script and reads `VESTA_VERSION` and
`VESTA_INSTALL_DIR`. See [Installing the CLI](#installing-the-cli).

### Chart values

The ones worth knowing. `helm show values oci://ghcr.io/vesta-infra/charts/vesta` lists
them all.

| Value | Default | Meaning |
|---|---|---|
| `postgres.enabled` | `false` | Deploy a bundled PostgreSQL and wire `DATABASE_URL` |
| `postgres.storage` | `10Gi` | PVC size for the bundled database |
| `api.database.existingSecret` | `""` | Secret holding `DATABASE_URL` |
| `api.database.url` | `""` | Connection string, if not using a secret |
| `api.jwt.secret` | generated | Session signing key, ≥32 chars. Reused across upgrades |
| `api.encryption.key` | generated | Encrypts TOTP secrets at rest. Reused across upgrades |
| `api.readinessPath` | `/readyz` | Set to `/healthz` if pinning an image older than 0.7.0 |
| `crdManagement.enabled` | `true` | Apply CRDs from a pre-install/pre-upgrade hook |
| `crdManagement.image` | `registry.k8s.io/kubectl` | Image the CRD hook runs |
| `selfUpdate.enabled` | `true` | Allow upgrading Vesta from the web UI |
| `selfUpdate.helmImage` | `alpine/helm:3.16.3` | Image the upgrade Job runs helm from |
| `config.ingressClassName` | `""` | Ingress class for deployed apps |
| `certManager.enabled` | `false` | Issue TLS certificates via cert-manager |
| `config.domain` | `apps.getvesta.sh` | Default domain for app ingresses |
| `config.clusterIssuer` | `""` | Default ClusterIssuer for apps. Managed from Settings → SSL Certificates; set here only to seed it |
| `certManager.namespace` | `cert-manager` | Where cert-manager runs. Issuer credentials are written there, since that is where a ClusterIssuer's secretRefs resolve |
| `api.ingress.enabled` | `false` | Expose the API through an Ingress |
| `api.ingress.host` | `kubernetes.getvesta.sh` | API ingress hostname |
| `ui.enabled` | `true` | Deploy the web UI |
| `ui.ingress.enabled` | `false` | Expose the UI through an Ingress |
| `ui.ingress.host` | `ui.getvesta.sh` | UI ingress hostname |
| `operator.image.tag` / `api.image.tag` / `ui.image.tag` | chart appVersion | Pin component images. Not normally needed |

### API environment variables

Set by the chart; listed for anyone running the API outside Kubernetes.

| Variable | Required | Meaning |
|---|---|---|
| `DATABASE_URL` | yes | PostgreSQL connection string |
| `JWT_SECRET` | yes | Session signing key, ≥32 bytes. Changing it signs everyone out |
| `PORT` | no | Listen port, default `8090` |
| `VESTA_ENCRYPTION_KEY` | no | base64 32 bytes. Without it, authenticator-app 2FA is unavailable; passkeys still work |
| `VESTA_NAMESPACE` | no | Namespace Vesta is installed in, for version reporting and self-update |
| `VESTA_RELEASE_NAME` | no | Helm release name, for self-update |
| `VESTA_HELM_IMAGE` | no | Image the upgrade Job runs helm from |
| `VESTA_PUBLIC_URL` | no | Externally reachable base URL, used to build links in email |
| `VESTA_TRUSTED_PROXIES` | no | CIDRs whose `X-Forwarded-For` may be trusted. Unset trusts none |
| `VESTA_ALLOWED_ORIGINS` | no | Browser origins allowed for WebSockets and passkeys |
| `CERT_MANAGER_NAMESPACE` | no | Where cert-manager runs, for issuer credentials |

## Upgrading

```bash
helm upgrade vesta oci://ghcr.io/vesta-infra/charts/vesta \
  -n vesta-system --reset-then-reuse-values
```

CRDs are applied automatically by a pre-upgrade hook, so the manual `kubectl apply` of
five CRD URLs that earlier versions required is gone. Schema changes land before the new
operator starts reconciling against them.

`--reset-then-reuse-values` rather than `--reuse-values`: the latter carries old values
forward wholesale and silently drops new chart defaults, which is how an upgrade ends up
running new images against the previous release's configuration.

### With the CLI

```bash
vesta upgrade                      # to the latest release
vesta upgrade --chart-version 0.7.0
vesta status                       # what is running, and whether an update exists
```

`vesta upgrade` deliberately takes no configuration flags. The installed values are
carried forward, and re-specifying them on upgrade is how a setting nobody meant to touch
quietly changes.

### From the web UI

An admin can upgrade under **Settings → System**. Vesta checks for new releases daily,
shows a banner when one exists, and applies it by running `helm upgrade` in a Job — so
Helm stays the source of truth and a later manual upgrade will not revert it.

The API restarts partway through, so the page briefly loses its connection and reconnects
on its own. Turn the outbound check off with the toggle on that page for an air-gapped
cluster, or disable the feature entirely with `--set selfUpdate.enabled=false`.

### Upgrading from 0.6.x

Nothing to do beyond the usual `helm upgrade`. The CRD hook adopts CRDs that earlier
versions installed by hand, so the manual applies can simply stop.

One caveat: 0.7.0 moves the API's readiness probe to `/readyz`, which earlier images do
not serve. If you pin `api.image.tag` to something older than 0.7.0, also set
`api.readinessPath=/healthz` or the pod will never become Ready.

### Rolling back

```bash
helm rollback vesta -n vesta-system
```

CRDs are deliberately not rolled back. Removing fields from a CRD would prune them from
resources already using them, which loses data rather than restoring it.

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

### Updating the CLI

```bash
vesta self-update                    # to the latest release
vesta self-update --version 0.7.0    # to a specific one
vesta self-update --dry-run          # show what would be downloaded and replaced
```

Downloads the release archive for your platform, verifies it against the release's
published `checksums.txt`, and replaces the running binary. The replacement is a rename,
so an interrupted update leaves the working binary in place rather than a half-written
one. Re-running `install.sh` does the same job.

Refuses to replace a development build without `--force`, and refuses to go backwards
without it either. If the binary lives somewhere unwritable it will tell you to re-run
with sudo rather than failing obscurely. Windows is not supported yet — download the
`.zip` from the releases page.

### CLI command reference

| Command | Purpose |
|---|---|
| `vesta install` | Install the platform with Helm |
| `vesta upgrade` | Upgrade the platform; CRDs come with it |
| `vesta status` | Running versions, and whether an update is available |
| `vesta self-update` | Replace this binary with the latest release |
| `vesta apps` / `vesta deploy` | Manage and deploy applications |
| `vesta builds` / `vesta build` | Trigger and inspect builds |
| `vesta project export/import` | Move a project between instances |
| `vesta secrets` | Manage secrets (values are write-only) |
| `vesta version` | Print the CLI version |

All commands accept `--api-url` and `--token`, so one CLI can address several instances.

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
 