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
import { CoreHttpClient } from '../lib/api/http-client.ts';
import {
  WorkspaceController,
  readPinnedInventory,
} from '../lib/api/workspace-controller.ts';
import { validateContract } from '../lib/api/validator.generated.mjs';

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
  const open = () => {
    const s = new CoreLabStore(file);
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

test('Candidate, session and request evidence survive closing and reopening the database', (t) => {
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

test('independent database connections enforce one strong ETag winner without overwriting it', (t) => {
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

test('idempotent replay returns the original snapshot; changed payload or ETag cannot reuse its key', (t) => {
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

test('per-user Candidates and request ledger are isolated even when users reuse a request ID', (t) => {
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

test('stale and overlapping external observations require reviewed atomic rebase and preserve running data', (t) => {
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

test('Observe-only Ports, provider failure and node admission lock cannot create editable intent', (t) => {
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
  const handler = coreLabMiddleware(store);
  const server = createServer(handler);
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const origin = `http://127.0.0.1:${server.address().port}`;
  t.after(() => {
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

test('HTTP bootstrap, paginated Ports, persisted save and read-only identity use the production contract validator', async (t) => {
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

test('an accepted save with a lost HTTP reply recovers by GET without another PATCH', async (t) => {
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

test('session switch invalidates the former session and clears private client snapshots on 401', async (t) => {
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

test('cross-origin, wrong epoch and missing CSRF are rejected at the local HTTP boundary', async (t) => {
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

test('paging rejects external generation changes instead of presenting mixed snapshots', async (t) => {
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

test('a disposed session cannot repopulate state from delayed reads', async (t) => {
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
