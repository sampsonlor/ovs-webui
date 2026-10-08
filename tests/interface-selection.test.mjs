import test from 'node:test';
import assert from 'node:assert/strict';
import { API } from '../frontend/src/api.ts';
import { Controller } from '../frontend/src/model.ts';
import {
  interfaceListPath,
  readInterfaceQuery,
} from '../frontend/src/interface-query.ts';
import { api as contract } from '../contracts/public-v1.mjs';

const bridge = '11111111-1111-4111-8111-111111111111';
const other = '22222222-2222-4222-8222-222222222222';
const session = {
  principal_id: other,
  effective_capabilities: [
    'session.read',
    'inventory.read',
    'state.read',
    'configuration.read',
  ],
  request_epochs: {},
};
const storage = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
};
const response = (value, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
async function until(check) {
  const end = Date.now() + 2000;
  while (!check()) {
    assert.ok(Date.now() < end, 'Controller did not settle');
    await new Promise((resolve) => setImmediate(resolve));
  }
}

await test('Interface selection URLs distinguish empty native default and retain exact encoded values', () => {
  const path = interfaceListPath('port & 12', 10, 'sealed+page', {
    bridgeID: bridge,
    nativeType: '',
    linkState: 'unknown',
  });
  const state = readInterfaceQuery(path.split('?')[1]);
  assert.equal(state.interfaceNativeType, '');
  assert.equal(state.interfaceFilter, 'port & 12');
  assert.equal(state.interfaceBridgeID, bridge);
  assert.equal(state.interfaceLinkState, 'unknown');
  assert.equal(state.interfaceLimit, 10);
  assert.equal(
    new URL(path, 'https://synthetic.invalid').searchParams.get('cursor'),
    'sealed+page',
  );
  assert.equal(readInterfaceQuery('').interfaceNativeType, null);
  const unknown = interfaceListPath('', 25, '', {
    bridgeID: '',
    nativeType: 'future & type',
    linkState: '',
  });
  assert.equal(
    readInterfaceQuery(unknown.split('?')[1]).interfaceNativeType,
    'future & type',
  );
  const parameters = contract.paths['/interfaces'].get.parameters;
  for (const name of ['bridge_id', 'native_type', 'link_state'])
    assert.equal(parameters.find((p) => p.name === name).required, false);
  assert.equal(
    parameters.find((p) => p.name === 'native_type').schema.minLength,
    undefined,
  );
  assert.ok(
    !contract.paths['/ports'].get.parameters.some(
      (p) => p.name === 'native_type',
    ),
  );
});

await test('Interface route reload and pagination preserve server selection and Bridge identity', async () => {
  const calls = [];
  const api = new API(async (url) => {
    calls.push(url);
    if (url.endsWith('/session')) return response(session);
    if (url.includes('/bridges/'))
      return response({ management_id: bridge, name: 'synthetic-bridge' });
    return response({ items: [], next_cursor: 'next+snapshot' });
  }, storage);
  const c = new Controller(
    api,
    `/interfaces?bridge_id=${bridge}&native_type=&link_state=unknown&filter=p2&limit=10`,
  );
  await c.refresh();
  assert.equal(c.state.interfaceNativeType, '');
  assert.equal(c.state.resource.value.name, 'synthetic-bridge');
  c.interfacePage(undefined, undefined, 'next+snapshot');
  await until(() => c.state.interfaces.status === 'ready');
  let q = new URL(
    calls.findLast((url) => url.includes('/interfaces?')),
    'https://synthetic.invalid',
  ).searchParams;
  for (const [key, value] of Object.entries({
    bridge_id: bridge,
    native_type: '',
    link_state: 'unknown',
    filter: 'p2',
    limit: '10',
    cursor: 'next+snapshot',
  }))
    assert.equal(q.get(key), value);
  c.interfacePage();
  await until(() => c.state.interfaces.status === 'ready');
  q = new URL(
    calls.findLast((url) => url.includes('/interfaces?')),
    'https://synthetic.invalid',
  ).searchParams;
  assert.equal(q.has('cursor'), false);
  assert.equal(q.get('bridge_id'), bridge);
  assert.equal(q.get('native_type'), '');
});

await test('Delayed Interface selections cannot cross Bridge scopes or silently recover an expired cursor', async () => {
  let release;
  const c = new Controller(
    new API(async (url) => {
      if (url.endsWith('/session')) return response(session);
      if (url.includes('/bridges/')) return response({ management_id: bridge });
      if (url.includes(`bridge_id=${bridge}`))
        return new Promise((resolve) => {
          release = resolve;
        });
      return response({ code: 'CURSOR_EXPIRED' }, 410);
    }, storage),
    `/interfaces?bridge_id=${bridge}&native_type=patch&link_state=up`,
  );
  const first = c.refresh();
  await until(() => release);
  c.go(
    `/interfaces?bridge_id=${other}&native_type=internal&link_state=down&cursor=expired`,
  );
  release(response({ items: [{ name: 'old protected type' }] }));
  await first;
  await until(() => c.state.interfaces.status === 'unavailable');
  assert.equal(c.state.interfaces.error, 'CURSOR_EXPIRED');
  assert.equal(c.state.interfaces.value, null);
  assert.equal(c.state.interfaceBridgeID, other);
  assert.equal(c.state.interfaceNativeType, 'internal');
  assert.equal(c.state.interfaceLinkState, 'down');
  assert.ok(c.state.query.includes('cursor=expired'));
});

await test('Configuration permission changes clear Interface values and deny type filters without widening the scope', async () => {
  let current = session;
  const calls = [];
  const c = new Controller(
    new API(async (url) => {
      calls.push(url);
      if (url.endsWith('/session')) return response(current);
      if (url.includes('/bridges/')) return response({ management_id: bridge });
      return response({ items: [{ name: 'protected native type' }] });
    }, storage),
    `/interfaces?bridge_id=${bridge}&native_type=&link_state=unknown`,
  );
  await c.refresh();
  assert.equal(c.state.interfaces.value.items.length, 1);
  const reads = calls.filter((url) => url.includes('/interfaces?')).length;
  current = {
    ...session,
    effective_capabilities: ['session.read', 'inventory.read', 'state.read'],
  };
  await c.refresh();
  await until(() => c.state.interfaces.status === 'denied');
  assert.equal(c.state.interfaces.value, null);
  assert.equal(
    calls.filter((url) => url.includes('/interfaces?')).length,
    reads,
  );
  assert.equal(c.state.interfaceBridgeID, bridge);
  assert.equal(c.state.interfaceNativeType, '');
  assert.equal(c.state.interfaceLinkState, 'unknown');
  assert.ok(c.state.query.includes('native_type='));
});
