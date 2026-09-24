#!/usr/bin/env bash
# Stage generated sources into charts and derive field/value references.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

# The operator chart ships the same generated CRD behind crds.enabled, as a
# non-templated file so the CRD text stays byte-identical to the CRD chart's.
mkdir -p charts/arkime-k8s-operator/files
cp charts/arkime-k8s-operator-crds/templates/arkime.arkime.com_arkimeclusters.yaml \
  charts/arkime-k8s-operator/files/arkime.arkime.com_arkimeclusters.yaml

sed 's|^  name: arkime-k8s-operator$|  name: {{ include "operator.name" . }}-{{ .Release.Namespace }}|' \
  config/rbac/role.yaml >charts/arkime-k8s-operator/templates/role.yaml

# Each struct in types.go becomes a table of its json tags. Fields whose name is
# qualified (metav1.ObjectMeta) are skipped; embedded inline ones get a link row.
awk '
BEGIN {
  print "# API reference (generated)\n"
  print "API: `arkime.arkime.com/v1alpha1`, namespaced `ArkimeCluster`.\n"
  print "Generated from Go API types. See the structural CRD for defaults and validation.\n"
}
/^type [A-Za-z0-9_]+ struct \{/ {
  if (nstruct++) print ""
  print "## " $2 "\n"
  print "| JSON field | Go type |"
  print "| --- | --- |"
  ninline = 0
  instruct = 1
  next
}
instruct && /^\}/ {
  for (i = 0; i < ninline; i++)
    print "| inline fields | [" inline[i] "](#" tolower(inline[i]) ") |"
  instruct = 0
  next
}
instruct {
  bt = index($0, "`")
  if (bt == 0) next
  head = substr($0, 1, bt - 1)
  tag = substr($0, bt + 1)
  if (tag !~ /^json:"/) next
  sub(/^json:"/, "", tag)
  name = match(tag, /^[^",]+/) ? substr(tag, 1, RLENGTH) : ""
  if (name == "") {
    if (tag ~ /^,inline"`/ && head ~ /^[ \t]*[A-Za-z0-9_]+[ \t]+$/) {
      gsub(/[ \t]/, "", head)
      inline[ninline++] = head
    }
    next
  }
  if (!match(head, /^[ \t]*[A-Za-z0-9_]+[ \t]+/)) next
  type = substr(head, RLENGTH + 1)
  gsub(/^[ \t]+|[ \t]+$/, "", type)
  if (type == "") next
  print "| `" name "` | `" type "` |"
}
' api/v1alpha1/types.go >docs/api.md

{
  printf '# Operator chart values (generated)\n\n```yaml\n'
  cat charts/arkime-k8s-operator/values.yaml
  printf '```\n'
} >docs/chart-values.md
