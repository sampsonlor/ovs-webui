import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  policingReady,
  policingSummary,
  policingRate,
} from '../frontend/src/interface-policing.ts';

await test('Policing observation keeps exact uint64 kernel rates and null separate from zero', () => {
  assert.equal(
    policingRate(
      { bytes_per_second: '18446744073709551615' },
      'bytes_per_second',
    ),
    '18446744073709551615 bytes/s',
  );
  assert.equal(
    policingRate({ packets_per_second: '0' }, 'packets_per_second'),
    '0 packets/s',
  );
  assert.equal(
    policingRate({ packets_per_second: null }, 'packets_per_second'),
    'Not reported',
  );
  for (const value of [
    undefined,
    9007199254740992,
    '01',
    '-1',
    '18446744073709551616',
    '1.5',
  ])
    assert.equal(
      policingRate({ bytes_per_second: value }, 'bytes_per_second'),
      'Unknown',
    );
});

await test('Policing summary distinguishes fresh empty coverage, partial rules and withheld values', () => {
  const item = {
    linux_ingress_policing: {
      availability: 'known',
      actions: [],
      source: {
        freshness: 'fresh',
        confidence: 'proven',
        authority: 'linux-netlink-observation',
      },
    },
  };
  assert.equal(policingReady(item), true);
  assert.match(
    policingSummary(item),
    /does not establish that traffic is unrestricted/,
  );
  item.linux_ingress_policing.availability = 'partial';
  assert.match(policingSummary(item), /empty list does not establish absence/);
  item.linux_ingress_policing.availability = 'withheld';
  assert.equal(policingReady(item), false);
  assert.match(policingSummary(item), /permission is required/);
});

await test('Policing source failure and binding changes never display retained action values', () => {
  for (const [key, value] of [
    ['freshness', 'stale'],
    ['confidence', 'unknown'],
    ['authority', 'ovsdb-configuration'],
  ]) {
    const item = {
      linux_ingress_policing: {
        availability: 'known',
        actions: [{}],
        source: {
          freshness: 'fresh',
          confidence: 'proven',
          authority: 'linux-netlink-observation',
        },
      },
    };
    item.linux_ingress_policing.source[key] = value;
    assert.equal(policingReady(item), false);
    assert.match(policingSummary(item), /unavailable/);
  }
  assert.match(
    policingSummary({
      linux_ingress_policing: {
        availability: 'unavailable',
        reason: 'OVS_DEVICE_BINDING_CHANGED',
      },
    }),
    /identities no longer match/,
  );
  assert.equal(policingReady({}), false);
});
