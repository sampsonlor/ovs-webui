import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { schemas } from '../contracts/core-v0.1.mjs';
import {
  candidate,
  command,
  provisional,
  requestRecord,
  problem,
  schemaExamples,
} from '../contracts/examples/core-fixtures.mjs';
import {
  CoreHttpClient,
  ApiProblemError,
  HttpContractError,
} from '../lib/api/http-client.ts';
import {
  submitSafeApplyOnce,
  recoverSafeApply,
} from '../lib/api/change-control.ts';

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
for (const [name, schema] of Object.entries(schemas))
  ajv.addSchema(schema, `#/components/schemas/${name}`);
const validate = (name, value) =>
  ajv.getSchema(`#/components/schemas/${name}`)(value);
const example = (name) =>
  structuredClone(schemaExamples.find(([key]) => key === name)[1]);
const reply = (data, status = 200, headers = {}) =>
  new Response(JSON.stringify(data), {
    status,
    headers: {
      'Content-Type':
        status >= 400 ? 'application/problem+json' : 'application/json',
      ...headers,
    },
  });
const makeClient = (transport, extra = {}) =>
  new CoreHttpClient({
    origin: 'https://switch.example',
    nodeId: 'node-demo',
    validate,
    csrfToken: () => 'fixture-csrf',
    fetch: transport,
    ...extra,
  });
const snapshot = { candidate, etag: '"candidate-r7-fresh"' };
const mutation = {
  requestId: 'req-stage-1',
  operation: 'set-vlan',
  portId: 'port-server-07',
  mine: { mode: 'trunk', tag: null, trunks: [120, 240] },
  expectedGeneration: candidate.currentGeneration,
};

test('Ports reads use the configured HTTP origin and preserve bounded query encoding', async (t) => {
  const inventory = example('PortsPage');
  let received;
  const server = createServer((req, res) => {
    received = { url: req.url, method: req.method, accept: req.headers.accept };
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify(inventory));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const client = makeClient(globalThis.fetch, {
    origin: `http://127.0.0.1:${server.address().port}`,
  });
  const result = await client.listPorts({
    search: 'bond & storage',
    cursor: 'snapshot+page/2',
  });
  const url = new URL(received.url, 'http://localhost');
  assert.equal(url.pathname, '/api/v1/ports');
  assert.equal(url.searchParams.get('search'), 'bond & storage');
  assert.equal(url.searchParams.get('cursor'), 'snapshot+page/2');
  assert.equal(received.method, 'GET');
  assert.match(received.accept, /application\/json/);
  assert.deepEqual(result, inventory);
});

test('Candidate round-trip preserves server ETag, idempotency key, CSRF and session transport policy', async () => {
  const calls = [];
  const client = makeClient(async (url, options) => {
    calls.push({ url, options });
    return reply(candidate, 200, { ETag: '"server-candidate-version"' });
  });
  const read = await client.readCandidate();
  const staged = await client.mutateCandidate(mutation, read);
  assert.equal(staged.kind, 'accepted');
  const { options } = calls[1];
  assert.equal(options.method, 'PATCH');
  assert.equal(options.headers.get('If-Match'), read.etag);
  assert.equal(options.headers.get('Idempotency-Key'), mutation.requestId);
  assert.equal(options.headers.get('X-CSRF-Token'), 'fixture-csrf');
  assert.equal(options.credentials, 'same-origin');
  assert.equal(options.redirect, 'error');
  assert.equal(options.cache, 'no-store');
  assert.deepEqual(JSON.parse(options.body), mutation);
  assert.equal(calls.length, 2);
});

test('missing or weak ETag, raw OVS writes and a locked candidate are rejected before transport', async () => {
  for (const etag of [null, '*', 'W/"version"', 'unquoted', '"bad\nvalue"']) {
    const client = makeClient(async () =>
      reply(candidate, 200, etag === null ? {} : { ETag: etag }),
    );
    await assert.rejects(() => client.readCandidate());
  }
  let calls = 0;
  const client = makeClient(async () => {
    calls++;
    return reply(candidate);
  });
  await assert.rejects(
    () =>
      client.mutateCandidate(
        { ...mutation, ovsdb: { op: 'update' } },
        snapshot,
      ),
    HttpContractError,
  );
  await assert.rejects(
    () =>
      client.mutateCandidate(mutation, {
        ...snapshot,
        candidate: {
          ...candidate,
          lockedByTransactionId: 'transaction-active',
        },
      }),
    HttpContractError,
  );
  await assert.rejects(
    () => client.mutateCandidate(mutation, { ...snapshot, etag: '*' }),
    HttpContractError,
  );
  assert.equal(calls, 0);
});

test('a 412 for this candidate request is explicit rejection; no silent rebase or retry occurs', async () => {
  let calls = 0;
  const rejected = {
    ...problem,
    status: 412,
    code: 'ETAG_MISMATCH',
    requestId: mutation.requestId,
  };
  const client = makeClient(async () => {
    calls++;
    return reply(rejected, 412);
  });
  assert.deepEqual(await client.mutateCandidate(mutation, snapshot), {
    kind: 'rejected',
    problem: rejected,
  });
  assert.equal(calls, 1);
});

test('lost, malformed and wrong-request write replies retain the original request as unknown', async () => {
  for (const response of [
    () => {
      throw new TypeError('lost reply');
    },
    () => new Response('<html>gateway</html>', { status: 502 }),
    () => reply({ ...problem, requestId: 'different-request' }, 409),
    () =>
      reply({ ...candidate, nodeId: 'different-node' }, 200, {
        ETag: '"other"',
      }),
  ]) {
    let calls = 0;
    const client = makeClient(async () => {
      calls++;
      return response();
    });
    assert.deepEqual(await client.mutateCandidate(mutation, snapshot), {
      kind: 'unknown',
      requestId: mutation.requestId,
      nodeId: 'node-demo',
    });
    assert.equal(calls, 1);
  }
});

test('provider failures and expired sessions are errors rather than fabricated empty inventory', async () => {
  for (const [status, code] of [
    [503, 'PROVIDER_UNAVAILABLE'],
    [401, 'UNAUTHENTICATED'],
    [403, 'FORBIDDEN'],
  ]) {
    const client = makeClient(async () =>
      reply({ ...problem, status, code }, status),
    );
    await assert.rejects(
      () => client.listPorts(),
      (error) =>
        error instanceof ApiProblemError && error.problem.code === code,
    );
  }
  const empty = {
    ...example('PortsPage'),
    items: [],
    availability: 'complete',
  };
  assert.deepEqual(
    (await makeClient(async () => reply(empty)).listPorts()).items,
    [],
  );
});

test('scope and schema checks reject wrong object IDs, mixed generations and partial bootstrap identity', async () => {
  const inventory = example('PortsPage');
  await assert.rejects(
    () =>
      makeClient(async () =>
        reply({ ...inventory, nodeId: 'another-node' }),
      ).listPorts(),
    HttpContractError,
  );
  await assert.rejects(
    () =>
      makeClient(async () =>
        reply({
          ...inventory,
          items: [
            { ...inventory.items[0], generation: 'different-generation' },
          ],
        }),
      ).listPorts(),
    HttpContractError,
  );
  await assert.rejects(
    () =>
      makeClient(async () => reply(inventory.items[0])).readPort(
        'not-this-port',
      ),
    HttpContractError,
  );
  const workspace = example('WorkspaceSnapshot');
  await assert.rejects(
    () =>
      makeClient(async () =>
        reply({
          ...workspace,
          candidate: { ...candidate, nodeId: 'other-node' },
        }),
      ).readWorkspace(),
    HttpContractError,
  );
  await assert.rejects(
    () => makeClient(async () => reply({})).readCandidate(),
    HttpContractError,
  );
});

test('real HTTP gateway connects lost Safe Apply acknowledgement to existing read-only recovery', async () => {
  const calls = [];
  const client = makeClient(async (url, options) => {
    calls.push({ url, method: options.method });
    if (options.method === 'POST')
      throw new TypeError('accepted but response lost');
    if (url.endsWith(`/requests/${command.requestId}`))
      return reply(requestRecord);
    return reply(provisional);
  });
  const result = await submitSafeApplyOnce(client, 'node-demo', command);
  assert.equal(result.kind, 'unknown');
  const recovered = await recoverSafeApply(client, result.handle);
  assert.equal(recovered.kind, 'accepted');
  assert.equal(recovered.transaction.id, provisional.id);
  assert.deepEqual(
    calls.map((call) => call.method),
    ['POST', 'GET', 'GET'],
  );
});

test('404 request lookup and unexpected acceptance status never prove Not Applied', async () => {
  const missing = makeClient(async () =>
    reply({ ...problem, status: 404, code: 'NOT_FOUND' }, 404),
  );
  assert.equal(await missing.readRequest(command.requestId), null);
  assert.equal(
    (
      await recoverSafeApply(missing, {
        requestId: command.requestId,
        nodeId: 'node-demo',
      })
    ).kind,
    'unknown',
  );
  const incorrectStatus = makeClient(async () => reply(provisional, 200));
  assert.equal(
    (await submitSafeApplyOnce(incorrectStatus, 'node-demo', command)).kind,
    'unknown',
  );
});

test('request timeout aborts once and preserves uncertainty without submitting again', async () => {
  let calls = 0;
  const client = makeClient(
    (_url, options) =>
      new Promise((_resolve, reject) => {
        calls++;
        options.signal.addEventListener(
          'abort',
          () => reject(new Error('timeout')),
          { once: true },
        );
      }),
    { timeoutMs: 5 },
  );
  assert.equal(
    (await client.mutateCandidate(mutation, snapshot)).kind,
    'unknown',
  );
  assert.equal(calls, 1);
});

test('in-flight staging retains its reviewed command and ETag while CSRF refresh is pending', async () => {
  const input = structuredClone(mutation);
  const reviewed = structuredClone(snapshot);
  let release;
  let sent;
  const csrf = new Promise((resolve) => {
    release = resolve;
  });
  const client = makeClient(
    async (_url, options) => {
      sent = options;
      return reply(candidate, 200, { ETag: '"updated"' });
    },
    { csrfToken: () => csrf },
  );
  const pending = client.mutateCandidate(input, reviewed);
  input.requestId = 'changed-after-submit';
  input.mine.trunks = [999];
  reviewed.etag = '"different-review"';
  release('fixture-csrf');
  assert.equal((await pending).kind, 'accepted');
  assert.deepEqual(JSON.parse(sent.body), mutation);
  assert.equal(sent.headers.get('If-Match'), snapshot.etag);
  assert.equal(sent.headers.get('Idempotency-Key'), mutation.requestId);
});
