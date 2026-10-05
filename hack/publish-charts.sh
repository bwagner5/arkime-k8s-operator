#!/usr/bin/env bash
# Publish the exact release archives, skipping only byte-identical existing charts.
set -euo pipefail
version=${1:?version required}
chart_dir=${2:?chart directory required}
checksums=${3:?checksum file required}
repository=${4:?OCI repository required}
helm=${HELM:-helm}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo 'Invalid release version' >&2; exit 1; }
[[ "$repository" == oci://* ]] || { echo 'Expected an OCI repository' >&2; exit 1; }
charts=(arkime-k8s-operator arkime-k8s-operator-crds)
# Validate all inputs before writing any remote artifact.
for name in "${charts[@]}"; do
  file="$name-$version.tgz"
  [[ -f "$chart_dir/$file" ]] || { echo "Missing $chart_dir/$file" >&2; exit 1; }
  expected=$(awk -v file="$file" '$2 == file {print $1}' "$checksums")
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { echo "Missing or duplicate checksum for $file" >&2; exit 1; }
  actual=$(shasum -a 256 "$chart_dir/$file" | awk '{print $1}')
  [[ "$actual" == "$expected" ]] || { echo "Checksum mismatch: $file" >&2; exit 1; }
done
stage=$(mktemp -d "${TMPDIR:-/tmp}/arkime-chart-publish.XXXXXX")
trap 'rm -rf "$stage"' EXIT
missing=()
# Inspect both destinations before pushing either one. Permission/network errors
# are not evidence of absence and must never cause an overwrite attempt.
for name in "${charts[@]}"; do
  file="$name-$version.tgz"
  if "$helm" pull "$repository/$name" --version "$version" --destination "$stage" >"$stage/pull.log" 2>&1; then
    cmp -s "$chart_dir/$file" "$stage/$file" || { echo "Refusing to overwrite different chart: $name:$version" >&2; exit 1; }
    echo "Already published, identical: $name:$version"
  elif grep -Eqi '(unauthorized|forbidden|denied)' "$stage/pull.log"; then
    cat "$stage/pull.log" >&2; exit 1
  elif grep -Eqi '(: not found|manifest unknown|MANIFEST_UNKNOWN|NAME_UNKNOWN)' "$stage/pull.log"; then
    missing+=("$name")
  else
    cat "$stage/pull.log" >&2; exit 1
  fi
done
for name in "${missing[@]}"; do
  echo "Publishing $name:$version"
  "$helm" push "$chart_dir/$name-$version.tgz" "$repository"
  "$helm" pull "$repository/$name" --version "$version" --destination "$stage"
  cmp "$chart_dir/$name-$version.tgz" "$stage/$name-$version.tgz"
done
