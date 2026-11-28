"""Exercise an authenticated snapshot on the explicitly isolated OVS witness."""

from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import subprocess

from authentication import run
from spanning_tree_prepare import reconfigure_draft_fixture, set_unit_option


def verify_openflow_frontend(repo, fixture, node, origin, accounts, call, get, vsctl, units, ovs, eventually):
    assert node and Path(node).is_file()
    bridge = 'br-oflow-ui'
    cookie = '0xf123456789abcdef'
    with ExitStack() as cleanup:
        vsctl('add-br', bridge, '--', 'set', 'Bridge', bridge, 'datapath_type=system',
              'protocols=OpenFlow13', 'other_config:datapath-id=0277685511223355')
        cleanup.callback(vsctl, '--no-wait', '--if-exists', 'del-br', bridge)
        binding = eventually(lambda: next((row for row in get('/bridges?filter=' + bridge)['items']
                                           if row['name'] == bridge), None))
        bridge_id = binding['management_id']
        endpoint = f'/openflow/bridges/{bridge_id}/snapshots?table=7'
        code, problem, _ = call(endpoint)
        assert code == 503 and problem['code'] == 'OPENFLOW_SOURCE_UNAVAILABLE'
        local = ovs / (bridge + '.mgmt')
        eventually(local.is_socket)
        ofctl = str(ovs / 'bin/ovs-ofctl')
        run(ofctl, '-O', 'OpenFlow13', 'add-flow', 'unix:' + str(local),
            f'table=7,priority=405,cookie={cookie},actions=drop')
        run(ofctl, '-O', 'OpenFlow13', 'add-flow', 'unix:' + str(local),
            f'table=8,priority=406,cookie={cookie},actions=drop')
        run(ofctl, '-O', 'OpenFlow13', 'add-group', 'unix:' + str(local),
            'group_id=17,type=all,bucket=output:LOCAL')

        control = ovs / 'switch.ctl'
        executable = ovs / 'bin/ovs-vswitchd'
        # This disposable executable and its exact profile were independently
        # checked by the witness matrix before invoking inventory.py.
        manifest = ovs / 'witness-build.json'
        assert manifest.is_file() and executable.is_file() and control.is_socket()
        unit_path = Path('/etc/systemd/system') / units['mgrd']
        original = unit_path.read_text()
        caps = 'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n'
        assert units['mgrd'].startswith('ovs-mgrd-inventory-') and caps in original
        cleanup.callback(reconfigure_draft_fixture, unit_path, units['mgrd'], original)
        updated = original.replace(caps, 'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW CAP_SYS_PTRACE\n')
        options = (
            ('openflow-observe-sockets', 'OPENFLOW_OBSERVE_SOCKETS', f'{bridge_id}={local}'),
            ('openflow-witness-socket', 'OPENFLOW_WITNESS_SOCKET', str(control)),
            ('openflow-witness-executable', 'OPENFLOW_WITNESS_EXECUTABLE', str(executable)),
            ('openflow-witness-manifest', 'OPENFLOW_WITNESS_MANIFEST', str(manifest)),
            ('openflow-witness-manifest-sha256', 'OPENFLOW_WITNESS_MANIFEST_SHA256',
             hashlib.sha256(manifest.read_bytes()).hexdigest()),
        )
        for argument, variable, value in options:
            updated = set_unit_option(updated, argument, variable, value)
        reconfigure_draft_fixture(unit_path, units['mgrd'], updated)

        # Inventory status can be 200 while the native monitor or the
        # reviewed OpenFlow source is still starting.
        selected = f'{endpoint}&row_limit=5&cookie={int(cookie, 16)}&groups_meters=true'

        def scoped_source_ready():
            status, payload, _ = call(selected)
            if status == 503 or (status == 409 and isinstance(payload, dict) and
                                 payload.get('code') == 'OPENFLOW_SCOPE_CHANGED'):
                return False
            assert status == 200, f'qualified OpenFlow readiness returned HTTP {status}'
            evidence = payload.get('evidence', {})
            return (payload.get('snapshot', {}).get('bridge_id') == bridge_id and
                    evidence.get('source') == 'openflow-1.3-unix' and
                    any(row.get('table') == 7 and row.get('priority') == 405 and row.get('cookie') == cookie
                        for row in (evidence.get('rows') or [])) and
                    any(row.get('group_id') == 17 for row in (evidence.get('group_rows') or [])))

        eventually(scoped_source_ready)

        metadata = fixture / 'openflow-browser-private.json'
        cleanup.callback(metadata.unlink, missing_ok=True)
        metadata.write_text(json.dumps({'origin': origin, 'accounts': accounts, 'bridge': bridge,
                                        'bridgeId': bridge_id, 'cookie': cookie}))
        metadata.chmod(0o600)
        result = subprocess.run([node, str(repo / 'node_modules/@playwright/test/cli.js'), 'test',
                                 '--config', str(repo / 'tests/frontend/openflow.playwright.config.ts')],
                                cwd=repo, env=dict(os.environ, OVS_OPENFLOW_FRONTEND_FIXTURE=str(metadata)),
                                timeout=100)
        assert result.returncode == 0, 'qualified native OpenFlow browser acceptance failed'
        assert vsctl('get', 'Bridge', bridge, '_uuid').strip() == binding['ovs_uuid']
        return {'verified': True, 'backend': 'qualified local OpenFlow 1.3 / HTTPS / Unix IPC',
                'report': 'test-results/ovs-witness/openflow.xml',
                'default_source_disabled': True, 'credentials_persisted_in_artifacts': False}
