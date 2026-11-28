import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { API } from '../frontend/src/api.ts';
import { Controller } from '../frontend/src/model.ts';
import {
  canExportVlans, canStageVlans, hasExactVlanFields, readVlanTransferFile, verifyVlanExport, vlanExportURL, vlanImportPath,
  vlanTransferMappings, vlanTransferMaxBytes, vlanTransferRoute, vlanTransferTarget,
} from '../frontend/src/vlan-transfer.ts';

function id(number) {
  return `11111111-1111-4111-8111-${String(number).padStart(12, '0')}`;
}
const generation = id(1), next = id(2), source = id(3), other = id(4), dest = id(5);
const principal = id(6), epoch = id(7), candidateID = id(8), revision = id(9);
const session = { principal_id: principal, request_epochs: { workspace: epoch, management: epoch },
  effective_capabilities: ['configuration.import', 'workspace.write', 'ovs.port.vlan.write', 'workspace.read', 'inventory.read'] };
const document = {
  format: 'ovs-webui.port-vlan.v1', instance_generation: generation, schema_digest: 'c'.repeat(64), ports: [
    { binding: { management_id: source, ovs_uuid: id(11), table: 'Port', instance_generation: generation },
      member: { management_id: id(12), ovs_uuid: id(13), table: 'Interface', instance_generation: generation },
      shape: 'standalone-system', vlan: { tag: [], trunks: [10, 24] } },
    { binding: { management_id: other, ovs_uuid: id(14), table: 'Port', instance_generation: generation },
      member: { management_id: id(15), ovs_uuid: id(16), table: 'Interface', instance_generation: generation },
      shape: 'standalone-internal', vlan: { vlan_mode: 'access' } },
  ],
};
const encode = (body) => new TextEncoder().encode(JSON.stringify(body) + '\n');
const data = encode(document);
const loaded = readVlanTransferFile(data);
const response = (body, status = 200) => new Response(JSON.stringify(body), {
  status, headers: { 'Content-Type': 'application/json' },
});

await test('VLAN export validates exact generation, Port scope and bytes, and local file preserves omissions', async () => {
  assert.equal(canStageVlans(session), true);
  assert.equal(canExportVlans(session), false);
  assert.equal(canExportVlans({ effective_capabilities: ['configuration.read', 'inventory.read', 'state.read'] }), true);
  assert.equal(vlanTransferTarget({ kind: 'bond', instance_generation: generation, interface_refs: [] }, generation), null);
  assert.equal(vlanExportURL([source, source]), null);
  assert.equal(vlanExportURL(Array.from({ length: 9 }, (_, i) => id(i + 20))), null);
  assert.equal(new URL(vlanExportURL([source, other]), 'https://fixture.invalid').searchParams.get('ports'), `${source},${other}`);
  const digest = createHash('sha256').update(data).digest('hex');
  assert.deepEqual(await verifyVlanExport(loaded.encoded, digest, [other, source], generation), data);
  await assert.rejects(verifyVlanExport(loaded.encoded, digest, [source], generation), /SCOPE_CHANGED/);
  await assert.rejects(verifyVlanExport(loaded.encoded, digest, [source, other], next), /SCOPE_CHANGED/);
  await assert.rejects(verifyVlanExport(loaded.encoded, '0'.repeat(64), [source, other], generation), /CHECKSUM_MISMATCH/);
  assert.equal('vlan_mode' in loaded.document.ports[0].vlan, false);
  assert.deepEqual(loaded.document.ports[0].vlan.tag, []);
  assert.equal('tag' in loaded.document.ports[1].vlan, false);
  assert.throws(() => readVlanTransferFile(new Uint8Array(vlanTransferMaxBytes + 1)), /DOCUMENT_LIMIT/);
  for (const vlan of [{ tag: null }, { trunks: [1, 1] }, { vlan_mode: 'dot1q-tunnel' }, { native_options: {} }]) {
    const invalid = { ...document, ports: [{ ...document.ports[0], vlan }] };
    assert.throws(() => readVlanTransferFile(encode(invalid)), /DOCUMENT_INVALID/);
  }
});

await test('VLAN mappings preserve original IDs in generation and require complete unique reviewed destinations across generations', () => {
  assert.deepEqual(vlanTransferMappings(document, generation, false, {}), []);
  assert.equal(vlanTransferMappings(document, next, false, {}), null);
  const target = { id: dest, name: 'fixture-target', member: id(19), generation: next };
  assert.equal(vlanTransferMappings(document, next, true, { [source]: target }), null);
  assert.equal(vlanTransferMappings(document, next, true, { [source]: target, [other]: target }), null);
  assert.equal(vlanTransferMappings(document, generation, true, { [source]: target, [other]: { ...target, id: other } }), null);
  assert.deepEqual(vlanTransferMappings(document, next, true, { [source]: target, [other]: { ...target, id: other } }), [
    { source_id: source, destination_id: dest }, { source_id: other, destination_id: other },
  ]);
});

await test('VLAN Exact Restore requires complete source fields and explicitly reviewed current Port and member IDs', () => {
  const exact = structuredClone(document);
  exact.ports[0].vlan = { vlan_mode: 'trunk', tag: [], trunks: [10, 24] };
  exact.ports[1].vlan = { vlan_mode: 'access', tag: [19], trunks: [] };
  const target = { id: dest, name: 'fixture-target', member: id(19), generation };
  const another = { ...target, id: other, member: id(20) };
  const mappings = { [source]: target, [other]: another };
  assert.equal(hasExactVlanFields(document), false);
  assert.equal(hasExactVlanFields(exact), true);
  assert.equal(vlanTransferMappings(exact, generation, false, {}, 'exact-restore'), null);
  assert.equal(vlanTransferMappings(document, generation, true, mappings, 'exact-restore'), null);
  assert.equal(vlanTransferMappings(exact, generation, true, { ...mappings, [other]: { ...another, member: id(19) } }, 'exact-restore'), null);
  assert.deepEqual(vlanTransferMappings(exact, generation, false, mappings, 'exact-restore'), [
    { source_id: source, destination_id: dest, destination_member_id: id(19) },
    { source_id: other, destination_id: other, destination_member_id: id(20) },
  ]);
});

await test('VLAN staging requires the reviewed Candidate and fresh generation and preserves workspace recovery', async () => {
  const command = { document_base64: loaded.encoded, expected_generation: generation, mappings: [] };
  const candidate = { id: candidateID, revision, intents: [] };
  let freshness = 'fresh';
  let requests = 0;
  let submitted;
  const values = new Map();
  const store = { getItem: (k) => values.get(k) ?? null, setItem: (k, value) => values.set(k, value), removeItem: (k) => values.delete(k) };
  const api = new API(async (path, request) => {
    if (path === '/api/v1/session') return response(session);
    if (path === '/api/v1/workspace') return response({ candidate });
    if (path === '/api/v1/ports?limit=25') return response({ source: { freshness }, availability: 'complete', instance_generation: generation, items: [] });
    if (path === '/api/v1' + vlanImportPath) {
      assert.equal(request.headers['If-Match'], `"${revision}"`);
      const body = JSON.parse(request.body);
      assert.equal(request.headers['Idempotency-Key'], body.request_id);
      assert.equal(request.headers['X-OVS-Request-Epoch'], epoch);
      assert.deepEqual(body, { ...command, request_id: body.request_id });
      submitted = body.request_id;
      requests++;
      throw new TypeError('fixture disconnected after staging');
    }
    assert.match(path, /\/requests\//);
    return response({ request_id: submitted, request_epoch: epoch, request_domain: 'workspace', state: 'accepted',
      resource_ref: { kind: 'candidate', id: candidateID }, job_ref: null });
  }, store);
  const model = new Controller(api, vlanTransferRoute);
  await model.refresh();
  await model.stageVlanTransfer(command, id(999));
  await model.stageVlanTransfer({ ...command, expected_generation: next }, revision);
  freshness = 'stale';
  await model.refresh();
  await model.stageVlanTransfer(command, revision);
  assert.equal(requests, 0);

  freshness = 'fresh';
  await model.refresh();
  await model.stageVlanTransfer(command, revision);
  assert.equal(requests, 1);
  const pending = api.pending();
  assert.equal(pending.request_id, submitted);
  assert.equal(pending.path, vlanImportPath);
  assert.doesNotMatch(JSON.stringify(pending), /document_base64|vlan_mode|mappings/);
  assert.equal((await api.recover()).resource_ref.id, candidateID);
  assert.equal(api.pending(), null);
  assert.equal(requests, 1);
});
