"""Pause and resume only the formal Mirror test's disposable OVSDB process."""

import json
import os
from pathlib import Path
import re
import select
import signal
import stat
import sys
import time


def require(condition, description):
    if not condition:
        raise RuntimeError(description)


def owned(path, kind, *, private=True):
    info = path.lstat()
    require(kind(info.st_mode) and info.st_uid == 0 and (not private or info.st_mode & 0o022 == 0),
            'Unsafe isolated Mirror fixture path')
    return info


def fixture_process():
    metadata = Path(os.environ['OVS_FRONTEND_FIXTURE'])
    fixture = metadata.parent
    require(os.geteuid() == 0 and fixture.parent == Path('/var/lib') and
            re.fullmatch(r'ovs-inventory-fixture-[0-9a-f]{10}', fixture.name) and
            metadata.name == 'browser-private.json', 'Isolated Mirror fixture required')
    owned(fixture, stat.S_ISDIR)
    require(owned(metadata, stat.S_ISREG).st_mode & 0o777 == 0o600, 'Unsafe Mirror fixture metadata')
    options = json.loads(metadata.read_text())

    ovs = fixture / 'ovs'
    database, socket, control, pidfile = (ovs / name for name in ('conf.db', 'db.sock', 'db.ctl', 'db.pid'))
    require((options['ovsDirectory'], options['database'], options['dbSocket']) ==
            (str(ovs), str(database), str(socket)), 'Unexpected Mirror fixture paths')
    binary = ovs / 'bin' / 'ovsdb-server'
    require(owned(ovs, stat.S_ISDIR).st_mode & 0o077 == 0, 'Mirror fixture OVS directory is not private')
    owned(binary.parent, stat.S_ISDIR)
    owned(binary, stat.S_ISREG)
    owned(database, stat.S_ISREG)
    owned(socket, stat.S_ISSOCK, private=False)
    owned(control, stat.S_ISSOCK, private=False)
    owned(pidfile, stat.S_ISREG)
    pid_text = pidfile.read_text().strip()
    require(re.fullmatch(r'[1-9][0-9]{0,8}', pid_text) and int(pid_text) > 1, 'Invalid Mirror fixture PID')
    args = [str(database), f'--remote=punix:{socket}', f'--pidfile={pidfile}', f'--unixctl={control}',
            '--detach', '--no-chdir', '--overwrite-pidfile']
    return int(pid_text), binary, args


def process_state(proc):
    return (proc / 'stat').read_text().rsplit(')', 1)[1].split()[0]


def interrupted(_signal, _frame):
    raise InterruptedError('Mirror fixture controller interrupted')


def pause_until_released():
    pid, binary, args = fixture_process()
    proc = Path('/proc') / str(pid)
    fd = os.pidfd_open(pid)
    try:
        executable = proc / 'exe'
        require(proc.stat().st_uid == 0 and executable.readlink() == binary and executable.samefile(binary),
                'Unproven Mirror fixture executable')
        with (proc / 'cmdline').open('rb') as cmdline:
            argv = cmdline.read(65537).split(b'\0')
        require(argv[1:] == [os.fsencode(arg) for arg in args] + [b''] and
                process_state(proc) not in ('T', 't', 'Z'), 'Unproven Mirror fixture process')
        try:
            signal.pidfd_send_signal(fd, signal.SIGSTOP)
            deadline = time.monotonic() + 1
            while process_state(proc) != 'T':
                require(time.monotonic() < deadline, 'Mirror fixture did not pause')
                time.sleep(0.01)
            print('paused', flush=True)
            # The parent closes the pipe from its finally block. EOF or this
            # deadline also releases the same process if the runner exits.
            require(select.select([sys.stdin], [], [], 25)[0] and sys.stdin.buffer.read(1) == b'',
                    'Mirror fixture pause did not finish normally')
        finally:
            signal.pidfd_send_signal(fd, signal.SIGCONT)
    finally:
        os.close(fd)


if __name__ == '__main__':
    signal.signal(signal.SIGTERM, interrupted)
    pause_until_released()
