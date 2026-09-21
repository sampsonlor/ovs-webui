import { test } from 'node:test';
import assert from 'node:assert/strict';
import { API, requestID } from '../frontend/src/api.ts';
import { Controller } from '../frontend/src/model.ts';
import {
  applyReady,
  decisionReady,
  editReason,
  remaining,
  vlanNumbers,
} from '../frontend/src/policy.ts';

const principal = '11111111-1111-4111-8111-111111111111';
const epoch = '22222222-2222-4222-8222-222222222222';
const resource = '33333333-3333-4333-8333-333333333333';
const session = {
  principal_id: principal,
  csrf_token: 'synthetic-csrf-only-in-memory',
  request_epochs: { workspace: epoch, management: epoch },
  effective_capabilities: [
    'workspace.read',
    'inventory.read',
    'configuration.read',
    'configuration.apply',
    'configuration.confirm',
    'configuration.rollback',
    'workspace.write',
    'ovs.port.vlan.write',
  ],
};
const storage = () => {
  const data = new Map();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => data.set(k, v),
    removeItem: (k) => data.delete(k),
  };
};
const response = (value, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

await test('formal client persists original identity before dispatch and recovers dropped replies with GET only', async () => {
  const disk = storage();
  const calls = [];
  let pending;
  const transport = async (url, options) => {
    calls.push({ url, options });
    if (options.method === 'POST') {
      pending = JSON.parse([...disk.data.values()][0]);
      assert.equal(JSON.parse(options.body).request_id, pending.request_id);
      assert.match(pending.request_id, /^[0-9a-f-]{14}7/);
      assert.equal(options.headers['Idempotency-Key'], pending.request_id);
      assert.equal(options.headers['X-OVS-CSRF-Token'], session.csrf_token);
      throw new TypeError('reply lost after commit');
    }
    return response({
      ...pending,
      state: 'accepted',
      command_effect: 'linked-resource',
      resource_ref: { kind: 'transaction', id: resource },
    });
  };
  const first = new API(transport, disk);
  first.session = session;
  await assert.rejects(
    first.command('/transactions', 'POST', 'management', {
      reason: 'private synthetic configuration reason',
    }),
  );
  assert.ok(
    !JSON.stringify([...disk.data.values()]).includes(session.csrf_token),
  );
  assert.ok(
    !JSON.stringify([...disk.data.values()]).includes('private synthetic'),
  );
  const reloaded = new API(transport, disk);
  reloaded.session = session;
  await assert.rejects(
    reloaded.command('/transactions', 'POST', 'management', {}),
    /REQUEST_RECOVERY_REQUIRED/,
  );
  const receipt = await reloaded.recover();
  assert.equal(receipt.resource_ref.id, resource);
  assert.equal(reloaded.pending(), null);
  assert.equal(calls.filter((c) => c.options.method === 'POST').length, 1);
  assert.match(calls[1].url, /domain=management&epoch=/);
});

await test('recovery never treats a missing, mismatched, unknown or unauthorized receipt as no execution', async () => {
  for (const result of ['404', 'mismatch', 'future', '403']) {
    const disk = storage();
    const pending = {
      principal_id: principal,
      request_id: requestID(),
      request_domain: 'workspace',
      request_epoch: epoch,
      path: '/candidate',
    };
    disk.setItem(`ovs.pending.v1.${principal}`, JSON.stringify(pending));
    const api = new API(
      async () =>
        result === '404' || result === '403'
          ? response({ code: 'NOT_FOUND' }, Number(result))
          : response({
              ...pending,
              request_epoch: result === 'mismatch' ? resource : epoch,
              state: 'future-state',
              resource_ref: { kind: 'candidate', id: resource },
            }),
      disk,
    );
    api.session = session;
    if (result === 'future') await api.recover();
    else await assert.rejects(api.recover());
    assert.ok(api.pending());
    api.session = { ...session, principal_id: resource };
    assert.equal(api.pending(), null);
  }
});

await test('storage failure prevents dispatch and completed workspace receipts survive refresh', async () => {
  let calls = 0;
  const disk = storage();
  disk.setItem = () => {
    throw new Error('storage denied');
  };
  const api = new API(async () => {
    calls++;
  }, disk);
  api.session = session;
  await assert.rejects(
    api.command('/candidate', 'PATCH', 'workspace', {}),
    /storage denied/,
  );
  assert.equal(calls, 0);
  const data = storage();
  const pending = {
    principal_id: principal,
    request_id: requestID(),
    request_domain: 'workspace',
    request_epoch: epoch,
    path: '/candidate',
  };
  data.setItem(`ovs.pending.v1.${principal}`, JSON.stringify(pending));
  const recovered = new API(
    async () =>
      response({
        ...pending,
        state: 'completed',
        resource_ref: { kind: 'candidate', id: resource },
      }),
    data,
  );
  recovered.session = session;
  await recovered.recover();
  assert.equal(recovered.pending(), null);
});

await test('responses arriving after sign-out cannot restore protected inventory or authority', async () => {
  let release, requested;
  const waiting = new Promise((r) => {
    requested = r;
  });
  const delayed = new Promise((r) => {
    release = r;
  });
  const api = new API(async (url, options) => {
    if (options.method === 'DELETE') return new Response(null, { status: 204 });
    if (url.endsWith('/session')) return response(session);
    if (url.endsWith('/workspace')) return response({ candidate: null });
    if (url.endsWith('/ports')) {
      requested();
      return delayed;
    }
    throw new Error(url);
  }, storage());
  const c = new Controller(api);
  const read = c.refresh();
  await waiting;
  await c.logout();
  release(response({ items: [{ name: 'revoked synthetic data' }] }));
  await read;
  assert.equal(c.state.session, null);
  assert.equal(c.state.ports.value, null);
  assert.equal(c.state.sessionReady, false);
});

await test('semantic gates preserve advanced VLANs and keep mode depth separate from authority and device responsibilities', () => {
  const native = { vlan_mode: 'access', tag: 10, trunks: [], cvlans: [] };
  const p = {
    allowed_operations: ['port.vlan.set'],
    fields: Object.fromEntries(
      ['vlan_mode', 'tag', 'trunks', 'cvlans'].map((k) => [
        k,
        { editable: true },
      ]),
    ),
    vlan: { native, availability: 'known', source: { freshness: 'fresh' } },
  };
  assert.equal(editReason(p, session, true), '');
  assert.ok(editReason(p, session, false));
  assert.ok(
    editReason(
      {
        ...p,
        vlan: { ...p.vlan, native: { ...native, vlan_mode: 'future-mode' } },
      },
      session,
      true,
    ),
  );
  assert.ok(
    editReason(
      { ...p, vlan: { ...p.vlan, native: { ...native, cvlans: [10] } } },
      session,
      true,
    ),
  );
  assert.ok(editReason({ ...p, allowed_operations: [] }, session, true));
  assert.ok(editReason(p, { ...session, effective_capabilities: [] }, true));
  const candidate = {
    id: resource,
    revision: epoch,
    state: 'dirty',
    safe_apply_available: true,
  };
  const validation = {
    candidate_id: resource,
    candidate_revision: epoch,
    state: 'passed',
    usable: true,
    execution_ready: true,
  };
  assert.ok(applyReady(candidate, validation, session, true));
  for (const changes of [
    { state: 'future' },
    { consumed_by: {} },
    { revision: principal },
    { diff_truncated: true },
  ])
    assert.ok(
      !applyReady({ ...candidate, ...changes }, validation, session, true),
    );
  const t = {
    safe_apply: 'awaiting-confirmation',
    allowed_actions: ['confirm', 'rollback'],
    knowledge: 'known',
    applied_outcome: 'applied',
    health: 'healthy',
    server_time: '2026-01-01T00:00:00Z',
    confirmation_deadline: '2026-01-01T00:02:00Z',
  };
  assert.ok(decisionReady(t, session, 'confirm'));
  assert.ok(!decisionReady({ ...t, safe_apply: 'future' }, session, 'confirm'));
  assert.ok(
    !decisionReady({ ...t, knowledge: 'outcome-unknown' }, session, 'confirm'),
  );
  assert.equal(remaining(t, 100, 121100), 0);
  assert.equal(t.safe_apply, 'awaiting-confirmation');
  assert.deepEqual(vlanNumbers('30, 10'), [10, 30]);
  for (const value of ['0', '4095', '1,1', '2e3', '10-20', '1,'])
    assert.throws(() => vlanNumbers(value));
});
