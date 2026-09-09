"""Ephemeral Linux CI only: real systemd services and isolated OVS dummy datapath.

Never invoked by a production binary, installer or normal local pnpm command.
Every unit, socket and database created here has a unique fixture identity.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import ssl
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def run(*args, env=None, check=True):
    result = subprocess.run(args, env=env, capture_output=True, text=True, timeout=20)
    if check and result.returncode:
        raise RuntimeError(f'{args[0]} failed: {result.stderr.strip()}')
    return result.stdout


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


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--bin-dir', required=True)
    args = parser.parse_args()
    assert os.geteuid() == 0, 'This isolated CI test needs root for systemd/UID evidence'
    assert Path('/run/systemd/system').is_dir(), 'A real systemd host is required'
    source = Path(args.bin_dir).resolve()
    repo = Path(__file__).resolve().parents[2]
    suffix = uuid.uuid4().hex[:12]
    fixture = Path('/run') / f'ovs-webui-fixture-{suffix}'
    runtime_name = f'ovs-webui-runtime-{suffix}'
    runtime = Path('/run') / runtime_name
    units = {service: f'ovs-{service}-ci-{suffix}.service' for service in ('mgrd', 'webd')}
    unit_paths = [Path('/etc/systemd/system') / name for name in units.values()]
    fixture.mkdir(mode=0o750)
    os.chown(fixture, 0, 65534)
    ovs = fixture / 'ovs'
    ovs.mkdir(mode=0o700)
    env = dict(os.environ, OVS_RUNDIR=str(ovs), OVS_LOGDIR=str(ovs), OVS_DBDIR=str(ovs))
    checks = []
    try:
        for service in units:
            target = fixture / f'ovs-{service}'
            shutil.copyfile(source / f'ovs-{service}', target)
            target.chmod(0o755)
        cert, key = fixture / 'server.crt', fixture / 'server.key'
        run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
            '-subj', '/CN=localhost', '-addext', 'subjectAltName=IP:127.0.0.1,DNS:localhost',
            '-keyout', str(key), '-out', str(cert))
        os.chown(key, 0, 65534)
        key.chmod(0o640)
        cert.chmod(0o644)
        with socket.socket() as reserve:
            reserve.bind(('127.0.0.1', 0))
            port = reserve.getsockname()[1]
        configuration = fixture / 'runtime.env'
        configuration.write_text(
            f'WEBD_UID=65534\nWEBD_GID=65534\nMANAGER_SOCKET={runtime}/mgrd.sock\n'
            f'HTTPS_LISTEN=127.0.0.1:{port}\nTLS_CERT={cert}\nTLS_KEY={key}\n')
        for service, name in units.items():
            template = (repo / 'packaging/systemd' / f'ovs-{service}.service').read_text()
            assert not re.search(r'^(PartOf|BindsTo|Requires|ExecStop)=', template, re.M)
            assert 'openvswitch' not in template and 'ovs-vswitchd' not in template
            rendered = template.replace('ovs-webui-web', 'nobody')
            # Ubuntu's nobody user belongs to the nogroup group.
            rendered = rendered.replace('Group=nobody', 'Group=nogroup')
            rendered = rendered.replace('/etc/ovs-webui/runtime.env', str(configuration))
            rendered = rendered.replace(f'/usr/libexec/ovs-{service}', str(fixture / f'ovs-{service}'))
            rendered = rendered.replace('/run/ovs-webui', str(runtime))
            rendered = rendered.replace('RuntimeDirectory=ovs-webui', f'RuntimeDirectory={runtime_name}')
            rendered = rendered.replace('After=network.target ovs-mgrd.service', f'After=network.target {units["mgrd"]}')
            (Path('/etc/systemd/system') / name).write_text(rendered)
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
            assert not result['configuration_ready'] and not result['authentication_ready']
            return True

        run('systemctl', 'start', units['mgrd'], units['webd'])
        eventually(ready)
        manager_pid = run('systemctl', 'show', '-p', 'MainPID', '--value', units['mgrd']).strip()
        web_pid = run('systemctl', 'show', '-p', 'MainPID', '--value', units['webd']).strip()
        assert re.search(r'^Uid:\s+0\s+0\s+0\s+0', Path(f'/proc/{manager_pid}/status').read_text(), re.M)
        assert re.search(r'^Uid:\s+65534\s+65534\s+65534\s+65534', Path(f'/proc/{web_pid}/status').read_text(), re.M)
        assert (runtime / 'mgrd.sock').stat().st_mode & 0o777 == 0o660
        checks.append('root mgrd and non-root webd communicate through credential-checked IPC and HTTPS')
        assert fetch('/api/v1/transactions')[0] == 404
        with socket.create_connection(('127.0.0.1', port), timeout=2) as plain:
            plain.sendall(b'GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n')
            assert b'200 OK' not in plain.recv(4096)
        checks.append('TLS-only listener and unavailable business API')
        forwarding()
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
        result = {'architecture': os.uname().machine, 'ovs': run('ovs-vswitchd', '--version').splitlines()[0], 'checks': checks}
        destination = repo / 'test-results/go-runtime-smoke.json'
        destination.parent.mkdir(exist_ok=True)
        destination.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
    finally:
        journal = []
        for name in units.values():
            journal.append(run('journalctl', '--no-pager', '-n', '40', '-u', name, check=False))
            run('systemctl', 'stop', name, check=False)
        log = repo / 'test-results/go-runtime-journal.log'
        log.parent.mkdir(exist_ok=True)
        log.write_text('\n'.join(journal))
        print(log.read_text())
        for control in ('switch.ctl', 'db.ctl'):
            if (ovs / control).exists():
                run('ovs-appctl', '-t', str(ovs / control), 'exit', env=env, check=False)
        for path in unit_paths:
            path.unlink(missing_ok=True)
        run('systemctl', 'daemon-reload', check=False)
        # These absolute paths were generated above under /run for this fixture.
        assert fixture.parent == Path('/run') and fixture.name.startswith('ovs-webui-fixture-')
        shutil.rmtree(fixture)


if __name__ == '__main__':
    main()
