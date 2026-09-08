import test from 'node:test';
import assert from 'node:assert/strict';
import {
  inventoryMetadata,
  inventoryBridges,
  inventoryPorts,
  inventoryInterfaces,
  attachmentCandidates,
  objectReference,
  resolveInventoryObject,
  objectHash,
  parseObjectHash,
} from '../lib/inventory-model.ts';
import { ports, corePorts } from '../lib/ovs-model.ts';
import {
  bridges,
  bonds,
  bridgeChildren,
  memberOptions,
  currentBridgeChildren,
} from '../lib/switching-model.ts';
import { diagnosticResult } from '../lib/diagnostics-model.ts';

void test('one instance snapshot has unique native identities and exactly one owning Port per Interface', () => {
  const all = [...inventoryBridges, ...inventoryPorts, ...inventoryInterfaces];
  assert.equal(new Set(all.map((item) => item.uuid)).size, all.length);
  for (const items of [inventoryBridges, inventoryPorts, inventoryInterfaces]) {
    assert.equal(new Set(items.map((item) => item.name)).size, items.length);
    for (const item of items)
      assert.match(
        item.uuid,
        /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-8[a-f0-9]{3}-[a-f0-9]{12}$/,
      );
  }
  for (const port of inventoryPorts) {
    assert.ok(inventoryBridges.some((bridge) => bridge.name === port.bridge));
    for (const name of port.interfaces) {
      const owners = inventoryPorts.filter((item) =>
        item.interfaces.includes(name),
      );
      assert.equal(owners.length, 1, `Ambiguous Interface owner: ${name}`);
      const native = inventoryInterfaces.find((item) => item.name === name);
      assert.equal(native.port, port.name);
      assert.equal(native.bridge, port.bridge);
    }
  }
});

void test('Ports, Bridge children and Bond details project the same identity and member set', () => {
  assert.equal(ports.length, 10);
  assert.equal(inventoryInterfaces.length, 13);
  assert.equal(
    bridges.reduce((count, bridge) => count + bridge.ports, 0),
    ports.length,
  );
  assert.equal(
    bridges.reduce((count, bridge) => count + bridge.interfaces, 0),
    inventoryInterfaces.length,
  );
  for (const port of ports) {
    const record = inventoryPorts.find((item) => item.uuid === port.uuid);
    assert.equal(port.bridge, record.bridge);
    assert.equal(port.interfaceName, record.interfaces.join(', '));
    const child = bridgeChildren[port.bridge].find(
      (item) => item.name === port.name,
    );
    assert.equal(child.interfaces, port.interfaceName);
    const bond = bonds.find((item) => item.name === port.name);
    if (bond) {
      assert.equal(bond.uuid, port.uuid);
      assert.equal(bond.bridge, port.bridge);
      assert.deepEqual(bond.members, port.members);
    }
  }
  assert.deepEqual(
    corePorts.map((port) => port.name),
    ['uplink-01', 'server-07', 'server-08', 'bond-storage', 'rep0', 'mgmt0'],
  );
});

void test('a confirmed VLAN value is shared with Bridge children without rewriting the captured relationship snapshot', () => {
  const live = {
    'server-07': { mode: 'trunk', tag: null, trunks: '120, 240' },
  };
  const projected = currentBridgeChildren('br-fabric', live).find(
    (item) => item.name === 'server-07',
  );
  assert.equal(projected.vlan, 'Trunk · 120, 240');
  assert.equal(projected.interfaces, 'enp129s0f1');
  assert.equal(
    bridgeChildren['br-fabric'].find((item) => item.name === 'server-07').vlan,
    'Access · 120',
  );
});

void test('host attachment candidates do not masquerade as native OVS Interface rows or steal existing members', () => {
  for (const candidate of attachmentCandidates) {
    assert.ok(
      !inventoryInterfaces.some((item) => item.name === candidate.name),
    );
    assert.equal(
      resolveInventoryObject(`Interface/${candidate.name}`).status,
      'unavailable',
    );
  }
  for (const bridge of bridges) {
    const options = memberOptions(bridge.name);
    for (const option of options.filter((item) => item.available))
      assert.ok(
        !inventoryInterfaces.some((native) => native.name === option.name),
      );
  }
  for (const bond of bonds.filter((item) => item.scope === 'Manage')) {
    const options = memberOptions(bond.bridge, bond);
    assert.ok(
      bond.members.every((name) =>
        options.some(
          (item) =>
            item.name === name && item.owner === bond.name && item.available,
        ),
      ),
    );
  }
});

void test('diagnostic member findings come from the same Bond membership and provider native type remains unknown', () => {
  const result = diagnosticResult(
    {
      id: 'diag.net.link-lacp',
      scope: 'Port/bond-storage',
      sampleSeconds: 5,
      detail: 'bounded',
    },
    'complete',
  );
  const storage = bonds.find((item) => item.name === 'bond-storage');
  for (const member of storage.members) {
    assert.ok(result.finding.includes(member));
    assert.ok(result.raw.includes(`member=${member}`));
  }
  assert.equal(
    inventoryInterfaces.find((item) => item.name === 'pf0hpf').nativeType,
    'Unknown',
  );
});

void test('stable URLs resolve exact native identity independent of the current selection', () => {
  for (const [kind, items] of Object.entries({
    Bridge: inventoryBridges,
    Port: inventoryPorts,
    Interface: inventoryInterfaces,
  })) {
    for (const item of items) {
      const reference = objectReference(kind, item);
      const parsed = parseObjectHash(objectHash(reference));
      assert.deepEqual(parsed.target, reference);
      assert.equal(
        resolveInventoryObject(parsed.target).object.uuid,
        item.uuid,
      );
      assert.equal(
        resolveInventoryObject(`${kind}/${item.name}`).object.uuid,
        item.uuid,
      );
    }
  }
});

void test('missing, malformed, foreign-instance and old-generation references cannot select another object', () => {
  const reference = objectReference('Port', inventoryPorts[3]);
  for (const input of [
    'Port/bond-created-but-not-observed',
    'Port/bond-storage/extra',
    'Bond/bond-storage',
    { ...reference, instance: 'other-node' },
    { ...reference, generation: inventoryMetadata.generation + 1 },
    { ...reference, kind: 'Bridge' },
    { ...reference, id: 'bond-storage' },
  ])
    assert.equal(resolveInventoryObject(input).status, 'unavailable');
  for (const hash of [
    '#object/%ZZ/Port/id?generation=1842',
    '#object/node/Job/id?generation=1842',
    '#object/node/Port/id?generation=NaN',
    '#object/node/Port/id?facet=port',
    '#object/node/Port/id?generation=1842&facet=unsafe',
  ])
    assert.equal(parseObjectHash(hash), null);
});
