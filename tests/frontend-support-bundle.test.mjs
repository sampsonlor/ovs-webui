import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { API } from '../frontend/src/api.ts';
import {
  authorizedSupportScopes,
  readVerifiedBundle,
  supportBundlePath,
} from '../frontend/src/support-bundles.ts';

const id = '11111111-1111-4111-8111-111111111111';
const epoch = '22222222-2222-4222-8222-222222222222';
const principal = '33333333-3333-4333-8333-333333333333';
const partBytes = 24 * 1024;
const content = Buffer.concat([
  Buffer.alloc(partBytes, 42),
  Buffer.from('last'),
]);
const now = Date.parse('2026-10-20T10:00:00Z');
const metadata = {
  id,
  bytes: content.length,
  parts: 2,
  sha256: createHash('sha256').update(content).digest('hex'),
  manifest: {
    version: 1,
    created_at: new Date(now).toISOString(),
    expires_at: new Date(now + 5000).toISOString(),
    sections: [{ kind: 'runtime', state: 'included' }],
    redaction: { policy: 'fixture', omitted: [] },
  },
};
const part = (index) => ({
  id,
  part: index,
  data_base64: content
    .subarray(index * partBytes, (index + 1) * partBytes)
    .toString('base64'),
});

await test('support download checks authorized scopes and assembles exact bounded parts', async () => {
  const session = { effective_capabilities: ['state.read', 'events.read'] };
  assert.equal(
    authorizedSupportScopes(['runtime', 'health', 'events'], session),
    true,
  );
  assert.equal(authorizedSupportScopes(['health', 'audit'], session), false);
  assert.equal(authorizedSupportScopes(['runtime', 'runtime'], session), false);
  const read = [];
  const output = await readVerifiedBundle(
    id,
    metadata,
    async (index) => {
      read.push(index);
      return part(index);
    },
    () => true,
    () => now,
  );
  assert.deepEqual(read, [0, 1]);
  assert.deepEqual(Buffer.from(output), content);
});

await test('support download rejects wrong parts, forged checksums, expiry and context changes', async () => {
  const verify = (meta, reader, active = () => true, clock = () => now) =>
    readVerifiedBundle(id, meta, reader, active, clock);
  let reads = 0;
  await assert.rejects(
    verify({ ...metadata, parts: 22 }, async () => (reads++, part(0))),
    /METADATA_INVALID/,
  );
  await assert.rejects(
    verify({ ...metadata, bytes: 524289 }, async () => (reads++, part(0))),
    /METADATA_INVALID/,
  );
  await assert.rejects(
    verify(
      metadata,
      async () => (reads++, part(0)),
      undefined,
      () => now + 5000,
    ),
    /EXPIRED/,
  );
  assert.equal(reads, 0);
  await assert.rejects(
    verify(metadata, async (i) => ({ ...part(i), part: i + 1 })),
    /PART_INVALID/,
  );
  await assert.rejects(
    verify(metadata, async (i) => ({ ...part(i), id: epoch })),
    /PART_INVALID/,
  );
  await assert.rejects(
    verify(metadata, async () => ({ ...part(0), data_base64: 'AA==' })),
    /PART_INVALID/,
  );
  await assert.rejects(
    verify(metadata, async (i) =>
      i === 1
        ? { ...part(i), data_base64: Buffer.from('fake').toString('base64') }
        : part(i),
    ),
    /CHECKSUM_MISMATCH/,
  );
  let active = true;
  await assert.rejects(
    verify(
      metadata,
      async (i) => {
        active = false;
        return part(i);
      },
      () => active,
    ),
    /CONTEXT_CHANGED/,
  );
  let time = now;
  await assert.rejects(
    verify(
      metadata,
      async (i) => {
        time = now + 5000;
        return part(i);
      },
      undefined,
      () => time,
    ),
    /EXPIRED/,
  );
});

await test('support artifact acknowledgement and recovery keep the same durable Job identity', async () => {
  const values = new Map();
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
  const json = (value, status = 200) =>
    new Response(JSON.stringify(value), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  let request;
  let posts = 0;
  const api = new API(async (path, input) => {
    if (path === '/api/v1' + supportBundlePath) {
      const body = JSON.parse(input.body);
      assert.equal(input.headers['Idempotency-Key'], body.request_id);
      assert.deepEqual(body.include_kinds, ['runtime']);
      assert.equal(body.redact, true);
      request = body.request_id;
      return json(
        {
          request_id: request,
          request_domain: 'management',
          request_epoch: epoch,
          job_id: id,
          resource_ref: {
            kind: 'artifact',
            id: ++posts === 1 ? id : principal,
          },
        },
        202,
      );
    }
    return json({
      request_id: request,
      request_domain: 'management',
      request_epoch: epoch,
      state: 'completed',
      resource_ref: { kind: 'artifact', id },
      job_ref: { kind: 'job', id },
    });
  }, storage);
  api.session = {
    principal_id: principal,
    request_epochs: { management: epoch },
  };
  const content = { include_kinds: ['runtime'], redact: true };
  assert.equal(
    (await api.command(supportBundlePath, 'POST', 'management', content))
      .job_id,
    id,
  );
  assert.equal(api.pending(), null);
  await assert.rejects(
    api.command(supportBundlePath, 'POST', 'management', content),
    /IDENTITY_MISMATCH/,
  );
  assert.equal(api.pending().request_id, request);
  assert.equal((await api.recover()).job_ref.id, id);
  assert.equal(api.pending(), null);
  assert.equal(posts, 2);
});
