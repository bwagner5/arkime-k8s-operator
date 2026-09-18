#!/usr/bin/env bash
set -euo pipefail
root=$(pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/arkime-generate.XXXXXX")
trap 'rm -rf "$stage"' EXIT
tar --exclude=.git --exclude=bin --exclude=_artifacts --exclude=dist -cf - . | tar -xf - -C "$stage"
(cd "$stage"; "$root/bin/controller-gen" object crd rbac:roleName=arkime-k8s-operator paths=./... output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac; python3 hack/generate.py)
for path in api config charts docs/api.md docs/chart-values.md; do diff -ru "$root/$path" "$stage/$path"; done
