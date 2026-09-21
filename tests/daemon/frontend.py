"""Real Svelte browser acceptance inside the disposable Go/OVS VM fixture."""
import json
import os
from pathlib import Path
import secrets
import subprocess

from authentication import request_id


def verify_frontend(repo, fixture, node, origin, password, call, get, vsctl, units,
                    db_socket, ovs, conf, credentials, network_metrics):
    assert node and Path(node).is_file(), 'an explicit CI Node build/test tool is required'
    assert call('/session/reauthentication', 'POST', {'password': password})[0] == 200
    roles = {role['name']: role['id'] for role in get('/roles')['items']}
    accounts = {}
    # The deadline scenario has its own principal so rapid test logins/step-ups
    # respect the production per-account authentication budget.
    for name, role in [('browser-admin', 'Administrator'), ('browser-deadline', 'Administrator'),
                       ('browser-reader', 'Reader'), ('browser-revoke', 'NetworkAdmin')]:
        secret = 'synthetic-browser-' + secrets.token_hex(16)
        credentials.append(secret)
        code, _, _ = call('/users', 'POST', {'request_id': request_id(), 'username': name, 'password': secret, 'role_ids': [roles[role]]})
        assert code == 202
        accounts[name] = secret
    vsctl('set', 'Port', 'inv-p1', 'vlan_mode=access', 'tag=10', 'trunks=[]', 'cvlans=[]')
    vsctl('set', 'Port', 'inv-p2', 'vlan_mode=dot1q-tunnel', 'tag=200', 'cvlans=300,301')
    metadata = {'origin': origin, 'accounts': accounts, 'units': units, 'dbSocket': str(db_socket),
                'ovsDirectory': str(ovs), 'database': str(conf)}
    path = fixture / 'browser-private.json'
    path.write_text(json.dumps(metadata))
    path.chmod(0o600)
    try:
        # The test runner prints test names and sanitized assertions only. No HAR,
        # network trace, storageState or fixture credentials are retained.
        env = dict(os.environ, OVS_FRONTEND_FIXTURE=str(path))
        result = subprocess.run([node, str(repo / 'node_modules/@playwright/test/cli.js'), 'test',
                                 '--config', str(repo / 'frontend/playwright.config.ts')],
                                cwd=repo, env=env, timeout=600)
        assert result.returncode == 0, 'formal frontend browser acceptance failed'
        return {'verified': True, 'network': network_metrics, 'backend': 'real ovs-webd / Unix IPC / ovs-mgrd / OVSDB / ovs-vswitchd',
                'report': 'test-results/frontend.xml', 'screenshots': 'test-results/frontend-evidence',
                'credentials_persisted_in_artifacts': False}
    finally:
        path.unlink(missing_ok=True)
