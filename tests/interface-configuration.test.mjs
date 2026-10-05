import { test } from 'node:test';
import assert from 'node:assert/strict';
import { configurationObservation } from '../frontend/src/interface-configuration.ts';

const observation = (value, availability = 'known') => ({
  value,
  availability,
  source: { freshness: 'fresh', confidence: 'proven' },
});

await test('Interface configuration retains native empty allocation, disabled rates, default bursts and exact native units', () => {
  const item = {
    fields: {
      ofport_request: observation([]),
      ingress_policing_rate: observation('0'),
      ingress_policing_burst: observation('0'),
      ingress_policing_kpkts_rate: observation('9'),
      ingress_policing_kpkts_burst: observation('9223372036854775807'),
    },
  };
  assert.equal(
    configurationObservation(item, 'ofport_request'),
    'Automatic allocation (native empty request)',
  );
  assert.equal(
    configurationObservation(item, 'ingress_policing_rate'),
    'Disabled (0 kbit/s)',
  );
  assert.equal(
    configurationObservation(item, 'ingress_policing_burst'),
    'Native default (0 kbit)',
  );
  assert.equal(
    configurationObservation(item, 'ingress_policing_kpkts_rate'),
    '9 kpps',
  );
  assert.equal(
    configurationObservation(item, 'ingress_policing_kpkts_burst'),
    '9223372036854775807 kpackets',
  );
  item.fields.ofport_request.value = ['65279'];
  assert.equal(configurationObservation(item, 'ofport_request'), '65279');
});

await test('Unsupported, missing, withheld and invalid configuration never turn into zero or a default request', () => {
  const item = { fields: {} };
  assert.equal(configurationObservation(item, 'ofport_request'), 'Unavailable');
  for (const [state, result] of [
    ['unknown', 'Unknown'],
    ['unsupported', 'Unsupported'],
    ['withheld', 'Withheld'],
    ['unavailable', 'Unavailable'],
  ]) {
    item.fields.ofport_request = observation(['12'], state);
    assert.equal(configurationObservation(item, 'ofport_request'), result);
  }
  for (const value of [['0'], ['65280'], ['1', '2'], '1', [1]]) {
    item.fields.ofport_request = observation(value);
    assert.equal(
      configurationObservation(item, 'ofport_request'),
      'Unknown native shape',
    );
  }
  for (const value of ['-1', '1.0', '01', '9223372036854775808', 0]) {
    item.fields.ingress_policing_rate = observation(value);
    assert.equal(
      configurationObservation(item, 'ingress_policing_rate'),
      'Unknown native shape',
    );
  }
});

await test('Stale configuration retains a labelled last observation and unavailable confidence cannot imply a current limit', () => {
  const item = { fields: { ingress_policing_rate: observation('1000') } };
  item.fields.ingress_policing_rate.source.freshness = 'stale';
  assert.equal(
    configurationObservation(item, 'ingress_policing_rate'),
    'Last observed: 1000 kbit/s',
  );
  item.fields.ingress_policing_rate.source.confidence = 'partial';
  assert.equal(
    configurationObservation(item, 'ingress_policing_rate'),
    'Unknown',
  );
  item.fields.ingress_policing_rate.source.confidence = 'proven';
  item.fields.ingress_policing_rate.source.freshness = 'unavailable';
  assert.equal(
    configurationObservation(item, 'ingress_policing_rate'),
    'Unknown',
  );
});
