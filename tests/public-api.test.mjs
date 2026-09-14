import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { breakingChanges } from '../scripts/check-public-compatibility.mjs';
import { StreamTracker, ResponseFence, knownAction, receiptURL, receiptMatches } from '../clients/typescript/recovery.ts';
const contract = JSON.parse(readFileSync(new URL('../contracts/v1.openapi.json', import.meta.url)));

await test('public compatibility rejects removals, input requirements, narrowing and identity drift', () => {
 for (const mutation of [
  c => delete c.paths['/candidate'],
  c => c.components.schemas.Id.pattern = '^bad$',
  c => c.components.schemas.LabelCommand.required.push('extra'),
  c => c.components.schemas.LabelCommand.properties.name.maxLength = 5,
  c => c.paths['/candidate'].patch['x-request-domain'] = 'management',
  c => c.components.schemas.Id.enum = ['only-this'],
  c => delete c.components.schemas.RequestReceipt.properties.request_epoch,
 ]) { const changed = structuredClone(contract); mutation(changed); assert.notEqual(breakingChanges(contract, changed).length, 0); }
});
await test('public compatibility accepts optional fields, unknown observed states and additional endpoints', () => {
 const changed = structuredClone(contract);
 changed.components.schemas.Resource.properties.future = { type: 'string' };
 changed.components.schemas.Resource.properties.state['x-known-values'] = ['future-state'];
 changed.paths['/new-observation'] = structuredClone(changed.paths['/health']);
 assert.deepEqual(breakingChanges(contract, changed), []);
});
await test('transport coverage remains complete and switching writes use Candidate', () => {
 const coverage = JSON.parse(readFileSync(new URL('../contracts/v1.coverage.json', import.meta.url)));
 assert.equal(Object.keys(coverage.scope).length,58); assert.equal(Object.keys(coverage.pages).length,53);
 for (const path of ['/bridges','/ports','/interfaces','/bonds','/vlans','/dpdk','/offload']) assert.deepEqual(Object.keys(contract.paths[path]),['get']);
 assert.ok(contract.paths['/candidate'].patch);
 for (const [path, methods] of Object.entries(contract.paths)) for (const op of Object.values(methods)) {
  assert.ok(op.operationId && Number.isInteger(op['x-service-issue']),path);
  if (op['x-request-domain']) assert.ok(op.parameters.some(p=>p.name==='Idempotency-Key'&&p.required),path);
 }
});
await test('recovery queries preserve the original identity and never submit another command', () => {
 const identity={request_id:'request',request_domain:'management',request_epoch:'epoch'};
 assert.equal(receiptURL(identity),'/api/v1/requests/request?domain=management&epoch=epoch');
 assert.equal(receiptMatches(identity,identity),true);
 for(const key of ['request_id','request_domain','request_epoch']) assert.equal(receiptMatches({...identity,[key]:'other'},identity),false);
});
await test('stream gaps, reconnects and unknown hints retain REST authority', () => {
 const t=new StreamTracker();const v=(sequence,type='resource.changed',stream_id='one')=>({sequence,type,stream_id});
 assert.equal(t.accept(v('1')),'resync');assert.equal(t.accept(v('2')),'hint');assert.equal(t.accept(v('1')),'ignore');
 assert.equal(t.accept(v('4')),'resync');assert.equal(t.accept(v('5','future')),'ignore');assert.equal(t.accept(v('6')),'hint');
 assert.equal(t.accept(v('1','heartbeat','two')),'resync');assert.equal(t.accept(v('18446744073709551616')),'resync');assert.equal(t.accept(v('0')),'resync');
});
await test('older REST responses and old permissions cannot override a current view', () => {
 const f=new ResponseFence();const a=f.begin('alice:policy1:port1');const b=f.begin('alice:policy1:port1');
 assert.equal(f.accepts(a),false);assert.equal(f.accepts(b),true);f.reset();assert.equal(f.accepts(b),false);
 const c=f.begin('bob:policy2:port2');assert.equal(f.accepts(c),true);assert.equal(f.accepts(b),false);
 assert.equal(knownAction('future',new Set(['ready']),['apply'],'apply',new Set(['apply'])),false);
 assert.equal(knownAction('ready',new Set(['ready']),['apply'],'apply',new Set(['apply'])),true);
});
