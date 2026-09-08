#!/usr/bin/env bash
# Fail if the self-update ServiceAccount cannot apply everything the chart renders.
#
# The upgrade Job runs `helm upgrade` as vesta-upgrader, so that identity needs create,
# update, patch and delete on every kind in the chart -- delete included, because hooks
# annotated helm.sh/hook-delete-policy: before-hook-creation are removed and recreated
# each run. 0.7.3 shipped without delete on RBAC resources and without namespaces at all,
# and the upgrade failed in the pre-upgrade hook with a forbidden error.
#
# Nothing catches this at render time: the chart is valid, the role is valid, and the
# mismatch only appears when a real upgrade runs as that account.
#
# Renders locally only -- no cluster is contacted.
set -euo pipefail

CHART_DIR="${CHART_DIR:-deploy/helm/vesta}"
command -v helm >/dev/null || { echo "helm is required" >&2; exit 2; }

helm template vesta "$CHART_DIR" -n vesta-system \
  --set selfUpdate.enabled=true --set postgres.enabled=true 2>/dev/null \
| python3 -c '
import sys, yaml

# Kind -> (apiGroup, plural). Only kinds this chart actually renders.
KINDS = {
    "Deployment":               ("apps", "deployments"),
    "StatefulSet":              ("apps", "statefulsets"),
    "Job":                      ("batch", "jobs"),
    "ConfigMap":                ("", "configmaps"),
    "Secret":                   ("", "secrets"),
    "Service":                  ("", "services"),
    "ServiceAccount":           ("", "serviceaccounts"),
    "Namespace":                ("", "namespaces"),
    "PersistentVolumeClaim":    ("", "persistentvolumeclaims"),
    "Ingress":                  ("networking.k8s.io", "ingresses"),
    "ClusterRole":              ("rbac.authorization.k8s.io", "clusterroles"),
    "ClusterRoleBinding":       ("rbac.authorization.k8s.io", "clusterrolebindings"),
    "Role":                     ("rbac.authorization.k8s.io", "roles"),
    "RoleBinding":              ("rbac.authorization.k8s.io", "rolebindings"),
    "CustomResourceDefinition": ("apiextensions.k8s.io", "customresourcedefinitions"),
    "VestaConfig":              ("kubernetes.getvesta.sh", "vestaconfigs"),
}

# Everything the chart owns is created and recreated, so it needs the full set -- except
# these, which hold state an upgrade has no business removing. A Namespace takes every
# Secret and PVC inside it; a VestaConfig holds the instance settings the dashboard
# writes, such as the default cluster issuer. Both stay rendered by the chart, so Helm
# never needs delete on them anyway.
NEEDED = {"create", "update", "patch", "delete"}
NO_DELETE = {"Namespace", "VestaConfig"}

docs = [d for d in yaml.safe_load_all(sys.stdin) if d and d.get("kind")]

rendered, role = set(), None
for d in docs:
    rendered.add(d["kind"])
    if d["kind"] == "ClusterRole" and "upgrader" in d["metadata"]["name"]:
        role = d

if role is None:
    print("FAIL: the chart renders no upgrader ClusterRole", file=sys.stderr); sys.exit(1)

allowed = {}
for rule in role["rules"]:
    for g in rule["apiGroups"]:
        for r in rule["resources"]:
            allowed.setdefault((g, r), set()).update(rule["verbs"])

problems, unknown = [], []
for kind in sorted(rendered):
    if kind not in KINDS:
        unknown.append(kind); continue
    key = KINDS[kind]
    need = NEEDED - ({"delete"} if kind in NO_DELETE else set())
    have = allowed.get(key, set())
    if "*" in have: continue
    missing = need - have
    if missing:
        group = key[0] or "core"
        problems.append(f"{kind} ({group}/{key[1]}): missing {sorted(missing)}")

if unknown:
    print("FAIL: the chart renders kinds this check does not know about:", file=sys.stderr)
    for k in unknown: print("  " + k, file=sys.stderr)
    print("\nAdd them to KINDS in hack/check-upgrader-rbac.sh and grant them in", file=sys.stderr)
    print("templates/upgrader-rbac.yaml, or self-update will fail on them.", file=sys.stderr)
    sys.exit(1)

if problems:
    print("FAIL: vesta-upgrader cannot apply everything the chart renders:", file=sys.stderr)
    for p in problems: print("  " + p, file=sys.stderr)
    print("\nGrant the missing verbs in templates/upgrader-rbac.yaml.", file=sys.stderr)
    sys.exit(1)

print(f"OK: vesta-upgrader can apply all {len(rendered)} kinds the chart renders.")
'
