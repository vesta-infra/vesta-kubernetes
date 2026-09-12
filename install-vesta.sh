#!/bin/sh
# Vesta platform installer -- installs Vesta into a Kubernetes cluster.
#
#   curl -fsSL https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/install-vesta.sh | sh
#
# For the CLI instead, see install.sh in the same directory.
#
# Environment variables:
#   VESTA_VERSION      chart version to install (default: latest published)
#   VESTA_NAMESPACE    namespace to install into (default: vesta-system)
#   VESTA_RELEASE      Helm release name (default: vesta)
#   VESTA_DATABASE_URL external Postgres to use; unset deploys a bundled one
#   VESTA_INGRESS_CLASS ingress class for app routing (default: none)
#   VESTA_DRY_RUN      set to 1 to print the helm command without running it

set -eu

CHART="oci://ghcr.io/vesta-infra/charts/vesta"
NAMESPACE="${VESTA_NAMESPACE:-vesta-system}"
RELEASE="${VESTA_RELEASE:-vesta}"
VERSION="${VESTA_VERSION:-}"

log()  { printf '%s\n' "$*" >&2; }
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31merror:\033[0m %s\n' "$*" >&2; exit 1; }

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed. $2"
}

check_prereqs() {
  log "Checking prerequisites..."
  need kubectl "Install it from https://kubernetes.io/docs/tasks/tools/"
  need helm "Install it from https://helm.sh/docs/intro/install/"

  # Helm 3 only. Helm 2 cannot read OCI charts and would fail much later with a
  # confusing error about the repository not being found.
  helm_major=$(helm version --template '{{.Version}}' 2>/dev/null | sed 's/^v\([0-9]*\).*/\1/')
  [ "${helm_major:-0}" -ge 3 ] || die "helm 3 or newer is required (found $(helm version --short 2>/dev/null || echo unknown))"
  ok "helm $(helm version --template '{{.Version}}' 2>/dev/null)"

  # Named explicitly, every time. A cluster-shaped command that does not say which cluster
  # it is about to change is how the wrong one gets changed.
  context="$(kubectl config current-context 2>/dev/null || echo 'unknown')"
  if [ "${VESTA_DRY_RUN:-}" = "1" ]; then
    ok "would target context: $context"
    return 0
  fi
  kubectl cluster-info >/dev/null 2>&1 || die "cannot reach a Kubernetes cluster. Check your kubeconfig and current context."
  ok "cluster reachable (context: $context)"

  server=$(kubectl version -o json 2>/dev/null | sed -n 's/.*"minor": *"\([0-9]*\).*/\1/p' | head -1)
  if [ -n "$server" ] && [ "$server" -lt 27 ]; then
    die "Kubernetes 1.27 or newer is required (cluster reports 1.${server})"
  fi
}

# ensure_namespace creates the target namespace already carrying the metadata Helm uses to
# recognise a resource as its own.
#
# The chart renders a Namespace object, and --create-namespace makes one first without that
# metadata, so the two collide: "namespaces vesta-system already exists", and the release is
# recorded as failed even though everything else installed. Pre-creating it with the
# ownership annotations lets Helm adopt it instead.
#
# The chart cannot simply stop rendering the Namespace. Helm deletes resources that
# disappear between chart versions, and that is precisely how 0.7.0 destroyed installs
# upgrading from 0.6.x. The template stays until the `resource-policy: keep` annotation it
# carries has reached existing releases, after which removing it is safe.
ensure_namespace() {
  # A dry run must not touch the cluster. This printed "created namespace" and then
  # actually created one, on whatever context happened to be current -- which is the whole
  # reason anyone passes VESTA_DRY_RUN in the first place.
  if [ "${VESTA_DRY_RUN:-}" = "1" ]; then
    log "Would ensure namespace $NAMESPACE exists and is adoptable by the $RELEASE release"
    return 0
  fi

  if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    ok "namespace $NAMESPACE exists"
  else
    kubectl create namespace "$NAMESPACE" >/dev/null
    ok "created namespace $NAMESPACE"
  fi

  kubectl annotate namespace "$NAMESPACE" \
    "meta.helm.sh/release-name=$RELEASE" \
    "meta.helm.sh/release-namespace=$NAMESPACE" --overwrite >/dev/null
  kubectl label namespace "$NAMESPACE" \
    app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
  ok "namespace is adoptable by the $RELEASE release"
}

build_args() {
  # No --create-namespace: ensure_namespace has already made it, with the metadata Helm
  # needs to adopt rather than collide with the chart's own Namespace object.
  set -- upgrade --install "$RELEASE" "$CHART" \
    --namespace "$NAMESPACE" \
    --wait --timeout 10m

  [ -n "$VERSION" ] && set -- "$@" --version "$VERSION"

  if [ -n "${VESTA_DATABASE_URL:-}" ]; then
    set -- "$@" --set-string "api.database.url=${VESTA_DATABASE_URL}"
  else
    # No database given, so bring one. Evaluating Vesta should not require standing up
    # Postgres first; production installs pass VESTA_DATABASE_URL instead.
    set -- "$@" --set postgres.enabled=true
  fi

  [ -n "${VESTA_INGRESS_CLASS:-}" ] && set -- "$@" --set-string "config.ingressClassName=${VESTA_INGRESS_CLASS}"

  printf '%s\n' "$@"
}

main() {
  log ""
  log "Installing Vesta into namespace '${NAMESPACE}'"
  log ""
  check_prereqs
  ensure_namespace

  # `upgrade --install` rather than `install`, so re-running this is the upgrade path
  # instead of an error about the release already existing.
  # shellcheck disable=SC2046
  set -- $(build_args)

  if [ "${VESTA_DRY_RUN:-}" = "1" ]; then
    log ""
    log "Would run: helm $*"
    exit 0
  fi

  log ""
  log "Running helm (this pulls images and can take a few minutes)..."
  helm "$@" || die "helm failed. Nothing was left half-applied: run 'helm status ${RELEASE} -n ${NAMESPACE}' for detail."

  ok "Vesta installed"
  log ""
  log "  Open the UI:"
  log "    kubectl port-forward -n ${NAMESPACE} svc/${RELEASE}-ui 8080:80"
  log "    then visit http://localhost:8080/setup to create the first admin account."
  log ""
  if [ -z "${VESTA_DATABASE_URL:-}" ]; then
    log "  Note: a bundled PostgreSQL was deployed. It is fine for evaluation, but it is"
    log "  a database you have to back up and operate. For production, reinstall with"
    log "  VESTA_DATABASE_URL pointing at a managed instance."
    log ""
  fi
}

main "$@"
