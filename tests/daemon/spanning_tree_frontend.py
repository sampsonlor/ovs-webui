"""Actual optional OVS build + production HTTPS/IPC/Safe Apply/browser fixture.

This is opt-in CI only. The default service's proc restriction is tested first;
the temporary unit adds one explicitly reviewed process-read capability later.
No production unit, authentication deadline or runtime fallback is modified.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import subprocess

from authentication import request_id, run
from spanning_tree_prepare import reconfigure_draft_fixture


def verify_spanning_tree_frontend(repo, fixture, node, origin, password, call, get, vsctl,
                                 units, ovs, manager_db, credentials, network, eventually):
    assert node and Path(node).is_file()
    assert call('/session/reauthentication','POST',{'password':password})[0]==200
    role = next(r for r in get('/roles')['items'] if r['name'] == 'NetworkAdmin')
    code, ack, _ = call('/roles', 'POST', {'request_id':request_id(),'name':'NativeTreeExecution','capabilities':role['capabilities']})
    assert code == 202, (code, ack)
    role_id = next(r['id'] for r in get('/roles')['items'] if r['name'] == 'NativeTreeExecution')
    accounts = {}
    for name in ['native-tree-denied','native-tree-confirm','native-tree-recover']:
        password = 'synthetic-native-tree-' + secrets.token_hex(16)
        credentials.append(password)
        code, ack, _ = call('/users','POST',{'request_id':request_id(),'username':name,'password':password,'role_ids':[role_id]})
        assert code == 202, (code,ack)
        accounts[name] = password
    multicast_password='synthetic-native-multicast-'+secrets.token_hex(16)
    credentials.append(multicast_password)
    reader_role=next(r['id'] for r in get('/roles')['items'] if r['name']=='Reader')
    code,ack,_=call('/users','POST',{'request_id':request_id(),'username':'native-multicast-reader','password':multicast_password,'role_ids':[reader_role]})
    assert code==202,(code,ack)
    name = 'br-native-tree'
    vsctl('add-br',name,'--','set','Bridge',name,'datapath_type=system')
    bridge = eventually(lambda: next((b for b in get('/bridges?filter='+name)['items'] if b['name']==name),None))
    unit_path = Path('/etc/systemd/system') / units['mgrd']
    original = unit_path.read_text()
    assert 'CapabilityBoundingSet=CAP_CHOWN CAP_NET_RAW\n' in original
    assert '--local-spanning-tree-bridges' not in original
    probe = re.search(r'--safe-apply-probe-interface=(\S+)',original).group(1)
    manifest = ovs / 'witness-build.json'
    sha = hashlib.sha256(manifest.read_bytes()).hexdigest()
    flags = f' --local-spanning-tree-bridges={bridge["management_id"]} --local-spanning-tree-execution-bridges={bridge["management_id"]}'
    flags += f' --spanning-tree-witness-socket={ovs}/switch.ctl --spanning-tree-witness-executable={ovs}/bin/ovs-vswitchd'
    flags += f' --spanning-tree-witness-manifest={manifest} --spanning-tree-witness-manifest-sha256={sha}'
    database = ovs / 'bin/ovsdb-server'
    database_hash = hashlib.sha256(database.read_bytes()).hexdigest()
    flags += f' --spanning-tree-database-executable={database} --spanning-tree-database-sha256={database_hash}'
    flags += f' --multicast-group-witness-socket={ovs}/switch.ctl --multicast-group-witness-executable={ovs}/bin/ovs-vswitchd'
    flags += f' --multicast-group-witness-manifest={manifest} --multicast-group-witness-manifest-sha256={sha}'
    # Provision only the dedicated fixture role; existing credentials cannot
    # acquire this capability and the builtin templates are unchanged.
    run('systemctl','is-active','--quiet',units['mgrd'])
    run('systemctl','stop',units['mgrd'])
    run(str(fixture/'ovs-mgrd'),'--database',str(manager_db),'--grant-spanning-tree-execution-role',role_id)
    unit_path.write_text(original.replace('--database=${MANAGER_DATABASE}','--database=${MANAGER_DATABASE}'+flags))
    run('systemctl','daemon-reload')
    run('systemctl','reset-failed',units['mgrd'])
    run('systemctl','start',units['mgrd'])
    eventually(lambda: call('/inventory')[0]==200)
    private = fixture / 'native-tree-browser.json'
    private.write_text(json.dumps({'origin':origin,'accounts':accounts,'bridge':bridge,'unit':units['mgrd'],
                                  'unitPath':str(unit_path),'probeInterface':probe,'dbSocket':str(ovs/'db.sock'),
                                  'nativeBinary':str(ovs/'bin/ovs-vsctl')}))
    private.chmod(0o600)
    try:
        env = dict(os.environ,OVS_SPANNING_TREE_FRONTEND_FIXTURE=str(private))
        result = subprocess.run([node,str(repo/'node_modules/@playwright/test/cli.js'),'test','--config',
                                 str(repo/'frontend/spanning-execution.playwright.config.ts')],cwd=repo,env=env,timeout=240)
        assert result.returncode==0,'qualified native spanning-tree frontend acceptance failed'
        vsctl('add-br','br-mcast-live','--','set','Bridge','br-mcast-live','datapath_type=system','mcast_snooping_enable=true')
        run('ip','link','add','mcast-live-p','type','veth','peer','name','mcast-live-peer')
        for iface in ['mcast-live-p','mcast-live-peer']:
            run('sysctl','-q','-w','net.ipv6.conf.'+iface+'.disable_ipv6=1')
        vsctl('add-port','br-mcast-live','mcast-live-p')
        run('ip','link','set','mcast-live-p','up')
        run('ip','addr','add','198.19.204.1/24','dev','mcast-live-peer')
        run('ip','link','set','mcast-live-peer','up')
        run('sysctl','-q','-w','net.ipv4.conf.mcast-live-peer.force_igmp_version=2')
        multicast_bridge=eventually(lambda: next((b for b in get('/bridges?filter=br-mcast-live')['items'] if b['name']=='br-mcast-live'),None))
        private.write_text(json.dumps({'origin':origin,'password':multicast_password,'bridge':multicast_bridge,'unit':units['mgrd'],'unitPath':str(unit_path),'manifestDigest':sha}))
        env=dict(os.environ,OVS_MULTICAST_FRONTEND_FIXTURE=str(private))
        result=subprocess.run([node,str(repo/'node_modules/@playwright/test/cli.js'),'test','--config',str(repo/'frontend/multicast-execution.playwright.config.ts')],cwd=repo,env=env,timeout=120)
        assert result.returncode==0,'qualified native multicast browser acceptance failed'
        return {'verified':True,'network':network,'backend':'qualified OVS 3.3.9 / root mgrd / unprivileged webd / HTTPS / Unix IPC',
                'default_proc_restriction_retained':True,'optional_fixture_capability':'CAP_SYS_PTRACE',
                'database_executable_sha256':database_hash,
                'report':'test-results/ovs-witness/frontend.xml','credentials_persisted_in_artifacts':False}
    finally:
        private.unlink(missing_ok=True)
        run('ip','link','set',probe,'up',check=False)
        reconfigure_draft_fixture(unit_path,units['mgrd'],original)
        vsctl('--no-wait','--if-exists','del-br',name)
        vsctl('--no-wait','--if-exists','del-br','br-mcast-live')
        run('ip','link','del','mcast-live-p',check=False)
