#!/usr/bin/env bash
# Exercise publication decisions without contacting any registry.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
stage=$(mktemp -d "${TMPDIR:-/tmp}/arkime-chart-test.XXXXXX")
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/local" "$stage/remote"
export MOCK_REMOTE="$stage/remote" MOCK_LOG="$stage/calls" MOCK_ERROR=''
cat >"$stage/helm" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$MOCK_LOG"
case "$1" in
  pull)
    if [[ -n "$MOCK_ERROR" ]]; then echo "$MOCK_ERROR" >&2; exit 1; fi
    name=${2##*/}; file="$name-$4.tgz"
    if [[ ! -f "$MOCK_REMOTE/$file" ]]; then echo "Error: $2:$4: not found" >&2; exit 1; fi
    cp "$MOCK_REMOTE/$file" "$6/$file"
    ;;
  push) cp "$2" "$MOCK_REMOTE/${2##*/}" ;;
  *) exit 1 ;;
esac
MOCK
chmod +x "$stage/helm"
export HELM="$stage/helm"
for name in arkime-k8s-operator arkime-k8s-operator-crds; do
  printf '%s\n' "$name" >"$stage/local/$name-1.2.3.tgz"
done
(cd "$stage/local"; shasum -a 256 ./*.tgz | sed 's|  ./|  |') >"$stage/checksums"
run() { bash "$root/hack/publish-charts.sh" 1.2.3 "$stage/local" "$stage/checksums" oci://example.invalid/charts >"$stage/output" 2>&1; }
no_push() { ! grep -q '^push ' "$MOCK_LOG"; }
: >"$MOCK_LOG"
run || { cat "$stage/output"; exit 1; }
[[ $(grep -c '^push ' "$MOCK_LOG") == 2 ]]
: >"$MOCK_LOG"
run || { cat "$stage/output"; exit 1; }
no_push
# A differing second chart must prevent publication of a missing first chart.
rm "$MOCK_REMOTE/arkime-k8s-operator-1.2.3.tgz"
printf 'different' >"$MOCK_REMOTE/arkime-k8s-operator-crds-1.2.3.tgz"
: >"$MOCK_LOG"
if run; then echo 'Accepted conflicting chart' >&2; exit 1; fi
no_push
grep -q 'Refusing to overwrite' "$stage/output"
for message in 'Error: unauthorized: not found' 'Error: request timeout'; do
  export MOCK_ERROR="$message"
  : >"$MOCK_LOG"
  if run; then echo 'Accepted registry error' >&2; exit 1; fi
  no_push
done
export MOCK_ERROR=''
printf 'tampered' >"$stage/local/arkime-k8s-operator-1.2.3.tgz"
: >"$MOCK_LOG"
if run; then echo 'Accepted checksum mismatch' >&2; exit 1; fi
[[ ! -s "$MOCK_LOG" ]]
grep -q 'Checksum mismatch' "$stage/output"
echo 'Chart publication recovery tests passed'
