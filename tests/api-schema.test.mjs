import test from 'node:test';
import assert from 'node:assert/strict';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { contract, schemas } from '../contracts/core-v0.1.mjs';
import {
  schemaExamples,
  command,
} from '../contracts/examples/core-fixtures.mjs';

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
// OpenAPI components are not themselves a JSON Schema vocabulary. Register the
// reusable schemas under the exact local references used by the contract.
for (const [name, schema] of Object.entries(schemas))
  ajv.addSchema(schema, `#/components/schemas/${name}`);
const validate = (name, value) => {
  const check = ajv.getSchema(`#/components/schemas/${name}`);
  assert.ok(check, name);
  return { valid: check(value), errors: check.errors };
};

void test('all contract schemas compile under JSON Schema 2020-12 and all examples validate', () => {
  for (const name of Object.keys(schemas))
    assert.ok(ajv.getSchema(`#/components/schemas/${name}`));
  for (const [name, example] of schemaExamples) {
    const result = validate(name, example);
    assert.equal(
      result.valid,
      true,
      `${name}: ${JSON.stringify(result.errors)}`,
    );
  }
});

void test('wire VLAN intents reject ambiguous all-VLAN input, malformed IDs, duplicates and raw OVS writes', () => {
  for (const value of [
    { mode: 'trunk', tag: null, trunks: [] },
    { mode: 'access', tag: 120, trunks: [240] },
    { mode: 'trunk', tag: 120, trunks: [240] },
    { mode: 'trunk', tag: null, trunks: [0, 4095] },
    { mode: 'trunk', tag: null, trunks: '120,240' },
    { mode: 'trunk', tag: null, trunks: [120, 120] },
    { mode: 'access', tag: 120, trunks: [], ovsdb: { op: 'update' } },
  ])
    assert.equal(validate('VlanIntent', value).valid, false);
  assert.equal(
    validate('VlanObservation', {
      availability: 'unsupported',
      value: null,
      native: { mode: 'trunk', tag: null, trunks: [] },
      reason:
        'All-VLAN configuration is observable but not editable in this slice.',
    }).valid,
    true,
  );
});

void test('reconciliation cannot accept a client-selected outcome; apply cannot accept force or expert authority', () => {
  assert.equal(
    validate('ReconciliationRequest', { requestId: 'req-1', result: 'applied' })
      .valid,
    false,
  );
  for (const extra of [
    { force: true },
    { mode: 'expert' },
    { userId: 'admin' },
    { timeout: 900000 },
  ])
    assert.equal(
      validate('StartSafeApplyRequest', { ...command, ...extra }).valid,
      false,
    );
  assert.equal(
    validate('StartSafeApplyRequest', { ...command, reason: '   ' }).valid,
    false,
  );
});

void test('every mutation declares an idempotency key and every operation resolves its schemas', () => {
  const operationIds = new Set();
  const visit = (node) => {
    if (!node || typeof node !== 'object') return;
    if (node.$ref) assert.ok(schemas[node.$ref.split('/').at(-1)], node.$ref);
    for (const value of Object.values(node)) visit(value);
  };
  visit(contract);
  for (const [path, methods] of Object.entries(contract.paths)) {
    for (const [method, op] of Object.entries(methods)) {
      assert.ok(!operationIds.has(op.operationId));
      operationIds.add(op.operationId);
      for (const parameter of path.matchAll(/\{([^}]+)\}/g))
        assert.ok(
          op.parameters.some(
            (p) => p.in === 'path' && p.name === parameter[1] && p.required,
          ),
        );
      if (method !== 'get') {
        assert.ok(
          op.parameters.some((p) => p.name === 'Idempotency-Key' && p.required),
        );
        assert.ok(
          op.parameters.some((p) => p.name === 'X-CSRF-Token' && p.required),
        );
      }
    }
  }
});
