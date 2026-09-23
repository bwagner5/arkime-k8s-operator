#!/usr/bin/env bash
# Bootstrap credentials for the optional getting-started OpenSearch installation.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

python3 -m venv _artifacts/getting-started/venv
_artifacts/getting-started/venv/bin/pip install 'PyYAML==6.0.2' 'bcrypt==4.3.0'
curl -fsSL https://raw.githubusercontent.com/opensearch-project/opensearch-k8s-operator/v2.8.0/opensearch-operator/examples/securityconfig-secret.yaml \
  -o _artifacts/getting-started/security-template.yaml
_artifacts/getting-started/venv/bin/python hack/setup-opensearch.py
