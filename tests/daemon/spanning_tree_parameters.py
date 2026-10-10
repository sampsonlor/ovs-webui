"""Compare read-only parameter checks with an isolated, real OVS daemon.

Only this disposable fixture uses vsctl/appctl. Production gets no shell path,
write authority or Applied proof from this test or from parameter validation.
Schema fixtures run against the installed OVS binary, whose version is retained.
"""

import re


def verify_spanning_tree_parameters(vsctl, get, call, observer, eventually, ovs_run, ovs):
    name = 'br-tree-params'
    checks, cases = [], []
    matrix = [
        ('stp-minimum', 'stp', {'stp-priority': '0', 'stp-hello-time': '1', 'stp-max-age': '6', 'stp-forward-delay': '4'}, 'valid', '', (0, 1, 6, 4)),
        ('stp-maximum', 'stp', {'stp-priority': '65535', 'stp-max-age': '40', 'stp-forward-delay': '30'}, 'valid', '', (65535, 2, 40, 30)),
        ('stp-clamped-max-age', 'stp', {'stp-max-age': '5'}, 'invalid', 'SPANNING_TREE_PARAMETER_RANGE', (32768, 2, 6, 15)),
        ('stp-clamped-forward-delay', 'stp', {'stp-forward-delay': '4'}, 'invalid', 'STP_FORWARD_DELAY_MAX_AGE_RELATION', (32768, 2, 20, 11)),
        ('stp-hello-time-unit-caveat', 'stp', {'stp-hello-time': '10', 'stp-max-age': '22', 'stp-forward-delay': '12'}, 'runtime-unverified', 'STP_HELLO_TIME_NATIVE_UNIT_CAVEAT', (32768, 1, 22, 12)),
        ('rstp-minimum', 'rstp', {'rstp-priority': '0', 'rstp-max-age': '6', 'rstp-forward-delay': '4'}, 'valid', '', (0, 2, 6, 4)),
        ('rstp-maximum-priority', 'rstp', {'rstp-priority': '61440'}, 'valid', '', (61440, 2, 20, 15)),
        ('rstp-rounded-priority', 'rstp', {'rstp-priority': '4097'}, 'invalid', 'RSTP_PRIORITY_MULTIPLE_4096', (4096, 2, 20, 15)),
        ('rstp-retained-forward-delay', 'rstp', {'rstp-forward-delay': '4'}, 'invalid', 'RSTP_FORWARD_DELAY_MAX_AGE_RELATION', (32768, 2, 20, 15)),
    ]
    previous = None
    try:
        for label, protocol, values, state, code, installed in matrix:
            vsctl('--if-exists', 'del-br', name)
            args = ['add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=dummy', protocol + '_enable=true']
            args += ['other_config:' + k + '=' + v for k, v in values.items()]
            vsctl(*args)

            def resource():
                row = next((b for b in get('/inventory/spanning-tree?filter=' + name)['items'] if b['name'] == name), None)
                if not row or row['management_id'] == previous:
                    return None
                path = '/bridges/' + row['management_id'] + '/spanning-tree'
                item = get(path)
                tree = item['spanning_tree']
                if tree['protocol'] != protocol or item['source']['freshness'] != 'fresh':
                    return None
                if any(tree['configuration'][k]['value'] != v for k, v in values.items()):
                    return None
                return item

            item = eventually(resource)
            previous = item['management_id']
            path = '/bridges/' + previous + '/spanning-tree'
            tree = item['spanning_tree']
            validation = tree['parameter_validation']
            assert validation['state'] == state, (label, validation)
            assert validation['scope'] == 'bridge-basic-parameters' and validation['version'] == 'bridge-basic-v1'
            assert [c['code'] for c in validation['checks']] == ([code] if code else []), (label, validation)
            assert not tree['editable'] and not item['editable'] and item['allowed_operations'] == []
            native = ovs_run('ovs-appctl', '-t', str(ovs / 'switch.ctl'), protocol + '/show', name)
            local = native.split('Bridge ID:', 1)[1]

            def number(key):
                match = re.search(r'^\s*' + re.escape(key) + r'\s+(\d+)s?\s*$', local, re.MULTILINE)
                assert match, (label, key, native)
                return int(match[1])

            actual = tuple(number(k) for k in ('stp-priority', 'stp-hello-time', 'stp-max-age', 'stp-fwd-delay'))
            assert actual == installed, (label, actual, installed)
            for key in ('stp-hello-time', 'stp-max-age', 'stp-forward-delay', 'rstp-max-age', 'rstp-forward-delay'):
                if key not in values:
                    assert tree['configuration'][key]['availability'] == 'unset' and tree['configuration'][key]['value'] is None
            assert get('/bridges/' + previous)['spanning_tree']['parameter_validation'] == validation
            status, hidden, _ = call(path, bearer=observer)
            assert status == 200
            assert hidden['spanning_tree']['parameter_validation']['state'] == 'withheld'
            assert hidden['spanning_tree']['parameter_validation']['checks'] == []
            cases.append({'name': label, 'protocol': protocol, 'configured': values, 'validation': validation,
                          'installed': dict(zip(('priority', 'hello_time', 'max_age', 'forward_delay'), actual))})
        checks += ['nine native boundary and normalization cases compared with ovs-vswitchd, not only OVSDB configuration',
                   'STP clamping, RSTP rounding and retained timer values remain distinct from raw configuration',
                   'same Bridge projection, raw unset defaults, immutable replacement identity and withheld validity checks',
                   'parameter validation never grants Candidate or native write authority']
        return {'verified': True, 'checks': checks, 'cases': cases,
                'ovs_binary_version': ovs_run('ovs-vswitchd', '--version').splitlines()[0],
                'write_gate': 'pending', 'applied_proof': False,
                'advanced_bridge_and_port_parameters': 'outside-basic-validator-scope',
                'installed_rstp_timer_transitions': 'separate-execution-gate'}
    finally:
        vsctl('--if-exists', 'del-br', name)
