"""Run the real SQLite full-disk test on a private, bounded tmpfs in Linux CI."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile

assert os.geteuid() == 0, 'Ephemeral Linux CI root is required'
binary = Path(sys.argv[1]).resolve(strict=True)
with tempfile.TemporaryDirectory(prefix='ovs-storage-full-', dir='/run') as directory:
    root = Path(directory)
    subprocess.run(['mount', '-t', 'tmpfs', '-o', 'size=2M,mode=0700', 'tmpfs', str(root)], check=True)
    try:
        subprocess.run([str(binary), '-test.run=^TestFilesystemFull$', '-test.v', '-test.timeout=30s'],
                       env=dict(os.environ, OVS_STORAGE_FULL_DIR=str(root)), check=True, timeout=40)
    finally:
        subprocess.run(['umount', str(root)], check=True)
