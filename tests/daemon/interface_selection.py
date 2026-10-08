"""Read-only selection acceptance against real HTTPS/IPC/OVS inventory.

All mutations below belong to a disposable fixture, never the product provider.
"""
import urllib.parse


def verify_interface_selection(vsctl, get, call, observer, eventually, ovs_run, ovs):
    names = ['ns-10', 'ns-2', 'ns-1']
    checks = []
    args = ['add-br', 'br-fselect', '--', 'set', 'Bridge', 'br-fselect', 'datapath_type=dummy']
    for name in names:
        args += ['--', 'add-port', 'br-fselect', name, '--', 'set', 'Interface', name, 'type=dummy']
    vsctl(*args)
    try:
        vsctl('add-br', 'br-fs-other', '--', 'set', 'Bridge', 'br-fs-other', 'datapath_type=dummy',
              '--', 'add-port', 'br-fs-other', 'ns-3', '--', 'set', 'Interface', 'ns-3', 'type=dummy')
        bridge = eventually(lambda: next((b for b in get('/bridges?filter=br-fselect')['items']
                                          if b['name'] == 'br-fselect'), None))
        scope = {'filter': 'ns-', 'bridge_id': bridge['management_id'], 'native_type': 'dummy'}
        path = lambda q: '/interfaces?' + urllib.parse.urlencode(q)
        def matching(query, expected):
            page = get(path(query))
            return page if [item['name'] for item in page['items']] == expected else None
        ovs_run('ovs-appctl', '-t', str(ovs / 'switch.ctl'), 'netdev-dummy/set-admin-state', 'ns-2', 'down')
        down = eventually(lambda: matching(dict(scope, link_state='down'), ['ns-2']))
        all_rows = get(path(scope))
        assert [i['name'] for i in all_rows['items']] == ['ns-1', 'ns-2', 'ns-10']
        assert all(i['bridge_ref']['id'] == bridge['management_id'] and 'linux_device' not in i for i in all_rows['items'])
        assert down['items'][0]['fields']['link_state']['value'] == ['down']
        checks.append('combined immutable Bridge, exact native type and actual OVS dummy link state; natural names before pagination; no host sampling')

        query = dict(scope, link_state='up', limit=1)
        first = get(path(query))
        assert [i['name'] for i in first['items']] == ['ns-1'] and first['next_cursor']
        query['cursor'] = first['next_cursor']
        second = get(path(query))
        assert [i['name'] for i in second['items']] == ['ns-10'] and second['next_cursor'] is None
        assert first['snapshot_id'] == second['snapshot_id']
        changed = dict(query, link_state='down')
        assert call(path(changed))[0] == 410
        checks.append('same-snapshot filtered pages and changed-filter cursor rejection')

        for typ in ('dummy', '', 'future-native'):
            code, problem, _ = call(path(dict(scope, native_type=typ)), bearer=observer)
            assert code == 403 and problem['code'] == 'CAPABILITY_DENIED'
        code, permitted, _ = call(path({'bridge_id': bridge['management_id'], 'link_state': 'down'}), bearer=observer)
        assert code == 200 and [i['name'] for i in permitted['items']] == ['ns-2']
        assert permitted['items'][0]['fields']['type']['availability'] == 'withheld'
        checks.append('type filters deny inventory-only token including empty or nonexistent values; OVS state scope remains permitted')

        vsctl('--no-wait', 'set', 'Interface', 'ns-10', 'type=""')
        defaults = eventually(lambda: matching(dict(scope, native_type=''), ['ns-10']))
        assert defaults['items'][0]['fields']['type']['value'] == ''
        checks.append('observed native empty type selected exactly without inferring device readiness')
        vsctl('set', 'Interface', 'ns-10', 'type=dummy')

        vsctl('del-br', 'br-fselect')
        eventually(lambda: call(path(scope))[0] == 404)
        vsctl('add-br', 'br-fselect', '--', 'set', 'Bridge', 'br-fselect', 'datapath_type=dummy',
              '--', 'add-port', 'br-fselect', 'ns-1', '--', 'set', 'Interface', 'ns-1', 'type=dummy')
        replacement = eventually(lambda: next((b for b in get('/bridges?filter=br-fselect')['items']
                                               if b['management_id'] != bridge['management_id']), None))
        code, problem, _ = call(path(scope))
        assert code == 404 and problem['code'] == 'BRIDGE_SCOPE_NOT_FOUND'
        selected = eventually(lambda: matching(dict(scope, bridge_id=replacement['management_id']), ['ns-1']))
        assert selected['items'][0]['bridge_ref']['id'] == replacement['management_id']
        checks.append('same-name Bridge replacement has a fresh identity; old scope remains rejected rather than empty or redirected')

        code, problem, _ = call('/interfaces?native_type=dummy&native_type=internal')
        assert code == 400 and problem['code'] == 'AMBIGUOUS_REQUEST'
        assert call('/interfaces?link_state=carrier')[0] == 422
        checks.append('duplicate selection and non-OVS state parameters rejected by the public contract')
        return {'verified': True, 'checks': checks, 'natural_order': ['ns-1', 'ns-2', 'ns-10'],
                'permission_oracle_blocked': True, 'same_name_scope_rejected': True, 'linux_samples': False}
    finally:
        vsctl('--if-exists', 'del-br', 'br-fselect', '--', '--if-exists', 'del-br', 'br-fs-other')
