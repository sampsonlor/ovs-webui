"""Native Linux fixture: encrypted TLS lifecycle over real systemd/Unix/HTTPS.

All keys, certificates, users and addresses belong to the removed CI fixture.
Only public fingerprints, timings and check names enter retained evidence.
"""
import argparse
import hashlib
import http.client
import json
import os
from pathlib import Path
import pwd
import secrets
import shutil
import socket
import sqlite3
import ssl
import time
import urllib.error
import urllib.request
import uuid

from authentication import eventually, request_id, run


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    args = parser.parse_args()
    assert os.geteuid() == 0 and Path('/run/systemd/system').is_dir()
    repo = Path(__file__).resolve().parents[2]
    source = Path(args.bin_dir).resolve()
    suffix = uuid.uuid4().hex[:12]
    fixture = Path('/run') / f'ovs-webui-tls-{suffix}'
    runtime_name = f'ovs-webui-tls-runtime-{suffix}'
    runtime = Path('/run') / runtime_name
    data_name = f'ovs-webui-tls-{suffix}'
    data = Path('/var/lib') / data_name
    account_name = f'ovs-tls-{suffix}'
    units = {s: f'ovs-{s}-tls-{suffix}.service' for s in ('mgrd', 'webd')}
    unit_paths = [Path('/etc/systemd/system') / name for name in units.values()]
    created_account = False
    fixture.mkdir(mode=0o700)
    password = 'synthetic-tls-password-' + secrets.token_hex(12)
    credentials = [password]
    checks, metrics = [], {}
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

        def web_action(*options, check=True):
            return run('runuser', '-u', account_name, '--', webd, '--database', str(web_db), *options, check=check)

        run(mgrd, '--init-database', '--database', str(manager_db))
        run(mgrd, '--init-auth-key', '--database', str(manager_db))
        run(mgrd, '--init-secret-store', '--database', str(manager_db))
        run(mgrd, '--bootstrap-admin', 'admin', '--database', str(manager_db), input_text=password + '\n')
        web_action('--init-database')
        web_action('--init-session-key')
        with socket.socket() as reserve:
            reserve.bind(('127.0.0.1', 0))
            port = reserve.getsockname()[1]
        origin = f'https://127.0.0.1:{port}'
        web_action('--bootstrap-tls', '--public-origin', origin)
        assert web_action('--bootstrap-tls', '--public-origin', origin, check=False).returncode != 0
        bootstrap = fixture / 'bootstrap.crt'
        bootstrap.write_text(web_action('--print-tls-certificate', '--public-origin', origin).stdout)
        assert 'PRIVATE KEY' not in bootstrap.read_text()
        ca, ca_key = fixture / 'root.crt', fixture / 'root.key'
        run('openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes',
            '-days', '2', '-subj', '/CN=Synthetic CI TLS Root', '-addext', 'basicConstraints=critical,CA:TRUE',
            '-keyout', str(ca_key), '-out', str(ca))
        roots = web_dir / 'trust.pem'
        roots.write_bytes(ca.read_bytes())
        roots.chmod(0o600)
        os.chown(roots, account.pw_uid, account.pw_gid)

        def identity(name, host='127.0.0.1'):
            cert, key, csr = [fixture / f'{name}.{ext}' for ext in ('crt', 'key', 'csr')]
            run('openssl', 'req', '-new', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes',
                '-subj', '/CN=Synthetic CI Leaf', '-keyout', str(key), '-out', str(csr))
            ext = fixture / f'{name}.ext'
            ext.write_text(f'basicConstraints=critical,CA:FALSE\nsubjectAltName=IP:{host}\nkeyUsage=digitalSignature\nextendedKeyUsage=serverAuth\n')
            run('openssl', 'x509', '-req', '-in', str(csr), '-CA', str(ca), '-CAkey', str(ca_key),
                '-set_serial', str(secrets.randbits(96) + 1), '-days', '1', '-extfile', str(ext), '-out', str(cert))
            credentials.append(key.read_text().strip())
            return cert.read_text(), key.read_text()

        first_cert, first_key = identity('first')
        second_cert, second_key = identity('second')
        wrong_host_cert, wrong_host_key = identity('wrong-host', '192.0.2.35')
        trust = ssl.create_default_context(cafile=str(bootstrap))
        trust.load_verify_locations(cafile=str(ca))
        config = fixture / 'runtime.env'
        config.write_text(f'WEBD_UID={account.pw_uid}\nWEBD_GID={account.pw_gid}\nMANAGER_SOCKET={runtime}/mgrd.sock\n'
                          f'HTTPS_LISTEN=127.0.0.1:{port}\nTLS_CERT=\nTLS_KEY=\nPUBLIC_ORIGIN={origin}\n'
                          f'MANAGER_DATABASE={manager_db}\nWEB_DATABASE={web_db}\n')
        config.chmod(0o600)
        for service, name in units.items():
            template = (repo / 'packaging/systemd' / f'ovs-{service}.service').read_text()
            rendered = template.replace('/run/ovs-webui', str(runtime)).replace('RuntimeDirectory=ovs-webui', f'RuntimeDirectory={runtime_name}')
            rendered = rendered.replace('StateDirectory=ovs-webui/', f'StateDirectory={data_name}/').replace('ovs-webui-web', account_name)
            rendered = rendered.replace('/etc/ovs-webui/runtime.env', str(config)).replace(f'/usr/libexec/ovs-{service}', str(fixture / f'ovs-{service}'))
            rendered = rendered.replace('After=network.target ovs-mgrd.service', f'After=network.target {units["mgrd"]}')
            if service == 'webd':
                rendered = rendered.replace('--public-origin=${PUBLIC_ORIGIN}', f'--public-origin=${{PUBLIC_ORIGIN}} --tls-trust-file={roots}')
            (Path('/etc/systemd/system') / name).write_text(rendered)
        run('systemctl', 'daemon-reload')
        run('systemd-analyze', 'verify', '--man=no', *map(str, unit_paths))

        def fingerprint():
            with socket.create_connection(('127.0.0.1', port), timeout=5) as raw:
                with trust.wrap_socket(raw, server_hostname='127.0.0.1') as secured:
                    assert secured.version() == 'TLSv1.3'
                    return hashlib.sha256(secured.getpeercert(binary_form=True)).hexdigest()

        def call(path, method='GET', body=None, cookie='', csrf='', extra=None, connection=None):
            headers = dict(extra or {})
            payload = None
            if body is not None:
                payload = json.dumps(body).encode()
                headers['Content-Type'] = 'application/json'
            if cookie:
                headers['Cookie'] = cookie
            if csrf:
                headers['X-OVS-CSRF-Token'] = csrf
            if connection:
                connection.request(method, '/api/v1' + path, body=payload, headers=headers)
                response = connection.getresponse()
            else:
                req = urllib.request.Request(origin + '/api/v1' + path, data=payload, headers=headers, method=method)
                try:
                    response = urllib.request.urlopen(req, context=trust, timeout=6)
                except urllib.error.HTTPError as error:
                    response = error
            with response:
                raw = response.read()
                return response.status, json.loads(raw) if raw else None, response.headers

        def ready():
            code, value, _ = call('/runtime')
            return code == 200 and value['authentication_ready']

        def login():
            code, session, headers = call('/sessions', 'POST', {'provider': 'local', 'username': 'admin', 'password': password}, extra={'Origin': origin})
            assert code == 201, f'login status {code}'
            cookie = headers['Set-Cookie'].split(';', 1)[0]
            credentials.extend([cookie.split('=', 1)[1], session['csrf_token']])
            assert call('/session/reauthentication', 'POST', {'password': password}, cookie, session['csrf_token'], {'Origin': origin})[0] == 200
            return cookie, session

        def command(path, body, cookie, session, etag=None, identifier=None, connection=None):
            identifier = identifier or request_id()
            headers = {'Origin': origin, 'Idempotency-Key': identifier, 'X-OVS-Request-Epoch': session['request_epochs']['management']}
            if etag:
                headers['If-Match'] = '"' + etag + '"'
            return identifier, call(path, 'POST', dict(body, request_id=identifier), cookie, session['csrf_token'], headers, connection)

        def certificate(cert_id, cookie):
            code, value, _ = call('/certificates/' + cert_id, cookie=cookie)
            assert code == 200, f'certificate read status {code}'
            assert value['private_key_configured'] and 'private_key_pem' not in value
            return value

        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(ready)
        original_fingerprint = fingerprint()
        cookie, session = login()
        # Offline tools cannot mutate an active daemon's locked database.
        assert web_action('--rotate-secret-key', 'tls', '--public-origin', origin, check=False).returncode != 0
        for cert, key in ((first_cert, second_key), (wrong_host_cert, wrong_host_key), (bootstrap.read_text(), first_key)):
            _, (code, _, _) = command('/certificates', {'certificate_pem': cert, 'private_key_pem': key}, cookie, session)
            assert code == 422, f'invalid certificate status {code}'
            assert fingerprint() == original_fingerprint
        checks.append('bootstrap HTTPS; invalid chain/SAN/private-key pair leaves serving identity unchanged')

        request, (code, imported, _) = command('/certificates', {'certificate_pem': first_cert, 'private_key_pem': first_key}, cookie, session)
        assert code == 202, f'certificate import status {code}'
        cert_id = imported['resource_ref']['id']
        _, (code, replay, _) = command('/certificates', {'certificate_pem': first_cert, 'private_key_pem': first_key}, cookie, session, identifier=request)
        assert code == 202 and replay['job_id'] == imported['job_id']
        assert fingerprint() == original_fingerprint
        old_connection = http.client.HTTPSConnection('127.0.0.1', port, context=trust, timeout=5)
        assert call('/session', cookie=cookie, connection=old_connection)[0] == 200
        old_socket = old_connection.sock
        _, (code, activation, _) = command('/certificates/' + cert_id + '/activations', {}, cookie, session, certificate(cert_id, cookie)['revision'])
        assert code == 202, f'activation status {code}'
        active = certificate(cert_id, cookie)
        assert active['state'] == 'awaiting-confirmation'
        assert fingerprint() == active['fingerprint'] != original_fingerprint
        _, (code, _, _) = command('/certificates/' + cert_id + '/confirmations', {}, cookie, session, active['revision'], connection=old_connection)
        assert old_connection.sock is old_socket and code == 409, f'old TLS connection confirmation status {code}'
        old_connection.close()
        _, (code, _, _) = command('/certificates/' + cert_id + '/confirmations', {}, cookie, session, active['revision'])
        assert code == 202, f'fresh TLS confirmation status {code}'
        assert certificate(cert_id, cookie)['state'] == 'active'
        assert call('/jobs/' + activation['job_id'], cookie=cookie)[1]['state'] == 'succeeded'
        confirmed_fingerprint = fingerprint()
        checks.append('import replay has one Job; atomic trial requires fresh TLS connection confirmation; durable success evidence')

        _, (code, imported, _) = command('/certificates', {'certificate_pem': second_cert, 'private_key_pem': second_key}, cookie, session)
        assert code == 202
        trial_id = imported['resource_ref']['id']
        before = time.monotonic()
        _, (code, activation, _) = command('/certificates/' + trial_id + '/activations', {}, cookie, session, certificate(trial_id, cookie)['revision'])
        assert code == 202
        trial = certificate(trial_id, cookie)
        deadline = trial['confirmation_deadline']
        assert fingerprint() == trial['fingerprint'] != confirmed_fingerprint
        for service in ('mgrd', 'webd'):
            previous = run('systemctl', 'show', '-p', 'MainPID', '--value', units[service]).stdout.strip()
            run('systemctl', 'kill', '--signal=KILL', '--kill-whom=main', units[service])
            eventually(lambda: run('systemctl', 'show', '-p', 'MainPID', '--value', units[service]).stdout.strip() not in (previous, '0') and ready())
            assert certificate(trial_id, cookie)['confirmation_deadline'] == deadline
            assert fingerprint() == trial['fingerprint']
        run('systemctl', 'stop', units['mgrd'])
        # Await the actual production 120-second window, without test flags.
        eventually(lambda: fingerprint() == confirmed_fingerprint, timeout=130)
        metrics['unconfirmed_recovery_seconds'] = round(time.monotonic() - before, 3)
        assert 110 <= metrics['unconfirmed_recovery_seconds'] <= 130
        run('systemctl', 'start', units['mgrd'])
        eventually(ready)
        assert certificate(trial_id, cookie)['state'] == 'rolled-back'
        failed = call('/jobs/' + activation['job_id'], cookie=cookie)[1]
        assert failed['state'] == 'failed' and failed['sequence'] == '2'
        checks.append('mgrd/webd SIGKILL preserve original deadline; webd rolls back after 120 seconds while mgrd is stopped; failed Job retained')

        # Master keys stay in owner-only consumer directories. Rotation is offline.
        assert run('runuser', '-u', account_name, '--', 'test', '-r', str(manager_dir / 'secret-keys/key-01'), check=False).returncode != 0
        run('systemctl', 'stop', units['webd'], units['mgrd'])
        web_action('--rotate-secret-key', 'tls', '--public-origin', origin, '--tls-trust-file', str(roots))
        web_action('--rotate-secret-key', 'session')
        run(mgrd, '--rotate-secret-key', '--database', str(manager_db))
        for directory in (web_dir / 'tls-keys', web_dir / 'session-keys', manager_dir / 'secret-keys'):
            assert (directory / 'active').read_text().strip() == '2'
            assert (directory / 'key-01').exists() and (directory / 'key-02').exists()
        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(ready)
        code, restored, _ = call('/session', cookie=cookie)
        assert code == 200 and restored['expires_at'] == session['expires_at']
        assert fingerprint() == confirmed_fingerprint
        checks.append('independent TLS/session/privileged key rotation preserves confirmed identity and original session expiry')

        # Missing consumer master keys fail closed; no automatic replacement.
        run('systemctl', 'stop', units['webd'])
        missing = web_dir / 'tls-keys/key-02'
        retained = web_dir / 'tls-keys/temporarily-unavailable'
        missing.rename(retained)
        assert web_action('--print-tls-certificate', '--public-origin', origin, '--tls-trust-file', str(roots), check=False).returncode != 0
        assert not missing.exists()
        retained.rename(missing)
        # Explicit restore reconciliation is offline and paired across daemons.
        run('systemctl', 'stop', units['mgrd'])
        run(mgrd, '--prepare-restore-security', '--database', str(manager_db))
        web_action('--prepare-restore-security', '--public-origin', origin, '--tls-trust-file', str(roots))
        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(ready)
        assert call('/session', cookie=cookie)[0] == 401
        _, fresh = login()
        assert fresh['request_epochs'] != session['request_epochs']
        assert fingerprint() == confirmed_fingerprint
        checks.append('missing key rejects recovery without regeneration; paired restore reconciliation revokes cookies and changes both request epochs')

        for db in (manager_db, web_db):
            with sqlite3.connect(db) as connection:
                snapshot = fixture / (db.name + '.snapshot')
                connection.execute('VACUUM INTO ?', (str(snapshot),))
            connection.close()
            raw = snapshot.read_bytes()
            for value in credentials:
                assert value.encode() not in raw, 'secret plaintext in SQLite snapshot'
            assert b'BEGIN PRIVATE KEY' not in raw
        checks.append('database snapshots, manager receipts, Audit and daemon journals contain no plaintext secret')
        evidence = {'architecture': os.uname().machine, 'metrics': metrics, 'checks': checks,
                    'bootstrap_fingerprint': original_fingerprint, 'confirmed_fingerprint': confirmed_fingerprint}
        target = repo / 'test-results/go-tls-smoke.json'
        target.parent.mkdir(exist_ok=True)
        target.write_text(json.dumps(evidence, indent=2) + '\n')
        print(json.dumps(evidence, indent=2))
    finally:
        journal = []
        for name in units.values():
            journal.append(run('journalctl', '-u', name, '--no-pager', '-o', 'cat', check=False).stdout)
            run('systemctl', 'stop', name, check=False)
        text = '\n'.join(journal)
        leaked = any(value in text for value in credentials)
        target = repo / 'test-results/go-tls-journal.log'
        target.parent.mkdir(exist_ok=True)
        target.write_text('[REDACTED: unexpected secret in journal]\n' if leaked else text)
        for path in unit_paths:
            path.unlink(missing_ok=True)
        run('systemctl', 'daemon-reload', check=False)
        if created_account:
            run('userdel', account_name, check=False)
        for path in (fixture, runtime, data):
            if path.exists():
                shutil.rmtree(path)
        assert not leaked, 'secret leaked to daemon journal'


if __name__ == '__main__':
    main()
