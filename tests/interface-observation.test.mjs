import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  observation,
  availability,
  deviceObservation,
  pciAssociation,
} from '../frontend/src/interface-observation.ts';
import { API } from '../frontend/src/api.ts';
import { Controller } from '../frontend/src/model.ts';

async function until(predicate) {
  const deadline = Date.now() + 2000;
  while (!predicate()) {
    assert.ok(
      Date.now() < deadline,
      'Controller did not reach the expected state',
    );
    await new Promise((resolve) => setImmediate(resolve));
  }
}

await test('Interface values retain empty, withheld, unknown and exact native integer meanings', () => {
  const item = { fields: {} };
  const value = (name, native, state = 'known') =>
    (item.fields[name] = { value: native, availability: state });
  value('type', '');
  value('mtu_request', []);
  value('mtu', []);
  value('ofport', '-1');
  assert.equal(observation(item, 'type'), 'system (native default)');
  assert.equal(
    observation(item, 'mtu_request'),
    'Not requested (native default)',
  );
  assert.equal(observation(item, 'mtu'), 'Not reported');
  assert.equal(observation(item, 'ofport'), '-1 · allocation failed');
  value('ofport', []);
  assert.equal(observation(item, 'ofport'), 'Not assigned');
  value('ifindex', '9223372036854775807');
  assert.equal(observation(item, 'ifindex'), '9223372036854775807');
  value('type', 'future-native');
  assert.equal(observation(item, 'type'), 'future-native');
  value('type', 'internal', 'withheld');
  assert.equal(observation(item, 'type'), 'Withheld');
  value('mtu', 1500, 'future-availability');
  assert.equal(observation(item, 'mtu'), 'Unknown');
  assert.equal(availability(item, 'duplex'), 'unavailable');
});

await test('PCI association requires reported bus evidence, independently of name and native type', () => {
  const item = {
    name: '0000:01:00.0',
    interface_type: 'dpdk',
    fields: { status: { availability: 'known', value: {} } },
  };
  assert.equal(pciAssociation(item), 'Not proven');
  item.fields.status.value.bus_info = 'pci:0000:01:00.0';
  assert.equal(pciAssociation(item), 'pci:0000:01:00.0');
  item.fields.status.value.bus_info = 'synthetic-virtual';
  assert.equal(pciAssociation(item), 'Not proven');
  item.fields.status.availability = 'withheld';
  assert.equal(deviceObservation(item, 'bus_info'), 'Withheld');
});

await test('Interface filters fence old replies and retain filter and limit on snapshot pagination', async () => {
  const session = {
    principal_id: '11111111-1111-4111-8111-111111111111',
    effective_capabilities: ['inventory.read'],
    request_epochs: {},
  };
  const urls = [];
  let release, resolveNew;
  const newPage = new Promise((resolve) => {
    resolveNew = resolve;
  });
  const api = new API(
    async (url) => {
      urls.push(url);
      if (url.endsWith('/session'))
        return new Response(JSON.stringify(session), {
          headers: { 'Content-Type': 'application/json' },
        });
      if (url.includes('/interfaces?')) {
        const q = new URL(url, 'https://synthetic.invalid').searchParams;
        if (!q.get('filter'))
          await new Promise((resolve) => {
            release = resolve;
          });
        else resolveNew();
        return new Response(
          JSON.stringify({
            items: [{ name: q.get('filter') || 'old' }],
            next_cursor: 'snapshot-token',
          }),
          { headers: { 'Content-Type': 'application/json' } },
        );
      }
      throw new Error('unexpected read');
    },
    { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  );
  const c = new Controller(api, '/interfaces');
  const first = c.refresh();
  await until(() => release);
  c.interfacePage('native port & member', 10);
  release();
  await first;
  await newPage;
  await until(() => c.state.interfaces.status === 'ready');
  assert.equal(c.state.interfaces.value.items[0].name, 'native port & member');
  c.interfacePage(
    c.state.interfaceFilter,
    c.state.interfaceLimit,
    'snapshot-token',
  );
  await until(() => urls.some((url) => url.includes('cursor=')));
  const query = new URL(urls.at(-1), 'https://synthetic.invalid').searchParams;
  assert.equal(query.get('filter'), 'native port & member');
  assert.equal(query.get('limit'), '10');
  assert.equal(query.get('cursor'), 'snapshot-token');
});

await test('Interface list and detail replies cannot restore protected observations after sign-out', async () => {
  const session = {
    principal_id: '11111111-1111-4111-8111-111111111111',
    effective_capabilities: ['inventory.read'],
    request_epochs: {},
  };
  for (const path of [
    '/interfaces',
    '/interfaces/33333333-3333-4333-8333-333333333333',
  ]) {
    let release;
    const response = (value) =>
      new Response(JSON.stringify(value), {
        headers: { 'Content-Type': 'application/json' },
      });
    const api = new API(
      async (url, options = {}) => {
        if (url.endsWith('/session'))
          return options.method === 'DELETE'
            ? new Response(null, { status: 204 })
            : response(session);
        if (url.includes('/interfaces'))
          return new Promise((resolve) => {
            release = resolve;
          });
        throw new Error('unexpected read');
      },
      { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    );
    const c = new Controller(api, path);
    const read = c.refresh();
    await until(() => release);
    await c.logout();
    release(
      response({
        name: 'retired synthetic Interface',
        items: [{ name: 'retired synthetic Interface' }],
      }),
    );
    await read;
    assert.equal(c.state.session, null);
    assert.equal(c.state.sessionReady, false);
    assert.equal(c.state.interfaces.value, null);
    assert.equal(c.state.interface.value, null);
  }
});
