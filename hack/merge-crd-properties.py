#!/usr/bin/env python3
"""Add newly generated properties to the shipped CRDs without tightening them.

The chart ships schemas that are deliberately more permissive than controller-gen output:
many Go fields lack omitempty, so generation marks them required, and shipping that made
existing VestaApps unappliable in 0.7.1. But the shipped schemas then go stale, and
Kubernetes prunes any field they lack -- silently. That is how per-environment TLS
settings vanished on save: the API wrote spec.environments[].ingress.clusterIssuer and
the API server dropped it, with no error anywhere.

This copies across every property the generated schema has and the shipped one lacks, and
strips any `required` list it would have brought with it. Adding a property is backward
compatible. Adding a requirement is a data migration.

  python3 hack/merge-crd-properties.py operator/config/crd/bases deploy/helm/vesta/crds
"""
import sys, os, copy, yaml

def strip_required(node):
    dropped = []
    def walk(n):
        if not isinstance(n, dict):
            return
        if isinstance(n.get("required"), list):
            dropped.extend(n.pop("required"))
        for v in (n.get("properties") or {}).values():
            walk(v)
        if "items" in n:
            walk(n["items"])
    walk(node)
    return dropped

def merge(old, new, path, added):
    if not isinstance(old, dict) or not isinstance(new, dict):
        return
    np = new.get("properties")
    if isinstance(np, dict):
        op = old.setdefault("properties", {})
        for k, v in np.items():
            if k not in op:
                op[k] = copy.deepcopy(v)
                strip_required(op[k])
                added.append(f"{path}.{k}")
            else:
                merge(op[k], v, f"{path}.{k}", added)
    if "items" in old and "items" in new:
        merge(old["items"], new["items"], path + "[]", added)

def main(src_dir, dst_dir):
    added = []
    copied = []

    # A CRD the chart does not have yet is copied wholesale, minus its `required` lists.
    # Iterating the destination alone silently skipped brand-new kinds, so adding one
    # produced "added 0 properties" and a chart that shipped without the CRD at all --
    # which surfaces much later, as the operator failing to watch a kind nobody installed.
    for f in sorted(os.listdir(src_dir)):
        if not f.endswith(".yaml") or os.path.exists(os.path.join(dst_dir, f)):
            continue
        doc = yaml.safe_load(open(os.path.join(src_dir, f)))
        strip_required(doc["spec"]["versions"][0]["schema"]["openAPIV3Schema"])
        yaml.safe_dump(doc, open(os.path.join(dst_dir, f), "w"),
                       default_flow_style=False, sort_keys=False)
        copied.append(f)

    for f in sorted(os.listdir(dst_dir)):
        if not f.endswith(".yaml"):
            continue
        src = os.path.join(src_dir, f)
        if not os.path.exists(src):
            continue
        if f in copied:
            continue
        dst = os.path.join(dst_dir, f)
        old, new = yaml.safe_load(open(dst)), yaml.safe_load(open(src))
        merge(old["spec"]["versions"][0]["schema"]["openAPIV3Schema"],
              new["spec"]["versions"][0]["schema"]["openAPIV3Schema"],
              f.replace("kubernetes.getvesta.sh_", "").replace(".yaml", ""), added)
        yaml.safe_dump(old, open(dst, "w"), default_flow_style=False, sort_keys=False)
    for c in copied:
        print(f"  copied new CRD {c}")
    print(f"added {len(added)} properties, copied {len(copied)} new CRDs")
    for a in added:
        print("  " + a)
    print("\nNow run: make check-crds")

if __name__ == "__main__":
    main(*(sys.argv[1:3] if len(sys.argv) > 2 else
           ("operator/config/crd/bases", "deploy/helm/vesta/crds")))
