#!/usr/bin/env bash
# Fail if a shipped CRD's enum is narrower than the Go types it is generated from.
#
# merge-crd-properties.py copies a property only when the key is ABSENT. It never touches a
# property that already exists, which means adding a value to an existing enum never reaches
# the chart. The property for a new feature arrives -- that key is new -- while the
# discriminator that selects it does not.
#
# That shipped. spec.openobserve and spec.forward were copied into the log drain CRD when
# they were added, so the config blocks validate; spec.type kept the six values it had, so:
#
#   VestaLogDrain "openobserve" is invalid: spec.type: Unsupported value: "openobserve":
#   supported values: "http", "loki", "syslog", "elasticsearch", "datadog", "s3"
#
# Nothing caught it. make generate produces the right enum, sync-crds reports success having
# copied the properties, check-crds compares against an earlier release rather than against
# the Go types, and the structural check only asks whether a type is declared.
#
# A chart enum that is MISSING ENTIRELY is not drift. The shipped schemas are deliberately
# more permissive than controller-gen's in places -- status.conditions[].status has no enum
# on purpose -- and adding one would tighten a schema against data already stored.
#
#   ./hack/check-crd-enums.sh
#
# Reads files only -- no cluster is contacted.
set -euo pipefail

BASES="${1:-operator/config/crd/bases}"
CHART="${2:-deploy/helm/vesta/crds}"

python3 - "$BASES" "$CHART" <<'PY'
import sys, pathlib, yaml

bases, chart = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])


def enums(node, path, out):
    if not isinstance(node, dict):
        return
    if isinstance(node.get("enum"), list):
        out[path] = node["enum"]
    for name, sub in (node.get("properties") or {}).items():
        enums(sub, f"{path}.{name}", out)
    if isinstance(node.get("items"), dict):
        enums(node["items"], path + "[]", out)


def collect(doc):
    out = {}
    for version in doc["spec"]["versions"]:
        schema = version.get("schema", {}).get("openAPIV3Schema")
        if schema:
            enums(schema, version["name"], out)
    return out


problems = []

for base_file in sorted(bases.glob("*.yaml")):
    chart_file = chart / base_file.name
    if not chart_file.exists():
        problems.append((base_file.name, "", "is not in the chart at all"))
        continue

    generated = collect(yaml.safe_load(base_file.read_text()))
    shipped = collect(yaml.safe_load(chart_file.read_text()))

    for path, values in generated.items():
        if path not in shipped:
            # Deliberately permissive: no enum accepts everything this one would.
            continue
        missing = [v for v in values if v not in shipped[path]]
        if missing:
            kind = base_file.name.replace("kubernetes.getvesta.sh_", "").replace(".yaml", "")
            problems.append((kind, path, "rejects " + ", ".join(repr(m) for m in missing)))

if problems:
    print("Shipped CRD enums are narrower than the Go types:", file=sys.stderr)
    for kind, path, why in problems:
        print(f"  {kind}: {path} {why}", file=sys.stderr)
    print("", file=sys.stderr)
    print("sync-crds cannot fix this: it only copies properties that are absent, and never", file=sys.stderr)
    print("edits one that already exists. Widen the enum in deploy/helm/vesta/crds by hand.", file=sys.stderr)
    sys.exit(1)

print("OK: every shipped CRD enum accepts everything the Go types can produce.")
PY
