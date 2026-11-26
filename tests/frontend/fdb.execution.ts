import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import type {
  FdbEntryPage,
  FdbObservation,
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
type NativeRow = { port: string; vlan: number; mac: string; age: string };

const fixturePath = process.env.OVS_FDB_FRONTEND_FIXTURE;
if (!fixturePath)
  throw new Error('Requires the isolated native FDB browser fixture.');
const fixture: {
  origin: string;
  accounts: Record<string, string>;
  dbSocket: string;
  controlSocket: string;
  bridge: Bridge;
  otherBridge: Bridge;
  namespace: string;
  ports: FixturePort[];
  disabled: FdbEntryPage;
} = JSON.parse(readFileSync(fixturePath, 'utf8'));

const view = fixture.origin + '/visibility/fdb';
const api = fixture.origin + '/api/v1/fdb';
const scope = 'bridge=' + fixture.bridge.id;
const mac = '02:00:24:44:00:01';
const absentMAC = '02:00:24:44:ff:ff';
const exportName = 'ovs-webui-fdb-page.json';
const completeEmpty = 'No FDB entries match this scope.';
const observedEmpty =
  'No entries are available in the observed portion of this scope.';
const downloadName = 'Download this page as JSON';
const samplePoll = { intervals: [200, 500, 1000], timeout: 22000 };
const evidence = 'test-results/ovs-witness/fdb-evidence';
const sendFrames = [
  'import socket, sys, time',
  'sources = sys.stdin.read().splitlines()',
  'with socket.socket(socket.AF_PACKET, socket.SOCK_RAW) as output:',
  '    output.bind((sys.argv[1], 0))',
  '    for source in sources:',
  '        frame = bytes.fromhex("ffffffffffff" + source.replace(":", "") + "88b5") + bytes(46)',
  '        assert len(frame) == output.send(frame)',
  '        if len(sources) > 1:',
  '            time.sleep(0.004)',
].join('\n');

function emit(port: FixturePort, sources: string[]) {
  execFileSync(
    'ip',
    [
      'netns',
      'exec',
      fixture.namespace,
      'python3',
      '-c',
      sendFrames,
      port.peer,
    ],
    { input: sources.join('\n'), timeout: 7000 },
  );
}

function vsctl(...args: string[]) {
  return execFileSync(
    'ovs-vsctl',
    ['--timeout=5', '--db=unix:' + fixture.dbSocket, ...args],
    {
      encoding: 'utf8',
      timeout: 7000,
    },
  ).trim();
}

function nativeFDB(bridge: Bridge): NativeRow[] {
  const [header, ...rows] = execFileSync(
    'ovs-appctl',
    ['--timeout=5', '-t', fixture.controlSocket, 'fdb/show', bridge.name],
    { encoding: 'utf8', timeout: 7000 },
  )
    .trim()
    .split('\n');
  expect(
    header
      .trim()
      .split(/\s+/)
      .map((field) => field.toLowerCase()),
  ).toEqual(['port', 'vlan', 'mac', 'age']);
  return rows.map((row) => {
    const [port, vlan, address, age] = row.trim().split(/\s+/);
    return { port, vlan: Number(vlan), mac: address, age };
  });
}

async function login(page: Page, suffix = 'observer') {
  const account = 'browser-fdb-' + suffix;
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
  query = scope + '&limit=25',
): Promise<FdbEntryPage> {
  const response = await page.request.get(api + '?' + query);
  expect(response.status()).toBe(200);
  return response.json();
}

function observed(row: Resource): FdbObservation {
  if (!row.fdb)
    throw new Error('FDB resource is missing its native observation.');
  return row.fdb;
}

const table = (page: Page) =>
  page.getByRole('region', { name: 'Forwarding database', exact: true });
const exportButton = (page: Page) =>
  page.getByRole('button', { name: downloadName, exact: true });
const refresh = (page: Page) =>
  page.getByRole('button', { name: 'Refresh FDB', exact: true }).click();

async function capture(page: Page, state: string) {
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({
    path: evidence + '/fdb-' + state + '.png',
    fullPage: true,
  });
}

test('FDB compares isolated kernel OVS MAC learning with the scoped read-only browser view', async ({
  page,
}) => {
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      !['GET', 'HEAD'].includes(request.method()) &&
      /\/api\/v1\/(?:fdb|candidate|transactions)(?:[/?]|$)/.test(request.url())
    ) {
      writes.push(request.method());
    }
  });
  await login(page);
  expect(fixture.disabled).toMatchObject({
    availability: 'unavailable',
    reason: 'disabled',
    items: [],
  });
  const listRoute = /\/api\/v1\/fdb(?:\?|$)/;
  // Replay only the genuine default-off page captured before the isolated
  // manager was restarted with the qualified native FDB source.
  await page.route(listRoute, (route) =>
    route.fulfill({ status: 200, json: fixture.disabled }),
  );
  await page.goto(view + '?' + scope + '&limit=1');
  await expect(
    page.getByText('Native FDB observation is disabled.', { exact: true }),
  ).toBeVisible();
  await expect(table(page)).toHaveCount(0);
  await expect(exportButton(page)).toHaveCount(0);
  await capture(page, 'disabled');
  await page.unroute(listRoute);

  expect(nativeFDB(fixture.bridge)).toEqual([]);
  await expect
    .poll(async () => {
      const data = await readPage(page);
      return [
        data.availability,
        data.freshness,
        data.source,
        data.coverage?.bridges_sampled,
        data.items.length,
      ];
    }, samplePoll)
    .toEqual(['available', 'fresh', 'ovs_fdb', 1, 0]);
  await refresh(page);
  await expect(
    table(page).getByText('The observed native FDB is empty.', { exact: true }),
  ).toBeVisible();
  await capture(page, 'empty');

  const before = vsctl('get', 'Open_vSwitch', '.', 'next_cfg');
  const sentAt = Date.now();
  for (const port of fixture.ports) emit(port, [mac]);
  const scoped = fixture.ports.filter(
    (port) => port.bridge.id === fixture.bridge.id,
  );
  const expectedNative = scoped.map((port) => ({
    port: vsctl('get', 'Interface', port.name, 'ofport'),
    vlan: port.vlan,
    mac,
  }));
  await expect
    .poll(
      () => nativeFDB(fixture.bridge).filter((row) => row.mac === mac),
      samplePoll,
    )
    .toEqual(
      expect.arrayContaining(
        expectedNative.map((row) => expect.objectContaining(row)),
      ),
    );
  await expect
    .poll(
      () =>
        nativeFDB(fixture.otherBridge).filter((row) => row.mac === mac).length,
    )
    .toBe(1);
  expect(new Set(expectedNative.map((row) => row.port)).size).toBe(2);
  expect(
    expectedNative.every(
      (row) => /^\d+$/.test(row.port) && Number(row.port) > 0,
    ),
  ).toBe(true);

  await expect
    .poll(async () => {
      const data = await readPage(page, scope + '&mac=' + mac);
      return [data.availability, data.items.length];
    }, samplePoll)
    .toEqual(['available', 2]);
  const live = await readPage(page, scope + '&mac=' + mac);
  expect(live).toMatchObject({
    freshness: 'fresh',
    source: 'ovs_fdb',
    truncated: false,
    coverage: {
      bridges_sampled: 1,
      bridges_total: 1,
      entries_sampled: 2,
      entries_total: 2,
      truncated: false,
    },
  });
  expect(new Set(live.items.map((row) => row.id)).size).toBe(2);
  const observedAt = Date.parse(live.observed_at ?? '');
  expect(observedAt).toBeGreaterThanOrEqual(sentAt - 10);
  for (const port of scoped) {
    const row = live.items.find((entry) => observed(entry).vlan === port.vlan);
    if (!row) throw new Error('Missing independently learned VLAN entry.');
    const native = expectedNative.find((entry) => entry.vlan === port.vlan);
    expect(row).toMatchObject({
      resource_kind: 'fdb_entry',
      state: 'learned',
      allowed_actions: [],
      source: {
        provider_id: 'ovs-vswitchd',
        authority: 'native-ovs-fdb',
        freshness: 'fresh',
      },
      fdb: {
        mac,
        vlan: port.vlan,
        ofport: Number(native?.port),
        port_name: port.name,
        bridge_name: fixture.bridge.name,
        port_resolution: 'resolved',
        bridge_ref: { id: fixture.bridge.id, kind: 'bridge' },
        port_ref: { id: port.id, kind: 'port' },
      },
    });
    const details = observed(row);
    expect(details.age_seconds).toBeGreaterThanOrEqual(0);
    expect(Date.parse(details.last_seen_at ?? '')).toBe(
      observedAt - Number(details.age_seconds) * 1000,
    );
    const filtered = await readPage(
      page,
      scope + '&vlan=' + port.vlan + '&port=' + port.id,
    );
    expect(filtered.items.map((entry) => entry.id)).toEqual([row.id]);
    const detail = await page.request.get(api + '/' + row.id);
    expect(detail.status()).toBe(200);
    expect(observed(await detail.json())).toMatchObject({
      mac,
      vlan: port.vlan,
      port_ref: { id: port.id },
    });
  }
  const all = await readPage(page, 'mac=' + mac);
  expect(all.items.map((row) => row.id).sort()).toEqual(
    live.items.map((row) => row.id).sort(),
  );
  const outside = await readPage(
    page,
    'bridge=' + fixture.otherBridge.id + '&mac=' + mac,
  );
  expect(outside).toMatchObject({
    availability: 'unavailable',
    reason: 'no_managed_bridges',
    items: [],
  });

  await page.goto(view + '?' + scope + '&mac=' + mac + '&limit=1');
  const rowLink = table(page).getByRole('link', { name: mac, exact: true });
  await expect(rowLink).toHaveCount(1);
  const firstHref = await rowLink.getAttribute('href');
  const next = page.getByRole('link', { name: 'Next FDB page', exact: true });
  await expect(next).toBeVisible();
  await capture(page, 'standard');
  await next.click();
  await expect(rowLink).toBeVisible();
  expect(await rowLink.getAttribute('href')).not.toBe(firstHref);
  await expect(next).toHaveCount(0);
  const download = page.waitForEvent('download');
  await exportButton(page).click();
  const file = await download;
  expect(file.suggestedFilename()).toBe(exportName);
  const path = await file.path();
  if (!path) throw new Error('Expected the current-page native FDB export.');
  const exported: {
    page: FdbEntryPage;
    query: Record<string, string>;
    exported_at: string;
  } = JSON.parse(readFileSync(path, 'utf8'));
  expect(exported.query).toMatchObject({
    mac,
    bridge: fixture.bridge.id,
    limit: '1',
    cursor: expect.any(String),
  });
  expect(Date.parse(exported.exported_at)).toBeGreaterThan(0);
  expect(exported.page).toMatchObject({
    source: 'ovs_fdb',
    truncated: false,
    next_cursor: null,
  });
  expect(exported.page.items).toHaveLength(1);
  await expect(rowLink).toHaveAttribute(
    'href',
    new RegExp('/visibility/fdb/' + exported.page.items[0].id),
  );

  await rowLink.focus();
  await expect(rowLink).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('heading', { name: mac, exact: true }),
  ).toBeVisible();
  const links = page.getByRole('region', {
    name: 'FDB Port scope',
    exact: true,
  });
  const chosen = observed(exported.page.items[0]);
  await expect(
    links.locator('a[href="/bridges/' + fixture.bridge.id + '"]'),
  ).toBeVisible();
  await expect(
    links.locator('a[href="/ports/' + chosen.port_ref?.id + '"]'),
  ).toBeVisible();
  await expect(
    page.getByText('Last seen estimate', { exact: true }),
  ).toBeVisible();
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page.getByText('FDB identity and source', { exact: true }).click();
  await expect(
    page.getByText(exported.page.items[0].id, { exact: true }),
  ).toBeVisible();
  await capture(page, 'expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(
      page.getByRole('heading', { name: mac, exact: true }),
    ).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await expect(
      page.getByRole('button', {
        name: /stage|apply now|configure|apply configuration/i,
      }),
    ).toHaveCount(0);
  }
  await capture(page, 'mobile');

  await page.goto(view + '?' + scope);
  const filters = page.getByRole('form', { name: 'FDB filters', exact: true });
  await filters.getByLabel('MAC', { exact: true }).fill(absentMAC);
  await filters
    .getByRole('button', { name: 'Apply filter', exact: true })
    .click();
  await expect(
    table(page).getByText(completeEmpty, { exact: true }),
  ).toBeVisible();
  expect(vsctl('get', 'Open_vSwitch', '.', 'next_cfg')).toBe(before);
  expect(writes).toEqual([]);
});

test('FDB reports native truncation and uncovered Bridges without claiming a complete empty result', async ({
  page,
}) => {
  await login(page);
  const port = fixture.ports.find(
    (link) => link.bridge.id === fixture.bridge.id,
  );
  if (!port) throw new Error('Missing isolated OVS FDB fixture Port.');
  const oldMAC = '02:00:24:44:ff:01';
  emit(port, [oldMAC]);
  let oldID = '';
  await expect
    .poll(async () => {
      const data = await readPage(page, scope + '&mac=' + oldMAC);
      oldID = data.items[0]?.id ?? '';
      return oldID;
    }, samplePoll)
    .not.toBe('');

  const many = Array.from({ length: 257 }, (_, index) => {
    const suffix = (index + 1).toString(16).padStart(4, '0');
    return '02:00:24:45:' + suffix.slice(0, 2) + ':' + suffix.slice(2);
  });
  emit(port, many);
  await expect
    .poll(
      () =>
        nativeFDB(fixture.bridge).filter((row) => many.includes(row.mac))
          .length,
      samplePoll,
    )
    .toBe(many.length);
  const nativeTotal = nativeFDB(fixture.bridge).length;
  await expect
    .poll(async () => {
      const current = await readPage(page, scope + '&limit=50');
      return [
        current.availability,
        current.reason,
        current.coverage?.entries_total,
        current.coverage?.entries_sampled,
      ];
    }, samplePoll)
    .toEqual(['degraded', 'truncated', nativeTotal, 256]);
  const truncated = await readPage(page, scope + '&limit=50');
  expect(truncated.truncated).toBe(true);
  expect(truncated.coverage?.truncated).toBe(true);
  expect(truncated.items).toHaveLength(50);
  expect(truncated.next_cursor).toEqual(expect.any(String));

  await page.goto(view + '?' + scope + '&mac=' + absentMAC + '&limit=1');
  await expect(
    table(page).getByText(observedEmpty, { exact: true }),
  ).toBeVisible();
  await expect(
    table(page).getByText(completeEmpty, { exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText(
      'The native FDB collection reached its entry limit. Some entries are omitted from this snapshot.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(exportButton(page)).toBeVisible();
  await capture(page, 'truncated');

  // The native table still contains the peer. A capped observation omitted
  // it, so detail must withhold it without claiming that it has disappeared.
  expect(nativeFDB(fixture.bridge).some((row) => row.mac === oldMAC)).toBe(
    true,
  );
  const detail = await page.request.get(api + '/' + oldID);
  expect(detail.status()).toBe(503);
  expect((await detail.json()).code).toMatch(/^FDB_(PARTIAL|TRUNCATED)$/);
  await page.goto(view + '/' + oldID + '?' + scope);
  await expect(
    page.getByRole('heading', { name: 'FDB entry', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(
      /Some Bridges or native FDB entries could not be observed|The native FDB collection reached its entry limit/,
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'FDB Port scope', exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText('This FDB entry is no longer in the current observation.', {
      exact: false,
    }),
  ).toHaveCount(0);

  await page.goto(view + '?bridge=' + fixture.otherBridge.id + '&limit=1');
  await expect(
    page.getByText('No managed Bridges could be verified for this FDB scope.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(table(page)).toHaveCount(0);
  await expect(exportButton(page)).toHaveCount(0);
  await capture(page, 'unavailable');
});

test('FDB requires inventory and state authority in Standard and Expert', async ({
  page,
}) => {
  for (const role of ['inventory-only', 'state-only']) {
    await page.context().clearCookies();
    await login(page, role);
    await page.goto(view + '?' + scope);
    const forbidden = await page.request.get(api + '?' + scope);
    expect(forbidden.status()).toBe(403);
    expect((await forbidden.json()).code).toBe('CAPABILITY_DENIED');
    await expect(
      page.getByText('Permission denied', { exact: true }),
    ).toBeVisible();
    await expect(table(page)).toHaveCount(0);
    await expect(exportButton(page)).toHaveCount(0);
  }
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Expert', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await expect(table(page)).toHaveCount(0);
  await expect(exportButton(page)).toHaveCount(0);
  await capture(page, 'withheld');
});
