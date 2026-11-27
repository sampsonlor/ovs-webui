"""Real native FDB browser acceptance in the opt-in OVS witness CI fixture."""
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess

from authentication import request_id, run
from spanning_tree_prepare import reconfigure_draft_fixture


def prepare_observation_links(vsctl, get, eventually, cleanup, bridge_name, other_name,
                              namespace, port_specs):
    bridges = {}
    for name in (bridge_name, other_name):
        vsctl('add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=system',
              'fail_mode=standalone', 'other_config:mac-aging-time=120',
              'other_config:mac-table-size=512')
        cleanup.callback(vsctl, '--no-wait', '--if-exists', 'del-br', name)
        run('sysctl', '-q', '-w', f'net.ipv6.conf.{name}.disable_ipv6=1')
        binding = eventually(lambda: next((row for row in get('/bridges?filter=' + name)['items']
                                           if row['name'] == name), None))
        bridges[name] = {'name': name, 'id': binding['management_id']}

    run('ip', 'netns', 'add', namespace)
    cleanup.callback(run, 'ip', 'netns', 'delete', namespace, check=False)
    ports = []
    for name, local, peer, vlan in port_specs:
        run('ip', 'link', 'add', local, 'type', 'veth', 'peer', 'name', peer)
        cleanup.callback(run, 'ip', 'link', 'del', local, check=False)
        run('ip', 'link', 'set', 'dev', peer, 'netns', namespace)
        run('sysctl', '-q', '-w', f'net.ipv6.conf.{local}.disable_ipv6=1')
        run('ip', 'netns', 'exec', namespace, 'sysctl', '-q', '-w', f'net.ipv6.conf.{peer}.disable_ipv6=1')
        vsctl('add-port', name, local, '--', 'set', 'Port', local, 'vlan_mode=access', f'tag={vlan}')
        run('ip', 'link', 'set', 'dev', local, 'up')
        run('ip', '-n', namespace, 'link', 'set', 'dev', peer, 'up')
        binding = eventually(lambda: next((row for row in get('/ports?filter=' + local)['items']
                                           if row['name'] == local and row.get('bridge_ref')), None))
        assert binding['bridge_ref']['id'] == bridges[name]['id']
        ports.append({'name': local, 'peer': peer, 'id': binding['management_id'],
                      'bridge': bridges[name], 'vlan': vlan})
    return {'bridge': bridges[bridge_name], 'otherBridge': bridges[other_name],
            'namespace': namespace, 'ports': ports}


def prepare_observer_accounts(password, call, get, credentials):
    assert call('/session/reauthentication', 'POST', {'password': password})[0] == 200
    accounts = {}
    for suffix, caps in (
        ('observer', ['state.read', 'inventory.read']),
        ('inventory-only', ['inventory.read']),
        ('state-only', ['state.read']),
    ):
        role = 'FDB-' + suffix
        assert call('/roles', 'POST', {'request_id': request_id(), 'name': role, 'capabilities': caps})[0] == 202
        role_id = next(row['id'] for row in get('/roles')['items'] if row['name'] == role)
        username = 'browser-fdb-' + suffix
        secret = 'synthetic-fdb-browser-' + secrets.token_hex(16)
        credentials.append(secret)
        assert call('/users', 'POST', {'request_id': request_id(), 'username': username,
                                      'password': secret, 'role_ids': [role_id]})[0] == 202
        accounts[username] = secret

    return accounts


def enable_fdb_scope(units, ovs, cleanup, bridge_ids, lldp_interface=None):
    unit_path = Path('/etc/systemd/system') / units['mgrd']
    original = unit_path.read_text()
    needle = '--database=${MANAGER_DATABASE}'
    caps = 'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n'
    assert needle in original and caps in original and '--fdb-' not in original
    assert units['mgrd'].startswith('ovs-mgrd-inventory-')
    pin = ovs / 'witness-build.json'
    executable = ovs / 'bin/ovs-vswitchd'
    control = ovs / 'switch.ctl'
    assert pin.is_file() and executable.is_file() and control.is_socket()
    flags = (
        f' --fdb-observe-bridges={",".join(bridge_ids)} --fdb-witness-socket={control}'
        f' --fdb-witness-executable={executable} --fdb-witness-manifest={pin}'
        f' --fdb-witness-manifest-sha256={hashlib.sha256(pin.read_bytes()).hexdigest()}'
    )
    cleanup.callback(reconfigure_draft_fixture, unit_path, units['mgrd'], original)
    # Only this disposable root service may inspect the qualified test OVS process.
    updated = original.replace(caps, 'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW CAP_SYS_PTRACE\n')
    if lldp_interface:
        families = 'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK'
        assert '--lldp-observe-interfaces' not in original and families + '\n' in original
        updated = updated.replace(families + '\n', families + ' AF_PACKET\n')
        flags += ' --lldp-observe-interfaces=' + lldp_interface
    reconfigure_draft_fixture(unit_path, units['mgrd'], updated.replace(needle, needle + flags))
    return control


def verify_fdb_frontend(repo, fixture, node, origin, accounts, call, get, vsctl, units, ovs, eventually):
    assert node and Path(node).is_file()

    def disabled_page():
        code, payload, _ = call('/fdb?limit=1')
        return payload if code == 200 else None

    disabled = eventually(disabled_page)
    assert disabled['availability'] == 'unavailable'
    assert disabled['reason'] == 'disabled' and disabled['items'] == []
    with ExitStack() as cleanup:
        topology = prepare_observation_links(vsctl, get, eventually, cleanup, 'br-fdb-ui',
                                             'br-fdb-off', 'ovs-fdb-ui-peer', (
            ('br-fdb-ui', 'fd-ui-v101', 'fd-src-v101', 101),
            ('br-fdb-ui', 'fd-ui-v202', 'fd-src-v202', 202),
            ('br-fdb-off', 'fd-ui-off', 'fd-src-off', 101),
        ))
        control = enable_fdb_scope(units, ovs, cleanup, [topology['bridge']['id']])
        eventually(lambda: call('/inventory')[0] == 200)

        private = fixture / 'fdb-browser-private.json'
        cleanup.callback(private.unlink, missing_ok=True)
        private.write_text(json.dumps({'origin': origin, 'accounts': accounts, 'dbSocket': str(ovs / 'db.sock'),
                                       'controlSocket': str(control), 'disabled': disabled, **topology}))
        private.chmod(0o600)
        env = dict(os.environ, OVS_FDB_FRONTEND_FIXTURE=str(private))
        result = subprocess.run([node, str(repo / 'node_modules/@playwright/test/cli.js'), 'test',
                                 '--config', str(repo / 'tests/frontend/fdb.playwright.config.ts')],
                                cwd=repo, env=env, timeout=240)
        assert result.returncode == 0, 'qualified native FDB browser acceptance failed'
        return {'verified': True, 'backend': 'qualified OVS 3.3.9 / HTTPS / Unix IPC',
                'report': 'test-results/ovs-witness/fdb.xml',
                'screenshots': 'test-results/ovs-witness/fdb-evidence',
                'credentials_persisted_in_artifacts': False}
