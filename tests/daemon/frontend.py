"""Real Svelte browser acceptance inside the disposable Go/OVS VM fixture."""
import json
import os
from pathlib import Path
import secrets
import subprocess

from authentication import request_id, run
from spanning_tree_prepare import reconfigure_draft_fixture


def verify_frontend(repo, fixture, node, origin, password, call, get, vsctl, units,
                    db_socket, ovs, conf, manager_db, credentials, network_metrics, eventually):
    assert node and Path(node).is_file(), 'an explicit CI Node build/test tool is required'
    assert call('/session/reauthentication', 'POST', {'password': password})[0] == 200
    code, _, _ = call('/roles', 'POST', {'request_id': request_id(), 'name': 'InterfaceObserver',
                                      'capabilities': ['state.read', 'inventory.read']})
    assert code == 202
    roles = {role['name']: role['id'] for role in get('/roles')['items']}
    network_role = next(role for role in get('/roles')['items'] if role['name']=='NetworkAdmin')
    code, _, _ = call('/roles','POST',{'request_id':request_id(),'name':'NativeIsolation','capabilities':network_role['capabilities']})
    assert code == 202
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
                       ('browser-tree-draft', 'NetworkAdmin'),
                       ('browser-isolation', 'NativeIsolation'), ('browser-isolation-reader', 'Reader'),
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
    # A separate root grant permits preparation only. Other native execution
    # grants and all production authentication/recovery budgets are preserved.
    vsctl('--no-wait', 'add-br', 'br-tree-plan', '--', 'set', 'Bridge', 'br-tree-plan', 'datapath_type=system')
    tree = eventually(lambda: next((b for b in get('/inventory/spanning-tree?filter=br-tree-plan')['items'] if b['name'] == 'br-tree-plan'), None))
    vsctl('add-br','br-iso-ui','--','set','Bridge','br-iso-ui','datapath_type=system','--','add-port','br-iso-ui','iso-ui-data','--','set','Interface','iso-ui-data','type=internal')
    isolation = eventually(lambda: next((p for p in get('/ports?filter=iso-ui-data')['items'] if p['name']=='iso-ui-data'),None))
    unit_path = Path('/etc/systemd/system') / units['mgrd']
    unit_original = unit_path.read_text()
    needle = '--database=${MANAGER_DATABASE}'
    assert needle in unit_original and '--local-spanning-tree-bridges' not in unit_original
    # The reserved capability cannot be delegated by an HTTP caller who does
    # not hold it. Provision this disposable role only through the root CLI.
    assert units['mgrd'].startswith('ovs-mgrd-inventory-')
    run('systemctl','is-active','--quiet',units['mgrd'])
    run('systemctl','stop',units['mgrd'])
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-port-isolation-role',roles['NativeIsolation'])
    unit_path.write_text(unit_original.replace(needle, needle + ' --local-spanning-tree-bridges=' + tree['management_id']+' --local-isolation-ports='+isolation['management_id']))
    run('systemctl','daemon-reload')
    run('systemctl','reset-failed',units['mgrd'])
    run('systemctl','start',units['mgrd'])
    eventually(lambda: call('/inventory')[0] == 200)
    metadata = {'origin': origin, 'accounts': accounts, 'units': units, 'dbSocket': str(db_socket),
                'ovsDirectory': str(ovs), 'database': str(conf), 'isolation': isolation}
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
        reconfigure_draft_fixture(unit_path, units['mgrd'], unit_original)
        vsctl('--no-wait', '--if-exists', 'del-br', 'br-tree-plan')
        vsctl('--no-wait', '--if-exists', 'del-br', 'br-iso-ui')
