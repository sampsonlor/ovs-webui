import assert from 'node:assert/strict';
import { test } from 'node:test';
import { API } from '../frontend/src/api.ts';
import { Controller } from '../frontend/src/model.ts';
import {
  canReadOpenFlowRawFields,
  matchesOpenFlowExport, matchesOpenFlowObservation, openFlowLocation, openFlowObservationPath,
  openFlowRoute, openFlowSnapshotAvailable, openFlowSnapshotPath, openFlowSnapshotRoute, validOpenFlowPage,
} from '../frontend/src/openflow.ts';

const bridge = '11111111-1111-4111-8111-111111111111';
const id = '22222222-2222-4222-8222-222222222222';
const generation = '33333333-3333-4333-8333-333333333333';
const principal = '44444444-4444-4444-8444-444444444444';
const cookie = '18446744073709551615';
const cookieEvidence = '0xffffffffffffffff';
const observed = '2090-01-01T00:00:00Z';
const expires = '2090-01-01T00:00:30Z';
const scope = { bridge, table: 254, limit: 26, cookie, groups: true };
const rows = Array.from({ length: 26 }, (_, i) => ({ table: 254, priority: i, cookie: cookieEvidence, duration_seconds: 1,
  duration_nanoseconds: 0, packets: '18446744073709551615', bytes: null, match_fields: [], instructions: [], metadata_truncated: false }));
const snapshot = { id, bridge_id: bridge, instance_generation: generation, observed_at: observed,
  expires_at: expires, retained_rows: rows.length, truncated: false };
const evidence = { bridge_id: bridge, instance_generation: generation, observed_at: observed, source: 'openflow-1.3-unix',
  ownership: 'unknown', field_coverage: 'metadata-only', query: { table: 254, limit: 26, cookie: cookieEvidence, include_groups_meters: true },
  groups: 'observed', meters: 'observed', truncated: false, group_rows: [], meter_rows: [] };
const first = { snapshot, evidence: { ...evidence, rows: rows.slice(0, 25) }, offset: 0, next_offset: 25 };
const last = { snapshot, evidence: { ...evidence, rows: rows.slice(25) }, offset: 25 };
const exported = { snapshot, evidence: { ...evidence, rows }, offset: 0 };

await test('OpenFlow bounds preserve the exact decimal cookie and reject ineligible scopes', () => {
  const path = openFlowObservationPath(scope);
  const url = new URL(path, 'https://fixture.invalid');
  assert.equal(url.pathname, `/openflow/bridges/${bridge}/snapshots`);
  assert.deepEqual(Object.fromEntries(url.searchParams), { table: '254', row_limit: '26', cookie, groups_meters: 'true' });
  assert.match(openFlowObservationPath({ ...scope, table: 0, cookie: '0' }), /(?:\?|&)cookie=0(?:&|$)/);
  for (const bad of [{ table: -1 }, { table: 255 }, { limit: 129 }, { limit: 0 }, { cookie: '0xff' }, { cookie: '01' }, { cookie: '18446744073709551616' }]) {
    assert.equal(openFlowObservationPath({ ...scope, ...bad }), null);
  }
  assert.equal(openFlowLocation('?snapshot=abc').kind, 'invalid');
  assert.equal(openFlowLocation(`?snapshot=${id}&offset=129`).kind, 'invalid');
  assert.equal(openFlowLocation(`?snapshot=${id}&offset=0&offset=25`).kind, 'invalid');
});

await test('paged and exported evidence stays within one original immutable snapshot', () => {
  assert.equal(matchesOpenFlowObservation(first, scope), true);
  assert.equal(matchesOpenFlowObservation(first, { ...scope, table: 0 }), false);
  assert.equal(matchesOpenFlowObservation(first, { ...scope, cookie: '' }), false);
  assert.equal(matchesOpenFlowObservation(first, { ...scope, cookie: '18446744073709551614' }), false);
  const zeroCookie = { ...first, evidence: { ...first.evidence, query: { ...evidence.query, cookie: '0x0000000000000000' } } };
  assert.equal(matchesOpenFlowObservation(zeroCookie, { ...scope, cookie: '0' }), true);
  assert.equal(matchesOpenFlowObservation(zeroCookie, { ...scope, cookie: '' }), false);
  assert.equal(validOpenFlowPage({ ...first, evidence: { ...first.evidence, query: { ...evidence.query, cookie } } }, id, 0), false);
  assert.equal(validOpenFlowPage(last, id, 25), true);
  assert.equal(validOpenFlowPage(last, id, 0), false);
  assert.equal(validOpenFlowPage({ ...last, evidence: { ...last.evidence, instance_generation: principal } }, id, 25), false);
  assert.equal(validOpenFlowPage({ ...first, next_offset: 26 }, id, 0), false);
  assert.equal(matchesOpenFlowExport(exported, last), true);
  assert.equal(matchesOpenFlowExport(last, last), false);
  assert.equal(matchesOpenFlowExport({ ...exported, snapshot: { ...snapshot, expires_at: '2090-01-01T00:00:31Z' } }, last), false);
  const wrongRows = exported.evidence.rows.map((row, index) => index === 25 ? { ...row, packets: '0' } : row);
  assert.equal(matchesOpenFlowExport({ ...exported, evidence: { ...exported.evidence, rows: wrongRows } }, last), false);
  assert.equal(openFlowSnapshotAvailable(first, Date.parse(expires)), false);
  assert.equal(openFlowSnapshotAvailable(first, Date.parse(expires) - 1), true);
});

await test('raw Flow fields require configuration authority and an explicit query option', () => {
  const observer = ['state.read', 'inventory.read'];
  assert.equal(canReadOpenFlowRawFields({ effective_capabilities: observer }), false);
  assert.equal(canReadOpenFlowRawFields({ effective_capabilities: ['configuration.read', 'state.read'] }), false);
  assert.equal(canReadOpenFlowRawFields({ effective_capabilities: [...observer, 'configuration.read'] }), true);
  assert.equal(canReadOpenFlowRawFields(null), false);
  for (const requested of [undefined, false, true]) {
    const url = new URL(openFlowObservationPath({ ...scope, rawFields: requested }), 'https://fixture.invalid');
    assert.equal(url.searchParams.get('raw_fields'), requested === true ? 'true' : null);
    assert.equal(url.searchParams.get('table'), '254');
    assert.equal(url.searchParams.get('cookie'), cookie);
  }
});

await test('raw snapshot paging and export keep unknown fields and bounded bytes intact', () => {
  const match = { class: 0xffff, field: 126, masked: false, payload_bytes: 6, raw_payload_hex: '00a1b2c300ff' };
  const instruction = { type: 0xffff, bytes: 16, raw_payload_hex: '0000f00d0001020304050607' };
  const rawRow = { ...rows[25], match_fields: [match], instructions: [instruction] };
  const rawEvidence = { ...evidence, field_coverage: 'bounded-raw-fields', query: { ...evidence.query, raw_fields: true } };
  const rawFirst = { ...first, evidence: { ...rawEvidence, rows: rows.slice(0, 25) } };
  const rawLast = { ...last, evidence: { ...rawEvidence, rows: [rawRow] } };
  const rawExport = { ...exported, evidence: { ...rawEvidence, rows: [...rows.slice(0, 25), rawRow] } };
  assert.equal(matchesOpenFlowObservation(rawFirst, { ...scope, rawFields: true }), true);
  assert.equal(matchesOpenFlowObservation(rawFirst, scope), false);
  assert.equal(matchesOpenFlowObservation(first, { ...scope, rawFields: true }), false);
  assert.equal(validOpenFlowPage(rawLast, id, 25), true);
  assert.equal(matchesOpenFlowExport(rawExport, rawLast), true);
  assert.equal(matchesOpenFlowExport(exported, rawLast), false);
  const reencoded = { ...rawRow, match_fields: [{ ...match, raw_payload_hex: '00a1b2c300fe' }] };
  assert.equal(matchesOpenFlowExport({ ...rawExport, evidence: { ...rawEvidence, rows: [...rows.slice(0, 25), reencoded] } }, rawLast), false);

  for (const altered of [
    { ...rawEvidence, field_coverage: 'metadata-only' },
    { ...rawEvidence, query: { ...rawEvidence.query, raw_fields: 'true' } },
    { ...rawEvidence, query: { ...rawEvidence.query, raw_fields: false } },
  ]) {
    assert.equal(validOpenFlowPage({ ...rawLast, evidence: { ...altered, rows: [rawRow] } }, id, 25), false);
  }
  assert.equal(validOpenFlowPage({ ...last, evidence: { ...last.evidence, rows: [rawRow] } }, id, 25), false);
  for (const invalid of [
    { ...rawRow, match_fields: [{ ...match, raw_payload_hex: '00A1b2c300ff' }] },
    { ...rawRow, match_fields: [{ ...match, raw_payload_hex: '00a1b2c300f' }] },
    { ...rawRow, match_fields: [{ ...match, payload_bytes: 256, raw_payload_hex: 'ab'.repeat(256) }] },
    { ...rawRow, instructions: [{ ...instruction, raw_payload_hex: '0000f00d' }] },
    { ...rawRow, instructions: [{ ...instruction, bytes: 264, raw_payload_hex: 'ab'.repeat(260) }] },
  ]) {
    assert.equal(validOpenFlowPage({ ...rawLast, evidence: { ...rawEvidence, rows: [invalid] } }, id, 25), false);
  }
  const omitted = { ...rawRow, instructions: [{ type: 0xffff, bytes: 264 }], metadata_truncated: true };
  assert.equal(validOpenFlowPage({ ...rawLast, evidence: { ...rawEvidence, rows: [omitted] } }, id, 25), true);
});

await test('automatic refresh lists Bridges or reads a retained page without activating or exporting', async () => {
  const authorized = { principal_id: principal, effective_capabilities: ['state.read', 'inventory.read'], request_epochs: {} };
  const storage = { getItem: () => null };
  const json = (value) => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
  const calls = [];
  function client(session) {
    return new API(async (path, request) => {
      assert.equal(request.method, 'GET');
      calls.push(path);
      if (path === '/api/v1/session') return json(session);
      if (path === '/api/v1/bridges?limit=25') return json({ items: [] });
      assert.equal(path, '/api/v1' + openFlowSnapshotPath(id, 25));
      return json(last);
    }, storage);
  }
  const inventory = new Controller(client(authorized), openFlowRoute);
  await inventory.refresh();
  await inventory.refresh();
  assert.deepEqual(calls, ['/api/v1/session', '/api/v1/bridges?limit=25', '/api/v1/session', '/api/v1/bridges?limit=25']);
  calls.length = 0;
  const retained = new Controller(client(authorized), openFlowSnapshotRoute(id, 25));
  await retained.refresh();
  assert.deepEqual(retained.state.openFlowSnapshot.value, last);
  assert.deepEqual(calls, ['/api/v1/session', `/api/v1/openflow/snapshots/${id}?offset=25&page_size=25`]);
  for (const effective_capabilities of [['state.read'], ['inventory.read']]) {
    calls.length = 0;
    const denied = new Controller(client({ ...authorized, effective_capabilities }), openFlowSnapshotRoute(id, 25));
    await denied.refresh();
    assert.equal(denied.state.openFlowSnapshot.status, 'denied');
    assert.deepEqual(calls, ['/api/v1/session']);
  }
});
