"""Ephemeral Linux CI only: real systemd services and isolated OVS dummy datapath.

Never invoked by a production binary, installer or normal local pnpm command.
Every unit, socket and database created here has a unique fixture identity.
"""
import argparse
from datetime import datetime, timezone
import fcntl
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


def stop_restored_daemon(component):
    if component.poll() is None:
        component.terminate()
        try:
            component.wait(timeout=6)
        except subprocess.TimeoutExpired:
            component.kill()
            component.wait(timeout=5)
            raise


def reconcile_restored_manager(manager, fixture, scratch_database, manager_key, owner, ipc_socket):
    def lifecycle(immutable):
        params = '?mode=ro' + ('&immutable=1' if immutable else '')
        with sqlite3.connect(scratch_database.as_uri() + params, uri=True, timeout=0.2) as db:
            return db.execute('SELECT generation,state,pending_digest,restore_required FROM inventory_state').fetchone()

    previous = lifecycle(True)
    assert previous[1] == 'reconciliation-required' and previous[3] == 1
    saved_root = fixture / 'ovs'
    live_args = [
        manager, '--database', str(scratch_database), '--auth-key-file', str(manager_key),
        '--ovsdb-socket', str(saved_root / 'db.sock'), '--ovsdb-file', str(saved_root / 'conf.db'),
    ]
    browser_peer = ['--webd-uid', str(owner.pw_uid), '--webd-gid', str(owner.pw_gid)]
    service = live_args + browser_peer + ['--socket', str(ipc_socket)]
    component = subprocess.Popen(service,
                                 stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        def reviewed():
            assert component.poll() is None, 'restore observation process exited'
            try:
                observed = lifecycle(False)
            except sqlite3.OperationalError:
                return None
            if observed[1] == 'reconciliation-required' and observed[3] == 0 and re.fullmatch(r'[a-f0-9]{64}', observed[2]):
                return observed
            return None

        observed = eventually(reviewed)
        assert observed[0] == previous[0], 'observation silently changed the restored generation'
    finally:
        stop_restored_daemon(component)
    offline = live_args + ['--reconciliation-reason', 'Reviewed disposable OVS restore evidence', '--reconcile-ovsdb']
    refused = subprocess.run(offline + ['0' * 64], check=False, capture_output=True, text=True, timeout=15)
    assert refused.returncode != 0 and lifecycle(True)[0] == previous[0]
    run(*offline, observed[2])
    accepted = lifecycle(True)
    assert accepted[0] != previous[0] and accepted[1:] == ('confirmed', '', 0)
    return service


def boot_published_restore(binary_dir, fixture, published, manifest, owner, manager_key, web_key,
                           tls_keys, origin, certificate, old_cookie, password, forwarding):
    working = fixture / 'warm-restore'
    working.mkdir(mode=0o700)
    os.chown(working, 0, owner.pw_gid)
    working.chmod(0o710)
    for kind, uid, gid in (('manager', 0, 0), ('web', owner.pw_uid, owner.pw_gid)):
        relative = f'{kind}/{kind}.db'
        assert manifest[kind]['file'] == relative
        target = working / relative
        target.parent.mkdir(mode=0o700)
        shutil.copyfile(published / relative, target)
        target.chmod(0o600)
        os.chown(target, uid, gid)
        os.chown(target.parent, uid, gid)
        assert not target.samefile(published / relative), 'warm boot requires distinct database images'
        with target.open('rb') as database:
            assert hashlib.file_digest(database, 'sha256').hexdigest() == manifest[kind]['sha256']

    ipc_socket = working / 'mgrd.sock'
    forwarding()
    manager_args = reconcile_restored_manager(str(binary_dir / 'ovs-mgrd'), fixture, working / 'manager/manager.db',
                                              manager_key, owner, ipc_socket)
    forwarding()
    web_args = [str(binary_dir / 'ovs-webd'), '--database', str(working / 'web/web.db'), '--manager-socket', str(ipc_socket),
                '--listen', origin.removeprefix('https://'), '--public-origin', origin,
                '--session-key-file', str(web_key), '--tls-key-directory', str(tls_keys)]
    trust = ssl.create_default_context(cadata=certificate)

    def request(path, *, cookie=None, data=None):
        headers = {'Accept': 'application/json'}
        if cookie is not None:
            headers['Cookie'] = cookie
        if data is not None:
            headers.update({'Content-Type': 'application/json', 'Origin': origin})
        req = urllib.request.Request(origin + path, headers=headers, data=data)
        try:
            with urllib.request.urlopen(req, context=trust, timeout=5) as response:
                return response.status, json.loads(response.read()), response.headers
        except urllib.error.HTTPError as error:
            with error:
                return error.code, json.loads(error.read()), error.headers

    manager = subprocess.Popen(manager_args, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        web = subprocess.Popen(web_args, user=owner.pw_uid, group=owner.pw_gid, extra_groups=(), umask=0o077,
                               stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            def ready():
                assert manager.poll() is None and web.poll() is None, (
                    f'restored daemon exited during warm boot (manager={manager.poll()}, web={web.poll()})')
                status, result, _ = request('/readyz')
                if status != 200:
                    return False
                return (result['authentication_ready'] and result['storage']['manager']['writable']
                        and result['storage']['web']['writable'])

            eventually(ready, timeout=20)
            with socket.create_connection(('127.0.0.1', int(origin.rsplit(':', 1)[1])), timeout=5) as raw:
                with trust.wrap_socket(raw, server_hostname='127.0.0.1') as tls:
                    assert tls.getpeercert(binary_form=True) == ssl.PEM_cert_to_DER_cert(certificate)
            assert request('/api/v1/session', cookie=old_cookie)[0] == 401
            login = json.dumps({'provider': 'local', 'username': 'admin', 'password': password}).encode()
            status, _, headers = request('/api/v1/sessions', data=login)
            assert status == 201 and headers.get('Set-Cookie'), 'restored management credentials cannot start a new session'
            new_cookie = headers['Set-Cookie'].split(';', 1)[0]
            assert new_cookie != old_cookie and request('/api/v1/session', cookie=new_cookie)[0] == 200
            forwarding()
        finally:
            stop_restored_daemon(web)
    finally:
        stop_restored_daemon(manager)


def canonical_restore_stage(binary_dir, fixture, destination, pair, manager_db, web_db, owner, origin,
                            tls_keys, original_certificate, old_cookie, password, forwarding, generation):
    staging = fixture / 'sealed-restore'
    staging.mkdir(mode=0o700)
    manager = str(binary_dir / 'ovs-mgrd')
    prepared = run(manager, '--stage-offline-restore-pair', pair['id'],
                   '--backup-pair-root', str(destination), '--restore-stage-root', str(staging))
    stage = json.loads(prepared)
    assert stage['state'] == 'sealed' and stage['backup_pair'] == pair
    assert stage['manager_security'] == 'owner-prepare-required'
    assert stage['web_security'] == 'owner-handoff-and-prepare-required'
    assert stage['ovs_reconciliation'] == 'required'
    assert stage['key_evidence_scope'] == 'secret-record-version-metadata-only'
    assert any(item['partition'] == 'webd-tls' and item['required'] == [1] and item['state'] == 'unknown'
               for item in stage['keys']), 'sealed fixture did not preserve key-version requirements'
    verify = [manager, '--verify-offline-restore-stage', stage['id'], '--restore-stage-root', str(staging)]
    assert json.loads(run(*verify)) == stage

    source = staging / ('restore-stage-' + stage['id'])
    work = {}
    for kind in ('manager', 'web'):
        # Separate owners get private scratch copies. Neither command sees a
        # canonical database path, and the sealed source remains unchanged.
        root = manager_db.parent.parent / f'fixture-restore-{kind}'
        root.mkdir(mode=0o700)
        image = root / ('manager.db' if kind == 'manager' else 'web.db')
        shutil.copyfile(source / kind / 'database.sqlite', image)
        image.chmod(0o600)
        if kind == 'web':
            os.chown(root, owner.pw_uid, owner.pw_gid)
            os.chown(image, owner.pw_uid, owner.pw_gid)
        work[kind] = image

    def rows(image, query):
        assert not Path(str(image) + '-wal').exists(), 'owner command did not close and checkpoint the working database'
        with sqlite3.connect(image.as_uri() + '?mode=ro&immutable=1', uri=True) as db:
            return db.execute(query).fetchone()

    def epochs(image, kind):
        api = rows(image, 'SELECT epoch FROM api_authority')
        return (*api, *rows(image, 'SELECT epoch FROM auth_state')) if kind == 'manager' else api

    original = {kind: epochs(image, kind) for kind, image in work.items()}
    assert rows(work['manager'], 'SELECT count(*) FROM grants WHERE revoked_at IS NULL')[0] > 0
    sessions_before = rows(work['web'], 'SELECT count(*) FROM browser_sessions')
    tls_before = rows(work['web'], 'SELECT envelope FROM tls_local_state')
    assert sessions_before[0] > 0 and tls_before and tls_before[0], 'staged Web fixture must retain authenticated state'
    manager_action = [manager, '--prepare-restore-security', '--database', str(work['manager'])]
    web_cli = ['runuser', '-u', owner.pw_name, '--', str(binary_dir / 'ovs-webd')]
    web_action = web_cli + [
        '--prepare-restore-security', '--database', str(work['web']), '--public-origin', origin,
    ]

    def rejected_restore(kind, action):
        result = subprocess.run(action, capture_output=True, text=True, check=False, timeout=25)
        assert result.returncode != 0, 'restore owner accepted invalid or absent key material'
        assert epochs(work[kind], kind) == original[kind], 'rejected key changed security epochs'
        if kind == 'web':
            assert rows(work[kind], 'SELECT count(*) FROM browser_sessions') == sessions_before
            assert rows(work[kind], 'SELECT envelope FROM tls_local_state') == tls_before

    rejected_restore('manager', manager_action)
    rejected_restore('web', web_action)
    web_action += ['--session-key-file', str(web_db.parent / 'session.key')]
    rejected_restore('web', web_action)
    wrong_tls = work['web'].parent / 'wrong-tls-key-ring'
    wrong_tls.mkdir(mode=0o700)
    for name, contents in (('active', b'1\n'), ('key-01', os.urandom(32))):
        path = wrong_tls / name
        with path.open('xb') as key:
            key.write(contents)
        path.chmod(0o600)
        os.chown(path, owner.pw_uid, owner.pw_gid)
    os.chown(wrong_tls, owner.pw_uid, owner.pw_gid)
    rejected_restore('web', web_action + ['--tls-key-directory', str(wrong_tls)])

    run(*manager_action, '--auth-key-file', str(manager_db.parent / 'auth.key'))
    run(*web_action, '--tls-key-directory', str(tls_keys))
    for kind, image in work.items():
        new = epochs(image, kind)
        assert len(new) == len(original[kind]) and all(a != b for a, b in zip(new, original[kind]))
    assert rows(work['manager'], 'SELECT generation,state,restore_required FROM inventory_state') == (
        generation, 'reconciliation-required', 1)
    assert rows(work['manager'], 'SELECT count(*) FROM grants WHERE revoked_at IS NULL') == (0,)
    assert rows(work['web'], 'SELECT count(*) FROM browser_sessions') == (0,)
    assert run(*web_cli, '--print-tls-certificate', '--database', str(work['web']), '--public-origin', origin,
               '--tls-key-directory', str(tls_keys)) == original_certificate
    assert json.loads(run(*verify)) == stage
    fresh_root = fixture / 'fresh-destinations'
    fresh_root.mkdir(mode=0o700)
    os.chown(fresh_root, 0, owner.pw_gid)
    fresh_root.chmod(0o710)
    executable_hashes = {}
    for kind, daemon in (('manager', 'ovs-mgrd'), ('web', 'ovs-webd')):
        with (binary_dir / daemon).open('rb') as executable:
            executable_hashes[kind] = hashlib.file_digest(executable, 'sha256').hexdigest()
    fresh_command = [manager, '--prepare-fresh-restore-stage', stage['id'], '--restore-stage-root', str(staging),
                     '--restore-destination-root', str(fresh_root), '--restore-manager-sha256', executable_hashes['manager'],
                     '--restore-web-sha256', executable_hashes['web'], '--restore-manager-auth-key', str(manager_db.parent / 'auth.key'),
                     '--restore-web-session-key', str(web_db.parent / 'session.key'), '--restore-public-origin', origin]
    verify_fresh = [manager, '--verify-fresh-restore-stage', stage['id'], '--restore-stage-root', str(staging),
                    '--restore-destination-root', str(fresh_root), '--restore-manager-sha256', executable_hashes['manager'],
                    '--restore-web-sha256', executable_hashes['web']]

    def prepare(keys, command=fresh_command):
        return subprocess.run(command + ['--restore-web-tls-keys', str(keys)],
                              check=False, capture_output=True, text=True, timeout=190)

    def reject_fresh_verification(command=verify_fresh):
        result = subprocess.run(command, check=False, capture_output=True, text=True, timeout=20)
        assert result.returncode != 0 and not result.stdout and 'RESTORE_STAGE_UNAVAILABLE' in result.stderr

    reject_fresh_verification()
    assert not list(fresh_root.iterdir()), 'verifying an absent publication created a destination'
    unreviewed = fresh_command.copy()
    unreviewed[unreviewed.index('--restore-web-sha256') + 1] = '0' * 64
    assert prepare(tls_keys, unreviewed).returncode != 0 and not list(fresh_root.iterdir()), 'unreviewed binary was accepted'
    rejected = prepare(wrong_tls)
    assert rejected.returncode != 0 and not list(fresh_root.iterdir()), 'invalid keys published or leaked a working tree'
    fresh = prepare(tls_keys)
    assert fresh.returncode == 0, f'fresh offline preparation failed with exit {fresh.returncode}'
    manifest = json.loads(fresh.stdout)
    assert manifest['stage_id'] == stage['id'] and manifest['backup_pair_id'] == pair['id']
    assert manifest['state'] == 'offline-prepared' and manifest['ovs_reconciliation_required'] is True
    prepared_path = fresh_root / ('restore-ready-' + stage['id'])
    assert [path.name for path in fresh_root.iterdir()] == [prepared_path.name]
    assert prepare(tls_keys).returncode != 0, 'same sealed stage replaced a prepared destination'
    manifest_path = prepared_path / 'manifest.json'
    assert json.loads(manifest_path.read_text()) == manifest
    assert manifest_path.stat().st_uid == 0 and manifest_path.stat().st_mode & 0o777 == 0o600
    for kind, uid, gid in (('manager', 0, 0), ('web', owner.pw_uid, owner.pw_gid)):
        entry = manifest[kind]
        assert entry['file'] == f'{kind}/{kind}.db' and entry['executable_sha256'] == executable_hashes[kind]
        image = prepared_path / entry['file']
        assert entry['database_id'] == pair[kind]['backup_manifest']['database_id']
        assert (image.stat().st_uid, image.stat().st_gid, image.stat().st_mode & 0o777) == (uid, gid, 0o600)
        assert (image.parent.stat().st_uid, image.parent.stat().st_gid, image.parent.stat().st_mode & 0o777) == (uid, gid, 0o700)
        actual_access = subprocess.run(['runuser', '-u', owner.pw_name, '--', 'test', '-r', str(image)],
                                       check=False, timeout=8)
        assert (actual_access.returncode == 0) is (kind == 'web'), 'fresh owner traversal does not match file ownership'
        with image.open('rb') as database:
            assert hashlib.file_digest(database, 'sha256').hexdigest() == entry['sha256']

    assert json.loads(run(*verify_fresh)) == manifest

    wrong_build = verify_fresh.copy()
    wrong_build[-1] = '0' * 64
    reject_fresh_verification(wrong_build)
    for kind in ('manager', 'web'):
        with (prepared_path / (manifest[kind]['file'] + '.lock')).open('rb') as guard:
            fcntl.flock(guard, fcntl.LOCK_EX | fcntl.LOCK_NB)
            reject_fresh_verification()

    damaged = prepared_path / manifest['web']['file']
    with damaged.open('r+b') as image:
        original_header = image.read(1)
        assert original_header == b'S'
        image.seek(0)
        image.write(b'X')
    try:
        reject_fresh_verification()
    finally:
        with damaged.open('r+b') as image:
            image.write(original_header)
    assert json.loads(run(*verify_fresh)) == manifest

    lost_report_root = fixture / 'fresh-destinations-lost-report'
    lost_report_root.mkdir(mode=0o700)
    os.chown(lost_report_root, 0, owner.pw_gid)
    lost_report_root.chmod(0o710)
    lost_prepare, lost_verify = fresh_command.copy(), verify_fresh.copy()
    for command in (lost_prepare, lost_verify):
        command[command.index('--restore-destination-root') + 1] = str(lost_report_root)
    reject_fresh_verification(lost_verify)
    assert not list(lost_report_root.iterdir()), 'verifying an absent recovery changed its destination'
    with open('/dev/full', 'wb', buffering=0) as output:
        lost = subprocess.run(lost_prepare + ['--restore-web-tls-keys', str(tls_keys)],
                              check=False, stdout=output, stderr=subprocess.PIPE, text=True, timeout=190)
    assert lost.returncode == 1 and 'RESTORE_STAGE_REPORT_UNAVAILABLE' in lost.stderr
    recovered_report = json.loads(run(*lost_verify))
    assert recovered_report['stage_id'] == stage['id'] and recovered_report['backup_pair_id'] == pair['id']
    assert recovered_report['state'] == 'offline-prepared' and recovered_report['ovs_reconciliation_required'] is True
    assert [path.name for path in lost_report_root.iterdir()] == [prepared_path.name]
    for kind in ('manager', 'web'):
        assert recovered_report[kind]['database_id'] == pair[kind]['backup_manifest']['database_id']
        assert recovered_report[kind]['executable_sha256'] == executable_hashes[kind]

    boot_published_restore(binary_dir, fixture, prepared_path, manifest, owner, manager_db.parent / 'auth.key',
                           web_db.parent / 'session.key', tls_keys, origin, original_certificate, old_cookie, password,
                           forwarding)
    assert json.loads(run(*verify_fresh)) == manifest, 'warm boot changed the published offline destination'
    assert json.loads(run(*verify)) == stage
    return {'sealed_restore_independently_verified': True,
            'old_generation_reconciled_on_private_copy': True,
            'owner_prepare_refused_absent_material': True,
            'missing_and_wrong_managed_tls_keys_refused': True,
            'restored_managed_tls_certificate_preserved': True,
            'restored_browser_contexts_and_manager_grants_revoked': True,
            'fresh_paired_destination_published': True,
            'fresh_restore_rejected_wrong_keys_and_replay': True,
            'fresh_restore_rejected_unreviewed_binary': True,
            'fresh_destination_owner_boundary_verified': True,
            'fresh_publication_independently_verified': True,
            'fresh_publication_recovered_after_failed_report': True,
            'published_pair_booted_with_original_tls_and_new_session_after_reconciliation': True,
            'warm_boot_preserved_sealed_stage_and_published_pair': True,
            'fresh_verification_rejected_wrong_build_busy_or_damaged_copy': True,
            'per_owner_epochs_rotated_on_work_copies': True,
            'restored_manager_awaits_ovs_reconciliation': True}


def canonical_offline_pair(binary_dir, fixture, manager_db, web_db, owner, origin, units, ready, forwarding,
                           tls_context, password):
    destination = fixture / 'sealed-pairs'
    destination.mkdir(mode=0o700)
    manager = str(binary_dir / 'ovs-mgrd')
    args = [
        manager, '--create-offline-backup-pair', '--backup-pair-root', str(destination),
        '--database', str(manager_db), '--backup-web-database', str(web_db),
        '--webd-uid', str(owner.pw_uid), '--webd-gid', str(owner.pw_gid),
    ]
    def confirmed_inventory():
        try:
            with sqlite3.connect(manager_db.as_uri() + '?mode=ro', uri=True, timeout=0.2) as db:
                row = db.execute('SELECT generation,state,evidence FROM inventory_state WHERE singleton=1').fetchone()
        except sqlite3.OperationalError:
            return None
        if row is None or row[1] != 'confirmed':
            return None
        evidence = json.loads(row[2])
        assert evidence['endpoint'] == str(fixture / 'ovs/db.sock') and evidence['database'] == 'Open_vSwitch'
        assert uuid.UUID(row[0]).version == 4
        return row[0]

    generation = eventually(confirmed_inventory)
    forwarding()
    live = subprocess.run(args, check=False, capture_output=True, text=True, timeout=30)
    assert live.returncode != 0 and not list(destination.iterdir()), 'live writers were not rejected'
    login = urllib.request.Request(origin + '/api/v1/sessions',
                                   data=json.dumps({'provider': 'local', 'username': 'admin', 'password': password}).encode(),
                                   headers={'Content-Type': 'application/json', 'Origin': origin}, method='POST')
    with urllib.request.urlopen(login, context=tls_context, timeout=6) as session:
        assert session.code == 201 and session.headers.get('Set-Cookie'), 'fixture did not establish an authenticated browser context'
        old_cookie = session.headers['Set-Cookie'].split(';', 1)[0]
    run('systemctl', 'stop', units['webd'], units['mgrd'])
    forwarding()
    # Dedicated test ring keeps the explicitly configured static listener
    # unchanged when the canonical fixture services are restarted.
    tls_keys = web_db.parent / 'fixture-restore-tls-keys'
    web_cli = ['runuser', '-u', owner.pw_name, '--', str(binary_dir / 'ovs-webd')]
    tls_options = ['--database', str(web_db), '--public-origin', origin, '--tls-key-directory', str(tls_keys)]
    run(*web_cli, '--bootstrap-tls', *tls_options)
    original_certificate = run(*web_cli, '--print-tls-certificate', *tls_options)
    assert original_certificate.startswith('-----BEGIN CERTIFICATE-----\n')
    before = [hashlib.sha256(p.read_bytes()).hexdigest() for p in (manager_db, web_db)]
    result = subprocess.run(args, check=False, capture_output=True, text=True, timeout=105)
    assert result.returncode == 0, f'paired backup command failed with exit {result.returncode}'
    pair = json.loads(result.stdout)
    assert not pair['includes_master_key_files']
    assert pair['manager']['backup_manifest']['kind'] == 'manager'
    assert pair['web']['backup_manifest']['kind'] == 'web'
    assert pair['manager']['backup_manifest']['database_id'] != pair['web']['backup_manifest']['database_id']
    pair_path = destination / ('backup-pair-' + pair['id'])
    assert {p.name for p in destination.iterdir()} == {pair_path.name}
    assert {p.name for p in pair_path.iterdir()} == {'manager', 'web', 'manifest.json'}
    for slot in ('manager', 'web'):
        root = pair_path / slot
        assert {p.name for p in root.iterdir()} == {'database.sqlite', 'manifest.json'}
        for path in root.iterdir():
            assert path.stat().st_uid == 0 and path.stat().st_mode & 0o777 == 0o600
    verified = subprocess.run([
        manager, '--verify-offline-backup-pair', pair['id'], '--backup-pair-root', str(destination),
    ], check=False, capture_output=True, text=True, timeout=95)
    assert verified.returncode == 0, 'offline verifier declined the exact pair'
    report = json.loads(verified.stdout)
    assert report['pair'] == pair
    restore = canonical_restore_stage(binary_dir, fixture, destination, pair, manager_db, web_db, owner, origin,
                                      tls_keys, original_certificate, old_cookie, password, forwarding, generation)
    assert [hashlib.sha256(p.read_bytes()).hexdigest() for p in (manager_db, web_db)] == before
    assert manager_db.stat().st_uid == 0 and web_db.stat().st_uid == owner.pw_uid
    run('systemctl', 'start', units['mgrd'], units['webd'])
    eventually(ready)
    forwarding()
    return {'live_writers_rejected': True, 'separate_database_owners_preserved': True,
            'pair_verified': True, 'excludes_master_key_files': True, 'source_bytes_preserved': True,
            'isolated_ovs_forwarding_preserved': True, **restore}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    parser.add_argument('--systemd-restart-probe', action='store_true')
    parser.add_argument('--offline-pair-probe', action='store_true')
    args = parser.parse_args()
    if args.systemd_restart_probe and args.offline_pair_probe:
        parser.error('Select one canonical acceptance mode')
    assert os.geteuid() == 0, 'This isolated CI test needs root for systemd/UID evidence'
    assert Path('/run/systemd/system').is_dir(), 'A real systemd host is required'
    source = Path(args.bin_dir).resolve()
    repo = Path(__file__).resolve().parents[2]
    suffix = uuid.uuid4().hex[:12]
    fixture = Path('/var/lib') / f'ovs-webui-fixture-{suffix}'
    runtime_name = f'ovs-webui-runtime-{suffix}'
    runtime = Path('/run') / runtime_name
    canonical = args.systemd_restart_probe or args.offline_pair_probe
    units = {service: f'ovs-{service}{"" if canonical else "-ci-" + suffix}.service'
             for service in ('mgrd', 'webd')}
    account_name = 'ovs-webui-web' if canonical else f'ovs-ci-{suffix}'
    if canonical:
        ensure_canonical_fixture_isolation(units, account_name)
        if args.systemd_restart_probe and not os.access(source / 'systemd-restart.test', os.X_OK):
            raise RuntimeError('The genuine systemd probe executable is required')
    unit_paths = []
    canonical_binaries = []
    canonical_binary_hashes = {}
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
            if canonical:
                with (source / target.name).open('rb') as original:
                    canonical_binary_hashes[service] = hashlib.file_digest(original, 'sha256').hexdigest()
            with (source / target.name).open('rb') as original, target.open('xb') as new:
                if canonical:
                    canonical_binaries.append(target)
                shutil.copyfileobj(original, new)
            target.chmod(0o755)
            if canonical:
                with target.open('rb') as installed:
                    assert hashlib.file_digest(installed, 'sha256').hexdigest() == canonical_binary_hashes[service]
                assert target.stat().st_uid == 0 and target.stat().st_mode & 0o777 == 0o755
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
            + ('MANAGEMENT_SERVICE_RESTART=true\n' if args.systemd_restart_probe else ''))
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
                # Only paired backup needs an observed generation from the exact
                # disposable OVS database. Keep other probes in bootstrap mode.
                ovs_endpoint = ovs / ('db.sock' if args.offline_pair_probe else 'unconfigured.sock')
                rendered = rendered.replace('--database=${MANAGER_DATABASE}',
                    f'--database=${{MANAGER_DATABASE}} --ovsdb-socket={ovs_endpoint} --ovsdb-file={ovs}/conf.db')
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
        if canonical:
            for service, pid in (('mgrd', manager_pid), ('webd', web_pid)):
                with Path(f'/proc/{pid}/exe').open('rb') as running:
                    assert hashlib.file_digest(running, 'sha256').hexdigest() == canonical_binary_hashes[service]
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
        if args.offline_pair_probe:
            report = canonical_offline_pair(binary_dir, fixture, manager_database, web_database,
                                            account, origin, units, ready, forwarding, context, restart_password)
            report['fixture_executables_match_ci_build'] = True
            destination = repo / 'test-results/go-offline-backup-proof.json'
            destination.parent.mkdir(exist_ok=True)
            destination.write_text(json.dumps({'architecture': os.uname().machine, **report}, indent=2) + '\n')
            return
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
        log_name = ('go-offline-backup-journal.log' if args.offline_pair_probe else
                    'go-systemd-restart-journal.log' if canonical else 'go-runtime-journal.log')
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
