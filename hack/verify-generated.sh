#!/usr/bin/env bash
set -euo pipefail
root=$(pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/arkime-generate.XXXXXX")
trap 'rm -rf "$stage"' EXIT
tar --exclude=.git --exclude=bin --exclude=_artifacts --exclude=dist -cf - . | tar -xf - -C "$stage"
(cd "$stage"; "${MAKE:-make}" generate GO="${GO:-go}")
for path in api config charts docs/api.md docs/chart-values.md; do diff -ru "$root/$path" "$stage/$path"; done
