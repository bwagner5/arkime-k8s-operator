#!/usr/bin/env python3
"""Create the getting-started OpenSearch and Arkime credential Secrets."""
import json, secrets, subprocess
from pathlib import Path
import bcrypt, yaml
password = secrets.token_urlsafe(32) + 'Aa1!'
security = yaml.safe_load(Path('_artifacts/getting-started/security-template.yaml').read_text())
security['metadata'] = {'name': 'opensearch-security-config', 'namespace': 'arkime'}
security['stringData']['internal_users.yml'] = yaml.safe_dump({
    '_meta': {'type': 'internalusers', 'config_version': 2},
    'admin': {'hash': bcrypt.hashpw(password.encode(), bcrypt.gensalt(12, prefix=b'2a')).decode(),
              'reserved': True, 'backend_roles': ['admin']},
})
def secret(name, data):
    return {'apiVersion': 'v1', 'kind': 'Secret',
            'metadata': {'name': name, 'namespace': 'arkime'},
            'type': 'Opaque', 'stringData': data}
for obj in [security,
            secret('opensearch-admin-credentials', {'username': 'admin', 'password': password}),
            secret('arkime-database', {'basicAuth': 'admin:' + password})]:
    subprocess.run(['kubectl', 'apply', '-f', '-'], input=json.dumps(obj), text=True, check=True)
