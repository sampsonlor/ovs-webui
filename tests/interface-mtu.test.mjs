import { test } from 'node:test';
import assert from 'node:assert/strict';
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
