import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import {
  mtuNumber,
  mtuEditReason,
  applyReady,
} from '../frontend/src/policy.ts';

const item = () => ({
  source: { freshness: 'fresh' },
  allowed_operations: ['interface.mtu.set'],
  mtu_editable: true,
  fields: {
    mtu_request: { availability: 'known', value: ['1500'], editable: true },
  },
});

const session = {
  effective_capabilities: [
    'workspace.write',
    'configuration.apply',
    'ovs.interface.mtu.write',
  ],
};
await test('known native empty MTU remains editable only with a valid server default and original field gates', () => {
  const automatic = item();
  automatic.fields.mtu_request.value = [];
  automatic.mtu_default = 1800;
  automatic.fields.mtu = { availability: 'known', value: ['1800'] };
  assert.equal(mtuEditReason(automatic, session, true), '');
  for (const value of [[], ['1500'], ['bad']]) {
    const changed = structuredClone(automatic);
    changed.fields.mtu.value = value;
    assert.match(
      mtuEditReason(changed, session, true),
      /not proven by the current device observation/,
    );
  }
  for (const value of [undefined, null, 0, 575, 65536, '1800', 1800.5]) {
    assert.notEqual(
      mtuEditReason({ ...automatic, mtu_default: value }, session, true),
      '',
    );
  }
  for (const value of [null, ['bad'], ['1800', '2000']]) {
    const changed = structuredClone(automatic);
    changed.fields.mtu_request.value = value;
    assert.notEqual(mtuEditReason(changed, session, true), '');
  }
  const hidden = structuredClone(automatic);
  hidden.fields.mtu_request.availability = 'withheld';
  assert.notEqual(mtuEditReason(hidden, session, true), '');
  assert.notEqual(mtuEditReason(automatic, session, false), '');
});

await test('clearing MTU uses the independent Interface permission in the shared Apply gate', () => {
  const candidate = {
    id: 'c',
    revision: 'r',
    state: 'dirty',
    safe_apply_available: true,
    intents: [{ operation: 'interface.mtu.clear' }],
  };
  const validation = {
    candidate_id: 'c',
    candidate_revision: 'r',
    state: 'passed',
    usable: true,
    execution_ready: true,
  };
  assert.equal(applyReady(candidate, validation, session, true), true);
  assert.equal(
    applyReady(
      candidate,
      validation,
      {
        effective_capabilities: ['configuration.apply', 'ovs.port.vlan.write'],
      },
      true,
    ),
    false,
  );
  assert.equal(applyReady(candidate, validation, session, false), false);
});

await test('public MTU clear is a closed object-only intent and never accepts a browser-selected default or outcome', () => {
  const contract = JSON.parse(
    readFileSync(new URL('../contracts/v1.openapi.json', import.meta.url)),
  );
  const ajv = new Ajv2020({ strict: false, allErrors: true });
  addFormats(ajv);
  const schemaId = 'urn:ovs:mtu-default-contract';
  ajv.addSchema({ $id: schemaId, components: contract.components });
  const check = ajv.compile({
    $ref: `${schemaId}#/components/schemas/InterfaceMTUClearIntent`,
  });
  const intent = {
    intent_id: '12345678-1234-4234-8234-123456789012',
    operation: 'interface.mtu.clear',
    object: {
      management_id: '12345678-1234-4234-8234-123456789012',
      ovs_uuid: '22345678-1234-4234-8234-123456789012',
      table: 'Interface',
      instance_generation: '33345678-1234-4234-8234-123456789012',
    },
  };
  assert.equal(check(intent), true, JSON.stringify(check.errors));
  for (const extra of [
    { mtu_request: null },
    { mtu_request: 0 },
    { default_context: { mtu: 1500 } },
    { compensating: true },
    { observed_after: 1500 },
    { ovsdb: { op: 'update' } },
  ]) {
    assert.equal(check({ ...intent, ...extra }), false);
  }
});
await test('MTU UI retains explicit optional values without normalizing native defaults', () => {
  assert.equal(mtuNumber(['1500']), 1500);
  for (const value of [
    [],
    null,
    ['1500', '2000'],
    [1500],
    ['1'],
    ['65536'],
    ['9223372036854775807'],
    ['1500.5'],
  ])
    assert.equal(mtuNumber(value), null);
});
await test('MTU edit and Apply gates enforce server hints, field permission and desktop responsibility', () => {
  assert.equal(mtuEditReason(item(), session, true), '');
  assert.notEqual(mtuEditReason(item(), session, false), '');
  assert.notEqual(
    mtuEditReason(
      item(),
      { effective_capabilities: ['workspace.write', 'ovs.port.vlan.write'] },
      true,
    ),
    '',
  );
  for (const change of [
    (i) => (i.source.freshness = 'stale'),
    (i) => (i.fields.mtu_request.availability = 'withheld'),
    (i) => (i.fields.mtu_request.editable = false),
    (i) => (i.allowed_operations = []),
    (i) => (i.mtu_editable = false),
  ]) {
    const i = item();
    change(i);
    assert.notEqual(mtuEditReason(i, session, true), '');
  }
  const candidate = {
    id: 'c',
    revision: 'r',
    safe_apply_available: true,
    intents: [{ operation: 'interface.mtu.set' }],
  };
  const validation = {
    usable: true,
    execution_ready: true,
    candidate_id: 'c',
    candidate_revision: 'r',
    risk_level: 'high',
  };
  // Complete the same public readiness fields as the established Apply gate.
  validation.state = 'passed';
  validation.expires_at = new Date(Date.now() + 60000).toISOString();
  candidate.state = 'dirty';
  assert.equal(applyReady(candidate, validation, session, true), true);
  assert.equal(
    applyReady(
      candidate,
      validation,
      {
        effective_capabilities: ['configuration.apply', 'ovs.port.vlan.write'],
      },
      true,
    ),
    false,
  );
  assert.equal(applyReady(candidate, validation, session, false), false);
});
