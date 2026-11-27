import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import type {
  EndpointObservation,
  EndpointPage,
  EndpointSourceCoverage,
  Resource,
} from '../../clients/typescript/public-v1.generated';

type Bridge = { id: string; name: string };
type FixturePort = {
  id: string;
  name: string;
  peer: string;
  bridge: Bridge;
  vlan: number;
};
type Observation = Resource & { endpoint: EndpointObservation };

const fixturePath = process.env.OVS_ENDPOINTS_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated native Endpoints fixture.');
const fixture: {
  origin: string;
  accounts: Record<string, string>;
  dbSocket: string;
  controlSocket: string;
  bridge: Bridge;
  otherBridge: Bridge;
  namespace: string;
  ports: FixturePort[];
  lldpInterface: string;
  l3: { id: string; port: string; interface: string };
  address: { mac: string; ipv4: string; ipv6: string };
} = JSON.parse(readFileSync(fixturePath, 'utf8'));

const viewPath = '/visibility/endpoints';
const view = fixture.origin + viewPath;
const api = fixture.origin + '/api/v1/endpoints';
const scope = 'bridge=' + fixture.bridge.id;
const { mac, ipv4, ipv6 } = fixture.address;
const lldpPorts = ['isolated-peer-a', 'isolated-peer-b'];
const remoteSystem = 'isolated-endpoint-peer';
const samplePoll = { intervals: [500, 1000, 1500], timeout: 26000 };
const evidence = 'test-results/ovs-witness/endpoints-evidence';
const sender = Buffer.from(mac.replaceAll(':', ''), 'hex');
const sendFrame = [
  'import socket, sys',
  'frame = bytes.fromhex(sys.argv[2])',
  'with socket.socket(socket.AF_PACKET, socket.SOCK_RAW) as output:',
  '    output.bind((sys.argv[1], 0))',
  '    assert output.send(frame) == len(frame)',
].join('\n');

function run(program: string, ...args: string[]) {
  return execFileSync(program, args, {
    encoding: 'utf8',
    timeout: 7000,
  }).trim();
}

function vsctl(...args: string[]) {
  return run(
    'ovs-vsctl',
    '--timeout=5',
    '--db=unix:' + fixture.dbSocket,
    ...args,
  );
}

function emit(port: FixturePort, frame: Buffer) {
  run(
    'ip',
    'netns',
    'exec',
    fixture.namespace,
    'python3',
    '-c',
    sendFrame,
    port.peer,
    frame.toString('hex'),
  );
}

function tlv(type: number, value: Buffer) {
  const header = Buffer.alloc(2);
  header.writeUInt16BE((type << 9) | value.length);
  return Buffer.concat([header, value]);
}

function advertise(port: FixturePort, remotePort: string) {
  const packet = Buffer.concat([
    Buffer.from('0180c200000e', 'hex'),
    sender,
    Buffer.from('88cc', 'hex'),
    tlv(1, Buffer.concat([Buffer.from([4]), sender])),
    tlv(2, Buffer.concat([Buffer.from([5]), Buffer.from(remotePort)])),
    tlv(3, Buffer.from([0, 120])),
    tlv(5, Buffer.from(remoteSystem)),
    tlv(0, Buffer.alloc(0)),
  ]);
  emit(
    port,
    Buffer.concat([packet, Buffer.alloc(Math.max(0, 60 - packet.length))]),
  );
}

function nativeFDB(bridge: Bridge) {
  const [header, ...rows] = run(
    'ovs-appctl',
    '--timeout=5',
    '-t',
    fixture.controlSocket,
    'fdb/show',
    bridge.name,
  ).split('\n');
  expect(header.trim().toLowerCase().split(/\s+/)).toEqual([
    'port',
    'vlan',
    'mac',
    'age',
  ]);
  return rows.map((row) => {
    const [ofport, vlan, address] = row.trim().split(/\s+/);
    return { ofport, vlan: Number(vlan), mac: address };
  });
}

async function login(page: Page, role: string) {
  const account = 'browser-fdb-' + role;
  expect((await page.goto(view))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const reply = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
}

async function readPage(
  page: Page,
  query = scope + '&limit=50',
): Promise<EndpointPage> {
  const response = await page.request.get(api + '?' + query);
  expect(response.status()).toBe(200);
  return response.json();
}

function observation(row: Resource): Observation {
  if (!row.endpoint) throw new Error('Missing endpoint observation');
  expect(row.resource_kind).toBe('endpoint');
  expect(row.state).toBe('observed');
  expect(row.allowed_actions).toEqual([]);
  return { ...row, endpoint: row.endpoint };
}

function source(data: EndpointPage, kind: EndpointSourceCoverage['kind']) {
  const coverage = data.sources?.find((item) => item.kind === kind);
  if (!coverage) throw new Error('Missing independent endpoint source ' + kind);
  return coverage;
}

const list = (page: Page) =>
  page.getByRole('region', { name: 'Endpoint observations', exact: true });
const coverage = (page: Page) =>
  page.getByRole('region', { name: 'Source coverage', exact: true });
const detailScope = (page: Page) =>
  page.getByRole('region', { name: 'Endpoint scope', exact: true });
const detailEvidence = (page: Page) =>
  page.getByRole('region', { name: 'Endpoint evidence', exact: true });

async function capture(page: Page, name: string) {
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({
    path: evidence + '/endpoints-' + name + '.png',
    fullPage: true,
  });
}

test('Endpoints correlate only verified scope with native FDB, ARP/NDP and LLDP evidence', async ({
  page,
}) => {
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      !['GET', 'HEAD'].includes(request.method()) &&
      /\/api\/v1\/(?:endpoints|fdb|neighbors|lldp|candidate|transactions)(?:[/?]|$)/.test(
        request.url(),
      )
    ) {
      writes.push(request.method());
    }
  });
  await login(page, 'observer');
  const before = vsctl('get', 'Open_vSwitch', '.', 'next_cfg');

  const learning = Buffer.concat([
    Buffer.from('ffffffffffff', 'hex'),
    sender,
    Buffer.from('88b5', 'hex'),
    Buffer.alloc(46),
  ]);
  for (const port of fixture.ports) emit(port, learning);
  run('ping', '-4', '-I', fixture.l3.interface, '-c', '1', '-W', '2', ipv4);
  run('ping', '-6', '-I', fixture.l3.interface, '-c', '1', '-W', '2', ipv6);

  const neighborAddresses = [
    ['-4', ipv4],
    ['-6', ipv6],
  ];
  for (const [family, destination] of neighborAddresses) {
    const native: { dst: string; lladdr?: string }[] = JSON.parse(
      run('ip', '-j', family, 'neigh', 'show', 'dev', fixture.l3.interface),
    );
    expect(native).toContainEqual(
      expect.objectContaining({ dst: destination, lladdr: mac }),
    );
  }

  for (const port of fixture.ports) {
    const ofport = vsctl('get', 'Interface', port.name, 'ofport');
    await expect
      .poll(() =>
        nativeFDB(port.bridge).some(
          (row) =>
            row.ofport === ofport && row.mac === mac && row.vlan === port.vlan,
        ),
      )
      .toBe(true);
  }

  const neighborStates = (endpoint?: EndpointObservation) =>
    neighborAddresses.map(
      ([, ip]) =>
        endpoint?.evidence.find(
          (item) =>
            item.ref.kind === 'neighbor' && item.ip === ip && item.mac === mac,
        )?.state,
    );

  // A LLDP advertised chassis may match all three learned MAC observations and
  // both L3 records. Neither the protocol ID nor a shared Bridge is a join key.
  let learnedNeighborID: string | undefined;
  await expect
    .poll(async () => {
      const data = await readPage(page);
      const rows = data.items.map(observation);
      const observed = rows.filter(
        (row) =>
          row.endpoint.basis === 'lldp' &&
          row.endpoint.evidence.some(
            (item) => item.system_name === remoteSystem,
          ),
      );
      if (observed.length !== lldpPorts.length) {
        for (const remotePort of lldpPorts)
          advertise(fixture.ports[0], remotePort);
      }
      learnedNeighborID = rows.find(
        (row) =>
          row.endpoint.basis === 'neighbor' &&
          row.endpoint.mac === mac &&
          neighborStates(row.endpoint).every(
            (state) => state && state !== 'permanent',
          ),
      )?.id;
      return {
        fdb: rows.filter(
          (row) => row.endpoint.basis === 'fdb' && row.endpoint.mac === mac,
        ).length,
        neighbor: learnedNeighborID !== undefined,
        lldp: observed.length,
      };
    }, samplePoll)
    .toEqual({ fdb: 2, neighbor: true, lldp: lldpPorts.length });

  // The dynamic entries already proved native learning. Keep just those two
  // entries stable while testing direct reads and snapshot-bound navigation.
  for (const [family, destination] of neighborAddresses) {
    run(
      'ip',
      family,
      'neigh',
      'replace',
      destination,
      'lladdr',
      mac,
      'nud',
      'permanent',
      'dev',
      fixture.l3.interface,
    );
  }
  await expect
    .poll(async () => {
      const data = await readPage(page);
      const neighbor = data.items.find(
        (row) => row.id === learnedNeighborID,
      )?.endpoint;
      const { authority, freshness } = source(data, 'neighbor').source;
      return { authority, freshness, states: neighborStates(neighbor) };
    }, samplePoll)
    .toEqual({
      authority: 'linux-rtnetlink-neighbors',
      freshness: 'fresh',
      states: ['permanent', 'permanent'],
    });

  const main = await readPage(page);
  const rows = main.items.map(observation);
  expect(main.freshness).toBe('fresh');
  expect(main.truncated).toBe(false);
  for (const [kind, authority] of [
    ['fdb', 'native-ovs-fdb'],
    ['neighbor', 'linux-rtnetlink-neighbors'],
    ['lldp', 'linux-passive-lldp'],
  ] as const) {
    expect(source(main, kind).source).toMatchObject({
      authority,
      freshness: 'fresh',
    });
    expect(source(main, kind).observed).toBeGreaterThan(0);
  }

  const fdb = rows
    .filter((row) => row.endpoint.basis === 'fdb' && row.endpoint.mac === mac)
    .sort((a, b) => (a.endpoint.vlan ?? -1) - (b.endpoint.vlan ?? -1));
  expect(new Set(fdb.map((row) => row.id)).size).toBe(2);
  expect(fdb.map((row) => row.endpoint.vlan)).toEqual([101, 202]);
  for (const row of fdb) {
    const port = fixture.ports.find(
      (port) =>
        port.bridge.id === fixture.bridge.id && port.vlan === row.endpoint.vlan,
    );
    expect(row.endpoint).toMatchObject({
      addresses: [],
      bridge_ref: { kind: 'bridge', id: fixture.bridge.id },
      port_ref: { kind: 'port', id: port?.id },
      evidence_total: 1,
      evidence: [{ ref: { kind: 'fdb_entry' }, mac, vlan: row.endpoint.vlan }],
    });
    expect(row.endpoint.interface_ref).toBeUndefined();
  }

  const other = await readPage(
    page,
    'bridge=' + fixture.otherBridge.id + '&mac=' + mac,
  );
  const alternate = other.items.map(observation);
  expect(alternate).toHaveLength(1);
  expect(alternate[0].endpoint).toMatchObject({
    basis: 'fdb',
    mac,
    vlan: 101,
    bridge_ref: { kind: 'bridge', id: fixture.otherBridge.id },
    port_ref: { kind: 'port', id: fixture.ports[2].id },
    addresses: [],
  });
  expect(fdb.map((row) => row.id)).not.toContain(alternate[0].id);

  const neighbors = rows.filter(
    (row) => row.endpoint.basis === 'neighbor' && row.endpoint.mac === mac,
  );
  expect(neighbors).toHaveLength(1);
  const neighbor = neighbors[0];
  expect(neighbor.endpoint).toMatchObject({
    addresses: expect.arrayContaining([ipv4, ipv6]),
    bridge_ref: { kind: 'bridge', id: fixture.bridge.id },
    port_ref: { kind: 'port', id: fixture.l3.port },
    interface_ref: { kind: 'interface', id: fixture.l3.id },
    evidence: expect.arrayContaining([
      expect.objectContaining({
        ref: expect.objectContaining({ kind: 'neighbor' }),
        family: 'ipv4',
        ip: ipv4,
        mac,
      }),
      expect.objectContaining({
        ref: expect.objectContaining({ kind: 'neighbor' }),
        family: 'ipv6',
        ip: ipv6,
        mac,
      }),
    ]),
  });
  expect(neighbor.endpoint.vlan).toBeUndefined();

  const advertisements = rows.filter(
    (row) =>
      row.endpoint.basis === 'lldp' &&
      row.endpoint.evidence.some((item) => item.system_name === remoteSystem),
  );
  expect(new Set(advertisements.map((row) => row.id)).size).toBe(
    lldpPorts.length,
  );
  expect(
    advertisements
      .flatMap((row) =>
        row.endpoint.evidence.map((item) => item.lldp_port_id?.value),
      )
      .sort((a, b) => (a ?? '').localeCompare(b ?? '')),
  ).toEqual(lldpPorts);
  for (const row of advertisements) {
    expect(row.endpoint).toMatchObject({
      addresses: [],
      bridge_ref: { kind: 'bridge', id: fixture.bridge.id },
      port_ref: { kind: 'port', id: fixture.ports[0].id },
      interface_ref: { kind: 'interface', id: fixture.lldpInterface },
      evidence_total: 1,
      evidence: [
        {
          ref: { kind: 'lldp_neighbor' },
          chassis_id: { subtype: 4, value: mac },
        },
      ],
    });
    expect(row.endpoint.mac).toBeUndefined();
    expect(row.endpoint.vlan).toBeUndefined();
  }

  const byIP = await readPage(page, scope + '&ip=' + encodeURIComponent(ipv6));
  expect(byIP.items.map((item) => item.id)).toEqual([neighbor.id]);
  expect(source(byIP, 'fdb').scope_reason).toBe('IP_NOT_REPORTED');
  expect(source(byIP, 'lldp').scope_reason).toBe('IP_NOT_REPORTED');

  const byPort = await readPage(
    page,
    'port=' + fixture.l3.port + '&mac=' + mac,
  );
  expect(byPort.items.map((item) => item.id)).toEqual([neighbor.id]);
  expect(
    source(await readPage(page, scope + '&mac=' + mac), 'lldp').scope_reason,
  ).toBe('MAC_NOT_VERIFIED');
  const byVLAN = await readPage(page, scope + '&vlan=101');
  expect(
    byVLAN.items
      .map(observation)
      .every(
        (row) => row.endpoint.basis === 'fdb' && row.endpoint.vlan === 101,
      ),
  ).toBe(true);
  expect(byVLAN.items.map((item) => item.id)).toContain(fdb[0].id);
  for (const kind of ['neighbor', 'lldp'] as const) {
    expect(source(byVLAN, kind).scope_reason).toBe('VLAN_NOT_REPORTED');
  }

  await page.goto(view + '?' + scope + '&mac=' + mac + '&limit=1');
  const first = list(page).getByRole('link', { name: mac, exact: true });
  await expect(first).toHaveCount(1);
  const firstHref = await first.getAttribute('href');
  await page.getByRole('link', { name: 'Next page', exact: true }).click();
  await expect
    .poll(() => new URL(page.url()).searchParams.has('cursor'))
    .toBe(true);
  await expect(first).toHaveCount(1);
  await expect(first).not.toHaveAttribute('href', firstHref ?? '');
  await expect(
    page.getByRole('link', { name: 'First page', exact: true }),
  ).toBeVisible();

  await page.goto(view + '?' + scope);
  for (const name of ['FDB', 'ARP/NDP', 'LLDP']) {
    await expect(
      coverage(page).getByRole('heading', { name, exact: true }),
    ).toBeVisible();
  }
  const filters = page.getByRole('form', {
    name: 'Endpoint filters',
    exact: true,
  });
  await expect(filters.getByLabel('Bridge', { exact: true })).toHaveValue(
    fixture.bridge.id,
  );
  await filters.getByLabel('MAC', { exact: true }).fill(mac);
  await filters
    .getByRole('button', { name: 'Apply filters', exact: true })
    .click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('mac'))
    .toBe(mac);
  await expect(
    list(page).getByRole('link', { name: mac, exact: true }),
  ).toHaveCount(3);
  await capture(page, 'standard');

  const link = list(page).locator(
    'a[href^="' + viewPath + '/' + neighbor.id + '"]',
  );
  await link.focus();
  await expect(link).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('heading', { name: mac, exact: true }),
  ).toBeVisible();
  for (const [path, id] of [
    ['/bridges/', fixture.bridge.id],
    ['/ports/', fixture.l3.port],
    ['/interfaces/', fixture.l3.id],
  ]) {
    await expect(
      detailScope(page).locator('a[href="' + path + id + '"]'),
    ).toBeVisible();
  }
  for (const item of neighbor.endpoint.evidence.filter(
    (item) => item.ip === ipv4 || item.ip === ipv6,
  )) {
    await expect(
      detailEvidence(page).locator(
        'a[href="/visibility/neighbors/' + item.ref.id + '"]',
      ),
    ).toBeVisible();
  }
  expect((await page.request.get(api + '/' + neighbor.id)).status()).toBe(200);

  const advertised = advertisements[0];
  await page.goto(view + '/' + advertised.id + '?' + scope);
  await expect(
    page.getByRole('heading', { name: remoteSystem, exact: true }),
  ).toBeVisible();
  await expect(
    detailEvidence(page).getByRole('link', {
      name: 'LLDP evidence',
      exact: true,
    }),
  ).toHaveAttribute(
    'href',
    '/visibility/lldp/' + advertised.endpoint.evidence[0].ref.id,
  );
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    detailEvidence(page).getByText('linux-passive-lldp', { exact: true }),
  ).toBeVisible();
  await capture(page, 'expert');

  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(390);
  await expect(
    detailEvidence(page).getByRole('link', {
      name: 'LLDP evidence',
      exact: true,
    }),
  ).toBeVisible();
  await capture(page, 'mobile');

  await page.goto(view + '?' + scope + '&vlan=101');
  await expect(
    coverage(page).getByText('This source does not report a verified VLAN.', {
      exact: true,
    }),
  ).toHaveCount(2);
  await expect(
    list(page).getByRole('link', { name: remoteSystem, exact: true }),
  ).toHaveCount(0);
  await capture(page, 'unproven-vlan');
  expect(vsctl('get', 'Open_vSwitch', '.', 'next_cfg')).toBe(before);
  expect(writes).toEqual([]);
});

test('Endpoints require inventory and state authority in Standard and Expert', async ({
  page,
}) => {
  for (const role of ['inventory-only', 'state-only']) {
    await page.context().clearCookies();
    await login(page, role);
    await page.goto(view + '?' + scope);
    const forbidden = await page.request.get(api + '?' + scope);
    expect(forbidden.status()).toBe(403);
    expect((await forbidden.json()).code).toBe('CAPABILITY_DENIED');
    expect(
      (
        await page.request.get(api + '/535a03ec-708a-4580-8abc-54558eaa9a4d')
      ).status(),
    ).toBe(403);
    await expect(
      page.getByText('Permission denied', { exact: true }),
    ).toBeVisible();
    await expect(list(page)).toHaveCount(0);
    await expect(coverage(page)).toHaveCount(0);
  }
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Expert', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await expect(list(page)).toHaveCount(0);
  await expect(coverage(page)).toHaveCount(0);
  await capture(page, 'permission');
});
