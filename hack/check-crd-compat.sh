#!/usr/bin/env bash
# Fail if the chart's CRDs would reject data an earlier release accepted.
#
# The CRDs in this chart are deliberately more permissive than what controller-gen
# produces from types.go: many Go fields lack `omitempty`, so generation marks them
# required, while the shipped schemas never did. Regenerating them in 0.7.0 made 26
# groups of fields newly required and made existing VestaApps unappliable -- an app in
# `Sleeping` state has spec.sleep without `enabled`, and the API server rejects it.
#
# Tightening a schema is a data migration, not a refactor. This check makes that visible
# before publish instead of at restore time.
#
#   ./hack/check-crd-compat.sh 0.6.3
#
# Renders locally only -- no cluster is contacted.
set -euo pipefail

BASELINE="${1:-0.6.3}"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT

helm pull "oci://ghcr.io/vesta-infra/charts/vesta" --version "$BASELINE" \
  --untar --untardir "$work" >/dev/null 2>&1 \
  || { echo "could not pull baseline $BASELINE" >&2; exit 2; }

python3 - "$work/vesta/crds" "deploy/helm/vesta/crds" "$BASELINE" <<'PY'
import sys, os, glob, yaml

old_dir, new_dir, baseline = sys.argv[1], sys.argv[2], sys.argv[3]

def required_paths(path):
    doc = yaml.safe_load(open(path))
    found = {}
    def walk(node, p):
        if not isinstance(node, dict): return
        if isinstance(node.get("required"), list):
            found[p] = set(node["required"])
        for k, v in (node.get("properties") or {}).items():
            walk(v, f"{p}.{k}")
        if "items" in node:
            walk(node["items"], p + "[]")
    walk(doc["spec"]["versions"][0]["schema"]["openAPIV3Schema"], "")
    return found

problems = []
for new_path in sorted(glob.glob(os.path.join(new_dir, "*.yaml"))):
    old_path = os.path.join(old_dir, os.path.basename(new_path))
    if not os.path.exists(old_path):
        continue   # a brand-new CRD constrains no existing data
    old, new = required_paths(old_path), required_paths(new_path)
    for p, req in sorted(new.items()):
        # status is server-written; tightening it cannot reject a user's apply.
        if p.startswith(".status"): continue
        added = req - old.get(p, set())
        if added:
            problems.append(f"{os.path.basename(new_path)}{p}: now requires {sorted(added)}")

if problems:
    print(f"\nFAIL: these CRDs would reject objects that {baseline} accepted:", file=sys.stderr)
    for p in problems: print("  " + p, file=sys.stderr)
    print("\nA field that was optional must stay optional, or every stored object", file=sys.stderr)
    print("without it becomes unappliable. Add `omitempty` to the Go field, or ship", file=sys.stderr)
    print("a migration alongside the change.", file=sys.stderr)
    sys.exit(1)

print(f"OK: no field became required that {baseline} allowed to be absent.")
PY
