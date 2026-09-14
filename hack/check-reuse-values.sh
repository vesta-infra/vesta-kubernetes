#!/usr/bin/env bash
# Fail if `helm upgrade --reuse-values` would not render this chart.
#
# --reuse-values does NOT layer the new chart's defaults over the previous release's values.
# It uses the previous release's values alone. So every key introduced in a new chart version
# is simply absent during such an upgrade, and a template that reads it as
# `.Values.newthing.enabled` fails with:
#
#     nil pointer evaluating interface {}.enabled
#
# Nothing catches this before it happens. The chart renders on a fresh install, `helm lint`
# passes, CI passes, and the check-no-dropped-resources comparison passes -- because all of
# them render with the chart's own values.yaml. It breaks only for somebody upgrading a
# running instance, which is where 0.10.0 shipped a reference to a brand new `activator` key.
#
# This reproduces the failure exactly: take the new chart's templates, replace values.yaml
# with a published release's, and render.
#
#   ./hack/check-reuse-values.sh 0.6.3 0.9.1
#
# Renders locally only -- no cluster is contacted.
set -euo pipefail

CHART_DIR="${CHART_DIR:-deploy/helm/vesta}"
[ $# -gt 0 ] || { echo "usage: $0 <baseline-chart-version>..." >&2; exit 2; }

command -v helm >/dev/null || { echo "helm is required" >&2; exit 2; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

failed=0

for baseline in "$@"; do
  echo "Checking --reuse-values upgrade from ${baseline}..."

  if ! helm pull "oci://ghcr.io/vesta-infra/charts/vesta" --version "$baseline" \
       --untar --untardir "$work/$baseline" >/dev/null 2>&1; then
    echo "  could not pull chart $baseline; skipping" >&2
    continue
  fi

  # The new chart's templates, with only the old release's values. That is what the upgrade
  # actually evaluates.
  rm -rf "$work/candidate-$baseline"
  cp -r "$CHART_DIR" "$work/candidate-$baseline"
  cp "$work/$baseline/vesta/values.yaml" "$work/candidate-$baseline/values.yaml"

  if err="$(helm template vesta "$work/candidate-$baseline" -n vesta-system 2>&1 >/dev/null)"; then
    echo "  OK"
  else
    failed=1
    echo "  FAIL: this chart does not render with ${baseline}'s values." >&2
    echo "$err" | sed 's/^/    /' >&2
    echo >&2
    echo "    A key added since ${baseline} is being read directly. On an upgrade with" >&2
    echo "    --reuse-values that key does not exist, so the upgrade fails here rather" >&2
    echo "    than on a fresh install. Read it defensively instead -- see" >&2
    echo "    vesta.enabled and vesta.value in templates/_helpers.tpl for the pattern," >&2
    echo "    including why '| default true' is wrong for a boolean." >&2
  fi
done

[ "$failed" -eq 0 ] || exit 1
echo "OK: the chart renders with every baseline's values, so --reuse-values upgrades work."
