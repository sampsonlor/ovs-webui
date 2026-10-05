import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  nativeTypeContext,
  currentPatchPeer,
  patchPeerReason,
} from '../frontend/src/interface-native-types.ts';

const fresh = { freshness: 'fresh', confidence: 'proven' };
function item(type) {
  return {
    name: 'eth0-dpdk-physical',
    fields: {
      type: { value: type, availability: 'known', source: { ...fresh } },
    },
  };
}

await test('native type context uses exact reported type and preserves unknown types without inferring hardware', () => {
  assert.equal(nativeTypeContext(item('')).label, 'System (native default)');
  assert.equal(nativeTypeContext(item('system')).label, 'System');
  assert.equal(nativeTypeContext(item('internal')).label, 'Internal');
  assert.equal(nativeTypeContext(item('patch')).patch, true);
  assert.match(
    nativeTypeContext(item('dpdk')).detail,
    /no hardware association/,
  );
  assert.match(
    nativeTypeContext(item('future-native')).label,
    /Unrecognized native type · future-native/,
  );
  assert.match(
    nativeTypeContext(item('dummy')).detail,
    /not evidence of a physical NIC/,
  );
  for (const value of [[], null, 0, {}])
    assert.equal(nativeTypeContext(item(value)).label, 'Unknown');
});

await test('native context keeps permission withholding and stale observations explicit in both depths', () => {
  const i = item('patch');
  i.fields.type.availability = 'withheld';
  assert.deepEqual(nativeTypeContext(i), {
    label: 'Withheld',
    detail:
      'Configuration read permission is required for native type and configured associations.',
    patch: false,
  });
  i.fields.type.availability = 'known';
  i.fields.type.source.freshness = 'stale';
  assert.match(nativeTypeContext(i).label, /^Last observed: Patch/);
  i.fields.type.source.freshness = 'fresh';
  i.fields.type.source.confidence = 'partial';
  assert.match(nativeTypeContext(i).label, /^Last observed:/);
});

await test('reported tunnel flow values remain dynamic configuration without fixed peers or raw extra map disclosure', () => {
  const i = item('geneve');
  i.fields.options = {
    availability: 'known',
    value: { remote_ip: 'flow', key: 'flow', secret: 'never-publish' },
  };
  assert.match(nativeTypeContext(i).detail, /depend on OpenFlow actions/);
  assert.doesNotMatch(JSON.stringify(nativeTypeContext(i)), /never-publish/);
  i.fields.options.availability = 'withheld';
  assert.doesNotMatch(nativeTypeContext(i).detail, /flow-valued/);
  i.fields.options = {
    availability: 'known',
    value: { remote_ip: '192.0.2.10' },
  };
  assert.doesNotMatch(nativeTypeContext(i).detail, /flow-valued/);
});

await test('patch navigation requires current reciprocal server evidence and three exact object kinds', () => {
  const i = item('patch');
  i.patch_peer = {
    availability: 'known',
    reason: 'PATCH_RECIPROCAL_CONFIGURATION',
    source: { ...fresh },
    peer_ref: { kind: 'interface', id: 'synthetic-interface' },
    peer_port_ref: { kind: 'port', id: 'synthetic-port' },
    peer_bridge_ref: { kind: 'bridge', id: 'synthetic-bridge' },
  };
  assert.equal(currentPatchPeer(i), true);
  assert.match(patchPeerReason(i), /does not prove packet forwarding/);
  for (const reason of [
    'PATCH_PEER_NOT_FOUND',
    'PATCH_PEER_NOT_RECIPROCAL',
    'PATCH_PEER_AMBIGUOUS',
    'PATCH_DATAPATH_MISMATCH',
    'PATCH_PEER_IDENTITY_UNAVAILABLE',
    'future-code',
  ]) {
    i.patch_peer.reason = reason;
    assert.equal(currentPatchPeer(i), false);
    assert.ok(patchPeerReason(i));
  }
  i.patch_peer.reason = 'PATCH_RECIPROCAL_CONFIGURATION';
  i.patch_peer.source.freshness = 'stale';
  assert.equal(currentPatchPeer(i), false);
  i.patch_peer.source = { ...fresh };
  i.fields.type.availability = 'withheld';
  assert.equal(currentPatchPeer(i), false);
  i.fields.type.availability = 'known';
  i.patch_peer.peer_ref.kind = 'port';
  assert.equal(currentPatchPeer(i), false);
});
