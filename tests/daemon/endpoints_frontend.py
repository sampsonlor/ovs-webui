"""Dual-architecture native endpoint correlation inside a disposable witness fixture."""
from contextlib import ExitStack
import json
import os
from pathlib import Path
import subprocess

from authentication import run
from fdb_frontend import enable_fdb_scope, prepare_observation_links


def prepare_endpoints(vsctl, get, eventually, cleanup):
    bridge, second_bridge, namespace = 'br-ep-ui', 'br-ep-alt', 'ovs-endpoint-peer'
    topology = prepare_observation_links(vsctl, get, eventually, cleanup, bridge, second_bridge,
                                         namespace, (
        (bridge, 'ep-ui-v101', 'ep-src-v101', 101),
        (bridge, 'ep-ui-v202', 'ep-src-v202', 202),
        (second_bridge, 'ep-ui-alt', 'ep-src-alt', 101),
    ))
    capture = topology['ports'][0]
    l3 = 'ep-ui-l3'
    peer = capture['peer']
    address = {'mac': '02:00:24:44:01:18', 'ipv4': '192.0.2.118', 'ipv6': '2001:db8:24:44::18'}

    # This internal L3 attachment has real ARP/NDP entries after traffic.
    # It shares the first VLAN but not the system Port or Interface identity.
    vsctl('add-port', bridge, l3, '--', 'set', 'Port', l3, 'vlan_mode=access', 'tag=101',
          '--', 'set', 'Interface', l3, 'type=internal')
    run('ip', 'link', 'set', 'dev', l3, 'up')
    run('ip', '-n', namespace, 'link', 'set', 'dev', peer, 'address', address['mac'])
    run('ip', '-4', 'addr', 'add', '192.0.2.117/29', 'dev', l3)
    run('ip', '-n', namespace, '-4', 'addr', 'add', address['ipv4'] + '/29', 'dev', peer)
    run('sysctl', '-q', '-w', f'net.ipv6.conf.{l3}.disable_ipv6=0')
    run('ip', 'netns', 'exec', namespace, 'sysctl', '-q', '-w', f'net.ipv6.conf.{peer}.disable_ipv6=0')
    run('ip', '-6', 'addr', 'add', '2001:db8:24:44::17/64', 'nodad', 'dev', l3)
    run('ip', '-n', namespace, '-6', 'addr', 'add', address['ipv6'] + '/64', 'nodad', 'dev', peer)

    def exact_interface(name):
        return eventually(lambda: next((row for row in get('/interfaces?filter=' + name)['items']
                                        if row['name'] == name and row.get('bridge_ref')), None))

    binding = exact_interface(l3)
    assert binding['bridge_ref']['id'] == topology['bridge']['id']
    topology['l3'] = {'interface': l3, 'id': binding['management_id'], 'port': binding['port_ref']['id']}
    topology['lldpInterface'] = exact_interface(capture['name'])['management_id']
    topology['address'] = address
    return topology


def verify_endpoints_frontend(repo, fixture, node, origin, accounts, call, get, vsctl, units,
                              ovs, eventually):
    assert node and Path(node).is_file()
    with ExitStack() as cleanup:
        topology = prepare_endpoints(vsctl, get, eventually, cleanup)

        def read_initial():
            code, page, _ = call('/endpoints?bridge=' + topology['bridge']['id'])
            return page if code == 200 else None

        initial = eventually(read_initial)
        source = {row['kind']: row for row in initial['sources']}
        assert source['fdb']['availability'] == 'unavailable' and source['fdb']['reason'] == 'disabled'
        assert source['lldp']['availability'] == 'unavailable' and source['lldp']['reason'] == 'LLDP_PROVIDER_DISABLED'

        control = enable_fdb_scope(units, ovs, cleanup,
                                   [topology['bridge']['id'], topology['otherBridge']['id']],
                                   topology['lldpInterface'])
        eventually(lambda: call('/inventory')[0] == 200)
        private = fixture / 'endpoints-browser-private.json'
        cleanup.callback(private.unlink, missing_ok=True)
        private.write_text(json.dumps({'origin': origin, 'accounts': accounts, 'dbSocket': str(ovs / 'db.sock'),
                                       'controlSocket': str(control), **topology}))
        private.chmod(0o600)
        env = dict(os.environ, OVS_ENDPOINTS_FRONTEND_FIXTURE=str(private))
        result = subprocess.run([node, str(repo / 'node_modules/@playwright/test/cli.js'), 'test',
                                 '--config', str(repo / 'tests/frontend/endpoints.playwright.config.ts')],
                                cwd=repo, env=env, timeout=100)
        assert result.returncode == 0, 'qualified native Endpoints browser acceptance failed'
        return {'verified': True, 'backend': 'OVS FDB, Linux ARP/NDP and Linux passive LLDP / HTTPS / Unix IPC',
                'defaults_verified': ['fdb_disabled', 'lldp_disabled'],
                'report': 'test-results/ovs-witness/endpoints.xml',
                'screenshots': 'test-results/ovs-witness/endpoints-evidence',
                'credentials_persisted_in_artifacts': False}
