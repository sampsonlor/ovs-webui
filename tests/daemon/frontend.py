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
    code, _, _ = call('/roles', 'POST', {'request_id': request_id(), 'name': 'InterfaceObserver',
                                      'capabilities': ['state.read', 'inventory.read']})
    assert code == 202
    roles = {role['name']: role['id'] for role in get('/roles')['items']}
    code, _, _ = call('/roles', 'POST', {'request_id': request_id(), 'name': 'SpanningTreeDenied', 'capabilities': ['state.read']})
    assert code == 202
    roles.update({role['name']: role['id'] for role in get('/roles')['items']})
    accounts = {}
    # The deadline scenario has its own principal so rapid test logins/step-ups
    # respect the production per-account authentication budget.
    for name, role in [('browser-admin', 'Administrator'), ('browser-deadline', 'Administrator'),
                       ('browser-reader', 'Reader'), ('browser-auth-diagnostics', 'Reader'), ('browser-revoke', 'NetworkAdmin'),
                       ('browser-bridge', 'NetworkAdmin'), ('browser-bridge-delete', 'NetworkAdmin'),
                       ('browser-qinq', 'NetworkAdmin'), ('browser-interfaces', 'Reader'),
                       ('browser-interface-selection', 'Reader'), ('browser-interface-selection-observer', 'InterfaceObserver'),
                       ('browser-topology', 'NetworkAdmin'), ('browser-topology-reader', 'Reader'), ('browser-topology-observer', 'InterfaceObserver'), ('browser-mtu', 'NetworkAdmin'), ('browser-mtu-drift', 'NetworkAdmin'),
                       ('browser-tree', 'NetworkAdmin'), ('browser-tree-observer', 'InterfaceObserver'), ('browser-tree-exceptions', 'Reader'), ('browser-tree-outage', 'Reader'),
                       ('browser-tree-denied', 'SpanningTreeDenied'), ('browser-tree-parameters', 'NetworkAdmin'),
                       ('browser-mtu-reader', 'Reader'),
                       ('browser-mtu-defaults', 'NetworkAdmin'), ('browser-mtu-default-drift', 'NetworkAdmin'),
                       ('browser-interface-observer', 'InterfaceObserver'),
                       ('browser-linux-device', 'Reader'), ('browser-linux-exceptions', 'InterfaceObserver'),
                       ('browser-policing', 'Reader'), ('browser-policing-observer', 'InterfaceObserver'),
                       ('browser-policing-edit', 'NetworkAdmin'), ('browser-policing-drift', 'NetworkAdmin'),
                       ('browser-policing-reader', 'Reader'),
                       ('browser-native-types', 'Reader'), ('browser-patch-observer', 'InterfaceObserver'),
                       ('browser-evidence-reader', 'Reader'), ('browser-evidence-observer', 'InterfaceObserver'),
                       ('browser-interface-config', 'Reader'), ('browser-interface-config-observer', 'InterfaceObserver'),
                       ('browser-internal-port', 'NetworkAdmin'), ('browser-internal-delete', 'NetworkAdmin')]:
        secret = 'synthetic-browser-' + secrets.token_hex(16)
        credentials.append(secret)
        code, _, _ = call('/users', 'POST', {'request_id': request_id(), 'username': name, 'password': secret, 'role_ids': [roles[role]]})
        assert code == 202
        accounts[name] = secret
    vsctl('--', '--id=@dp', 'create', 'Datapath', 'external_ids:synthetic-qinq=fixture', '--', 'set', 'Open_vSwitch', '.', 'datapaths:dummy=@dp', 'other_config:vlan-limit=2')
    vsctl('set', 'Port', 'inv-p1', 'vlan_mode=access', 'tag=10', 'trunks=[]', 'cvlans=[]')
    vsctl('set', 'Port', 'inv-p2', 'vlan_mode=dot1q-tunnel', 'tag=200', 'cvlans=300,301', 'other_config:qinq-ethtype=unproven')
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
