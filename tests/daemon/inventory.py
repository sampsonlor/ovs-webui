"""Ephemeral native Linux OVSDB/ovs-vswitchd + authenticated HTTPS inventory.

Every unit, account, path, credential and database belongs to this removed
fixture. Provider code itself has no OVSDB write operation or shell runner.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import pwd
import secrets
import shutil
import signal
import socket
import sqlite3
import ssl
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from authentication import request_id, run


def eventually(check, timeout=20):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        try:
            value = check()
            if value:
                return value
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(.1)
    raise AssertionError('inventory fixture condition timed out')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    parser.add_argument('--schema-version', default='3.3.9', choices=['3.3.9', '3.7.1', '4.0.0'])
    parser.add_argument('--safe-apply-network', action='store_true')
    parser.add_argument('--frontend-browser', action='store_true')
    parser.add_argument('--browser-node')
    args = parser.parse_args()
    assert os.geteuid() == 0 and Path('/run/systemd/system').is_dir()
    repo = Path(__file__).resolve().parents[2]
    source = Path(args.bin_dir).resolve()
    schema = repo / f'internal/provider/ovsdb/testdata/vswitch-{args.schema_version}.ovsschema'
    suffix = uuid.uuid4().hex[:10]
    fixture = Path('/run') / f'ovs-inventory-{suffix}'
    runtime_name = f'ovs-inventory-runtime-{suffix}'
    runtime = Path('/run') / runtime_name
    data_name = f'ovs-inventory-{suffix}'
    data = Path('/var/lib') / data_name
    account_name = f'ovs-inv-{suffix}'
    units = {s: f'ovs-{s}-inventory-{suffix}.service' for s in ('mgrd', 'webd')}
    unit_paths = [Path('/etc/systemd/system') / name for name in units.values()]
    fixture.mkdir(mode=0o700)
    created_account = False
    password = 'synthetic-inventory-password-' + secrets.token_hex(12)
    credentials = [password]
    checks, metrics = [], {}
    ovs = fixture / 'ovs'
    ovs.mkdir(mode=0o700)
    ovs_env = dict(os.environ, OVS_RUNDIR=str(ovs), OVS_LOGDIR=str(ovs), OVS_DBDIR=str(ovs))
    conf, db_socket = ovs / 'conf.db', ovs / 'db.sock'

    def ovs_run(*cmd):
        result = subprocess.run(cmd, env=ovs_env, capture_output=True, text=True, timeout=20)
        if result.returncode:
            raise RuntimeError(f'{cmd[0]} failed: {result.stderr[:1000]}')
        return result.stdout

    def start_db():
        ovs_run('ovsdb-server', str(conf), f'--remote=punix:{db_socket}', f'--pidfile={ovs}/db.pid',
                f'--unixctl={ovs}/db.ctl', '--detach', '--no-chdir', '--overwrite-pidfile')

    def stop_ovs(name):
        path = ovs / f'{name}.pid'
        if path.exists():
            pid = int(path.read_text())
            try:
                os.kill(pid, signal.SIGTERM)
                eventually(lambda: not Path(f'/proc/{pid}').exists() or Path(f'/proc/{pid}/stat').read_text().split(')')[1].strip().startswith('Z'))
            except ProcessLookupError:
                pass
            path.unlink(missing_ok=True)

    def vsctl(*cmd):
        return ovs_run('ovs-vsctl', '--timeout=5', f'--db=unix:{db_socket}', *cmd)

    try:
        run('useradd', '--system', '--no-create-home', '--home-dir', '/nonexistent',
            '--shell', '/usr/sbin/nologin', '--user-group', account_name)
        created_account = True
        account = pwd.getpwnam(account_name)
        os.chown(fixture, 0, account.pw_gid)
        fixture.chmod(0o750)
        data.mkdir(mode=0o755)
        manager_dir, web_dir = data / 'manager', data / 'web'
        manager_dir.mkdir(mode=0o700)
        web_dir.mkdir(mode=0o700)
        os.chown(web_dir, account.pw_uid, account.pw_gid)
        manager_db, web_db = manager_dir / 'manager.db', web_dir / 'web.db'
        for service in units:
            binary = fixture / f'ovs-{service}'
            shutil.copyfile(source / f'ovs-{service}', binary)
            binary.chmod(0o755)
        mgrd, webd = str(fixture / 'ovs-mgrd'), str(fixture / 'ovs-webd')
        run(mgrd, '--init-database', '--database', str(manager_db))
        run(mgrd, '--init-auth-key', '--database', str(manager_db))
        run(mgrd, '--bootstrap-admin', 'admin', '--database', str(manager_db), input_text=password + '\n')
        run('runuser', '-u', account_name, '--', webd, '--init-database', '--database', str(web_db))
        run('runuser', '-u', account_name, '--', webd, '--init-session-key', '--database', str(web_db))
        cert, key = fixture / 'server.crt', fixture / 'server.key'
        run('openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-days', '1',
            '-subj', '/CN=localhost', '-addext', 'subjectAltName=IP:127.0.0.1', '-keyout', str(key), '-out', str(cert))
        os.chown(key, 0, account.pw_gid)
        key.chmod(0o640)
        cert.chmod(0o644)
        with socket.socket() as reserve:
            reserve.bind(('127.0.0.1', 0))
            port = reserve.getsockname()[1]
        origin = f'https://127.0.0.1:{port}'
        config = fixture / 'runtime.env'
        config.write_text(f'WEBD_UID={account.pw_uid}\nWEBD_GID={account.pw_gid}\nMANAGER_SOCKET={runtime}/mgrd.sock\n'
                          f'HTTPS_LISTEN=127.0.0.1:{port}\nTLS_CERT={cert}\nTLS_KEY={key}\nPUBLIC_ORIGIN={origin}\n'
                          f'MANAGER_DATABASE={manager_db}\nWEB_DATABASE={web_db}\n')
        config.chmod(0o600)
        for service, name in units.items():
            template = (repo / 'packaging/systemd' / f'ovs-{service}.service').read_text()
            rendered = template.replace('/run/ovs-webui', str(runtime)).replace('RuntimeDirectory=ovs-webui', f'RuntimeDirectory={runtime_name}')
            rendered = rendered.replace('StateDirectory=ovs-webui/', f'StateDirectory={data_name}/').replace('ovs-webui-web', account_name)
            rendered = rendered.replace('/etc/ovs-webui/runtime.env', str(config)).replace(f'/usr/libexec/ovs-{service}', str(fixture / f'ovs-{service}'))
            rendered = rendered.replace('After=network.target ovs-mgrd.service', f'After=network.target {units["mgrd"]}')
            if service == 'mgrd':
                rendered = rendered.replace('--database=${MANAGER_DATABASE}', f'--database=${{MANAGER_DATABASE}} --ovsdb-socket={db_socket} --ovsdb-file={conf}')
            (Path('/etc/systemd/system') / name).write_text(rendered)
        run('systemctl', 'daemon-reload')
        run('systemd-analyze', 'verify', '--man=no', *map(str, unit_paths))
        tls = ssl.create_default_context(cafile=str(cert))
        cookie, csrf, epoch = '', '', ''

        def call(path, method='GET', body=None, bearer=None, anonymous=False, extra_headers=None, drop_response=False):
            headers, payload = {}, None
            if not anonymous:
                headers['Authorization' if bearer else 'Cookie'] = 'Bearer ' + bearer if bearer else cookie
            if body is not None:
                payload = json.dumps(body).encode()
                headers.update({'Content-Type': 'application/json', 'Origin': origin})
                if method != 'GET' and path != '/sessions':
                    headers['X-OVS-CSRF-Token'] = csrf
                    if 'request_id' in body:
                        headers.update({'X-OVS-Request-Epoch': epoch, 'Idempotency-Key': body['request_id']})
            headers.update(extra_headers or {})
            request = urllib.request.Request(origin + '/api/v1' + path, data=payload, headers=headers, method=method)
            try:
                response = urllib.request.urlopen(request, context=tls, timeout=8)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                if drop_response:
                    return response.status, None, response.headers
                raw = response.read()
                return response.status, (json.loads(raw) if raw else None), response.headers

        def login():
            nonlocal cookie, csrf, epoch
            code, value, headers = call('/sessions', 'POST', {'provider': 'local', 'username': 'admin', 'password': password}, anonymous=True)
            assert code == 201, (code, value)
            cookie = headers['Set-Cookie'].split(';')[0]
            credentials.append(cookie)
            csrf, epoch = value['csrf_token'], value['request_epochs']['management']

        def get(path):
            code, value, _ = call(path)
            assert code == 200, (path, code, value)
            return value

        def observed_ports(predicate):
            code, value, _ = call('/ports')
            return value if code == 200 and predicate(value) else None

        ovs_run('ovsdb-tool', 'create', str(conf), str(schema))
        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(lambda: call('/runtime', anonymous=True)[0] == 200)
        login()
        assert call('/ports', anonymous=True)[0] == 401
        assert call('/ports')[0] == 503
        assert get('/inventory')['instance_generation'] is None
        start_db()
        page = eventually(lambda: observed_ports(lambda p: p['source']['freshness'] == 'fresh'))
        assert page['items'] == [] and page['availability'] == 'complete'
        generation = page['instance_generation']
        schema_page = get('/inventory/schema')
        schema_tables = schema_page['items']
        while schema_page['next_cursor']:
            schema_page = get('/inventory/schema?cursor=' + urllib.parse.quote(schema_page['next_cursor']))
            schema_tables.extend(schema_page['items'])
        port_schema = next(item for item in schema_tables if item['name'] == 'Port')
        references = next(col for col in port_schema['columns'] if col['name'] == 'interfaces')['references']
        assert references == [{'table': 'Interface', 'strength': 'strong', 'position': 'key'}]
        checks.append('unavailable has no fabricated generation; genuinely empty monitored DB is complete; discovered types, references, indexes and mutability')

        ovs_run('ovs-vswitchd', f'unix:{db_socket}', '--enable-dummy', f'--pidfile={ovs}/switch.pid',
                f'--unixctl={ovs}/switch.ctl', '--detach', '--no-chdir')
        vsctl('add-br', 'br-inv', '--', 'set', 'Bridge', 'br-inv', 'datapath_type=dummy',
              '--', 'add-port', 'br-inv', 'inv-p1', '--', 'set', 'Interface', 'inv-p1', 'type=dummy',
              '--', 'add-port', 'br-inv', 'inv-p2', '--', 'set', 'Interface', 'inv-p2', 'type=dummy',
              '--', 'add-bond', 'br-inv', 'inv-bond', 'inv-b1', 'inv-b2',
              '--', 'set', 'Interface', 'inv-b1', 'type=dummy', '--', 'set', 'Interface', 'inv-b2', 'type=dummy')
        page = eventually(lambda: observed_ports(lambda p: len(p['items']) == 4))
        assert page['instance_generation'] == generation
        by_name = {p['name']: p for p in page['items']}
        assert by_name['br-inv']['local_port'] and by_name['inv-bond']['kind'] == 'bond'
        original = by_name['inv-p1']
        bridge = get('/bridges')['items'][0]
        interfaces = get('/interfaces')['items']
        assert {p['management_id'] for p in page['items']} == {r['id'] for r in bridge['port_refs']}
        bond = get('/bonds')['items'][0]
        assert bond['management_id'] == by_name['inv-bond']['management_id'] and len(bond['member_refs']) == 2
        assert all(i['port_ref']['id'] in {p['management_id'] for p in page['items']} for i in interfaces)
        assert any(i['internal'] for i in interfaces)
        assert all(p['allowed_operations'] == [] and p['linux_carrier']['value'] is None for p in page['items'])
        vsctl('set', 'Port', 'inv-p1', 'vlan_mode=native-untagged', 'tag=37', 'trunks=[]')
        updated = eventually(lambda: (p if (p := get('/ports/' + original['management_id']))['vlan']['native']['tag'] == 37 else None))
        assert updated['vlan']['native']['trunks'] == [] and updated['config_revision'] != original['config_revision']
        assert updated['instance_generation'] == generation and updated['ovs_uuid'] == original['ovs_uuid']
        checks.append('one snapshot for Bridge -> Port -> Interface, local internal port and Bond-as-Port; external VLAN changes retain identity and native empty-trunks semantics')

        first = get('/interfaces?limit=1')
        assert first['next_cursor']
        second = get('/interfaces?limit=1&cursor=' + urllib.parse.quote(first['next_cursor']))
        assert first['items'][0]['id'] != second['items'][0]['id'] and first['snapshot_id'] == second['snapshot_id']
        vsctl('set', 'Port', 'inv-p1', 'tag=38')
        eventually(lambda: get('/ports/' + original['management_id'])['vlan']['native']['tag'] == 38)
        assert call('/interfaces?limit=1&cursor=' + urllib.parse.quote(first['next_cursor']))[0] == 410
        token_command = {'request_id': request_id(), 'name': 'synthetic-inventory-observer',
                         'scopes': ['state.read', 'inventory.read'],
                         'expires_at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time() + 3600))}
        code, token, _ = call('/tokens', 'POST', token_command)
        assert code == 403 and token['code'] == 'REAUTHENTICATION_REQUIRED', (code, token)
        assert call('/session/reauthentication', 'POST', {'password': password})[0] == 200
        token_command['request_id'] = request_id()
        code, token, _ = call('/tokens', 'POST', token_command)
        assert code == 201, (code, token)
        credentials.append(token['secret'])
        code, filtered, _ = call('/ports', bearer=token['secret'])
        assert code == 200 and all(p['vlan']['native'] is None for p in filtered['items'])
        assert call('/interfaces?limit=1&cursor=' + urllib.parse.quote(first['next_cursor']), bearer=token['secret'])[0] == 410
        checks.append('snapshot-bound pagination rejects changed snapshot/scope; current token permissions withhold configuration fields')

        # Shared evidence runs through the real HTTPS -> Unix IPC -> manager
        # authority. Inventory-only tokens cannot inspect jobs or audit data.
        assert call('/audit', bearer=token['secret'])[0] == 403
        assert call('/jobs', bearer=token['secret'])[0] == 403
        role_command = {'request_id': request_id(), 'name': 'synthetic-evidence-role', 'capabilities': ['state.read']}
        code, accepted, _ = call('/roles', 'POST', role_command)
        assert code == 202, (code, accepted)
        shared_job = get('/jobs/' + accepted['job_id'])
        assert shared_job['state'] == 'succeeded' and shared_job['business_outcome'] == 'success'
        assert shared_job['applied_outcome'] == 'not-applicable' and shared_job['confirmation_state'] == 'not-applicable'
        assert shared_job['correlation_id'] == accepted['correlation_id']
        audit = get('/audit?correlation_id=' + accepted['correlation_id'])
        assert len(audit['items']) == 1 and audit['items'][0]['job_id'] == accepted['job_id']
        assert audit['items'][0]['request_id'] == role_command['request_id']
        audit_id = audit['items'][0]['id']
        assert get('/audit/' + audit_id)['id'] == audit_id
        assert get('/audit/export?correlation_id=' + accepted['correlation_id'])['items'][0]['id'] == audit_id
        assert get('/jobs/export?filter=createRole')['items'][0]['id'] == accepted['job_id']
        linked_events = get('/events?correlation_id=' + accepted['correlation_id'])['items']
        assert linked_events and linked_events[0]['job_id'] == accepted['job_id']
        assert get('/events/' + linked_events[0]['id'])['id'] == linked_events[0]['id']
        external = get('/events?origin=External')['items']
        assert external and all(item['actor_id'] is None and item['origin'] == 'External' for item in external)
        serialized = json.dumps([audit, linked_events, shared_job])
        assert all(secret not in serialized for secret in credentials)
        metrics['shared_evidence_verified'] = True
        from candidate_validation import verify_candidate
        metrics['candidate_validation'] = verify_candidate(
            call, get, vsctl, units, manager_db, web_db,
            lambda: stop_ovs('db'), start_db, eventually, credentials)
        checks.append('formal Candidate and Validation acceptance passed on the real HTTPS/IPC/OVSDB path; detailed checks in metrics.candidate_validation')

        if args.safe_apply_network or args.frontend_browser:
            from safe_apply import verify_safe_apply
            exercise = None
            if args.frontend_browser:
                from frontend import verify_frontend
                def exercise(network_metrics):
                    return verify_frontend(repo, fixture, args.browser_node, origin, password, call, get,
                                           vsctl, units, db_socket, ovs, conf, credentials, network_metrics)
            result = verify_safe_apply(call, get, login, vsctl, units, manager_db, web_db,
                                       eventually, fixture, config, cert, port, exercise=exercise)
            result.update({'platform': os.uname().machine, 'schema_fixture': args.schema_version,
                           'ovsdb_binary': ovs_run('ovsdb-server', '--version').splitlines()[0],
                           'switch_binary': ovs_run('ovs-vswitchd', '--version').splitlines()[0]})
            target = repo / ('test-results/go-frontend.json' if args.frontend_browser else 'test-results/go-safe-apply-vm.json')
            target.parent.mkdir(exist_ok=True)
            target.write_text(json.dumps(result, indent=2) + '\n')
            print(json.dumps(result, indent=2))
            return


        vsctl('del-port', 'br-inv', 'inv-p1')
        eventually(lambda: call('/ports/' + original['management_id'])[0] == 404)
        vsctl('add-port', 'br-inv', 'inv-p1', '--', 'set', 'Interface', 'inv-p1', 'type=dummy')
        recreated = eventually(lambda: next((p for p in get('/ports')['items'] if p['name'] == 'inv-p1'), None))
        assert recreated['management_id'] != original['management_id'] and recreated['ovs_uuid'] != original['ovs_uuid']
        assert recreated['instance_generation'] == generation

        lifecycle_validation = metrics['candidate_validation']['validation_id_for_lifecycle_checks']
        invalid = get('/validations/' + lifecycle_validation)
        assert not invalid['usable'] and any(g['code'] == 'OBJECT_BINDING_CHANGED' for g in invalid['invalidations'])
        assert get('/candidate')['state'] == 'reconciliation-required'
        metrics['candidate_validation']['recreated_object_invalidated'] = True
        with sqlite3.connect(manager_db) as db:
            assert db.execute('SELECT state FROM identities WHERE management_id=?', (original['management_id'],)).fetchone() == ('tombstone',)
        checks.append('delete/recreate with the same name gets a new native UUID and management ID; old ID is tombstoned and returns 404')

        stop_ovs('db')
        eventually(lambda: observed_ports(lambda p: p['source']['freshness'] == 'stale' and p['availability'] == 'degraded'))
        before = time.monotonic()
        start_db()
        page = eventually(lambda: observed_ports(lambda p: p['source']['freshness'] == 'fresh'))
        metrics['database_reconnect_seconds'] = round(time.monotonic() - before, 3)
        assert page['instance_generation'] == generation
        assert next(p for p in page['items'] if p['name'] == 'inv-p1')['management_id'] == recreated['management_id']
        fingerprint = hashlib.sha256(vsctl('--format=json', '--columns=_uuid,name,ports', 'list', 'Bridge').encode()).hexdigest()
        run('systemctl', 'kill', '--signal=SIGKILL', units['mgrd'])
        eventually(lambda: call('/runtime', anonymous=True)[0] == 200)
        page = eventually(lambda: observed_ports(lambda p: p['source']['freshness'] == 'fresh'))
        assert page['instance_generation'] == generation
        assert hashlib.sha256(vsctl('--format=json', '--columns=_uuid,name,ports', 'list', 'Bridge').encode()).hexdigest() == fingerprint
        checks.append('OVSDB process restart and mgrd SIGKILL preserve continuing conf.db identity; outage returns explicit stale state without OVS writes')

        # Copy-back has exactly the same root and row UUIDs. It must still be
        # detected from the independent file witness, not silently relinked.
        stop_ovs('switch')
        stop_ovs('db')
        copied = ovs / 'copy.db'
        shutil.copyfile(conf, copied)
        os.replace(copied, conf)
        start_db()
        state = eventually(lambda: (v if (v := get('/inventory'))['reconciliation_state'] == 'reconciliation-required' else None))
        assert state['lifecycle_reason'] == 'database-file-replaced-or-rewound'
        assert call('/ports')[0] == 503
        digest = state['reviewed_evidence_digest']
        run('systemctl', 'stop', units['mgrd'])
        bad = run(mgrd, '--database', str(manager_db), '--ovsdb-socket', str(db_socket), '--ovsdb-file', str(conf),
                  '--reconcile-ovsdb', '0' * 64, '--reconciliation-reason', 'synthetic reviewed copy restore', check=False)
        assert bad.returncode != 0
        run(mgrd, '--database', str(manager_db), '--ovsdb-socket', str(db_socket), '--ovsdb-file', str(conf),
            '--reconcile-ovsdb', digest, '--reconciliation-reason', 'synthetic reviewed copy restore')
        # Independent root-operated maintenance scenario: earlier deliberate
        # restarts must not consume this fixture's next start-limit budget.
        # The production unit and its automatic crash-restart limits are intact.
        run('systemctl', 'reset-failed', units['mgrd'])
        run('systemctl', 'start', units['mgrd'])
        page = eventually(lambda: observed_ports(lambda p: p['source']['freshness'] == 'fresh'))
        assert page['instance_generation'] != generation
        assert call('/ports/' + recreated['management_id'])[0] == 404

        invalid = get('/validations/' + lifecycle_validation)
        assert not invalid['usable'] and any(g['code'] == 'GENERATION_RECONCILIATION_REQUIRED' for g in invalid['invalidations'])
        metrics['candidate_validation']['real_generation_change_invalidated'] = True
        generation = page['instance_generation']
        with sqlite3.connect(manager_db) as db:
            assert db.execute("SELECT count(*) FROM auth_audit WHERE operation='inventory-explicit-reconciliation'").fetchone()[0] == 1
        checks.append('same-UUID copied DB is reconciliation-required; exact reviewed digest plus root-only reason resets bindings with Audit, never writes OVS')

        stop_ovs('db')
        replacement = ovs / 'new.db'
        ovs_run('ovsdb-tool', 'create', str(replacement), str(schema))
        ovs_run('ovsdb-tool', 'transact', str(replacement), json.dumps(['Open_vSwitch', {'op': 'insert', 'table': 'Open_vSwitch', 'row': {}}]))
        os.replace(replacement, conf)
        start_db()
        page = eventually(lambda: observed_ports(lambda p: p['source']['freshness'] == 'fresh' and p['instance_generation'] != generation))
        assert page['items'] == []
        generation = page['instance_generation']
        run('systemctl', 'stop', units['mgrd'])
        run(mgrd, '--database', str(manager_db), '--prepare-restore-security')
        run('systemctl', 'reset-failed', units['mgrd'])
        run('systemctl', 'start', units['mgrd'])
        eventually(lambda: call('/runtime', anonymous=True)[0] == 200)
        assert call('/inventory')[0] == 401
        login()
        state = eventually(lambda: (v if (v := get('/inventory'))['reconciliation_state'] == 'reconciliation-required' else None))
        assert state['lifecycle_reason'] == 'management-restore' and call('/ports')[0] == 503
        assert not get('/runtime')['configuration_ready']
        checks.append('fresh initialized DB with broken root/anchors creates a new generation; explicit manager restore revokes cookie and blocks old identity until reconciliation')

        evidence = {'platform': os.uname().machine, 'schema_fixture': args.schema_version,
                    'schema_sha256': hashlib.sha256(schema.read_bytes()).hexdigest(),
                    'ovsdb_binary': ovs_run('ovsdb-server', '--version').splitlines()[0],
                    'switch_binary': ovs_run('ovs-vswitchd', '--version').splitlines()[0],
                    'metrics': metrics, 'checks': checks}
        destination = repo / f'test-results/go-inventory-{args.schema_version}.json'
        destination.parent.mkdir(exist_ok=True)
        destination.write_text(json.dumps(evidence, indent=2) + '\n')
        print(json.dumps(evidence, indent=2))
    finally:
        journal = []
        for name in units.values():
            journal.append(run('journalctl', '-u', name, '--no-pager', '-o', 'cat', check=False).stdout)
            run('systemctl', 'stop', name, check=False)
        text = '\n'.join(journal)
        for credential in credentials:
            assert credential not in text, 'credential leaked to daemon journal'
        target = repo / ('test-results/go-frontend-journal.log' if args.frontend_browser else 'test-results/go-safe-apply-vm-journal.log' if args.safe_apply_network else f'test-results/go-inventory-{args.schema_version}-journal.log')
        target.parent.mkdir(exist_ok=True)
        target.write_text(text)
        for name in ('switch', 'db'):
            stop_ovs(name)
        for path in unit_paths:
            path.unlink(missing_ok=True)
        run('systemctl', 'daemon-reload', check=False)
        if created_account:
            run('userdel', account_name, check=False)
        for path in (fixture, runtime, data):
            if path.exists():
                shutil.rmtree(path)


if __name__ == '__main__':
    main()
