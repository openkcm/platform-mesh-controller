#!/usr/bin/env python3
"""
Regenerate charts/openkcm-pm-integration/templates/api-resource-schemas.yaml
from the controller-gen'd CRDs under config/crd/bases/.

Each APIResourceSchema is named with a chart-version-derived prefix
(``v<version-dotted-to-dashed>-v1alpha1.<plural>.<group>``) so KCP's
schema-immutability rule doesn't block chart upgrades that change the
underlying CRD schema.
"""

from __future__ import annotations

import io
from pathlib import Path

import yaml


REPO_ROOT = Path(__file__).resolve().parent.parent
CRD_DIR = REPO_ROOT / "config" / "crd" / "bases"
OUT_FILE = REPO_ROOT / "charts" / "openkcm-pm-integration" / "templates" / "api-resource-schemas.yaml"

# Order matters so the generated file is reviewable as a diff over time.
KIND_ORDER = [
    "awsrootkeys",
    "azurerootkeys",
    "openbaorootkeys",
    "gcprootkeys",
    "vaultrootkeys",
    "hsmrootkeys",
    "domainkeys",
    "servicekeys",
    "dataencryptionkeys",
    "tenants",
]

# The double-curly Go template syntax must survive Python interpolation, so
# we use plain placeholders rather than str.format() (which would eat the
# `{{`/`}}` as literal-brace escapes).
CHART_VERSION_NAME_TPL = (
    '{{ printf "v%s-v1alpha1" (.Chart.Version | replace "." "-") }}.__PLURAL__.__GROUP__'
)


def main() -> None:
    documents: list[dict] = []
    for plural in KIND_ORDER:
        crd_path = CRD_DIR / f"operations.openkcm.io_{plural}.yaml"
        if not crd_path.exists():
            print(f"skip: {crd_path} does not exist")
            continue
        with crd_path.open() as f:
            crd = yaml.safe_load(f)

        if crd.get("kind") != "CustomResourceDefinition":
            raise RuntimeError(f"{crd_path}: not a CustomResourceDefinition")

        spec = crd["spec"]
        group = spec["group"]
        names = spec["names"]

        # APIResourceSchema's spec.versions[].schema is the OpenAPI schema
        # itself; a CustomResourceDefinition nests it one level deeper inside
        # spec.versions[].schema.openAPIV3Schema. Unwrap so KCP doesn't
        # reject the resulting object with "type: Required value: must not
        # be empty at the root".
        versions = []
        for v in spec["versions"]:
            v_out = dict(v)
            if "schema" in v_out and isinstance(v_out["schema"], dict):
                sch = v_out["schema"]
                if "openAPIV3Schema" in sch:
                    v_out["schema"] = sch["openAPIV3Schema"]
            versions.append(v_out)

        documents.append(
            {
                "apiVersion": "apis.kcp.io/v1alpha1",
                "kind": "APIResourceSchema",
                "metadata": {
                    "name": (
                        CHART_VERSION_NAME_TPL.replace("__PLURAL__", plural).replace("__GROUP__", group)
                    ),
                },
                "spec": {
                    "group": group,
                    "names": names,
                    "scope": spec["scope"],
                    "versions": versions,
                },
            }
        )

    out = io.StringIO()
    out.write(
        "# THIS FILE IS GENERATED. Do not edit by hand.\n"
        "# Source: config/crd/bases/operations.openkcm.io_*.yaml\n"
        "# Regenerate: python3 hack/sync-api-resource-schemas.py\n"
    )
    for i, doc in enumerate(documents):
        if i > 0:
            out.write("---\n")
        yaml.safe_dump(doc, out, default_flow_style=False, sort_keys=False, width=1000)

    OUT_FILE.write_text(out.getvalue())
    print(f"wrote {OUT_FILE} ({len(documents)} schemas)")


if __name__ == "__main__":
    main()
