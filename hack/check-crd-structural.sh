#!/usr/bin/env bash
# Fail if a shipped CRD would be rejected by the API server.
#
# Kubernetes requires a structural schema: every property that is specified must say what
# type it is. A property written as an empty object is not a permissive schema, it is an
# invalid one, and the API server refuses the ENTIRE CustomResourceDefinition:
#
#   The CustomResourceDefinition "vestaconfigs.kubernetes.getvesta.sh" is invalid:
#   * spec.validation.openAPIV3Schema.properties[spec].properties[cost].properties[cpuCoreHour].type:
#     Required value: must not be empty for specified object fields
#
# Nothing else catches it. `helm lint` does not validate CRD schemas, `helm template` renders
# the file happily, and the chart installs right up until the CRDs are applied -- at which
# point every kind in that file fails, not just the offending property.
#
# 0.10.0 shipped exactly this. While `make generate` was failing on the cost rate card's
# float fields, controller-gen still wrote a partial vestaconfigs CRD whose cost properties
# carried no type, and merge-crd-properties.py copied them across as written.
#
#   ./hack/check-crd-structural.sh deploy/helm/vesta/crds
#
# Reads files only -- no cluster is contacted.
set -euo pipefail

DIR="${1:-deploy/helm/vesta/crds}"
[ -d "$DIR" ] || { echo "not a directory: $DIR" >&2; exit 2; }

python3 - "$DIR" <<'PY'
import sys, pathlib, yaml

# A property is well-formed if it says what it is, one way or another.
TYPE_KEYS = ("type", "x-kubernetes-preserve-unknown-fields", "x-kubernetes-int-or-string",
             "$ref", "allOf", "oneOf", "anyOf")

problems = []


def walk(node, path):
    if not isinstance(node, dict):
        return

    props = node.get("properties")
    if isinstance(props, dict):
        # A schema cannot both enumerate properties and accept arbitrary ones.
        if isinstance(node.get("additionalProperties"), dict):
            problems.append((path, "declares both properties and additionalProperties"))
        for name, sub in props.items():
            child = f"{path}.{name}"
            if not isinstance(sub, dict):
                problems.append((child, "is not a schema object"))
                continue
            if not any(k in sub for k in TYPE_KEYS):
                problems.append((child, "has no type"))
            elif sub.get("type") == "":
                problems.append((child, "has an empty type"))
            walk(sub, child)

    for key in ("items", "additionalProperties"):
        if isinstance(node.get(key), dict):
            walk(node[key], f"{path}[]" if key == "items" else f"{path}{{}}")


for f in sorted(pathlib.Path(sys.argv[1]).glob("*.yaml")):
    doc = yaml.safe_load(f.read_text())
    if not doc or doc.get("kind") != "CustomResourceDefinition":
        continue
    kind = doc["metadata"]["name"]
    for version in doc["spec"]["versions"]:
        schema = version.get("schema", {}).get("openAPIV3Schema")
        if schema:
            walk(schema, f"{kind}/{version['name']}")

if problems:
    print("Invalid CRD schemas -- the API server will reject these files:", file=sys.stderr)
    for path, why in problems:
        print(f"  {path}: {why}", file=sys.stderr)
    print("", file=sys.stderr)
    print("Run 'make generate && make sync-crds'. If a property is still bare afterwards,", file=sys.stderr)
    print("controller-gen could not describe it -- check that `make generate` succeeded", file=sys.stderr)
    print("rather than failing partway and leaving half a schema behind.", file=sys.stderr)
    sys.exit(1)

print("OK: every property in every shipped CRD declares a type.")
PY
