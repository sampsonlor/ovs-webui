"""Read native exporter references through the authenticated disposable service."""

import json

TYPES = ('sflow', 'netflow', 'ipfix')


def verify_telemetry(vsctl, get, call, observer, eventually):
    name = 'br-telem-native'
    collectors = {
        'sflow': ['127.0.0.1:6343', '[::1]:6343'],
        'netflow': ['127.0.0.1:2055'],
        'ipfix': ['127.0.0.1:4739'],
    }
    private_marker = 'synthetic-private-telemetry'
    checks = []

    def listed_bridge():
        items = get('/telemetry?filter=' + name)['items']
        return next((item for item in items if item['name'] == name), None)

    def read_states(path, expected):
        code, value, _ = call(path)
        if code == 200 and tuple(value['native_telemetry']['exporters'][k]['state'] for k in TYPES) == expected:
            return value
        return None

    vsctl('add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=dummy')
    try:
        bridge = eventually(listed_bridge)
        bridge_id = bridge['bridge_ref']['id']
        path = '/telemetry/' + bridge_id
        absent = ('unconfigured',) * len(TYPES)
        before = eventually(lambda: read_states(path, absent))
        assert before['bridge_ref'] == {'kind': 'bridge', 'id': bridge_id}
        assert get('/bridges/' + bridge_id)['name'] == name
        assert before['native_telemetry']['delivery'] == 'unverified'
        assert all(exporter['native_uuid'] is None for exporter in before['native_telemetry']['exporters'].values())
        checks.append('empty native Bridge exporter sets are independently unconfigured')

        # Fixture writes use loopback collectors and never treat sending as evidence.
        vsctl('--', '--id=@sf', 'create', 'sFlow', 'targets=' + json.dumps(collectors['sflow']),
              'sampling=200', 'polling=0', 'header=128', 'external_ids:fixture=' + private_marker,
              '--', '--id=@nf', 'create', 'NetFlow', 'targets=' + json.dumps(collectors['netflow']),
              'engine_type=0', 'active_timeout=-1', 'add_id_to_interface=true',
              '--', '--id=@ipf', 'create', 'IPFIX', 'targets=' + json.dumps(collectors['ipfix']),
              'sampling=400', 'obs_domain_id=0', 'obs_point_id=21', 'cache_active_timeout=0',
              'other_config:enable-tunnel-sampling=false', 'other_config:fixture=' + private_marker,
              '--', 'set', 'Bridge', name, 'sflow=@sf', 'netflow=@nf', 'ipfix=@ipf')
        ready = ('configured',) * len(TYPES)
        detail = eventually(lambda: read_states(path, ready))
        assert detail['resource_kind'] == 'telemetry'
        assert detail['instance_generation'] == before['instance_generation']
        assert detail['source']['authority'] == 'ovsdb-configuration'
        assert detail['source']['freshness'] == 'fresh' and detail['source']['observed_at']
        assert detail['native_telemetry']['delivery'] == 'unverified'
        exporters = detail['native_telemetry']['exporters']
        for kind, targets in collectors.items():
            actual = exporters[kind]
            assert actual['native_uuid'] == vsctl('get', 'Bridge', name, kind).strip()
            assert actual['target_count'] == len(targets) and actual['targets_state'] == 'known'
            assert sorted(actual['targets']) == sorted(targets)
        assert len({exporters[kind]['native_uuid'] for kind in TYPES}) == len(TYPES)
        for kind, known in {'sflow': {'sampling': 200, 'polling': 0, 'header': 128},
                            'netflow': {'engine_type': 0, 'active_timeout': -1, 'add_id_to_interface': True},
                            'ipfix': {'sampling': 400, 'obs_domain_id': 0, 'obs_point_id': 21,
                                      'cache_active_timeout': 0, 'enable-tunnel-sampling': False}}.items():
            for field, value in known.items():
                actual = exporters[kind]['settings'][field]
                assert actual['state'] == 'known' and type(actual['value']) is type(value) and actual['value'] == value
        for kind, field in [('sflow', 'agent'), ('netflow', 'engine_id'), ('ipfix', 'cache_max_flows')]:
            assert exporters[kind]['settings'][field] == {'state': 'unset', 'value': None}
        assert private_marker not in json.dumps(detail)
        checks.append('three exact exporter references, native sentinels/zero/unset, IPv4 and IPv6 targets and allowlisted settings')

        listing = eventually(listed_bridge)
        listed = listing['native_telemetry']['exporters']
        assert all(listed[k]['state'] == 'configured' and listed[k]['target_count'] == len(collectors[k])
                   and listed[k]['targets'] is None and listed[k]['targets_state'] == 'not-projected'
                   and listed[k]['settings'] == {} for k in TYPES)
        assert call('/telemetry', bearer=observer)[0] == 403 and call(path, bearer=observer)[0] == 403
        assert call(path, anonymous=True)[0] == 401
        checks.append('bounded list withholds targets and settings; authenticated configuration permission gates list and detail')

        more_targets = [f'127.0.0.1:{port}' for port in range(4700, 4717)]
        vsctl('set', 'IPFIX', exporters['ipfix']['native_uuid'], 'targets=' + json.dumps(more_targets))

        def capped_ipfix():
            row = get(path)['native_telemetry']['exporters']['ipfix']
            return row if row['targets_state'] == 'limited' else None

        capped = eventually(capped_ipfix)
        assert capped['target_count'] == len(more_targets)
        assert len(capped['targets'] or []) <= 16 and set(capped['targets'] or []).issubset(more_targets)
        checks.append('target set beyond the detail budget reports its count and a limited projection')

        vsctl('clear', 'Bridge', name, 'sflow')
        without_sflow = eventually(lambda: read_states(path, ('unconfigured', 'configured', 'configured')))
        assert without_sflow['native_telemetry']['exporters']['netflow'] == exporters['netflow']
        assert without_sflow['native_telemetry']['exporters']['ipfix']['native_uuid'] == exporters['ipfix']['native_uuid']
        checks.append('clearing a single native exporter preserves the independent remaining configurations')

        vsctl('del-br', name)
        eventually(lambda: call(path)[0] == 404)
        vsctl('add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=dummy')
        replacement = eventually(listed_bridge)
        assert replacement['bridge_ref']['id'] != bridge_id and call(path)[0] == 404
        checks.append('same-name Bridge recreation does not inherit the retired telemetry identity')
        return {'verified': True, 'mode': 'observe', 'checks': checks, 'exporters': list(TYPES),
                'source_authority': 'ovsdb-configuration', 'collector_delivery': 'unverified'}
    finally:
        vsctl('--no-wait', '--if-exists', 'del-br', name)
