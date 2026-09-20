"""Safe Apply acceptance on a real Linux VM, kernel OVS and management VLAN.

Called only by the ephemeral inventory fixture. All interfaces, namespaces,
units, accounts, addresses and credentials are synthetic and removed afterward.
"""
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import time
import uuid

from authentication import request_id, run


def verify_safe_apply(call, get, login, vsctl, units, manager_db, web_db,
                      eventually, fixture, runtime_config, cert, https_port):
    suffix = uuid.uuid4().hex[:7]
    bridge, port, peer, namespace = 'bs' + suffix, 'ps' + suffix, 'cs' + suffix, 'safe-' + suffix
    network = f'198.18.{20 + int(suffix[:2], 16) % 200}'
    host_ip, client_ip = network + '.1', network + '.2'
    notes, metrics = [], {}
    listener = None
    original_unit = (Path('/etc/systemd/system') / units['mgrd']).read_text()
    original_config = runtime_config.read_text()

    def ready():
        code, value, _ = call('/inventory')
        return code == 200 and value['availability'] == 'complete'

    def durable(transaction_id):
        db = sqlite3.connect('file:' + str(manager_db) + '?mode=ro', uri=True)
        try:
            row = db.execute('SELECT document FROM safe_applies WHERE id=?', (transaction_id,)).fetchone()
            return json.loads(row[0]) if row else None
        finally:
            db.close()

    def client_connected():
        result = subprocess.run(['ip', 'netns', 'exec', namespace, 'curl', '--silent', '--noproxy', '*',
                                 '--max-time', '2', '--cacert', str(cert), '--connect-to',
                                 f'127.0.0.1:{https_port}:{host_ip}:{https_port}',
                                 f'https://127.0.0.1:{https_port}/api/v1/runtime'],
                                capture_output=True, text=True, timeout=4)
        return result.returncode == 0 and 'configuration_ready' in result.stdout

    def stage(name, tag):
        draft = get('/candidate')
        epochs = get('/session')['request_epochs']
        if draft['intents']:
            code, _, _ = call('/candidate', 'PATCH', {'request_id': request_id(), 'operation': 'discard'},
                              extra_headers={'If-Match': '"' + draft['revision'] + '"', 'X-OVS-Request-Epoch': epochs['workspace']})
            assert code == 200
            draft = get('/candidate')
        target = next(p for p in get('/ports')['items'] if p['name'] == name)
        binding = {k: target[k] for k in ('management_id', 'ovs_uuid', 'instance_generation')}
        binding['table'] = 'Port'
        body = {'request_id': request_id(), 'operation': 'stage', 'intents': [{
            'intent_id': str(uuid.uuid4()), 'operation': 'port.vlan.set', 'object': binding,
            'value': {'vlan_mode': 'access', 'tag': tag, 'trunks': [], 'cvlans': []}}]}
        code, draft, _ = call('/candidate', 'PATCH', body,
                             extra_headers={'If-Match': '"' + draft['revision'] + '"', 'X-OVS-Request-Epoch': epochs['workspace']})
        assert code == 200, (code, draft)
        code, accepted, _ = call('/validations', 'POST', {'request_id': request_id(), 'candidate_id': draft['id'], 'candidate_revision': draft['revision']})
        assert code == 202, (code, accepted)
        validation = get('/validations/' + accepted['resource_ref']['id'])
        assert validation['usable'] and validation['execution_ready'], validation
        body = {'request_id': request_id(), 'candidate_id': draft['id'], 'candidate_revision': draft['revision'],
                'validation_id': validation['id'], 'mode': 'safe-apply', 'reason': 'synthetic VM recovery acceptance'}
        code, accepted, _ = call('/transactions', 'POST', body)
        assert code == 202, (code, accepted)
        return accepted['resource_ref']['id'], body

    def awaiting(transaction_id):
        return eventually(lambda: (v if (v := get('/transactions/' + transaction_id))['safe_apply'] == 'awaiting-confirmation' else None))

    def decide(transaction_id, action, expected=202):
        state = get('/transactions/' + transaction_id)
        body = {'request_id': request_id(), 'decision': action, 'expected_sequence': state['sequence']}
        code, result, _ = call('/transactions/' + transaction_id + '/decisions', 'POST', body)
        assert code == expected, (code, result)
        return body

    try:
        # Failure is a failed VM acceptance, not a skip or a dummy fallback.
        run('modprobe', 'openvswitch')
        run('ip', 'netns', 'add', namespace)
        run('ip', 'link', 'add', port, 'type', 'veth', 'peer', 'name', peer)
        run('ip', 'link', 'set', peer, 'netns', namespace)
        run('ip', 'netns', 'exec', namespace, 'ip', 'link', 'set', 'lo', 'up')
        run('ip', 'netns', 'exec', namespace, 'ip', 'addr', 'add', client_ip + '/24', 'dev', peer)
        run('ip', 'netns', 'exec', namespace, 'ip', 'link', 'set', peer, 'up')
        vsctl('add-br', bridge, '--', 'set', 'Bridge', bridge, 'datapath_type=system',
              '--', 'set', 'Port', bridge, 'vlan_mode=access', 'tag=10',
              '--', 'add-port', bridge, port, '--', 'set', 'Port', port, 'vlan_mode=access', 'tag=10')
        run('ip', 'addr', 'add', host_ip + '/24', 'dev', bridge)
        run('ip', 'link', 'set', bridge, 'up')
        run('ip', 'link', 'set', port, 'up')
        server = "import socket,sys; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind((sys.argv[1],18080)); s.listen(16)\nwhile True:\n c,_=s.accept(); c.close()"
        listener = subprocess.Popen(['ip', 'netns', 'exec', namespace, 'python3', '-u', '-c', server, client_ip],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        def has_management_port():
            page = get('/ports')
            return page if any(p['name'] == port for p in page['items']) else None
        page = eventually(has_management_port)
        by_name = {p['name']: p for p in page['items']}
        ids = ','.join(by_name[n]['management_id'] for n in ('inv-p1', port))
        unit_path = Path('/etc/systemd/system') / units['mgrd']
        text = re.sub(r'--local-vlan-ports=\S+', '--local-vlan-ports=' + ids, original_unit)
        text = text.replace('--database=${MANAGER_DATABASE}', '--database=${MANAGER_DATABASE}' +
                            f' --safe-apply-probe-address={client_ip}:18080 --safe-apply-probe-interface={bridge}')
        unit_path.write_text(text)
        runtime_config.write_text(original_config.replace(f'HTTPS_LISTEN=127.0.0.1:{https_port}', f'HTTPS_LISTEN=0.0.0.0:{https_port}'))
        run('systemctl', 'daemon-reload')
        run('systemctl', 'reset-failed', units['mgrd'])
        run('systemctl', 'restart', units['mgrd'], units['webd'])
        eventually(ready)
        eventually(client_connected)
        assert listener.poll() is None
        metrics['kernel_datapath'] = vsctl('get', 'Bridge', bridge, 'datapath_type').strip()
        metrics['virtualization'] = run('systemd-detect-virt', check=False).stdout.strip()

        normal, original_request = stage('inv-p1', 40)
        current = awaiting(normal)
        assert current['applied_outcome'] == 'applied' and current['health'] == 'healthy'
        decision = decide(normal, 'confirm')
        assert get('/transactions/' + normal)['safe_apply'] == 'confirmed'
        assert get('/candidate')['intents'] == []
        code, replay, headers = call('/transactions', 'POST', original_request)
        assert code == 202 and headers['Idempotency-Replayed'] == 'true' and replay['resource_ref']['id'] == normal
        code, _, headers = call('/transactions/' + normal + '/decisions', 'POST', decision)
        assert code == 202 and headers['Idempotency-Replayed'] == 'true'
        notes.append('real HTTPS/CSRF/Unix IPC admission, Applied, interface-bound TCP reachability, explicit confirmation, consumed workspace and replay without a second native write')

        # Wait the REAL 120-second production deadline; no accelerated clock.
        timed, _ = stage('inv-p1', 41)
        before = awaiting(timed)['confirmation_deadline']
        run('systemctl', 'stop', units['webd'])
        run('systemctl', 'kill', '--signal=SIGKILL', units['mgrd'])
        eventually(lambda: durable(timed)['state'] == 'rolled-back', timeout=145)
        recovered = durable(timed)
        assert recovered['confirmation']['deadline_wall'] == before
        assert int(vsctl('get', 'Port', 'inv-p1', 'tag')) == 40
        run('systemctl', 'start', units['webd'])
        eventually(ready)
        assert get('/candidate')['intents'], 'rollback discarded user intent'
        notes.append('webd stopped and mgrd SIGKILL: durable same-boot deadline did not extend; independent watchdog restored touched fields after the real confirmation timeout')

        broken, _ = stage(port, 20)
        eventually(lambda: int(vsctl('get', 'Port', port, 'tag')) == 20)
        assert not client_connected(), 'management TLS path did not actually break'
        # Neither an HTTP handler nor browser remains alive to drive recovery.
        run('systemctl', 'stop', units['webd'])
        run('systemctl', 'kill', '--signal=SIGKILL', units['mgrd'])
        eventually(lambda: durable(broken)['state'] == 'rolled-back', timeout=45)
        assert durable(broken)['confirmation'] is None, 'unreachable apply opened confirmation'
        assert int(vsctl('get', 'Port', port, 'tag')) == 10
        run('systemctl', 'start', units['webd'])
        eventually(ready)
        eventually(client_connected)
        notes.append('kernel OVS access VLAN isolated a real namespace client; failed TCP probes withheld confirmation; mgrd restart restored VLAN and actual management TLS connectivity with webd stopped')

        revoked, _ = stage('inv-p1', 42)
        awaiting(revoked)
        assert call('/session', 'DELETE', {})[0] == 204
        eventually(lambda: durable(revoked)['state'] == 'rolled-back')
        login()
        assert int(vsctl('get', 'Port', 'inv-p1', 'tag')) == 40
        notes.append('revoking the initiating session causes server-authoritative compensation without storing a bearer in the journal')

        conflict, _ = stage('inv-p1', 43)
        awaiting(conflict)
        vsctl('set', 'Port', 'inv-p1', 'tag=44', 'other_config:synthetic=preserved')
        decide(conflict, 'confirm', expected=409)
        decide(conflict, 'rollback')
        eventually(lambda: durable(conflict)['state'] == 'rollback-conflict')
        assert int(vsctl('get', 'Port', 'inv-p1', 'tag')) == 44
        db = sqlite3.connect('file:' + str(manager_db) + '?mode=ro', uri=True)
        try:
            assert db.execute('SELECT count(*) FROM operation_protections WHERE transaction_id=?', (conflict,)).fetchone()[0] == 1
            assert db.execute('SELECT transaction_id FROM last_known_good').fetchall() == [(normal,)]
        finally:
            db.close()
        notes.append('external overlapping VLAN edit blocks confirmation and produces rollback conflict; protection and the single previously confirmed Last Known Good remain intact')

        # Corrupt only this disposable manager DB after clean service shutdown.
        run('systemctl', 'stop', units['mgrd'])
        manager_db.write_bytes(b'synthetic-invalid-manager-database')
        for suffix in ('-wal', '-shm'):
            Path(str(manager_db) + suffix).unlink(missing_ok=True)
        run('systemctl', 'reset-failed', units['mgrd'])
        run('systemctl', 'start', units['mgrd'])
        eventually(lambda: call('/transactions/' + conflict)[0] == 503)
        assert int(vsctl('get', 'Port', 'inv-p1', 'tag')) == 44
        notes.append('corrupt manager.db fails closed; it neither confirms nor invents an unjournalled rollback')
        return {'checks': notes, 'metrics': metrics}
    finally:
        run('systemctl', 'stop', units['mgrd'], units['webd'], check=False)
        if listener is not None:
            listener.terminate()
            listener.communicate(timeout=5)
        # These are literal names generated inside this disposable fixture.
        run('ip', 'link', 'delete', port, check=False)
        run('ip', 'netns', 'delete', namespace, check=False)
        try:
            vsctl('--if-exists', 'del-br', bridge)
        finally:
            (Path('/etc/systemd/system') / units['mgrd']).write_text(original_unit)
            runtime_config.write_text(original_config)
