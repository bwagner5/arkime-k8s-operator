#!/usr/bin/env python3
"""Stage generated sources into charts and derive field/value references."""
import json
from pathlib import Path
root = Path(__file__).resolve().parents[1]
for p in (root/'config/crd/bases').glob('*.yaml'):
    text = p.read_text().replace('  annotations:\n', '  annotations:\n    helm.sh/resource-policy: keep\n', 1)
    (root/'charts/arkime-k8s-operator-crds/templates'/p.name).write_text(text)
p = root/'config/rbac/role.yaml'
text = p.read_text().replace('  name: arkime-k8s-operator', '  name: {{ include "operator.name" . }}-{{ .Release.Namespace }}')
(root/'charts/arkime-k8s-operator/templates/role.yaml').write_text(text)
import re
api = (root/'api/v1alpha1/types.go').read_text()
lines = ['# API reference (generated)', '', 'API: `arkime.arkime.com/v1alpha1`, namespaced `ArkimeCluster`.', '', 'Generated from Go API types. See the structural CRD for defaults and validation.', '']
for name, body in re.findall(r'type (\w+) struct \{(.*?)\n\}', api, re.S):
    lines += ['## '+name, '', '| JSON field | Go type |', '| --- | --- |']
    for typ, tag in re.findall(r'^[ \t]*\w+[ \t]+([^`\n]+)`json:"([^",]+)(?:,[^"]*)?"`', body, re.M):
        lines.append('| `'+tag+'` | `'+typ.strip()+'` |')
    for embedded in re.findall(r'^[ \t]*(\w+)[ \t]+`json:",inline"`', body, re.M):
        lines.append('| inline fields | ['+embedded+'](#'+embedded.lower()+') |')
    lines.append('')
(root/'docs/api.md').write_text('\n'.join(lines))
(root/'docs/chart-values.md').write_text('# Operator chart values (generated)\n\n```yaml\n'+(root/'charts/arkime-k8s-operator/values.yaml').read_text()+'```\n')
