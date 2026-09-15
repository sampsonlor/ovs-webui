"""Candidate acceptance exercised inside the isolated real OVS inventory fixture."""
from concurrent.futures import ThreadPoolExecutor
import json
from pathlib import Path
import secrets
import sqlite3
import uuid

from authentication import request_id, run


def verify_candidate(call, get, vsctl, units, manager_db, web_db,
                     stop_db, start_db, eventually, credentials):
    target = next(p for p in get('/ports')['items'] if p['name'] == 'inv-p1')
    binding = {key: target[key] for key in ('management_id', 'ovs_uuid', 'instance_generation')}
    binding['table'] = 'Port'
    epochs = get('/session')['request_epochs']
    notes = []

    def ready():
        code, value, _ = call('/inventory')
        return code == 200 and value['availability'] == 'complete'

    def stage_command(tag):
        return {'request_id': request_id(), 'operation': 'stage', 'intents': [{
            'intent_id': intent_id, 'operation': 'port.vlan.set', 'object': binding,
            'value': {'vlan_mode': 'native-tagged', 'tag': tag, 'trunks': [], 'cvlans': []}}]}

    def patch(body, revision, expected=200, drop=False):
        code, result, _ = call('/candidate', 'PATCH', body, extra_headers={
            'If-Match': '"' + revision + '"', 'X-OVS-Request-Epoch': epochs['workspace']}, drop_response=drop)
        assert code == expected, (code, result)
        return result

    def validate(draft):
        command = {'request_id': request_id(), 'candidate_id': draft['id'], 'candidate_revision': draft['revision']}
        code, accepted, _ = call('/validations', 'POST', command)
        assert code == 202, (code, accepted)
        return command, accepted, get('/validations/' + accepted['resource_ref']['id'])

    def rebase(draft, choice):
        body = {'request_id': request_id(), 'operation': 'rebase',
                'instance_generation': draft['current_instance_generation'],
                'current_config_revision': draft['current_config_revision'],
                'conflict_snapshot_id': draft['conflict_snapshot_id'],
                'resolutions': [{'intent_id': intent_id, 'choice': choice}]}
        return patch(body, draft['revision'])

    initial = get('/candidate')
    assert initial['state'] == 'empty'
    intent_id = str(uuid.uuid4())
    stage = stage_command(67)
    draft = patch(stage, initial['revision'])
    assert draft['intents'][0]['before']['tag'] == 38
    assert int(vsctl('get', 'Port', 'inv-p1', 'tag')) == 38
    _, accepted, blocked = validate(draft)
    assert blocked['state'] == 'blocked' and not blocked['usable']
    assert any(g['code'] == 'OWNERSHIP_UNKNOWN' for g in blocked['checks'])
    assert get('/jobs/' + accepted['job_id'])['state'] == 'succeeded'
    notes.append('unknown VLAN authority blocks validation even when its durable Job succeeds')

    unit_path = Path('/etc/systemd/system') / units['mgrd']
    text = unit_path.read_text()
    needle = '--database=${MANAGER_DATABASE}'
    assert needle in text and '--local-vlan-ports' not in text
    unit_path.write_text(text.replace(needle, needle + ' --local-vlan-ports=' + binding['management_id']))
    run('systemctl', 'daemon-reload')
    run('systemctl', 'restart', units['mgrd'])
    eventually(ready)
    # The explicit server-side authority change is a dependency change, and
    # cannot silently rebase a draft prepared under the previous authority.
    draft = rebase(get('/candidate'), 'keep-mine')
    command, passed_ack, passed = validate(draft)
    validation_id = passed['id']
    assert passed['state'] == 'passed' and passed['usable'] and not passed['execution_ready']
    assert any(g['code'] == 'EMPTY_TRUNKS_MEANS_ALL_VLANS' for g in passed['checks'])
    audit = get('/audit?correlation_id=' + passed_ack['correlation_id'])['items']
    assert len(audit) == 1 and audit[0]['changeset_id'] == passed['changeset_id']
    assert get('/jobs/' + passed_ack['job_id'])['applied_outcome'] == 'not-applicable'
    notes.append('reviewed local Port authority and discovered native schema produce an immutable typed ChangeSet, Validation, Job and Audit')

    vsctl('set', 'Port', 'inv-p1', 'tag=39')
    eventually(lambda: get('/ports/' + binding['management_id'])['vlan']['native']['tag'] == 39)
    conflict = get('/candidate')
    tag_diff = next(d for d in conflict['diff'] if d['field'] == 'tag')
    assert (tag_diff['before'], tag_diff['current'], tag_diff['after']) == (38, 39, 67)
    assert conflict['state'] == 'conflict' and not get('/validations/' + validation_id)['usable']
    unresolved = {'request_id': request_id(), 'operation': 'rebase',
                  'instance_generation': conflict['current_instance_generation'],
                  'current_config_revision': conflict['current_config_revision'],
                  'conflict_snapshot_id': conflict['conflict_snapshot_id'], 'resolutions': []}
    patch(unresolved, conflict['revision'], expected=409)
    draft = rebase(conflict, 'keep-mine')
    assert draft['intents'][0]['before']['tag'] == 39 and draft['intents'][0]['value']['tag'] == 67
    code, replay, headers = call('/validations', 'POST', command)
    assert code == 202 and headers['Idempotency-Replayed'] == 'true' and replay['resource_ref'] == passed_ack['resource_ref']
    historical = get('/validations/' + validation_id)
    assert historical['state'] == 'passed' and not historical['usable']
    notes.append('real external VLAN change exposes Original/Current/Yours; explicit snapshot-bound rebase preserves history and invalidates old use')

    bodies = [stage_command(70), stage_command(71)]
    def concurrent_save(body):
        return call('/candidate', 'PATCH', body, extra_headers={
            'If-Match': '"' + draft['revision'] + '"', 'X-OVS-Request-Epoch': epochs['workspace']})
    with ThreadPoolExecutor(max_workers=2) as pool:
        responses = list(pool.map(concurrent_save, bodies))
    assert sum(code == 200 for code, _, _ in responses) == 1, [(c, b) for c, b, _ in responses]
    assert all(c in (200, 409, 412) for c, _, _ in responses)
    draft = get('/candidate')
    lost = stage_command(72)
    patch(lost, draft['revision'], drop=True)
    saved = get('/candidate')
    lost_validation = {'request_id': request_id(), 'candidate_id': saved['id'], 'candidate_revision': saved['revision']}
    assert call('/validations', 'POST', lost_validation, drop_response=True)[0] == 202
    request_path = '/requests/' + lost_validation['request_id'] + '?domain=management&epoch=' + epochs['management']
    original_receipt = get(request_path)
    run('systemctl', 'restart', units['webd'], units['mgrd'])
    eventually(ready)
    assert get('/candidate')['revision'] == saved['revision']
    assert get(request_path)['resource_ref'] == original_receipt['resource_ref']
    stop_db()
    eventually(lambda: get('/candidate')['state'] == 'stale')
    patch(lost, draft['revision'])
    code, recovered, headers = call('/validations', 'POST', lost_validation)
    assert code == 202 and headers['Idempotency-Replayed'] == 'true'
    assert recovered['resource_ref'] == original_receipt['resource_ref']
    receipt = get('/requests/' + lost['request_id'] + '?domain=workspace&epoch=' + epochs['workspace'])
    assert receipt['resource_ref']['id'] == saved['id']
    assert not get('/validations/' + recovered['resource_ref']['id'])['usable']
    start_db()
    eventually(ready)
    assert int(vsctl('get', 'Port', 'inv-p1', 'tag')) == 39
    notes.append('concurrent saves use strong CAS; dropped response bodies recover original receipts across both daemon restarts and provider outage without OVS writes')

    role = next(r for r in get('/roles')['items'] if r['name'] == 'NetworkAdmin')
    user_password = 'synthetic-candidate-' + secrets.token_hex(16)
    credentials.append(user_password)
    code, created, _ = call('/users', 'POST', {'request_id': request_id(), 'username': 'candidate-peer',
                                            'password': user_password, 'role_ids': [role['id']]})
    assert code == 202, (code, created)
    code, other, headers = call('/sessions', 'POST', {'provider': 'local', 'username': 'candidate-peer',
                                                   'password': user_password}, anonymous=True)
    assert code == 201
    other_cookie = headers['Set-Cookie'].split(';')[0]
    credentials.append(other_cookie)
    code, private, _ = call('/candidate', extra_headers={'Cookie': other_cookie})
    assert code == 200 and private['state'] == 'empty' and private['id'] != saved['id']
    assert call('/validations/' + validation_id, extra_headers={'Cookie': other_cookie})[0] == 404
    assert call('/requests/' + lost['request_id'] + '?domain=workspace&epoch=' + epochs['workspace'],
                extra_headers={'Cookie': other_cookie})[0] == 404
    user = get('/users/' + other['principal_id'])
    code, _, _ = call('/users/' + user['id'], 'PATCH', {'request_id': request_id(), 'disabled': False, 'role_ids': []},
                      extra_headers={'If-Match': '"' + user['revision'] + '"'})
    assert code == 202
    assert call('/candidate', extra_headers={'Cookie': other_cookie})[0] == 403
    notes.append('two real authenticated principals have isolated drafts, validations and receipts; current role revocation stops reads')

    with sqlite3.connect(manager_db) as db:
        counts = dict(zip(('validations', 'witnesses'), [db.execute('SELECT count(*) FROM ' + t).fetchone()[0]
                                                       for t in ('candidate_validations', 'candidate_witnesses')]))
        assert counts['validations'] >= 3 and counts['witnesses'] == 2
        assert db.execute('SELECT count(*) FROM candidate_validations v JOIN jobs j ON j.id=v.job_id').fetchone()[0] == counts['validations']
    with sqlite3.connect(web_db) as db:
        assert db.execute('SELECT count(*) FROM candidate_workspaces').fetchone()[0] == 2
        serialized = ''.join(bytes(row[0]).decode() for row in db.execute('SELECT envelope FROM candidate_workspaces'))
        assert all(secret not in serialized for secret in credentials)
    return {'verified': True, 'validation_count': counts['validations'], 'principal_count': 2,
            'native_live_tag_after_candidate_operations': 39, 'checks': notes}
