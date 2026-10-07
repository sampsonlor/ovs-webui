import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import {
  policingNumber,
  policingEditReason,
  applyReady,
} from '../frontend/src/policy.ts';

const session = {
  effective_capabilities: [
    'workspace.write',
    'configuration.apply',
    'ovs.interface.policing.write',
  ],
};
const item = () => ({
  source: { freshness: 'fresh' },
  policing_editable: true,
  policing_ownership: 'local-exclusive',
  allowed_operations: ['interface.policing.set'],
  linux_ingress_policing: { availability: 'known' },
  fields: Object.fromEntries(
    [
      'ingress_policing_rate',
      'ingress_policing_burst',
      'ingress_policing_kpkts_rate',
      'ingress_policing_kpkts_burst',
    ].map((name) => [
      name,
      { value: '0', availability: 'known', editable: name.endsWith('_rate') },
    ]),
  ),
});
await test('policing write policy requires independent permission, all configuration and desktop responsibility', () => {
  assert.equal(policingEditReason(item(), session, true), '');
  assert.notEqual(policingEditReason(item(), session, false), '');
  assert.notEqual(
    policingEditReason(
      item(),
      {
        effective_capabilities: [
          'workspace.write',
          'ovs.interface.mtu.write',
          'ovs.port.vlan.write',
        ],
      },
      true,
    ),
    '',
  );
  for (const change of [
    (i) => (i.policing_editable = false),
    (i) => (i.policing_ownership = 'unknown'),
    (i) => (i.allowed_operations = []),
    (i) => (i.source.freshness = 'stale'),
    (i) => (i.linux_ingress_policing.availability = 'partial'),
    (i) => (i.fields.ingress_policing_burst.value = '8000'),
    (i) => (i.fields.ingress_policing_kpkts_rate.value = '1001'),
    (i) => (i.fields.ingress_policing_rate.editable = false),
    (i) => (i.fields.ingress_policing_kpkts_burst.availability = 'withheld'),
    (i) => {
      i.fields.ingress_policing_rate.value = '100';
      i.fields.ingress_policing_kpkts_rate.value = '1';
    },
  ]) {
    const copy = item();
    change(copy);
    assert.notEqual(policingEditReason(copy, session, true), '');
  }
  for (const value of [null, 0, [], ['0'], '00', '1.5', '1000001', '-1'])
    assert.equal(policingNumber(value), null);
  const c = {
    id: 'c',
    revision: 'r',
    state: 'dirty',
    safe_apply_available: true,
    intents: [{ operation: 'interface.policing.set' }],
  };
  const v = {
    candidate_id: 'c',
    candidate_revision: 'r',
    state: 'passed',
    usable: true,
    execution_ready: true,
  };
  assert.equal(applyReady(c, v, session, true), true);
  assert.equal(
    applyReady(
      c,
      v,
      {
        effective_capabilities: [
          'configuration.apply',
          'ovs.interface.mtu.write',
        ],
      },
      true,
    ),
    false,
  );
  assert.equal(applyReady(c, v, session, false), false);
});
await test('policing public command enforces exclusive modes, bounds and no captured or raw host input', () => {
  const api = JSON.parse(
    readFileSync(new URL('../contracts/v1.openapi.json', import.meta.url)),
  );
  const ajv = new Ajv2020({ strict: false, allErrors: true });
  addFormats(ajv);
  ajv.addSchema({ $id: 'urn:ovs:policing-edit', components: api.components });
  const check = ajv.compile({
    $ref: 'urn:ovs:policing-edit#/components/schemas/InterfacePolicingIntent',
  });
  const id = '12345678-1234-4234-8234-123456789012';
  const intent = {
    intent_id: id,
    operation: 'interface.policing.set',
    object: {
      management_id: id,
      ovs_uuid: id,
      instance_generation: id,
      table: 'Interface',
    },
    policing: { mode: 'bandwidth', rate: 1000 },
  };
  for (const policing of [
    { mode: 'disabled', rate: 0 },
    { mode: 'bandwidth', rate: 1000000 },
    { mode: 'packets', rate: 1000 },
  ])
    assert.equal(
      check({ ...intent, policing }),
      true,
      JSON.stringify(check.errors),
    );
  for (const policing of [
    { mode: 'disabled', rate: 1 },
    { mode: 'packets', rate: 1001 },
    { mode: 'bandwidth', rate: 0 },
    { mode: 'bandwidth', rate: '1000' },
    { mode: 'combined', rate: 100 },
    { mode: 'bandwidth', rate: 100, burst: 8000 },
  ])
    assert.equal(check({ ...intent, policing }), false);
  for (const extra of [
    { policing_change: { before: {} } },
    { compensating: true },
    { path: '/sys/class/net/eth0' },
    { kernel_digest: 'forged' },
    { observed_after: 100 },
    { ovsdb: { op: 'update' } },
  ])
    assert.equal(check({ ...intent, ...extra }), false);
});
