import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AuthDiagnostics } from './frontend/auth-diagnostics.ts';

const origin = 'https://synthetic.invalid';

await test('auth metadata keeps request order across delayed login responses and distinguishes browser from helper', () => {
  const probe = new AuthDiagnostics(origin);
  const initial = probe.begin(origin + '/api/v1/session', 'GET', 'browser');
  const login = probe.begin(origin + '/api/v1/sessions', 'POST', 'browser');
  const helper = probe.begin(origin + '/api/v1/session', 'GET', 'helper');
  probe.response(login, 201);
  probe.response(helper, 503, 'AUTH_UNAVAILABLE');
  probe.response(initial, 401, 'UNAUTHENTICATED');
  assert.deepEqual(
    probe
      .snapshot()
      .entries.map((e) => [
        e.sequence,
        e.source,
        e.operation,
        e.status,
        e.code,
      ]),
    [
      [1, 'browser', 'read_session', 401, 'UNAUTHENTICATED'],
      [2, 'browser', 'create_session', 201, null],
      [3, 'helper', 'read_session', 503, 'AUTH_UNAVAILABLE'],
    ],
  );
});

await test('auth metadata rejects unrelated endpoints and never retains payloads, queries or unrecognized codes', () => {
  const probe = new AuthDiagnostics(origin);
  const secret = 'synthetic-private-password-cookie-csrf-SQL';
  for (const [url, method] of [
    ['https://other.invalid/api/v1/session', 'GET'],
    [origin + '/api/v1/users/' + secret, 'GET'],
    [origin + '/api/v1/sessions', 'DELETE'],
    [origin + '/api/v1/session/' + secret, 'GET'],
  ])
    assert.equal(probe.begin(url, method, 'browser'), null);
  const request = probe.begin(
    origin + '/api/v1/session?secret=' + secret,
    'GET',
    'helper',
  );
  probe.response(request, 503, secret);
  const success = probe.begin(origin + '/api/v1/sessions', 'POST', 'browser');
  probe.response(success, 201, secret);
  assert.equal(probe.snapshot().entries[0].code, 'OTHER_ERROR');
  assert.equal(probe.snapshot().entries[1].code, null);
  assert.ok(!JSON.stringify(probe.snapshot()).includes(secret));
  const snapshot = probe.snapshot();
  snapshot.entries[0].code = secret;
  assert.ok(!JSON.stringify(probe.snapshot()).includes(secret));
});

await test('auth metadata is bounded while retaining the latest failure and ignores evicted or duplicate completions', () => {
  const probe = new AuthDiagnostics(origin);
  const first = probe.begin('/api/v1/session', 'GET', 'browser');
  for (let i = 0; i < 64; i++) {
    const ticket = probe.begin('/api/v1/session', 'GET', 'browser');
    probe.response(ticket, 200);
  }
  const last = probe.begin('/api/v1/session', 'GET', 'helper');
  probe.response(last, 503, 'AUTH_UNAVAILABLE');
  probe.response(last, 200);
  probe.response(first, 401, 'UNAUTHENTICATED');
  const snapshot = probe.snapshot();
  assert.equal(snapshot.entries.length, 64);
  assert.equal(snapshot.observed, 66);
  assert.equal(snapshot.dropped, 2);
  assert.equal(snapshot.entries.at(-1).status, 503);
  assert.equal(snapshot.entries.at(-1).code, 'AUTH_UNAVAILABLE');
});

await test('auth metadata preserves pending, network failure and malformed status without logging raw errors', () => {
  const probe = new AuthDiagnostics(origin);
  const failed = probe.begin('/api/v1/session', 'GET', 'browser');
  const pending = probe.begin('/api/v1/workspace', 'GET', 'browser');
  probe.failed(failed);
  probe.response(pending, 999, 'AUTH_UNAVAILABLE');
  probe.response(failed, 200);
  assert.deepEqual(
    probe.snapshot().entries.map((e) => [e.outcome, e.status, e.code]),
    [
      ['network_error', null, null],
      ['pending', null, null],
    ],
  );
});
