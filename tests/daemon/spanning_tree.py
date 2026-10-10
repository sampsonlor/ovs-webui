"""Native read acceptance; writes are disposable fixture setup, never provider actions."""


def verify_spanning_tree(vsctl, get, call, observer, eventually):
    name = 'br-tree-native'
    checks = []
    vsctl('add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=dummy',
          'rstp_enable=true', 'other_config:rstp-priority=4096',
          '--', 'add-port', name, 'tree-native-p', '--', 'set', 'Interface', 'tree-native-p', 'type=dummy',
          '--', 'add-bond', name, 'tree-native-bond', 'tree-native-b1', 'tree-native-b2',
          '--', 'set', 'Interface', 'tree-native-b1', 'type=dummy',
          '--', 'set', 'Interface', 'tree-native-b2', 'type=dummy')
    try:
        bridge = eventually(lambda: next((b for b in get('/inventory/spanning-tree?filter='+name)['items']
                                          if b['name'] == name and b['spanning_tree']['protocol'] == 'rstp'), None))
        path = '/bridges/' + bridge['management_id'] + '/spanning-tree'
        def observed():
            resource = get(path)
            tree = resource['spanning_tree']
            p = next((p for p in resource['ports'] if p['name'] == 'tree-native-p'), None)
            if tree['runtime']['rstp_bridge_id']['availability'] == 'known' and p and p['spanning_tree']['runtime']['rstp_port_state']['availability'] == 'known':
                return resource
        resource = eventually(observed)
        tree = resource['spanning_tree']
        assert resource['mode'] == 'observe' and not resource['editable'] and resource['allowed_operations'] == []
        assert tree['configuration']['rstp-priority']['value'] == '4096'
        assert tree['configuration']['rstp-max-age']['value'] is None
        assert tree['configuration']['rstp-max-age']['availability'] == 'unset'
        assert tree['runtime']['rstp_bridge_id']['source']['authority'] == 'ovs-vswitchd-observation'
        assert tree['configuration']['rstp_enable']['source']['authority'] == 'ovsdb-configuration'
        ports = {p['name']: p for p in resource['ports']}
        assert not resource['ports_truncated'] and len(ports) == 3
        assert ports[name]['spanning_tree']['rstp_participation']['value'] == 'excluded-internal'
        assert ports['tree-native-bond']['spanning_tree']['rstp_participation']['value'] == 'excluded-bond'
        assert ports['tree-native-p']['spanning_tree']['rstp_participation']['value'] == 'enabled-by-default'
        checks.append('native RSTP configured parameters, unset defaults, actual daemon status and Bond/internal exclusions')

        native_bridge = get('/bridges/' + bridge['management_id'])
        def values(fields):
            return {key: (field['value'], field['availability']) for key, field in fields.items()}
        assert native_bridge['spanning_tree']['protocol'] == tree['protocol']
        assert values(native_bridge['spanning_tree']['configuration']) == values(tree['configuration'])
        native_port = get('/ports/' + ports['tree-native-p']['port_ref']['id'])
        expected = values(ports['tree-native-p']['spanning_tree']['configuration'])
        assert all(expected[key] == value for key, value in values(native_port['spanning_tree']['configuration']).items())
        assert native_port['spanning_tree']['rstp_participation']['value'] == ports['tree-native-p']['spanning_tree']['rstp_participation']['value']
        checks.append('Bridge and Port detail reuse the same spanning-tree projections')

        code, withheld, _ = call(path, bearer=observer)
        assert code == 200
        assert withheld['spanning_tree']['protocol'] == 'unknown' and withheld['spanning_tree']['ownership'] == 'withheld'
        assert all(f['availability'] == 'withheld' and f['value'] is None for f in withheld['spanning_tree']['configuration'].values())
        assert withheld['spanning_tree']['runtime']['rstp_bridge_id']['availability'] == 'known'
        assert all(p['spanning_tree']['rstp_participation']['availability'] == 'withheld' for p in withheld['ports'])
        checks.append('configuration and type participation withheld; independent runtime retained for state/inventory observer')

        vsctl('set', 'Port', 'tree-native-p', 'other_config:rstp-enable=false')
        eventually(lambda: next(p for p in get(path)['ports'] if p['name'] == 'tree-native-p')['spanning_tree']['rstp_participation']['value'] == 'disabled-on-port')
        checks.append('explicit native Port exclusion observed independently from Bridge enablement')

        vsctl('set', 'Bridge', name, 'stp_enable=true')
        eventually(lambda: get(path)['spanning_tree']['protocol'] == 'invalid-both-enabled')
        checks.append('both-enable native conflict remains visible without automatic protocol choice')
        vsctl('set', 'Bridge', name, 'stp_enable=false', 'external_ids:ovn-owner=synthetic')
        eventually(lambda: get(path)['spanning_tree']['ownership'] == 'externally-controlled')
        checks.append('external authority remains visible and never grants writes')

        vsctl('del-br', name)
        eventually(lambda: call(path)[0] == 404)
        vsctl('add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=dummy')
        recreated = eventually(lambda: next((b for b in get('/inventory/spanning-tree?filter='+name)['items'] if b['name'] == name), None))
        assert recreated['management_id'] != bridge['management_id'] and recreated['ovs_uuid'] != bridge['ovs_uuid']
        assert call(path)[0] == 404
        checks.append('same-name native replacement cannot adopt the retired spanning-tree Bridge identity')
        return {'verified': True, 'mode': 'observe', 'checks': checks, 'write_gate': 'pending',
                'daemon_status_observed': True, 'schema_not_version_inferred': True}
    finally:
        vsctl('--if-exists', 'del-br', name)
