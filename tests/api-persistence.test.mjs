import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { randomUUID } from 'node:crypto';
import { CoreLabStore } from '../dev/core-lab-store.mjs';
import { coreLabMiddleware } from '../dev/core-lab-plugin.mjs';
import { startValidationWorker } from '../dev/core-lab-validation.mjs';
import { CoreHttpClient } from '../lib/api/http-client.ts';
import {
  WorkspaceController,
  readPinnedInventory,
} from '../lib/api/workspace-controller.ts';
import { validateContract } from '../lib/api/validator.generated.mjs';
import { setTimeout as delay } from 'node:timers/promises';

const freshCommand = () => ({
  requestId: randomUUID(),
  operation: 'set-vlan',
  portId: 'port-2',
  mine: { mode: 'trunk', tag: null, trunks: [120, 240] },
});
function fixture(t) {
  const root = resolve(tmpdir());
  const dir = mkdtempSync(join(root, 'ovs-candidate-test-'));
  const file = join(dir, 'state.sqlite');
  const stores = [];
  const open = (options) => {
    const s = new CoreLabStore(file, options);
    stores.push(s);
    return s;
  };
  t.after(() => {
    for (const s of stores) {
      try {
        s.close();
      } catch {
        /* Already closed for restart. */
      }
    }
    if (dirname(resolve(dir)) !== root)
      throw new Error('Cleanup target escaped its temporary root.');
    rmSync(dir, { recursive: true, force: true });
  });
  return { open, file };
}
function stage(store, principal, requestId = randomUUID()) {
  const snapshot = store.snapshot(principal);
  const command = {
    ...freshCommand(),
    requestId,
    expectedGeneration: snapshot.candidate.currentGeneration,
  };
  const session = {
    principal,
    nodeId: snapshot.candidate.nodeId,
    editable: principal !== 'observer',
  };
  return {
    result: store.mutate(session, command, snapshot.etag, requestId),
    command,
    snapshot,
    session,
  };
}

function validationCommand(store, principal = 'alice') {
  const candidate = store.candidate(principal);
  return {
    requestId: randomUUID(),
    candidateId: candidate.id,
    expectedCandidateRevision: candidate.revision,
    expectedGeneration: candidate.currentGeneration,
  };
}
function validateStaged(
  store,
  session,
  command = validationCommand(store, session.principal),
) {
  const accepted = store.validations.start(session, command, command.requestId);
  assert.equal(accepted.status, 202);
  store.validations.tick();
  store.validations.tick();
  return {
    accepted,
    command,
    result: store.validations.read(session.principal, accepted.body.id).body,
  };
}

void test('validation admission, captured diff, job and request survive restart; server worker needs no browser', (t) => {
  const f = fixture(t);
  const first = f.open();
  const { session } = stage(first, 'alice');
  first.validations.policyChange('available');
  const command = validationCommand(first);
  const accepted = first.validations.start(session, command, command.requestId);
  assert.equal(accepted.status, 202);
  assert.equal(accepted.body.status, 'pending');
  assert.equal(validateContract('ValidationResource', accepted.body), true);
  first.close();
  const second = f.open();
  second.validations.tick();
  assert.equal(
    second.validations.read('alice', accepted.body.id).body.status,
    'running',
  );
  second.close();
  const third = f.open();
  third.validations.tick();
  const completed = third.validations.read('alice', accepted.body.id).body;
  assert.equal(completed.status, 'passed');
  assert.deepEqual(completed.diff, accepted.body.diff);
  assert.equal(
    third.validations.job('alice', completed.jobId).body.state,
    'succeeded',
  );
  assert.equal(
    third.readRequest('alice', command.requestId).body.validationId,
    completed.id,
  );
  assert.deepEqual(third.workspace(session).latestValidation, completed);
  assert.equal(third.workspace(session).permissions.startSafeApply, true);
  assert.equal(
    third.meta('inventory').items[1].configuration.value.mode,
    'access',
  );
});

void test('validation replay preserves original acceptance after completion and expiry; keys cannot cross operations', (t) => {
  const store = fixture(t).open();
  const staged = stage(store, 'alice');
  const v = validateStaged(store, staged.session);
  store.validations.expireLatest('alice');
  assert.deepEqual(
    store.validations.start(staged.session, v.command, v.command.requestId),
    v.accepted,
  );
  assert.equal(
    store.validations.start(
      staged.session,
      { ...v.command, expectedGeneration: 'different' },
      v.command.requestId,
    ).body.code,
    'IDEMPOTENCY_MISMATCH',
  );
  const reused = {
    ...validationCommand(store),
    requestId: staged.command.requestId,
  };
  assert.equal(
    store.validations.start(staged.session, reused, reused.requestId).body.code,
    'IDEMPOTENCY_MISMATCH',
  );
  const candidate = store.snapshot('alice');
  assert.equal(
    store.mutate(
      staged.session,
      { requestId: v.command.requestId, operation: 'discard' },
      candidate.etag,
      v.command.requestId,
    ).body.code,
    'IDEMPOTENCY_MISMATCH',
  );
  assert.equal(
    store.db.prepare('SELECT COUNT(*) AS n FROM validations').get().n,
    1,
  );
});

void test('server clock expiry is persisted, scoped, and never resurrected by a later read', (t) => {
  let time = Date.parse('2026-09-05T01:00:00.000Z');
  const store = fixture(t).open({ clock: () => time });
  const { session } = stage(store, 'alice');
  store.validations.policyChange('available');
  const { result } = validateStaged(store, session);
  assert.equal(result.status, 'passed');
  time += 120_001;
  const expired = store.validations.read('alice', result.id).body;
  assert.equal(expired.status, 'expired');
  assert.equal(expired.checks.at(-1).code, 'VALIDATION_EXPIRED');
  time -= 120_001;
  assert.equal(
    store.validations.read('alice', result.id).body.status,
    'expired',
  );
  assert.equal(store.validations.read('bob', result.id).status, 404);
  assert.equal(store.validations.job('bob', result.jobId).status, 404);
  assert.equal(store.readRequest('bob', result.requestId).status, 404);
});

void test('Candidate edits and external generation changes invalidate validation without rewriting its diff', (t) => {
  const store = fixture(t).open();
  const { session } = stage(store, 'alice');
  store.validations.policyChange('available');
  const original = validateStaged(store, session).result;
  const snapshot = store.snapshot('alice');
  const update = {
    ...freshCommand(),
    mine: { mode: 'access', tag: 241, trunks: [] },
    expectedGeneration: snapshot.candidate.currentGeneration,
  };
  assert.equal(
    store.mutate(session, update, snapshot.etag, update.requestId).status,
    200,
  );
  assert.equal(
    store.validations.read('alice', original.id).body.status,
    'expired',
  );
  assert.deepEqual(
    store.validations.read('alice', original.id).body.diff,
    original.diff,
  );
  const next = validateStaged(store, session).result;
  store.externalChange(null, null);
  assert.equal(store.validations.read('alice', next.id).body.status, 'expired');
  const stale = validateStaged(store, session).result;
  assert.equal(stale.status, 'blocked');
  assert.equal(
    stale.checks.find((c) => c.code === 'CANDIDATE_CURRENT').state,
    'block',
  );
  store.externalChange('port-2', { mode: 'access', tag: 333, trunks: [] });
  assert.equal(store.candidate('alice').freshness, 'conflict');
  assert.equal(validateStaged(store, session).result.status, 'blocked');
  const current = store.candidate('alice');
  const rebase = {
    requestId: randomUUID(),
    operation: 'rebase',
    currentGeneration: current.currentGeneration,
    conflictSnapshotId: current.conflictSnapshotId,
    resolutions: [{ intentId: current.intents[0].id, choice: 'mine' }],
  };
  assert.equal(
    store.mutate(
      session,
      rebase,
      store.snapshot('alice').etag,
      rebase.requestId,
    ).status,
    200,
  );
  assert.equal(validateStaged(store, session).result.status, 'passed');
});

void test('admission races are rejected and queued work expires when revision, generation or policy changes', (t) => {
  const store = fixture(t).open();
  const { session } = stage(store, 'alice');
  const old = validationCommand(store);
  store.externalChange(null, null);
  assert.equal(
    store.validations.start(session, old, old.requestId).body.code,
    'CANDIDATE_STALE',
  );
  assert.equal(
    store.readRequest('alice', old.requestId).body.state,
    'rejected',
  );
  const oldRevision = {
    ...validationCommand(store),
    expectedCandidateRevision: 'other-revision',
  };
  assert.equal(
    store.validations.start(session, oldRevision, oldRevision.requestId).status,
    412,
  );
  const command = validationCommand(store);
  const pending = store.validations.start(
    session,
    command,
    command.requestId,
  ).body;
  store.validations.policyChange('available');
  store.validations.tick();
  assert.equal(
    store.validations.read('alice', pending.id).body.status,
    'expired',
  );
  assert.equal(
    store.validations.job('alice', pending.jobId).body.state,
    'cancelled',
  );
});

void test('provider, safety and node checks block explicitly; job success never means validation passed', (t) => {
  const store = fixture(t).open();
  const { session } = stage(store, 'alice');
  const defaults = validateStaged(store, session).result;
  assert.equal(defaults.status, 'blocked');
  assert.equal(defaults.safetyPlan.checkpoint, 'unavailable');
  assert.equal(
    store.validations.job('alice', defaults.jobId).body.state,
    'succeeded',
  );
  store.validations.policyChange('unknown');
  assert.equal(
    store.validations.read('alice', defaults.id).body.status,
    'expired',
  );
  assert.ok(
    validateStaged(store, session).result.checks.some(
      (c) => c.state === 'unknown',
    ),
  );
  store.validations.policyChange('available');
  store.setMeta('providerAvailable', false);
  const provider = validateStaged(store, session).result;
  assert.equal(provider.status, 'blocked');
  assert.equal(
    provider.checks.find((c) => c.code === 'PROVIDER_AVAILABLE').state,
    'block',
  );
  store.setMeta('providerAvailable', true);
  store.setMeta('nodeBlocked', true);
  const locked = validateStaged(store, session).result;
  assert.equal(
    locked.checks.find((c) => c.code === 'NODE_ADMISSION').state,
    'block',
  );
  store.setMeta('nodeBlocked', false);
  assert.equal(
    store.validations.read('alice', locked.id).body.status,
    'expired',
  );
  assert.equal(store.workspace(session).permissions.startSafeApply, false);
});

void test('read-only and revoked permissions cannot validate; restored permission cannot revive old validation', (t) => {
  const store = fixture(t).open();
  const { session } = stage(store, 'alice');
  store.validations.policyChange('available');
  const result = validateStaged(store, session).result;
  store.setMeta('revokedEditors', ['alice']);
  const command = validationCommand(store);
  assert.equal(
    store.validations.start(session, command, command.requestId).status,
    403,
  );
  assert.equal(store.workspace(session).permissions.validate, false);
  assert.equal(
    store.validations.read('alice', result.id).body.status,
    'expired',
  );
  store.setMeta('revokedEditors', []);
  assert.equal(
    store.validations.read('alice', result.id).body.status,
    'expired',
  );
  const observer = store.login('observer').session;
  const readonly = validationCommand(store, 'observer');
  assert.equal(
    store.validations.start(observer, readonly, readonly.requestId).status,
    403,
  );
});

void test('queued checks cannot pass after a Candidate edit and current native write authority is checked on execution', (t) => {
  const store = fixture(t).open();
  const { session } = stage(store, 'alice');
  store.validations.policyChange('available');
  const command = validationCommand(store);
  const queued = store.validations.start(
    session,
    command,
    command.requestId,
  ).body;
  const discard = { requestId: randomUUID(), operation: 'discard' };
  store.mutate(
    session,
    discard,
    store.snapshot('alice').etag,
    discard.requestId,
  );
  store.validations.tick();
  assert.equal(
    store.validations.read('alice', queued.id).body.status,
    'expired',
  );
  stage(store, 'alice');
  const inventory = store.meta('inventory');
  inventory.items[1].authority = 'external';
  inventory.items[1].scope = 'observe';
  inventory.items[1].writableFields = [];
  store.setMeta('inventory', inventory);
  const blocked = validateStaged(store, session).result;
  assert.equal(blocked.status, 'blocked');
  assert.equal(
    blocked.checks.find((check) => check.code === 'PORT_WRITABLE').state,
    'block',
  );
});

void test('Candidate, session and request evidence survive closing and reopening the database', (t) => {
  const f = fixture(t);
  const first = f.open();
  const login = first.login('alice');
  const staged = stage(first, 'alice');
  assert.equal(staged.result.status, 200);
  first.close();
  const restarted = f.open();
  assert.deepEqual(restarted.snapshot('alice').candidate, staged.result.body);
  assert.equal(restarted.session(login.token).principal, 'alice');
  assert.equal(
    restarted.readRequest('alice', staged.command.requestId).body.state,
    'accepted',
  );
  assert.equal(
    restarted.meta('inventory').items[1].configuration.value.mode,
    'access',
  );
});

void test('independent database connections enforce one strong ETag winner without overwriting it', (t) => {
  const f = fixture(t);
  const a = f.open();
  const b = f.open();
  const old = b.snapshot('alice');
  const first = stage(a, 'alice');
  const command = {
    ...freshCommand(),
    expectedGeneration: old.candidate.currentGeneration,
    mine: { mode: 'access', tag: 999, trunks: [] },
  };
  const stale = b.mutate(first.session, command, old.etag, command.requestId);
  assert.equal(stale.status, 412);
  assert.equal(stale.body.code, 'ETAG_MISMATCH');
  assert.deepEqual(
    b.snapshot('alice').candidate.intents,
    first.result.body.intents,
  );
  assert.equal(
    b.readRequest('alice', command.requestId).body.state,
    'rejected',
  );
});

void test('idempotent replay returns the original snapshot; changed payload or ETag cannot reuse its key', (t) => {
  const store = fixture(t).open();
  const first = stage(store, 'alice');
  const repeat = store.mutate(
    first.session,
    first.command,
    first.snapshot.etag,
    first.command.requestId,
  );
  assert.deepEqual(repeat, first.result);
  assert.deepEqual(
    store.mutate(
      first.session,
      { ...first.command, mine: { ...first.command.mine, trunks: [240, 120] } },
      first.snapshot.etag,
      first.command.requestId,
    ),
    first.result,
  );
  assert.equal(
    store.mutate(
      first.session,
      { ...first.command, mine: { mode: 'access', tag: 900, trunks: [] } },
      first.snapshot.etag,
      first.command.requestId,
    ).body.code,
    'IDEMPOTENCY_MISMATCH',
  );
  assert.equal(
    store.mutate(
      first.session,
      first.command,
      first.result.etag,
      first.command.requestId,
    ).body.code,
    'IDEMPOTENCY_MISMATCH',
  );
});

void test('per-user Candidates and request ledger are isolated even when users reuse a request ID', (t) => {
  const store = fixture(t).open();
  const requestId = randomUUID();
  const alice = stage(store, 'alice', requestId);
  assert.equal(store.snapshot('bob').candidate.intents.length, 0);
  assert.equal(store.readRequest('bob', requestId).status, 404);
  const bob = stage(store, 'bob', requestId);
  assert.equal(bob.result.status, 200);
  assert.notEqual(alice.result.body.id, bob.result.body.id);
  assert.equal(stage(store, 'observer').result.status, 403);
});

void test('stale and overlapping external observations require reviewed atomic rebase and preserve running data', (t) => {
  const store = fixture(t).open();
  const first = stage(store, 'alice');
  store.externalChange(null, null);
  let snapshot = store.snapshot('alice');
  assert.equal(snapshot.candidate.freshness, 'stale');
  assert.notEqual(snapshot.etag, first.result.etag);
  let command = {
    requestId: randomUUID(),
    operation: 'rebase',
    currentGeneration: snapshot.candidate.currentGeneration,
    conflictSnapshotId: null,
    resolutions: [],
  };
  assert.equal(
    store.mutate(first.session, command, snapshot.etag, command.requestId)
      .status,
    200,
  );
  store.externalChange('port-2', { mode: 'access', tag: 130, trunks: [] });
  snapshot = store.snapshot('alice');
  assert.equal(snapshot.candidate.freshness, 'conflict');
  const conflict = snapshot.candidate.conflicts[0];
  assert.equal(conflict.current.tag, 130);
  command = {
    requestId: randomUUID(),
    operation: 'rebase',
    currentGeneration: snapshot.candidate.currentGeneration,
    conflictSnapshotId: snapshot.candidate.conflictSnapshotId,
    resolutions: [{ intentId: conflict.intentId, choice: 'mine' }],
  };
  store.externalChange('port-2', { mode: 'access', tag: 140, trunks: [] });
  assert.equal(
    store.mutate(first.session, command, snapshot.etag, command.requestId)
      .status,
    412,
  );
  snapshot = store.snapshot('alice');
  command = {
    ...command,
    requestId: randomUUID(),
    currentGeneration: snapshot.candidate.currentGeneration,
    conflictSnapshotId: snapshot.candidate.conflictSnapshotId,
  };
  const rebased = store.mutate(
    first.session,
    command,
    snapshot.etag,
    command.requestId,
  );
  assert.equal(rebased.body.intents[0].base.tag, 140);
  assert.equal(rebased.body.intents[0].mine.mode, 'trunk');
  assert.equal(store.meta('inventory').items[1].configuration.value.tag, 140);
});

void test('Observe-only Ports, provider failure and node admission lock cannot create editable intent', (t) => {
  const store = fixture(t).open();
  const snapshot = store.snapshot('alice');
  const session = {
    principal: 'alice',
    nodeId: snapshot.candidate.nodeId,
    editable: true,
  };
  const command = {
    ...freshCommand(),
    portId: 'port-5',
    expectedGeneration: snapshot.candidate.currentGeneration,
  };
  assert.equal(
    store.mutate(session, command, snapshot.etag, command.requestId).body.code,
    'UNSUPPORTED_CONFIGURATION',
  );
  store.setMeta('providerAvailable', false);
  assert.equal(store.inventory().status, 503);
  assert.equal(stage(store, 'alice').result.status, 503);
  store.setMeta('providerAvailable', true);
  store.setMeta('nodeBlocked', true);
  assert.equal(stage(store, 'alice').result.body.code, 'TRANSACTION_ACTIVE');
  assert.equal(store.snapshot('alice').candidate.intents.length, 0);
});

async function httpFixture(t) {
  const store = fixture(t).open();
  const stopWorker = startValidationWorker(store);
  const handler = coreLabMiddleware(store);
  const server = createServer(handler);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const origin = `http://127.0.0.1:${server.address().port}`;
  t.after(() => {
    stopWorker();
    server.closeAllConnections();
    server.close();
  });
  async function login(principal, prior) {
    const response = await fetch(`${origin}/__ovs_lab/session`, {
      method: 'POST',
      headers: {
        Origin: origin,
        'Content-Type': 'application/json',
        ...(prior
          ? { Cookie: prior.cookie, 'X-CSRF-Token': prior.session.csrfToken }
          : {}),
      },
      body: JSON.stringify({ principal }),
    });
    assert.equal(response.status, 200);
    const session = await response.json();
    const cookie = response.headers.get('Set-Cookie').split(';')[0];
    const calls = [];
    const transport = async (url, options) => {
      const headers = new Headers(options.headers);
      headers.set('Cookie', cookie);
      headers.set('Origin', origin);
      headers.set('X-OVS-Lab-Epoch', session.epoch);
      calls.push(options.method);
      return fetch(url, { ...options, headers });
    };
    const client = new CoreHttpClient({
      origin,
      nodeId: session.nodeId,
      csrfToken: () => session.csrfToken,
      validate: validateContract,
      fetch: transport,
    });
    return { session, cookie, client, calls, transport };
  }
  return { store, origin, login };
}

void test('HTTP bootstrap, paginated Ports, persisted save and read-only identity use the production contract validator', async (t) => {
  const { login } = await httpFixture(t);
  const alice = await login('alice');
  const controller = new WorkspaceController(alice.client);
  await controller.refresh();
  assert.equal(controller.getSnapshot().inventory.items.length, 6);
  assert.equal(controller.canWrite(), true);
  const command = {
    ...freshCommand(),
    expectedGeneration:
      controller.getSnapshot().snapshot.candidate.currentGeneration,
  };
  assert.equal(await controller.mutate(command), true);
  const restored = new WorkspaceController(alice.client);
  await restored.refresh();
  assert.equal(
    restored.getSnapshot().snapshot.candidate.intents[0].mine.mode,
    'trunk',
  );
  const observer = await login('observer');
  const readonly = new WorkspaceController(observer.client);
  await readonly.refresh();
  assert.equal(readonly.canWrite(), false);
  assert.equal(await readonly.mutate(command), false);
});

void test('HTTP validation completes on the server and restores its exact result and job in a new controller', async (t) => {
  const { login, store } = await httpFixture(t);
  const alice = await login('alice');
  stage(store, 'alice');
  store.validations.policyChange('available');
  const controller = new WorkspaceController(alice.client);
  await controller.refresh();
  assert.equal(controller.canValidate(), true);
  assert.equal(await controller.validate(randomUUID()), true);
  const validationId = controller.getSnapshot().workspace.latestValidation.id;
  controller.dispose();
  // The worker keeps going after the requesting client has gone away.
  await delay(550);
  const restored = new WorkspaceController(alice.client);
  await restored.refresh();
  const state = restored.getSnapshot();
  assert.equal(state.workspace.latestValidation.id, validationId);
  assert.equal(state.workspace.latestValidation.status, 'passed');
  assert.equal(state.validationJob.state, 'succeeded');
  assert.equal(state.workspace.permissions.startSafeApply, true);
  store.setMeta('providerAvailable', false);
  await restored.pollValidation();
  assert.equal(
    restored.getSnapshot().workspace.latestValidation.status,
    'expired',
  );
  assert.equal(restored.getSnapshot().inventory, null);
  assert.ok(restored.getSnapshot().snapshot.candidate.intents.length);
});

void test('lost validation response recovers original request by GET without a duplicate POST', async (t) => {
  const { login, origin, store } = await httpFixture(t);
  const alice = await login('alice');
  stage(store, 'alice');
  const client = new CoreHttpClient({
    origin,
    nodeId: alice.session.nodeId,
    csrfToken: () => alice.session.csrfToken,
    validate: validateContract,
    fetch: async (url, options) => {
      const response = await alice.transport(url, options);
      if (options.method === 'POST') {
        await response.text();
        throw new TypeError('Accepted reply lost.');
      }
      return response;
    },
  });
  const controller = new WorkspaceController(client);
  await controller.refresh();
  const requestId = randomUUID();
  assert.equal(await controller.validate(requestId), false);
  assert.equal(controller.getSnapshot().phase, 'unknown');
  assert.equal(controller.getSnapshot().pendingValidation.requestId, requestId);
  assert.equal(controller.canWrite(), false);
  assert.equal(controller.canValidate(), false);
  await controller.recover();
  assert.equal(controller.getSnapshot().phase, 'ready');
  assert.equal(
    controller.getSnapshot().workspace.latestValidation.requestId,
    requestId,
  );
  assert.equal(alice.calls.filter((method) => method === 'POST').length, 1);
});

void test('wrong-snapshot validation acceptance remains unknown and recovery refuses unrelated ledger evidence', async (t) => {
  const { login, origin, store } = await httpFixture(t);
  const alice = await login('alice');
  stage(store, 'alice');
  const client = new CoreHttpClient({
    origin,
    nodeId: alice.session.nodeId,
    csrfToken: () => alice.session.csrfToken,
    validate: validateContract,
    fetch: async (url, options) => {
      const response = await alice.transport(url, options);
      const address =
        typeof url === 'string' ? url : url instanceof URL ? url.href : url.url;
      if (address.endsWith('/validations')) {
        const data = await response.json();
        data.candidateRevision = 'wrong-revision';
        return new Response(JSON.stringify(data), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      if (address.includes('/requests/')) {
        const data = await response.json();
        data.operation = 'candidate';
        return new Response(JSON.stringify(data), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return response;
    },
  });
  const controller = new WorkspaceController(client);
  await controller.refresh();
  assert.equal(await controller.validate(randomUUID()), false);
  await controller.recover();
  assert.equal(controller.getSnapshot().phase, 'unknown');
  assert.equal(alice.calls.filter((method) => method === 'POST').length, 1);
});

void test('revocation between browser review and HTTP validation clears private state and closes controls', async (t) => {
  const { login, store } = await httpFixture(t);
  const alice = await login('alice');
  stage(store, 'alice');
  const controller = new WorkspaceController(alice.client);
  await controller.refresh();
  assert.equal(controller.canValidate(), true);
  store.setMeta('revokedEditors', ['alice']);
  assert.equal(await controller.validate(randomUUID()), false);
  assert.equal(controller.getSnapshot().permissionError, true);
  assert.equal(controller.getSnapshot().snapshot, null);
  assert.equal(controller.getSnapshot().workspace, null);
  assert.equal(controller.canValidate(), false);
  assert.equal(
    store.db.prepare('SELECT COUNT(*) AS n FROM validations').get().n,
    0,
  );
});

void test('HTTP reads reject passed validation with unknown checks and unrelated job evidence', async (t) => {
  const { login, origin, store } = await httpFixture(t);
  const alice = await login('alice');
  stage(store, 'alice');
  store.validations.policyChange('available');
  const validation = validateStaged(store, alice.session).result;
  const client = new CoreHttpClient({
    origin,
    nodeId: alice.session.nodeId,
    csrfToken: () => alice.session.csrfToken,
    validate: validateContract,
    fetch: async (url, options) => {
      const response = await alice.transport(url, options);
      const data = await response.json();
      if (data.checks) data.checks[0].state = 'unknown';
      if (data.kind === 'validation') data.correlationId = 'unrelated-request';
      return new Response(JSON.stringify(data), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    },
  });
  await assert.rejects(
    client.readValidation(validation.id),
    /blocking evidence/,
  );
  await assert.rejects(
    client.readValidationJob(validation),
    /another validation job/,
  );
});

void test('an accepted save with a lost HTTP reply recovers by GET without another PATCH', async (t) => {
  const { login, origin } = await httpFixture(t);
  const alice = await login('alice');
  const client = new CoreHttpClient({
    origin,
    nodeId: alice.session.nodeId,
    csrfToken: () => alice.session.csrfToken,
    validate: validateContract,
    fetch: async (url, options) => {
      const response = await alice.transport(url, options);
      if (options.method === 'PATCH') {
        await response.text();
        throw new TypeError('Reply lost after durable commit.');
      }
      return response;
    },
  });
  const controller = new WorkspaceController(client);
  await controller.refresh();
  const command = {
    ...freshCommand(),
    expectedGeneration:
      controller.getSnapshot().snapshot.candidate.currentGeneration,
  };
  assert.equal(await controller.mutate(command), false);
  assert.equal(controller.getSnapshot().phase, 'unknown');
  assert.equal(controller.canWrite(), false);
  await controller.recover();
  assert.equal(controller.getSnapshot().phase, 'ready');
  assert.equal(controller.getSnapshot().pendingRequestId, null);
  assert.equal(alice.calls.filter((method) => method === 'PATCH').length, 1);
});

void test('session switch invalidates the former session and clears private client snapshots on 401', async (t) => {
  const { login } = await httpFixture(t);
  const alice = await login('alice');
  const controller = new WorkspaceController(alice.client);
  await controller.refresh();
  await controller.mutate({
    ...freshCommand(),
    expectedGeneration:
      controller.getSnapshot().snapshot.candidate.currentGeneration,
  });
  const bob = await login('bob', alice);
  assert.equal((await bob.client.readCandidate()).candidate.intents.length, 0);
  await controller.refresh();
  assert.equal(controller.getSnapshot().phase, 'signed-out');
  assert.equal(controller.getSnapshot().snapshot, null);
  assert.equal(controller.getSnapshot().inventory, null);
});

void test('cross-origin, wrong epoch and missing CSRF are rejected at the local HTTP boundary', async (t) => {
  const { login, origin } = await httpFixture(t);
  const alice = await login('alice');
  const snapshot = await alice.client.readCandidate();
  const command = {
    ...freshCommand(),
    expectedGeneration: snapshot.candidate.currentGeneration,
  };
  const headers = {
    Origin: origin,
    Cookie: alice.cookie,
    'Content-Type': 'application/json',
    'Idempotency-Key': command.requestId,
    'If-Match': snapshot.etag,
    'X-OVS-Lab-Epoch': alice.session.epoch,
    'X-CSRF-Token': alice.session.csrfToken,
  };
  for (const override of [
    { Origin: 'https://other.example' },
    { 'X-CSRF-Token': 'wrong-token' },
    { 'X-OVS-Lab-Epoch': 'another-session' },
  ]) {
    const response = await fetch(`${origin}/api/v1/candidate`, {
      method: 'PATCH',
      headers: { ...headers, ...override },
      body: JSON.stringify(command),
    });
    assert.ok([401, 403].includes(response.status));
  }
  assert.equal(
    (await alice.client.readCandidate()).candidate.intents.length,
    0,
  );
});

void test('paging rejects external generation changes instead of presenting mixed snapshots', async (t) => {
  const { login, store } = await httpFixture(t);
  const alice = await login('alice');
  let reads = 0;
  await assert.rejects(() =>
    readPinnedInventory({
      listPorts: async (query) => {
        if (++reads === 2) store.externalChange(null, null);
        return alice.client.listPorts(query);
      },
    }),
  );
});

void test('a disposed session cannot repopulate state from delayed reads', async (t) => {
  const { login } = await httpFixture(t);
  const alice = await login('alice');
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const client = {
    nodeId: alice.session.nodeId,
    readWorkspace: async () => {
      await gate;
      return alice.client.readWorkspace();
    },
    readCandidate: () => alice.client.readCandidate(),
    listPorts: (query) => alice.client.listPorts(query),
  };
  const controller = new WorkspaceController(client);
  const pending = controller.refresh();
  controller.dispose();
  release();
  await pending;
  assert.equal(controller.getSnapshot().phase, 'signed-out');
  assert.equal(controller.getSnapshot().snapshot, null);
});
