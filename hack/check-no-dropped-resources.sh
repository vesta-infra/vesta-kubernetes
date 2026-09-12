#!/usr/bin/env bash
# Fail if this chart stops rendering a resource that a previously published chart rendered.
#
# Helm deletes resources that disappear between chart versions. That is usually what you
# want for a Deployment; it is catastrophic for a Namespace, which takes every Secret,
# PVC and pod inside it. Chart 0.7.0 dropped templates/namespace.yaml and deleted the
# vesta-system namespace of every cluster that upgraded from 0.6.x.
#
# Nothing about that failure was visible in `helm lint`, in a fresh install, or in an
# upgrade on a cluster whose namespace was not chart-owned. Comparing the rendered
# resource sets is what catches it.
#
#   ./hack/check-no-dropped-resources.sh 0.6.3
#
# Renders locally only -- no cluster is contacted.
set -euo pipefail

BASELINE="${1:-}"
CHART_DIR="${CHART_DIR:-deploy/helm/vesta}"
[ -n "$BASELINE" ] || { echo "usage: $0 <baseline-chart-version>" >&2; exit 2; }

command -v helm >/dev/null || { echo "helm is required" >&2; exit 2; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "Pulling baseline chart $BASELINE..."
helm pull "oci://ghcr.io/vesta-infra/charts/vesta" --version "$BASELINE" \
  --untar --untardir "$work" >/dev/null 2>&1 \
  || { echo "could not pull baseline $BASELINE" >&2; exit 2; }

# Identity only: a resource is "the same" if kind and name match. Field-level changes are
# ordinary upgrades; a kind/name vanishing is what causes a delete.
render() {
  helm template vesta "$1" -n vesta-system 2>/dev/null \
    | awk '/^kind:/{k=$2} /^  name:/{if(k && !seen[k" "$2]++) print k" "$2}' \
    | sort -u
}

render "$work/vesta" > "$work/baseline.txt"
render "$CHART_DIR"  > "$work/current.txt"

dropped="$(comm -23 "$work/baseline.txt" "$work/current.txt" || true)"

if [ -n "$dropped" ]; then
  echo
  echo "FAIL: this chart no longer renders resources that $BASELINE did." >&2
  echo "Helm DELETES these on upgrade:" >&2
  echo "$dropped" | sed 's/^/  /' >&2
  echo >&2
  echo "If a removal is deliberate, keep rendering the resource and annotate it" >&2
  echo "  helm.sh/resource-policy: keep" >&2
  echo "so Helm leaves the existing object alone instead of deleting it." >&2
  exit 1
fi

echo "OK: every resource rendered by $BASELINE is still rendered ($(wc -l < "$work/baseline.txt" | tr -d ' ') checked)."
