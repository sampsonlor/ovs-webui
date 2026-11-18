"""Real Svelte browser acceptance inside the disposable Go/OVS VM fixture."""
import json
import hashlib
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
    code, _, _ = call('/roles','POST',{'request_id':request_id(),'name':'NativeMulticast','capabilities':network_role['capabilities']})
    assert code == 202
    code, _, _ = call('/roles','POST',{'request_id':request_id(),'name':'NativeStaticTunnel','capabilities':network_role['capabilities']})
    assert code == 202
    code, _, _ = call('/roles','POST',{'request_id':request_id(),'name':'NativeMirror','capabilities':network_role['capabilities']})
    assert code == 202
    code, _, _ = call('/roles','POST',{'request_id':request_id(),'name':'NativeQoS','capabilities':network_role['capabilities']})
    assert code == 202
    code, _, _ = call('/roles','POST',{'request_id':request_id(),'name':'NativeTelemetry','capabilities':network_role['capabilities']})
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
                       ('browser-multicast', 'NativeMulticast'), ('browser-multicast-reader', 'Reader'),
                       ('browser-multicast-observer', 'InterfaceObserver'),
                       ('browser-mirror', 'NativeMirror'), ('browser-mirror-reader', 'Reader'),
                       ('browser-mirror-observer', 'InterfaceObserver'),
                       ('browser-tunnel', 'NativeStaticTunnel'), ('browser-tunnel-reader', 'NetworkAdmin'),
                       ('browser-tunnel-observer', 'InterfaceObserver'),
                       ('browser-qos-reader', 'Reader'), ('browser-qos-observer', 'InterfaceObserver'),
                       ('browser-qos-runtime', 'Reader'),
                       ('browser-qos-edit', 'NativeQoS'),
                       ('browser-qos-detach', 'NativeQoS'),
                       ('browser-qos-attach', 'NativeQoS'),
                       ('browser-qos-create', 'NativeQoS'),
                       ('browser-qos-create-reader', 'Reader'),
                       ('browser-telemetry-reader', 'Reader'), ('browser-telemetry-observer', 'InterfaceObserver'),
                       ('browser-telemetry-writer', 'NativeTelemetry'),
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
    vsctl('add-br','br-mcast-ui','--','set','Bridge','br-mcast-ui','datapath_type=system','other_config:multicast-opaque=preserve','--','add-port','br-mcast-ui','mcast-ui-data','--','set','Interface','mcast-ui-data','type=internal')
    multicast = eventually(lambda: next((b for b in get('/bridges?filter=br-mcast-ui')['items'] if b['name']=='br-mcast-ui'),None))
    vsctl('add-br','br-tunnel-ui','--','set','Bridge','br-tunnel-ui','datapath_type=system','other_config:tunnel-foreign=preserve',
          '--','add-port','br-tunnel-ui','tun-ui-data','--','set','Interface','tun-ui-data','type=vxlan','options:remote_ip=192.0.2.20','options:key=1001','external_ids:tunnel-foreign=preserve')
    vsctl('add-br','br-tunnel-flow','--','set','Bridge','br-tunnel-flow','datapath_type=system',
          '--','add-port','br-tunnel-flow','tun-ui-flow','--','set','Interface','tun-ui-flow','type=gre','options:remote_ip=flow','options:key=flow')
    tunnel = eventually(lambda: next((p for p in get('/ports?filter=tun-ui-data')['items'] if p['name']=='tun-ui-data'),None))
    tunnel_bridge = eventually(lambda: next((b for b in get('/bridges?filter=br-tunnel-ui')['items'] if b['name']=='br-tunnel-ui'),None))
    topology = get('/inventory/topology')
    assert topology['truncated'] is False, 'existing formal topology fixture must stay complete'
    tunnel_ids = [n['binding']['management_id'] for n in topology['nodes']
                  if n['name'] in ['br-tunnel-ui','tun-ui-data']]
    assert len(tunnel_ids) == 5
    vsctl('add-br','br-mirror-ui','--','set','Bridge','br-mirror-ui','datapath_type=system',
          '--','add-port','br-mirror-ui','mi-ui-src','--','set','Interface','mi-ui-src','type=internal',
          '--','add-port','br-mirror-ui','mi-ui-dst','--','set','Interface','mi-ui-dst','type=internal',
          '--','add-port','br-mirror-ui','mi-ui-out','--','set','Interface','mi-ui-out','type=internal')
    vsctl('--','--id=@src','get','Port','mi-ui-src','--','--id=@out','get','Port','mi-ui-out',
          '--','--id=@m','create','Mirror','name=mi-ui-data','select_src_port=@src','output_port=@out','external_ids:mirror-foreign=preserve',
          '--','set','Bridge','br-mirror-ui','mirrors=@m')
    mirror = eventually(lambda: next((m for m in get('/mirrors?filter=mi-ui-data')['items'] if m['name']=='mi-ui-data'),None))
    mirror_bridge = eventually(lambda: next((b for b in get('/bridges?filter=br-mirror-ui')['items'] if b['name']=='br-mirror-ui'),None))
    # Read exact immutable native references rather than widening the shared
    # topology response budget to accommodate unrelated test scenarios.
    mirror_graph = get('/bridges/'+mirror_bridge['management_id'])
    mirror_ids = [mirror['management_id'], mirror_bridge['management_id']]
    for ref in mirror_graph['port_refs']:
        port = get('/ports/'+ref['id'])
        mirror_ids.append(port['id'])
        mirror_ids.extend(ref['id'] for ref in port['interface_refs'])
    assert len(mirror_ids) == 10
    qos_bridge, qos_port, qos_peer = 'br-qos-edit', 'qos-ui-port', 'qos-ui-peer'
    run('ip', 'link', 'add', qos_port, 'type', 'veth', 'peer', 'name', qos_peer)
    run('ip', 'link', 'set', qos_port, 'up')
    run('ip', 'link', 'set', qos_peer, 'up')
    vsctl('add-br', qos_bridge, '--', 'set', 'Bridge', qos_bridge, 'datapath_type=system', '--', 'add-port', qos_bridge, qos_port)
    vsctl('--', '--id=@queue', 'create', 'Queue', 'other_config:min-rate=1000000', 'other_config:max-rate=3000000',
          'other_config:fixture-opaque=keep', '--', '--id=@qos', 'create', 'QoS', 'type=linux-hfsc',
          'other_config:max-rate=8000000', 'other_config:fixture-opaque=keep', 'queues:7=@queue',
          '--', 'set', 'Port', qos_port, 'qos=@qos')
    qos_uuid = vsctl('get', 'Port', qos_port, 'qos').strip()
    qos_queue = vsctl('get', 'QoS', qos_uuid, 'queues:7').strip()
    qos_for_edit = eventually(lambda: next((item for item in get('/qos')['items'] if item['ovs_uuid'] == qos_uuid), None))
    qos_port_for_edit = eventually(lambda: next((item for item in get('/ports?filter=' + qos_port)['items'] if item['name'] == qos_port), None))
    qos_grant = qos_port_for_edit['management_id']+':'+qos_for_edit['management_id']
    # The same Port has distinct grants for two profiles. Tests clear its
    # original binding before attaching the second profile through WebUI.
    vsctl('--', '--id=@queue', 'create', 'Queue', 'other_config:min-rate=1000000', 'other_config:max-rate=3000000',
          'other_config:fixture-opaque=keep', 'dscp=0', '--', 'create', 'QoS', 'type=linux-hfsc',
          'other_config:max-rate=8000000', 'external_ids:fixture=qos-attach', 'queues:7=@queue')
    qos_attach_uuid = vsctl('--data=bare', '--no-headings', '--columns=_uuid', 'find', 'QoS',
                            'external_ids:fixture=qos-attach').strip()
    qos_attach_queue = vsctl('get', 'QoS', qos_attach_uuid, 'queues:7').strip()
    qos_for_attach = eventually(lambda: next((item for item in get('/qos')['items'] if item['ovs_uuid'] == qos_attach_uuid), None))
    qos_attach_grant = qos_port_for_edit['management_id']+':'+qos_for_attach['management_id']
    qos_create_name = 'ui-saved-hfsc'
    telemetry_bridge = 'br-telem-write'
    vsctl('add-br', telemetry_bridge, '--', '--id=@nf', 'create', 'NetFlow',
          'targets="127.0.0.1:2055"', 'active_timeout=60', 'external_ids:fixture=preserve',
          '--', 'set', 'Bridge', telemetry_bridge, 'datapath_type=dummy', 'netflow=@nf')
    telemetry = eventually(lambda: next((item for item in get('/telemetry?filter='+telemetry_bridge)['items']
                                         if item['name'] == telemetry_bridge), None))
    telemetry_id = telemetry['bridge_ref']['id']
    telemetry_grant = telemetry_id+':netflow'
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
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-multicast-role',roles['NativeMulticast'])
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-static-tunnel-role',roles['NativeStaticTunnel'])
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-mirror-role',roles['NativeMirror'])
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-qos-role',roles['NativeQoS'])
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-telemetry-role',roles['NativeTelemetry'])
    mirror_pin=ovs/'mirror-build.json'
    assert mirror_pin.is_file(), 'formal Mirror writes require the separately fixed OVS build'
    mirror_flags=' --mirror-runtime-socket='+str(ovs/'switch.ctl')+' --mirror-runtime-executable='+str(ovs/'bin/ovs-vswitchd')+' --mirror-runtime-manifest='+str(mirror_pin)+' --mirror-runtime-manifest-sha256='+hashlib.sha256(mirror_pin.read_bytes()).hexdigest()
    assert 'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n' in unit_original
    # This disposable fixed-build fixture explicitly opts into the same process
    # inspection capability as the strict STP source gate. Restore the original
    # unit in finally; the packaged default remains unchanged.
    mirror_unit = unit_original.replace('CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n',
                                        'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW CAP_SYS_PTRACE\n')
    unit_path.write_text(mirror_unit.replace(needle, needle + mirror_flags + ' --local-spanning-tree-bridges=' + tree['management_id']+' --local-isolation-ports='+isolation['management_id']+' --local-multicast-bridges='+multicast['management_id']
                                              +' --local-tunnel-objects='+','.join(tunnel_ids)+' --local-tunnel-create-targets='+tunnel_bridge['management_id']+':tun-ui-new'
                                              +' --local-mirror-objects='+','.join(mirror_ids)+' --local-mirror-create-targets='+mirror_bridge['management_id']+':mi-ui-new'
                                              +' --local-qos-bindings='+qos_grant+' --local-qos-detach-bindings='+qos_grant
                                              +' --local-qos-attach-bindings='+qos_attach_grant+' --local-qos-create-names='+qos_create_name
                                              +' --local-telemetry-bindings='+telemetry_grant))
    run('systemctl','daemon-reload')
    run('systemctl','reset-failed',units['mgrd'])
    run('systemctl','start',units['mgrd'])
    eventually(lambda: call('/inventory')[0] == 200)
    metadata = {'origin': origin, 'accounts': accounts, 'units': units, 'dbSocket': str(db_socket),
                'ovsDirectory': str(ovs), 'database': str(conf), 'isolation': isolation, 'multicast': multicast,
                'tunnel': tunnel, 'tunnelBridge': tunnel_bridge, 'mirror': mirror, 'mirrorBridge': mirror_bridge,
                'qosEdit': {'id': qos_for_edit['management_id'], 'port': qos_port, 'bridge': qos_bridge,
                            'uuid': qos_uuid, 'queue': qos_queue},
                'qosAttach': {'id': qos_for_attach['management_id'], 'port': qos_port,
                              'uuid': qos_attach_uuid, 'queue': qos_attach_queue},
                'qosCreateName': qos_create_name,
                'telemetryWrite': {'id': telemetry_id, 'bridge': telemetry_bridge}}
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
        vsctl('--no-wait', '--if-exists', 'del-br', 'br-mcast-ui')
        vsctl('--no-wait', '--if-exists', 'del-br', 'br-tunnel-ui')
        vsctl('--no-wait', '--if-exists', 'del-br', 'br-tunnel-flow')
        vsctl('--no-wait', '--if-exists', 'del-br', 'br-mirror-ui')
        vsctl('--no-wait', '--if-exists', 'del-br', telemetry_bridge)
        vsctl('--no-wait', '--if-exists', 'del-br', qos_bridge)
        vsctl('--no-wait', '--if-exists', 'destroy', 'QoS', qos_uuid)
        vsctl('--no-wait', '--if-exists', 'destroy', 'QoS', qos_attach_uuid)
        vsctl('--no-wait', '--if-exists', 'destroy', 'Queue', qos_queue)
        vsctl('--no-wait', '--if-exists', 'destroy', 'Queue', qos_attach_queue)
        run('ip', 'link', 'del', qos_port)
