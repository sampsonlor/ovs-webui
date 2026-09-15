"""Ephemeral Linux CI: real systemd/UID/Unix IPC/HTTPS authentication evidence.

All credentials are synthetic and scoped to the unique, removed CI fixture.
Never run by an installer, deployment, or the production binaries.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
import hashlib
import json
import os
from pathlib import Path
import pwd
import secrets
import shutil
import socket
import sqlite3
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def run(*args, input_text=None, check=True):
    result = subprocess.run(args, input=input_text, capture_output=True, text=True, timeout=30)
    if check and result.returncode:
        raise RuntimeError(f'{args[0]} failed with exit {result.returncode}')
    return result


def eventually(check, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            value = check()
            if value:
                return value
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.1)
    raise AssertionError('authentication fixture readiness deadline')


def request_id():
    value = (int(time.time() * 1000) << 80) | secrets.randbits(80)
    value = (value & ~(15 << 76)) | (7 << 76)
    value = (value & ~(3 << 62)) | (2 << 62)
    return str(uuid.UUID(int=value))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    args = parser.parse_args()
    assert os.geteuid() == 0 and Path('/run/systemd/system').is_dir()
    repo = Path(__file__).resolve().parents[2]
    source = Path(args.bin_dir).resolve()
    suffix = uuid.uuid4().hex[:12]
    fixture = Path('/run') / f'ovs-webui-auth-{suffix}'
    runtime_name = f'ovs-webui-auth-runtime-{suffix}'
    runtime = Path('/run') / runtime_name
    data_name = f'ovs-webui-auth-{suffix}'
    data = Path('/var/lib') / data_name
    account_name = f'ovs-auth-{suffix}'
    units = {s: f'ovs-{s}-auth-{suffix}.service' for s in ('mgrd', 'webd')}
    unit_paths = [Path('/etc/systemd/system') / name for name in units.values()]
    created_account = False
    fixture.mkdir(mode=0o700)
    password = 'synthetic-ci-password-' + secrets.token_hex(12)
    credentials = [password]
    checks = []
    metrics = {}
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
        assert run(mgrd, '--init-auth-key', '--database', str(manager_db), check=False).returncode != 0
        run(mgrd, '--bootstrap-admin', 'admin', '--database', str(manager_db), input_text=password + '\n')
        assert run(mgrd, '--bootstrap-admin', 'other', '--database', str(manager_db), input_text=password, check=False).returncode != 0
        run('runuser', '-u', account_name, '--', webd, '--init-database', '--database', str(web_db))
        run('runuser', '-u', account_name, '--', webd, '--init-session-key', '--database', str(web_db))
        assert run('runuser', '-u', account_name, '--', 'test', '-r', str(manager_db), check=False).returncode != 0
        assert run('runuser', '-u', account_name, '--', 'test', '-r', str(manager_dir / 'auth.key'), check=False).returncode != 0
        checks.append('explicit root bootstrap, no key overwrite, manager DB/key inaccessible to webd UID')
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
        config = fixture / 'runtime.env'
        config.write_text(
            f'WEBD_UID={account.pw_uid}\nWEBD_GID={account.pw_gid}\nMANAGER_SOCKET={runtime}/mgrd.sock\n'
            f'HTTPS_LISTEN=127.0.0.1:{port}\nTLS_CERT={cert}\nTLS_KEY={key}\nPUBLIC_ORIGIN={origin}\n'
            f'MANAGER_DATABASE={manager_db}\nWEB_DATABASE={web_db}\n')
        config.chmod(0o600)
        for service, name in units.items():
            template = (repo / 'packaging/systemd' / f'ovs-{service}.service').read_text()
            rendered = template.replace('/run/ovs-webui', str(runtime))
            rendered = rendered.replace('RuntimeDirectory=ovs-webui', f'RuntimeDirectory={runtime_name}')
            rendered = rendered.replace('StateDirectory=ovs-webui/', f'StateDirectory={data_name}/')
            rendered = rendered.replace('ovs-webui-web', account_name)
            rendered = rendered.replace('/etc/ovs-webui/runtime.env', str(config))
            rendered = rendered.replace(f'/usr/libexec/ovs-{service}', str(fixture / f'ovs-{service}'))
            if service == 'mgrd':
                rendered = rendered.replace('--database=${MANAGER_DATABASE}',
                    f'--database=${{MANAGER_DATABASE}} --ovsdb-socket={fixture}/unconfigured.sock --ovsdb-file={fixture}/absent.db')
            rendered = rendered.replace('After=network.target ovs-mgrd.service', f'After=network.target {units["mgrd"]}')
            (Path('/etc/systemd/system') / name).write_text(rendered)
        run('systemctl', 'daemon-reload')
        run('systemd-analyze', 'verify', '--man=no', *map(str, unit_paths))
        tls = ssl.create_default_context(cafile=str(cert))

        def call(path, method='GET', body=None, cookie='', csrf='', bearer='', extra=None):
            headers = dict(extra or {})
            payload = None
            if body is not None:
                payload = json.dumps(body).encode()
                headers['Content-Type'] = 'application/json'
            if cookie:
                headers['Cookie'] = cookie
            if csrf:
                headers['X-OVS-CSRF-Token'] = csrf
            if bearer:
                headers['Authorization'] = 'Bearer ' + bearer
            req = urllib.request.Request(origin + '/api/v1' + path, data=payload, headers=headers, method=method)
            try:
                response = urllib.request.urlopen(req, context=tls, timeout=6)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                return response.status, (json.loads(raw) if raw else None), response.headers

        def ready():
            code, value, _ = call('/runtime')
            return code == 200 and value['authentication_ready'] and not value['configuration_ready']

        def login(username):
            before = time.monotonic()
            code, value, headers = call('/sessions', 'POST', {'provider': 'local', 'username': username, 'password': password}, extra={'Origin': origin})
            assert code == 201, f'local authentication failed: {code}'
            raw = headers['Set-Cookie']
            for attribute in ('Secure', 'HttpOnly', 'SameSite=Strict', 'Path=/'):
                assert attribute in raw
            assert 'Domain=' not in raw and raw.startswith('__Host-ovs_session=')
            assert 'ovsg_' not in json.dumps(value) and password not in json.dumps(value)
            cookie = raw.split(';', 1)[0]
            credentials.extend([cookie.split('=', 1)[1], value['csrf_token']])
            return cookie, value, int((time.monotonic() - before) * 1000)

        def reauth(cookie, session):
            code, value, _ = call('/session/reauthentication', 'POST', {'password': password}, cookie, session['csrf_token'], extra={'Origin': origin})
            assert code == 200, f'reauthentication failed: {code}'
            assert value['expires_at'] == session['expires_at']

        def command(path, body, cookie, session, method='POST', etag=None, request=None):
            request = request or request_id()
            body = dict(body, request_id=request)
            headers = {'Origin': origin, 'Idempotency-Key': request, 'X-OVS-Request-Epoch': session['request_epochs']['management']}
            if etag:
                headers['If-Match'] = '"' + etag + '"'
            return request, call(path, method, body, cookie, session['csrf_token'], extra=headers)

        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(ready)
        assert call('/sessions', 'POST', {'provider': 'local', 'username': 'admin', 'password': password})[0] == 403
        assert call('/sessions', 'POST', {'provider': 'local', 'username': 'admin', 'password': password}, extra={'Origin': 'https://hostile.example'})[0] == 403
        cookie, session, metrics['login_ms'] = login('admin')
        assert call('/session/reauthentication', 'POST', {'password': password}, cookie, extra={'Origin': origin})[0] == 403
        reauth(cookie, session)
        checks.append('real Local login, secure host-only cookie, login Origin and session-bound CSRF; grant never exposed')
        code, roles, _ = call('/roles', cookie=cookie)
        assert code == 200 and len(roles['items']) == 5
        reader_role = next(role['id'] for role in roles['items'] if role['name'] == 'Reader')
        code, admin, _ = call('/users/' + session['principal_id'], cookie=cookie)
        assert code == 200
        _, (code, failure, _) = command('/users/' + admin['id'], {'disabled': True, 'role_ids': admin['role_ids']}, cookie, session, 'PATCH', admin['revision'])
        assert code == 409 and failure.get('code') == 'LAST_LOCAL_ADMINISTRATOR', f'last administrator guard: HTTP {code}, code={failure.get("code")}'
        user_request, (code, created, _) = command('/users', {'username': 'observer', 'password': password, 'role_ids': [reader_role]}, cookie, session)
        assert code == 202
        observer_id = created['resource_ref']['id']
        code, job, _ = call('/jobs/' + created['job_id'], cookie=cookie)
        assert code == 200 and job['state'] == 'succeeded' and job['sequence'] == '1' and job['correlation_id'] == created['correlation_id'], f'created user job: HTTP {code}, code={job.get("code")}'
        code, receipt, _ = call('/requests/' + user_request + '?domain=management&epoch=' + session['request_epochs']['management'], cookie=cookie)
        assert code == 200 and receipt['resource_ref']['id'] == observer_id
        checks.append('last administrator protection and durable user effect/Job/receipt with one correlation')
        observer_cookie, observer, _ = login('observer')
        assert call('/users', cookie=observer_cookie, extra={'X-Actor': 'admin', 'X-View-Mode': 'expert'})[0] == 403
        reauth(observer_cookie, observer)
        token_body = {'name': 'synthetic-token', 'scopes': ['state.read', 'requests.read', 'access.tokens.self'], 'expires_at': (datetime.now(timezone.utc) + timedelta(days=1)).isoformat()}
        token_request, (code, token, _) = command('/tokens', token_body, observer_cookie, observer)
        assert code == 201 and token['secret_available']
        credentials.append(token['secret'])
        _, (code, replay, _) = command('/tokens', token_body, observer_cookie, observer, request=token_request)
        assert code == 200 and 'secret' not in replay and replay['request_id'] == token_request
        token_receipt = '/requests/' + token_request + '?domain=management&epoch=' + observer['request_epochs']['management']
        assert call(token_receipt, bearer=token['secret'])[0] == 200
        assert call('/session', bearer=token['secret'])[0] == 403
        assert call('/users', bearer=token['secret'])[0] == 403
        assert call('/session', cookie=observer_cookie, bearer=token['secret'])[0] == 400
        checks.append('scoped Bearer credential, session separation and original-request recovery without secret replay')
        code, user, _ = call('/users/' + observer_id, cookie=cookie)
        assert code == 200
        _, (code, _, _) = command('/users/' + observer_id, {'disabled': False, 'role_ids': []}, cookie, session, 'PATCH', user['revision'])
        assert code == 202 and call(token_receipt, bearer=token['secret'])[0] == 403
        assert call('/session', cookie=observer_cookie)[1]['effective_capabilities'] == []
        checks.append('current role reduction immediately narrows existing sessions and tokens')
        with ThreadPoolExecutor(max_workers=2) as pool:
            results = list(pool.map(login, ['observer', 'observer']))
        metrics['two_concurrent_login_ms'] = [result[2] for result in results]
        metrics['mgrd_memory_peak_bytes'] = run('systemctl', 'show', '-p', 'MemoryPeak', '--value', units['mgrd']).stdout.strip()
        checks.append('two concurrent 64 MiB Argon2id logins under the actual systemd memory limit')
        for service in ('mgrd', 'webd'):
            previous = run('systemctl', 'show', '-p', 'MainPID', '--value', units[service]).stdout.strip()
            run('systemctl', 'kill', '--signal=KILL', '--kill-whom=main', units[service])
            eventually(lambda: run('systemctl', 'show', '-p', 'MainPID', '--value', units[service]).stdout.strip() not in (previous, '0') and ready())
            code, restored, _ = call('/session', cookie=cookie)
            assert code == 200 and restored['principal_id'] == session['principal_id'] and restored['expires_at'] == session['expires_at']
        checks.append('independent mgrd/webd SIGKILL recovery preserves the sealed session and original absolute expiry')
        raw_cookie = cookie.split('=', 1)[1]
        cookie_hash = hashlib.sha256(raw_cookie.encode()).digest()
        with sqlite3.connect(web_db) as connection:
            saved = connection.execute('SELECT envelope,expires_at FROM browser_sessions WHERE session_hash=?', (cookie_hash,)).fetchone()
            for value in credentials:
                assert value.encode() not in saved[0]
            connection.execute('UPDATE browser_sessions SET envelope=? WHERE session_hash=?', (b'{"role":"Administrator","actor":"forged"}', cookie_hash))
        connection.close()
        assert call('/session', cookie=cookie)[0] == 401
        with sqlite3.connect(web_db) as connection:
            connection.execute('UPDATE browser_sessions SET envelope=? WHERE session_hash=?', (saved[0], cookie_hash))
        connection.close()
        assert call('/session', cookie=cookie)[0] == 200
        assert call('/session', 'DELETE', cookie=cookie, csrf=session['csrf_token'], extra={'Origin': origin})[0] == 204
        with sqlite3.connect(web_db) as connection:
            connection.execute('INSERT INTO browser_sessions VALUES(?,?,?)', (cookie_hash, saved[0], saved[1]))
        connection.close()
        assert call('/session', cookie=cookie)[0] == 401
        checks.append('web DB forgery fails authentication; restoring an old valid mapping cannot undo mgrd logout')
        with sqlite3.connect(manager_db) as connection:
            audit = connection.execute('SELECT operation,result,request_id FROM auth_audit').fetchall()
            responses = connection.execute('SELECT response FROM api_receipts').fetchall()
        connection.close()
        assert len(audit) >= 10
        for value in credentials:
            assert value not in json.dumps(audit)
            assert all(value.encode() not in response[0] for response in responses)
        checks.append('persistent security audit and stored receipts contain no password, cookie, CSRF or token plaintext')
        evidence = {'architecture': os.uname().machine, 'logical_cpus': os.cpu_count(),
                    'argon2id': {'memory_kib': 65536, 'iterations': 3, 'parallelism': 1, 'active_limit': 2},
                    'metrics': metrics, 'checks': checks}
        destination = repo / 'test-results/go-auth-smoke.json'
        destination.parent.mkdir(exist_ok=True)
        destination.write_text(json.dumps(evidence, indent=2) + '\n')
        print(json.dumps(evidence, indent=2))
    finally:
        journal = []
        for name in units.values():
            journal.append(run('journalctl', '-u', name, '--no-pager', '-o', 'cat', check=False).stdout)
            run('systemctl', 'stop', name, check=False)
        text = '\n'.join(journal)
        for value in credentials:
            assert value not in text, 'credential leaked to daemon journal'
        target = repo / 'test-results/go-auth-journal.log'
        target.parent.mkdir(exist_ok=True)
        target.write_text(text)
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
