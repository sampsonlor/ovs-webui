"""Ephemeral Linux CI only: real systemd services and isolated OVS dummy datapath.

Never invoked by a production binary, installer or normal local pnpm command.
Every unit, socket and database created here has a unique fixture identity.
"""
import argparse
from datetime import datetime, timezone
import grp
import hashlib
import http.client
import json
import os
from pathlib import Path
import platform
import pwd
import re
import shutil
import signal
import socket
import ssl
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def run(*args, env=None, check=True, input_text=None):
    result = subprocess.run(args, env=env, input=input_text, capture_output=True, text=True, timeout=20)
    if check and result.returncode:
        raise RuntimeError(f'{args[0]} failed: {result.stderr.strip()}')
    return result.stdout


def cleanup_run(*args, env=None):
    try:
        return run(*args, env=env, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        print(f'fixture cleanup command failed: {error}')
        return ''


def eventually(check, timeout=12):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
        except (OSError, urllib.error.URLError, RuntimeError) as error:
            last = error
        time.sleep(0.1)
    raise AssertionError(f'timed out waiting for fixture: {last}')


def ensure_canonical_fixture_isolation(units, account):
    release = platform.freedesktop_os_release()
    if (os.environ.get('OVS_CI_SYSTEMD_RESTART') != '1'
            or os.environ.get('GITHUB_ACTIONS') != 'true'
            or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted'
            or release.get('ID') != 'ubuntu' or release.get('VERSION_ID') != '24.04'):
        raise RuntimeError('Canonical systemd fixture requires the dedicated disposable GitHub-hosted Ubuntu job')
    for lookup in (pwd.getpwnam, grp.getgrnam):
        try:
            lookup(account)
        except KeyError:
            continue
        raise RuntimeError(f'Canonical fixture account or group already exists: {account}')
    libexec = Path('/usr/libexec')
    if libexec.is_symlink() or any(libexec.glob('ovs-*')):
        raise RuntimeError('Existing canonical executable directory or artifacts')
    paths = [Path(path) for path in run('systemd-analyze', 'unit-paths').splitlines() if path.startswith('/')]
    if Path('/etc/systemd/system') not in paths:
        raise RuntimeError('Cannot establish systemd unit search paths')
    for name in units.values():
        state = run('systemctl', 'show', '--property=LoadState', '--value', name, check=False).strip()
        if state != 'not-found':
            raise RuntimeError(f'Canonical service is not absent: {name} ({state})')
        for directory in paths:
            for suffix in ('', '.d', '.wants', '.requires', '.upholds'):
                if os.path.lexists(directory / (name + suffix)):
                    raise RuntimeError(f'Existing canonical systemd artifact: {name}{suffix}')


def canonical_https_web_restart(origin, tls, units, password, ready, forwarding):
    from authentication import request_id

    def call(path, method='GET', body=None, cookie='', csrf='', extra=None):
        headers = {'Accept': 'application/json', **(extra or {})}
        if body is not None:
            headers.update({'Content-Type': 'application/json', 'Origin': origin})
        if cookie:
            headers['Cookie'] = cookie
        if csrf:
            headers['X-OVS-CSRF-Token'] = csrf
        payload = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(origin + '/api/v1' + path, data=payload, headers=headers, method=method)
        try:
            response = urllib.request.urlopen(request, context=tls, timeout=6)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, json.loads(response.read()), response.headers

    def identity(service):
        output = run('systemctl', 'show', '-p', 'InvocationID', '-p', 'MainPID', '-p', 'ActiveState', units[service])
        values = dict(line.split('=', 1) for line in output.splitlines() if '=' in line)
        if values.get('ActiveState') == 'active' and values.get('InvocationID') and int(values.get('MainPID', '0')):
            return values['InvocationID'], values['MainPID']
        return None

    code, session, headers = call('/sessions', 'POST', {'provider': 'local', 'username': 'admin', 'password': password})
    assert code == 201, f'canonical restart local login failed: HTTP {code}'
    cookie = headers['Set-Cookie'].split(';', 1)[0]
    assert cookie.startswith('__Host-ovs_session=')
    code, elevated, _ = call('/session/reauthentication', 'POST', {'password': password}, cookie, session['csrf_token'])
    assert code == 200, f'canonical restart elevation failed: HTTP {code}'
    assert elevated['principal_id'] == session['principal_id']
    assert 'management.services.control' in elevated['effective_capabilities']
    assert datetime.fromisoformat(elevated['elevated_until']) > datetime.now(timezone.utc)

    web_before, manager_before = identity('webd'), identity('mgrd')
    assert web_before and manager_before, 'both canonical service invocations must be active before dispatch'
    forwarding()
    original = request_id()
    epoch = elevated['request_epochs']['management']
    accepted = None
    reply = 'acknowledged'
    try:
        code, accepted, _ = call('/health/service-restarts', 'POST', {'service': 'web', 'request_id': original},
                                 cookie, elevated['csrf_token'], {'Idempotency-Key': original, 'X-OVS-Request-Epoch': epoch})
    except (OSError, http.client.HTTPException, json.JSONDecodeError):
        reply = 'transport-interrupted'
    else:
        if code in (502, 503, 504) and accepted.get('command_effect') == 'unknown':
            accepted = None
            reply = 'http-outcome-unconfirmed'
        else:
            assert code == 202, f'canonical web restart was not admitted: HTTP {code}, {accepted.get("code")}'
            assert (accepted['request_id'], accepted['request_domain'], accepted['request_epoch']) == (original, 'management', epoch)

    def replacement():
        current = identity('webd')
        return current if current and current[0] != web_before[0] and current[1] != web_before[1] else None

    web_after = eventually(replacement, timeout=20)
    eventually(ready)

    # Reconnect using only the original identity, even if the self-restart
    # closed the HTTPS response. A second restart POST would invalidate proof.
    def read(path):
        status, value, _ = call(path, cookie=cookie)
        if status in (404, 503):
            return None
        assert status == 200, f'canonical restart evidence read failed: HTTP {status}'
        return value

    def completed_receipt():
        receipt = read(f'/requests/{original}?domain=management&epoch={epoch}')
        return receipt if receipt and receipt['state'] == 'completed' else None

    receipt = eventually(completed_receipt, timeout=20)
    assert (receipt['request_id'], receipt['request_domain'], receipt['request_epoch']) == (original, 'management', epoch)
    assert receipt['job_ref']['kind'] == 'job' and receipt['resource_ref'] == receipt['job_ref']
    job_id = receipt['job_ref']['id']
    if accepted is not None:
        assert accepted['job_id'] == job_id and accepted['correlation_id'] == receipt['correlation_id']
        assert accepted['resource_ref'] == receipt['resource_ref']
    job = read('/jobs/' + job_id)
    assert job and job['id'] == job_id and job['operation'] == 'restartManagementService'
    assert job['owner_id'] == elevated['principal_id']
    assert job['correlation_id'] == receipt['correlation_id'] and job['request_id'] == original
    assert job['business_outcome'] == 'unknown' and job['reason_code'] == 'restart-new-invocation-observed'

    audit = read('/audit?limit=100')
    records = [item for item in audit['items'] if item['operation'] == job['operation'] and item.get('request_id') == original]
    assert len(records) == 2 and all(item['job_id'] == job_id and item['correlation_id'] == receipt['correlation_id'] for item in records)
    assert any(item['result'] == 'restart-admitted' for item in records)
    assert any(item['result'] == 'unknown' and item['reason_code'] == job['reason_code'] for item in records)
    assert identity('mgrd') == manager_before and identity('webd') == web_after
    forwarding()
    return {'http_reply': reply, 'receipt_state': receipt['state'], 'job_state': job['state'],
            'business_outcome': job['business_outcome'], 'job_reason': job['reason_code'], 'matching_audit_records': len(records)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    parser.add_argument('--systemd-restart-probe', action='store_true')
    args = parser.parse_args()
    assert os.geteuid() == 0, 'This isolated CI test needs root for systemd/UID evidence'
    assert Path('/run/systemd/system').is_dir(), 'A real systemd host is required'
    source = Path(args.bin_dir).resolve()
    repo = Path(__file__).resolve().parents[2]
    suffix = uuid.uuid4().hex[:12]
    fixture = Path('/var/lib') / f'ovs-webui-fixture-{suffix}'
    runtime_name = f'ovs-webui-runtime-{suffix}'
    runtime = Path('/run') / runtime_name
    canonical = args.systemd_restart_probe
    units = {service: f'ovs-{service}{"" if canonical else "-ci-" + suffix}.service'
             for service in ('mgrd', 'webd')}
    account_name = 'ovs-webui-web' if canonical else f'ovs-ci-{suffix}'
    if canonical:
        ensure_canonical_fixture_isolation(units, account_name)
        if not os.access(source / 'systemd-restart.test', os.X_OK):
            raise RuntimeError('The genuine systemd probe executable is required')
    unit_paths = []
    canonical_binaries = []
    created_libexec = False
    account_created = False
    data_name = f'ovs-webui-ci-{suffix}'
    data_root = Path('/var/lib') / data_name
    fixture.mkdir(mode=0o700)
    ovs = fixture / 'ovs'
    ovs.mkdir(mode=0o700)
    env = dict(os.environ, OVS_RUNDIR=str(ovs), OVS_LOGDIR=str(ovs), OVS_DBDIR=str(ovs))
    checks = []
    try:
        run('useradd', '--system', '--no-create-home', '--home-dir', '/nonexistent',
            '--shell', '/usr/sbin/nologin', '--user-group', account_name)
        account_created = True
        account = pwd.getpwnam(account_name)
        os.chown(fixture, 0, account.pw_gid)
        fixture.chmod(0o750)
        binary_dir = Path('/usr/libexec') if canonical else fixture
        if canonical and not binary_dir.exists():
            binary_dir.mkdir(mode=0o755)
            created_libexec = True
        for service in units:
            target = binary_dir / f'ovs-{service}'
            with (source / target.name).open('rb') as original, target.open('xb') as new:
                if canonical:
                    canonical_binaries.append(target)
                shutil.copyfileobj(original, new)
            target.chmod(0o755)
        data_root.mkdir(mode=0o755)
        manager_dir, web_dir = data_root / 'manager', data_root / 'web'
        manager_dir.mkdir(mode=0o700)
        web_dir.mkdir(mode=0o700)
        os.chown(web_dir, account.pw_uid, account.pw_gid)
        manager_database, web_database = manager_dir / 'manager.db', web_dir / 'web.db'
        run(str(binary_dir / 'ovs-mgrd'), '--init-database', '--database', str(manager_database))
        run('runuser', '-u', account_name, '--', str(binary_dir / 'ovs-webd'),
            '--init-database', '--database', str(web_database))
        if canonical:
            restart_password = 'synthetic-restart-' + uuid.uuid4().hex
            run(str(binary_dir / 'ovs-mgrd'), '--init-auth-key', '--database', str(manager_database))
            run(str(binary_dir / 'ovs-mgrd'), '--bootstrap-admin', 'admin', '--database', str(manager_database),
                input_text=restart_password + '\n')
            run('runuser', '-u', account_name, '--', str(binary_dir / 'ovs-webd'),
                '--init-session-key', '--database', str(web_database))
        else:
            with sqlite3.connect(manager_database) as connection:
                connection.execute("INSERT INTO principals VALUES('fixture-user','synthetic-user',1,?)", (b'fixture-verifier',))
            connection.close()
            with sqlite3.connect(web_database) as connection:
                connection.execute("INSERT INTO metadata VALUES('label','fixture-user','fixture-label',?)", (b'{"synthetic":true}',))
            connection.close()
            database_hashes = {path: hashlib.sha256(path.read_bytes()).hexdigest() for path in (manager_database, web_database)}
        denied = subprocess.run(['runuser', '-u', account_name, '--', 'test', '-r', str(manager_database)], check=False)
        assert denied.returncode != 0, 'webd identity can read manager authority'
        cert, key = fixture / 'server.crt', fixture / 'server.key'
        run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
            '-subj', '/CN=localhost', '-addext', 'subjectAltName=IP:127.0.0.1,DNS:localhost',
            '-keyout', str(key), '-out', str(cert))
        os.chown(key, 0, account.pw_gid)
        key.chmod(0o640)
        cert.chmod(0o644)
        with socket.socket() as reserve:
            reserve.bind(('127.0.0.1', 0))
            port = reserve.getsockname()[1]
        origin = f'https://127.0.0.1:{port}'
        configuration = fixture / 'runtime.env'
        configuration.write_text(
            f'WEBD_UID={account.pw_uid}\nWEBD_GID={account.pw_gid}\nMANAGER_SOCKET={runtime}/mgrd.sock\n'
            f'HTTPS_LISTEN=127.0.0.1:{port}\nTLS_CERT={cert}\nTLS_KEY={key}\nPUBLIC_ORIGIN={origin if canonical else ""}\n'
            f'MANAGER_DATABASE={manager_database}\nWEB_DATABASE={web_database}\n'
            + ('MANAGEMENT_SERVICE_RESTART=true\n' if canonical else ''))
        for service, name in units.items():
            template = (repo / 'packaging/systemd' / f'ovs-{service}.service').read_text()
            assert not re.search(r'^(PartOf|BindsTo|Requires|ExecStop)=', template, re.M)
            assert 'openvswitch' not in template and 'ovs-vswitchd' not in template
            # Replace template paths before inserting fixture paths that share
            # the same prefix. Each test owns a dedicated service identity.
            rendered = template.replace('/run/ovs-webui', str(runtime))
            rendered = rendered.replace('RuntimeDirectory=ovs-webui', f'RuntimeDirectory={runtime_name}')
            rendered = rendered.replace('StateDirectory=ovs-webui/', f'StateDirectory={data_name}/')
            rendered = rendered.replace('ovs-webui-web', account_name)
            rendered = rendered.replace('/etc/ovs-webui/runtime.env', str(configuration))
            rendered = rendered.replace(f'/usr/libexec/ovs-{service}', str(binary_dir / f'ovs-{service}'))
            if service == 'mgrd':
                # This bootstrap/storage fixture requires no provider writes to
                # manager.db. Never connect accidentally to the runner's OVS.
                rendered = rendered.replace('--database=${MANAGER_DATABASE}',
                    f'--database=${{MANAGER_DATABASE}} --ovsdb-socket={ovs}/unconfigured.sock --ovsdb-file={ovs}/conf.db')
            rendered = rendered.replace('After=network.target ovs-mgrd.service', f'After=network.target {units["mgrd"]}')
            unit_path = Path('/etc/systemd/system') / name
            with unit_path.open('x') as unit_file:
                unit_paths.append(unit_path)
                unit_file.write(rendered)
        run('systemctl', 'daemon-reload')
        run('systemd-analyze', 'verify', '--man=no', *map(str, unit_paths))
        checks.append('actual hardened systemd unit validation')

        db = ovs / 'conf.db'
        db_socket = ovs / 'db.sock'
        run('ovsdb-tool', 'create', str(db), '/usr/share/openvswitch/vswitch.ovsschema', env=env)
        run('ovsdb-server', str(db), f'--remote=punix:{db_socket}', f'--pidfile={ovs}/db.pid',
            f'--unixctl={ovs}/db.ctl', '--detach', '--no-chdir', env=env)
        run('ovs-vswitchd', f'unix:{db_socket}', '--enable-dummy', f'--pidfile={ovs}/switch.pid',
            f'--unixctl={ovs}/switch.ctl', '--detach', '--no-chdir', env=env)

        def vsctl(*cmd):
            return run('ovs-vsctl', '--timeout=5', f'--db=unix:{db_socket}', *cmd, env=env)

        vsctl('add-br', 'br-ci', '--', 'set', 'Bridge', 'br-ci', 'datapath_type=dummy',
              '--', 'add-port', 'br-ci', 'ci-p1', '--', 'set', 'Interface', 'ci-p1', 'type=dummy', 'ofport_request=1',
              '--', 'add-port', 'br-ci', 'ci-p2', '--', 'set', 'Interface', 'ci-p2', 'type=dummy', 'ofport_request=2')
        of_socket = f'unix:{ovs}/br-ci.mgmt'
        run('ovs-ofctl', 'add-flow', of_socket, 'in_port=1,actions=output:2', env=env)
        ovs_pids = [(ovs / file).read_text().strip() for file in ('db.pid', 'switch.pid')]

        def fingerprint():
            data = [vsctl('--format=json', f'--columns={columns}', 'list', table) for table, columns in (
                ('Bridge', '_uuid,name,ports,datapath_type'),
                ('Port', '_uuid,name,interfaces,tag,trunks,vlan_mode'),
                ('Interface', '_uuid,name,type,options'))]
            return hashlib.sha256('\n'.join(data).encode()).hexdigest()

        baseline = fingerprint()

        def forwarding():
            def transmitted():
                output = run('ovs-ofctl', 'dump-ports', of_socket, '2', env=env)
                return int(re.search(r'tx pkts=(\d+)', output)[1])
            before = transmitted()
            packet = ('0200000000020200000000010806' '0001080006040001'
                      '020000000001c0000201' '000000000000c0000202')
            run('ovs-appctl', '-t', str(ovs / 'switch.ctl'), 'netdev-dummy/receive', 'ci-p1', packet, env=env)
            eventually(lambda: transmitted() > before)
            assert fingerprint() == baseline, 'management lifecycle changed OVS configuration'
            assert ovs_pids == [(ovs / file).read_text().strip() for file in ('db.pid', 'switch.pid')]
            for pid in ovs_pids:
                os.kill(int(pid), 0)

        context = ssl.create_default_context(cafile=str(cert))

        def fetch(path):
            try:
                with urllib.request.urlopen(f'https://127.0.0.1:{port}{path}', context=context, timeout=4) as response:
                    return response.status, response.read().decode()
            except urllib.error.HTTPError as response:
                return response.code, response.read().decode()

        def ready():
            status, body = fetch('/readyz')
            if status != 200:
                return False
            result = json.loads(body)
            assert result['scope'] == 'runtime-bootstrap'
            assert not result['configuration_ready'] and result['authentication_ready'] is canonical
            assert result['storage']['manager']['writable'] and result['storage']['web']['writable']
            return True

        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(ready)
        manager_pid = run('systemctl', 'show', '-p', 'MainPID', '--value', units['mgrd']).strip()
        web_pid = run('systemctl', 'show', '-p', 'MainPID', '--value', units['webd']).strip()
        assert re.search(r'^Uid:\s+0\s+0\s+0\s+0', Path(f'/proc/{manager_pid}/status').read_text(), re.M)
        assert re.search(r'^Uid:' + (rf'\s+{account.pw_uid}' * 4) + r'\s*$', Path(f'/proc/{web_pid}/status').read_text(), re.M)
        assert (runtime / 'mgrd.sock').stat().st_mode & 0o777 == 0o660
        checks.append('root mgrd and non-root webd communicate through credential-checked IPC and HTTPS')
        status, body = fetch('/api/v1/transactions')
        problem = json.loads(body)
        assert status == 401 and problem['code'] == 'UNAUTHENTICATED'
        assert problem['command_effect'] == 'unknown' and problem['correlation_id']
        assert json.loads(fetch('/api/v1/contract')[1])['major'] == 1
        assert json.loads(fetch('/api/v1/openapi.json')[1])['openapi'] == '3.1.1'
        assert json.loads(fetch('/api/v1/runtime')[1])['authentication_ready'] is canonical
        checks.append('public OpenAPI v1, structured Problem and closed production authority')
        with socket.create_connection(('127.0.0.1', port), timeout=2) as plain:
            plain.sendall(b'GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n')
            assert b'200 OK' not in plain.recv(4096)
        checks.append('TLS-only listener and anonymous business API denied' if canonical else
                      'TLS-only listener and unavailable business API')
        forwarding()
        if canonical:
            probe = fixture / 'systemd-restart.test'
            shutil.copyfile(source / 'systemd-restart.test', probe)
            probe.chmod(0o700)
            for target in ('web', 'manager'):
                # Exercise the same restrictions that apply to busctl when the
                # manager invokes it from its hardened unit.
                transcript = subprocess.run([
                    'systemd-run', '--wait', '--collect', '--pipe', '--quiet',
                    '--unit', f'ovs-restart-probe-{suffix}-{target}',
                    '--property=NoNewPrivileges=true',
                    '--property=CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW',
                    '--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK',
                    '--property=ProtectSystem=strict', '--property=ProtectHome=true',
                    '--property=PrivateTmp=true', '--property=PrivateDevices=true',
                    '--property=MemoryDenyWriteExecute=true', '--property=RuntimeMaxSec=20',
                    '--setenv=OVS_CI_SYSTEMD_RESTART=1',
                    '--setenv=GITHUB_ACTIONS=true', '--setenv=RUNNER_ENVIRONMENT=github-hosted',
                    '--setenv=OVS_CI_SYSTEMD_RESTART_TARGET=' + target,
                    str(probe), '-test.run', '^TestNativeSystemdRestartInHostedUbuntu$',
                    '-test.v', '-test.timeout=18s',
                ], check=False, timeout=25, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                print(transcript.stdout, end='')
                transcript.check_returncode()
                assert f'replaced only the {target} invocation' in transcript.stdout
                eventually(ready)
                forwarding()
                checks.append(f'canonical {target} invocation replaced independently with OVS state and forwarding preserved')
            https = canonical_https_web_restart(origin, context, units, restart_password, ready, forwarding)
            checks.append('authenticated HTTPS web restart recovered the original receipt, Job and Audit while manager and OVS remained intact')
            destination = repo / 'test-results/go-systemd-restart.json'
            destination.parent.mkdir(exist_ok=True)
            destination.write_text(json.dumps({'architecture': os.uname().machine, 'checks': checks, 'https_web_restart': https}, indent=2) + '\n')
            return
        run('systemctl', 'kill', '--signal=KILL', '--kill-whom=main', units['mgrd'])
        eventually(lambda: fetch('/readyz')[0] == 503, timeout=1.5)
        assert fetch('/healthz')[0] == 200
        forwarding()
        eventually(ready)
        assert run('systemctl', 'show', '-p', 'MainPID', '--value', units['mgrd']).strip() != manager_pid
        checks.append('mgrd SIGKILL: systemd restart, stale socket recovery, webd liveness and OVS forwarding preserved')
        run('systemctl', 'kill', '--signal=KILL', '--kill-whom=main', units['webd'])
        forwarding()
        eventually(ready)
        assert run('systemctl', 'show', '-p', 'MainPID', '--value', units['webd']).strip() != web_pid
        checks.append('webd SIGKILL: independent restart and OVS forwarding preserved')
        run('systemctl', 'stop', units['webd'])
        assert run('systemctl', 'is-active', units['mgrd']).strip() == 'active'
        forwarding()
        run('systemctl', 'stop', units['mgrd'])
        forwarding()
        checks.append('graceful management shutdown preserves both OVS processes, native configuration and packet forwarding')
        for path, expected in database_hashes.items():
            assert hashlib.sha256(path.read_bytes()).hexdigest() == expected, 'committed database state changed across restarts'
        checks.append('separate private web and manager databases preserve committed fixture data across both process restarts')

        manager_original = manager_database.with_suffix('.original')
        manager_database.rename(manager_original)
        corruption = b'synthetic damaged manager database'
        manager_database.write_bytes(corruption)
        manager_database.chmod(0o600)
        run('systemctl', 'start', units['mgrd'], units['webd'])

        def degraded(component, code):
            status, body = fetch('/readyz')
            if status != 503:
                return False
            result = json.loads(body)
            storage = result.get('storage', {}).get(component, {})
            return storage.get('code') == code and storage.get('writable') is False

        eventually(lambda: degraded('manager', 'STORAGE_INTEGRITY_FAILED'))
        assert fetch('/healthz')[0] == 200
        forwarding()
        run('systemctl', 'stop', units['mgrd'])
        assert manager_database.read_bytes() == corruption, 'damaged authority database was overwritten'
        manager_database.rename(manager_database.with_suffix('.corrupt-evidence'))
        run('systemctl', 'start', units['mgrd'])
        eventually(lambda: degraded('manager', 'STORAGE_MISSING'))
        assert not manager_database.exists(), 'missing authority database recreated automatically'
        forwarding()
        run('systemctl', 'stop', units['mgrd'], units['webd'])
        # Restore only the unchanged, synthetic fixture to test the other fault.
        manager_original.rename(manager_database)
        web_database.write_bytes(b'synthetic damaged web database')
        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(lambda: degraded('web', 'STORAGE_INTEGRITY_FAILED'))
        assert fetch('/healthz')[0] == 200
        forwarding()
        checks.append('missing/corrupt manager DB and corrupt web DB produce explicit 503 storage degradation without recreation or OVS impact')
        result = {'architecture': os.uname().machine, 'ovs': run('ovs-vswitchd', '--version').splitlines()[0], 'checks': checks}
        destination = repo / 'test-results/go-runtime-smoke.json'
        destination.parent.mkdir(exist_ok=True)
        destination.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
    finally:
        journal = []
        for path in reversed(unit_paths):
            name = path.name
            journal.append(cleanup_run('journalctl', '--no-pager', '-n', '40', '-u', name))
            cleanup_run('systemctl', 'stop', name)
        log_name = 'go-systemd-restart-journal.log' if canonical else 'go-runtime-journal.log'
        log = repo / 'test-results' / log_name
        log.parent.mkdir(exist_ok=True)
        log.write_text('\n'.join(journal))
        print(log.read_text())
        for control in ('switch.ctl', 'db.ctl'):
            if (ovs / control).exists():
                cleanup_run('ovs-appctl', '-t', str(ovs / control), 'exit', env=env)
        for path in unit_paths:
            path.unlink(missing_ok=True)
        if unit_paths:
            cleanup_run('systemctl', 'daemon-reload')
        for path in canonical_binaries:
            path.unlink(missing_ok=True)
        if created_libexec:
            Path('/usr/libexec').rmdir()
        # These absolute paths were generated above under /run for this fixture.
        assert fixture.parent == Path('/var/lib') and fixture.name.startswith('ovs-webui-fixture-')
        shutil.rmtree(fixture)
        if data_root.exists():
            assert data_root.parent == Path('/var/lib') and data_root.name == data_name
            shutil.rmtree(data_root)
        if account_created:
            cleanup_run('userdel', account_name)
            cleanup_run('groupdel', account_name)


if __name__ == '__main__':
    main()
