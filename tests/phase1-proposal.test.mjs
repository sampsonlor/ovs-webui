import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

const proposal = JSON.parse(await readFile(
  new URL('../contracts/proposals/phase1-v1.openapi.json', import.meta.url), 'utf8',
));
const examples = JSON.parse(await readFile(
  new URL('../contracts/proposals/phase1-v1.examples.json', import.meta.url), 'utf8',
));
const schemaId = 'urn:ovs:phase1:proposal';
const ajv = new Ajv2020({ strict: false, allErrors: true });
addFormats(ajv);
ajv.addSchema({ $id: schemaId, components: proposal.components });
const validators = Object.fromEntries(Object.keys(proposal.components.schemas).map(name => [
  name, ajv.compile({ $ref: `${schemaId}#/components/schemas/${name}` }),
]));

await test('Phase 1 proposal has valid component schemas and resolvable local references', () => {
  assert.equal(proposal['x-review-status'], 'proposed');
  for (const schema of Object.values(proposal.components.schemas)) {
    assert.ok(ajv.validateSchema(schema), JSON.stringify(ajv.errors));
  }
  function visit(value) {
    if (!value || typeof value !== 'object') return;
    if (value.$ref) {
      assert.ok(value.$ref.startsWith('#/'), value.$ref);
      let target = proposal;
      for (const segment of value.$ref.slice(2).split('/')) {
        target = target?.[segment.replace(/~1/g, '/').replace(/~0/g, '~')];
      }
      assert.notEqual(target, undefined, value.$ref);
    }
    Object.values(value).forEach(visit);
  }
  visit(proposal);
});

await test('Phase 1 operations bind path identities and explicit request domains', () => {
  const operationIds = new Set();
  for (const [path, methods] of Object.entries(proposal.paths)) {
    for (const [method, operation] of Object.entries(methods)) {
      assert.ok(!operationIds.has(operation.operationId));
      operationIds.add(operation.operationId);
      const names = [...path.matchAll(/\{([^}]+)\}/g)].map(match => match[1]).sort();
      const parameters = operation.parameters ?? [];
      assert.deepEqual(parameters.filter(p => p.in === 'path' && p.required).map(p => p.name).sort(), names);
      if (method === 'get') assert.ok(!operation.requestBody);
      if (operation['x-request-domain']) {
        assert.ok(['workspace', 'management'].includes(operation['x-request-domain']));
        for (const name of ['Idempotency-Key', 'X-OVS-Request-Epoch']) {
          assert.ok(parameters.some(p => p.name === name && p.required));
        }
      }
    }
  }
});

// Shape checks do not prove authorization or real OVS execution; those have
// separate implementation gates in issues #34 and #38–#40.
for (const example of examples.cases) {
  await test(`Phase 1 wire example: ${example.name}`, () => {
    const validate = validators[example.schema];
    assert.ok(validate, `Missing schema ${example.schema}`);
    assert.equal(validate(example.value), example.valid, JSON.stringify(validate.errors));
  });
}
