"""Draft-only acceptance on real HTTPS/IPC/OVSDB, with root-owned fixture grants.

No production shell path is added. This proves preparation and review guards;
neither schema fixtures nor configured values prove installed timers or Applied.
"""
from pathlib import Path
import uuid

from authentication import request_id, run


def reconfigure_draft_fixture(unit_path, unit, text):
    # These explicit, manual authority transitions belong only to a disposable
    # inventory fixture. Verify it is healthy BEFORE stopping/resetting the
    # manual start counter, so a genuine crash/start-limit failure is not hidden.
    # The production unit template, crash restart limit and auth budgets remain
    # intact. Subsequent crash/recovery tests retain their own original gates.
    assert unit.startswith('ovs-mgrd-inventory-') and unit.endswith('.service') and unit_path.name == unit
    run('systemctl', 'is-active', '--quiet', unit)
    run('systemctl', 'stop', unit)
    unit_path.write_text(text)
    run('systemctl', 'daemon-reload')
    run('systemctl', 'reset-failed', unit)
    run('systemctl', 'start', unit)


def verify_spanning_tree_prepare(vsctl, get, call, observer, eventually, units):
    name = 'br-tree-draft'
    unit_path = Path('/etc/systemd/system') / units['mgrd']
    original = unit_path.read_text()
    needle = '--database=${MANAGER_DATABASE}'
    assert needle in original and '--local-spanning-tree-bridges' not in original
    checks = []
    epochs = get('/session')['request_epochs']

    def ready():
        code, value, _ = call('/inventory')
        return code == 200 and value['availability'] == 'complete'

    def authority(ids):
        text = original.replace(needle, needle + ' --local-spanning-tree-bridges=' + ids) if ids else original
        reconfigure_draft_fixture(unit_path, units['mgrd'], text)
        eventually(ready)

    def patch(body, revision, expected=200):
        code, result, _ = call('/candidate', 'PATCH', body, extra_headers={
            'If-Match': '"' + revision + '"', 'X-OVS-Request-Epoch': epochs['workspace']})
        assert code == expected, (code, result)
        return result

    def discard():
        current = get('/candidate')
        if current['intents']:
            assert len(current['intents']) == 1 and current['intents'][0]['operation'] == 'bridge.spanning-tree.prepare'
            patch({'request_id': request_id(), 'operation': 'discard'}, current['revision'])

    def stage(protocol='rstp', parameters=None, expected=200):
        current = get('/candidate')
        result = patch({'request_id': request_id(), 'operation': 'stage', 'intents': [{
            'intent_id': intent_id, 'operation': 'bridge.spanning-tree.prepare', 'object': binding,
            'spanning_tree': {'protocol': protocol, 'parameters': parameters if parameters is not None else {'rstp-priority': '4096'}}}]}, current['revision'], expected)
        # PATCH returns the stored Candidate. Diff and current guard checks are
        # deliberately computed by the authoritative GET, not the write reply.
        return get('/candidate') if expected == 200 else result

    def validate(draft):
        code, ack, _ = call('/validations', 'POST', {'request_id': request_id(), 'candidate_id': draft['id'], 'candidate_revision': draft['revision']})
        assert code == 202, (code, ack)
        value = get('/validations/' + ack['resource_ref']['id'])
        assert value['state'] == 'blocked' and not value['usable'] and not value['execution_ready'], value
        assert any(c['code'] == 'SPANNING_TREE_EXECUTION_PENDING' and c['state'] == 'blocked' for c in value['checks'])
        job = get('/jobs/' + ack['job_id'])
        assert job['state'] == 'succeeded' and job['applied_outcome'] == 'not-applicable'
        audit = get('/audit?correlation_id=' + ack['correlation_id'])['items']
        assert len(audit) == 1 and audit[0]['changeset_id'] == value['changeset_id']
        return value

    assert get('/candidate')['state'] == 'empty'
    vsctl('--no-wait', 'add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=system')
    binding, path, intent_id = None, None, str(uuid.uuid4())
    try:
        eventually(lambda: any(b['name'] == name for b in get('/inventory/spanning-tree?filter=' + name)['items']))
        row = next(b for b in get('/inventory/spanning-tree?filter=' + name)['items'] if b['name'] == name)
        binding = {k: row[k] for k in ('management_id', 'ovs_uuid', 'instance_generation')}
        binding['table'] = 'Bridge'
        path = '/bridges/' + binding['management_id'] + '/spanning-tree'
        assert not get(path)['preparation']['available']
        denied = stage(expected=409)
        assert denied['code'] == 'SPANNING_TREE_BRIDGE_AUTHORITY_REQUIRED'
        authority(binding['management_id'])
        item = eventually(lambda: get(path) if get(path)['preparation']['available'] else None)
        assert not item['editable'] and item['allowed_operations'] == []
        assert item['preparation']['mode'] == 'draft-only'
        code, withheld, _ = call(path, bearer=observer)
        assert code == 200 and not withheld['preparation']['available'] and withheld['preparation']['authority'] == 'withheld'
        checks.append('independent immutable Bridge grant permits draft-only hints; legacy editing and observer configuration stay closed')

        for protocol, params in [('rstp', {'rstp-hello-time': '2'}), ('rstp', {'stp-priority': '4096'}), ('disabled', {'rstp-priority': None})]:
            stage(protocol, params, expected=422)
        original_native = vsctl('get', 'Bridge', name, 'stp_enable', 'rstp_enable', 'other_config')
        draft = stage()
        change = draft['intents'][0]['spanning_tree_change']
        assert not change['before']['stp_enable'] and not change['before']['rstp_enable']
        assert all(v is None for v in change['before']['parameters'].values())
        assert change['after']['rstp_enable'] and change['after']['parameters']['rstp-priority'] == '4096'
        assert len(draft['diff']) == 9 and not any(d['conflict'] for d in draft['diff'])
        review = validate(draft)
        assert vsctl('get', 'Bridge', name, 'stp_enable', 'rstp_enable', 'other_config') == original_native
        checks.append('closed request, sealed original, nine-field native Diff and durable blocked Validation Job/Audit leave live OVS unchanged')

        cleared = stage(parameters={'rstp-priority': None})
        assert cleared['intents'][0]['spanning_tree_change']['before'] == change['before']
        assert cleared['intents'][0]['spanning_tree_change']['after']['parameters']['rstp-priority'] is None
        rounded = stage(parameters={'rstp-priority': '4097'})
        assert any(c['code'] == 'RSTP_PRIORITY_MULTIPLE_4096' for c in validate(rounded)['checks'])
        caveat = stage('stp', {'stp-hello-time': '10', 'stp-max-age': '22', 'stp-forward-delay': '12'})
        assert any(c['code'] == 'STP_HELLO_TIME_NATIVE_UNIT_CAVEAT' for c in validate(caveat)['checks'])
        assert vsctl('get', 'Bridge', name, 'stp_enable', 'rstp_enable', 'other_config') == original_native
        checks.append('null restores native unset, restaging preserves originals and native rounding/unit caveats remain blocking')

        vsctl('--no-wait', 'set', 'Bridge', name, 'other_config:rstp-priority=8192')
        eventually(lambda: get(path)['spanning_tree']['configuration']['rstp-priority']['value'] == '8192')
        conflict = get('/candidate')
        assert conflict['state'] == 'reconciliation-required'
        assert conflict['intents'][0]['spanning_tree_change']['before'] == change['before']
        stage(expected=409)
        rejected = patch({'request_id': request_id(), 'operation': 'rebase',
                          'instance_generation': conflict['current_instance_generation'],
                          'current_config_revision': conflict['current_config_revision'],
                          'conflict_snapshot_id': conflict['conflict_snapshot_id'],
                          'resolutions': [{'intent_id': intent_id, 'choice': 'keep-mine'}]}, conflict['revision'], 409)
        assert rejected['code'] == 'SPANNING_TREE_RESTAGE_REQUIRED'
        discard()
        fresh = stage()
        assert fresh['intents'][0]['spanning_tree_change']['before']['parameters']['rstp-priority'] == '8192'
        checks.append('field drift cannot overwrite the captured original through edit or Rebase; discard and fresh review capture the current value')

        vsctl('--no-wait', 'set', 'Bridge', name, 'other_config:synthetic-private=dependency')
        eventually(lambda: get('/candidate')['state'] == 'reconciliation-required')
        assert any(c['code'] == 'SPANNING_TREE_DEPENDENCY_CHANGED' for c in get('/candidate')['checks'])
        discard()
        fresh = stage()
        authority('')
        assert not get(path)['preparation']['available']
        assert get('/candidate')['state'] == 'reconciliation-required'
        stage(expected=409)
        assert not get('/validations/' + review['id'])['usable']
        checks.append('unrelated persistent configuration and root grant revocation invalidate preparation without widening existing permissions')
        discard()

        vsctl('--no-wait', '--if-exists', 'del-br', name, '--', 'add-br', name, '--', 'set', 'Bridge', name, 'datapath_type=system')
        eventually(lambda: call(path)[0] == 404)
        authority(binding['management_id'])
        stage(expected=409)
        checks.append('a same-name replacement does not inherit the retired immutable Bridge grant')
        return {'verified': True, 'checks': checks, 'operation': 'bridge.spanning-tree.prepare',
                'execution_gate': 'SPANNING_TREE_EXECUTION_PENDING', 'applied_proof': False,
                'native_write_observed_from_draft': False}
    finally:
        discard()
        reconfigure_draft_fixture(unit_path, units['mgrd'], original)
        eventually(ready)
        vsctl('--no-wait', '--if-exists', 'del-br', name)
