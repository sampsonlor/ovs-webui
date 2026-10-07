"""Real kernel ingress rule evidence in each isolated native schema fixture."""
import json
import subprocess
import uuid


def verify_policing(vsctl, get, call, bearer, eventually, switch_log):
    suffix = uuid.uuid4().hex[:6]
    bridge, name, peer = 'br-pol-' + suffix, 'pol-' + suffix, 'polp-' + suffix

    def ip(*args):
        return subprocess.check_output(['ip', *args], text=True, timeout=5)

    def tc(*args):
        return subprocess.check_output(['tc', *args], text=True, timeout=5)

    def interface():
        item = next((i for i in get('/interfaces?filter=' + name)['items'] if i['name'] == name), None)
        return get('/interfaces/' + item['management_id']) if item else None

    def sample_ready():
        item = interface()
        if item and item['linux_ingress_policing']['availability'] == 'known':
            return item

    try:
        ip('link', 'add', name, 'type', 'veth', 'peer', 'name', peer)
        ip('link', 'set', name, 'up')
        ip('link', 'set', peer, 'up')
        vsctl('add-br', bridge, '--', 'add-port', bridge, name, '--', 'set', 'Interface', name,
              'ingress_policing_rate=1000', 'ingress_policing_kpkts_rate=5')

        def installed():
            item = sample_ready()
            if item and any(a['bytes_per_second'] == '125000' and a['packets_per_second'] == '5000'
                            and a['exceed_action'] == 'drop' for a in item['linux_ingress_policing']['actions']):
                return item

        try:
            item = eventually(installed)
        except AssertionError as error:
            # Only synthetic fixture state and kernel rates; never credentials,
            # headers, sessions or a general-purpose HTTP dump.
            last = interface()
            diagnostic = {'observation': last.get('linux_ingress_policing') if last else None,
                          'native_ifindex': last['fields']['ifindex'] if last else None,
                          'native_configuration': vsctl('list', 'Interface', name),
                          'kernel_qdiscs': json.loads(tc('-j', 'qdisc', 'show', 'dev', name)),
                          'kernel_filters': json.loads(tc('-j', 'filter', 'show', 'dev', name, 'parent', 'ffff:')),
                          'ovs_device_messages': [line for line in switch_log.read_text().splitlines() if name in line][-10:]}
            raise AssertionError('Synthetic ingress policing evidence: ' + json.dumps(diagnostic)) from error
        sample = item['linux_ingress_policing']
        assert sample['ifindex'] == json.loads(ip('-j', 'link', 'show', 'dev', name))[0]['ifindex']
        assert sample['source']['authority'] == 'linux-netlink-observation'
        assert sample['source']['freshness'] == 'fresh' and sample['source']['confidence'] == 'proven'
        assert item['allowed_operations'] == []
        # Independent iproute2 kernel observation; no mocked provider response.
        kernel = json.loads(tc('-j', 'filter', 'show', 'dev', name, 'parent', 'ffff:'))
        assert kernel and any(a.get('kind') == 'police' for f in kernel for a in f.get('options', {}).get('actions', []))
        revision = item['config_revision']
        code, observer, _ = call('/interfaces/' + item['management_id'], bearer=bearer)
        assert code == 200 and observer['linux_ingress_policing']['availability'] == 'withheld'
        assert observer['linux_ingress_policing']['actions'] == [] and observer['linux_ingress_policing']['ifindex'] is None
        assert all('linux_ingress_policing' not in i for i in get('/interfaces?filter=' + name)['items'])
        tc('filter', 'add', 'dev', name, 'parent', 'ffff:', 'protocol', 'all', 'pref', '80', 'matchall', 'action', 'pass')
        partial = interface()
        assert partial['linux_ingress_policing']['availability'] == 'partial'
        assert partial['linux_ingress_policing']['reason'] == 'LINUX_POLICING_COVERAGE_PARTIAL'
        assert partial['linux_ingress_policing']['actions'] and partial['config_revision'] == revision
        tc('filter', 'del', 'dev', name, 'parent', 'ffff:', 'pref', '80')
        vsctl('set', 'Interface', name, 'ingress_policing_rate=0', 'ingress_policing_kpkts_rate=0')
        eventually(lambda: (i if (i := sample_ready()) and i['linux_ingress_policing']['actions'] == [] else None))
        return {'verified': True, 'real_kernel': True, 'configuration_permission_required': True,
                'source_authority': 'linux-netlink-observation', 'byte_rate': '125000', 'packet_rate': '5000',
                'units': ['bytes/s', 'packets/s'], 'partial_coverage_verified': True,
                'runtime_does_not_change_config_revision': True, 'list_scan': False, 'editable': False,
                'traffic_effectiveness_claimed': False, 'installer_ownership_claimed': False}
    finally:
        vsctl('--if-exists', 'del-br', bridge)
        subprocess.run(['ip', 'link', 'del', name], capture_output=True, timeout=5, check=False)
