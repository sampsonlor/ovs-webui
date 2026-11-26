import { expect, test, type Page } from '@playwright/test';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import type {
  LldpNeighborPage,
  Resource,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
type Port = {
  interface: string;
  peer: string;
  id: string;
  portID: string;
  bridgeID: string;
};
const fixture: {
  origin: string;
  dbSocket: string;
  accounts: Record<string, string>;
  lldp: {
    bridge: string;
    namespace: string;
    capture: Port;
    excluded: Port;
    disabled: LldpNeighborPage;
  };
} = JSON.parse(readFileSync(fixturePath, 'utf8'));
const href = fixture.origin + '/visibility/lldp';
const capturePort = fixture.lldp.capture;
const scope = `interface_id=${capturePort.id}&port_id=${capturePort.portID}&bridge_id=${capturePort.bridgeID}`;
const sourceMAC = '02:00:25:44:00:01';
const remotePort = 'lab-uplink-1';
const system = 'isolated-lldp-peer';

const sendEthernet = `import socket, sys
frame = bytes.fromhex(sys.argv[2])
with socket.socket(socket.AF_PACKET, socket.SOCK_RAW) as output:
    output.bind((sys.argv[1], 0))
    assert output.send(frame) == len(frame)`;

function tlv(type: number, value: Buffer) {
  const header = Buffer.alloc(2);
  header.writeUInt16BE((type << 9) | value.length);
  return Buffer.concat([header, value]);
}

function advertisement(port: Port, mac: string, name: string, ttl: number) {
  const sender = Buffer.from(mac.replaceAll(':', ''), 'hex');
  const ttlField = Buffer.alloc(2);
  ttlField.writeUInt16BE(ttl);
  const frame = Buffer.concat([
    Buffer.from('0180c200000e', 'hex'),
    sender,
    Buffer.from('88cc', 'hex'),
    tlv(1, Buffer.concat([Buffer.from([4]), sender])),
    tlv(2, Buffer.concat([Buffer.from([5]), Buffer.from(remotePort)])),
    tlv(3, ttlField),
    tlv(5, Buffer.from(name)),
    tlv(0, Buffer.alloc(0)),
  ]);
  const ethernet =
    frame.length < 60
      ? Buffer.concat([frame, Buffer.alloc(60 - frame.length)])
      : frame;
  execFileSync(
    'ip',
    [
      'netns',
      'exec',
      fixture.lldp.namespace,
      'python3',
      '-c',
      sendEthernet,
      port.peer,
      ethernet.toString('hex'),
    ],
    { timeout: 7000 },
  );
}

function nativeLLDP() {
  return execFileSync(
    'ovs-vsctl',
    [
      '--timeout=5',
      `--db=unix:${fixture.dbSocket}`,
      'get',
      'Interface',
      capturePort.interface,
      'lldp',
    ],
    { encoding: 'utf8', timeout: 7000 },
  ).trim();
}

async function login(page: Page, account: string) {
  expect((await page.goto(href))?.status()).toBe(200);
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

function api(path: string) {
  return `${fixture.origin}/api/v1/lldp${path}`;
}

async function readPage(page: Page, query = scope): Promise<LldpNeighborPage> {
  const response = await page.request.get(api('?' + query));
  expect(response.status()).toBe(200);
  return response.json();
}

async function receive(page: Page, mac: string, after: number) {
  let row: Resource | undefined;
  await expect
    .poll(
      async () => {
        row = (await readPage(page)).items.find(
          (candidate) => candidate.lldp?.chassis_id.value === mac,
        );
        return Date.parse(row?.lldp?.last_seen_at ?? '') >= after;
      },
      { intervals: [125, 250, 500], timeout: 15000 },
    )
    .toBe(true);
  if (!row?.lldp) throw new Error('No received LLDP advertisement');
  return { ...row, lldp: row.lldp };
}

function table(page: Page) {
  return page.getByRole('region', { name: 'LLDP advertisements', exact: true });
}

async function capture(page: Page, label: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: `test-results/frontend-evidence/lldp-${label}.png`,
    fullPage: true,
  });
}

test('Passive LLDP reads real scoped ingress and refreshes and expires advertisements without writes', async ({
  page,
}) => {
  await login(page, 'browser-lldp-observer');
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() !== 'GET' &&
      /\/api\/v1\/(lldp|candidate|transactions)/.test(request.url())
    ) {
      writes.push(request.method());
    }
  });
  expect(fixture.lldp.disabled).toMatchObject({
    availability: 'unavailable',
    reason: 'LLDP_PROVIDER_DISABLED',
    items: [],
  });
  // The runner read this response from the real manager before adding its
  // opt-in flag. Replay only the earlier disabled screen in the current SPA.
  const disabledRoute = /\/api\/v1\/lldp\?/;
  await page.route(disabledRoute, (route) =>
    route.fulfill({ json: fixture.lldp.disabled }),
  );
  await page.goto(`${href}?${scope}`);
  await expect(
    page.getByText(
      'Passive LLDP capture is disabled. An administrator can opt in managed Interfaces.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(table(page)).toHaveCount(0);
  await capture(page, 'disabled');
  await page.unroute(disabledRoute);

  await expect
    .poll(async () => {
      const current = await readPage(page);
      return {
        availability: current.availability,
        freshness: current.source?.freshness,
        observed: current.coverage?.observed_interfaces,
        items: current.items,
      };
    })
    .toEqual({
      availability: 'complete',
      freshness: 'fresh',
      observed: 1,
      items: [],
    });
  await page.getByRole('button', { name: 'Refresh LLDP', exact: true }).click();
  await expect(
    page.getByText(
      'No LLDP advertisements observed within this capture scope.',
      { exact: true },
    ),
  ).toBeVisible();
  await capture(page, 'empty');

  const ovsBefore = nativeLLDP();
  advertisement(
    fixture.lldp.excluded,
    '02:00:25:44:00:02',
    'excluded-lldp-peer',
    90,
  );
  const firstSend = Date.now();
  advertisement(capturePort, sourceMAC, system, 90);
  const first = await receive(page, sourceMAC, firstSend);
  expect(first).toMatchObject({
    allowed_actions: [],
    interface_name: capturePort.interface,
    interface_ref: { id: capturePort.id, kind: 'interface' },
    port_ref: { id: capturePort.portID, kind: 'port' },
    bridge_ref: { id: capturePort.bridgeID, kind: 'bridge' },
    source: { provider_id: 'linux', authority: 'linux-passive-lldp', freshness: 'fresh', confidence: 'partial' },
    lldp: {
      chassis_id: { subtype: 4, encoding: 'mac', value: sourceMAC },
      port_id: { subtype: 5, encoding: 'text', value: remotePort },
      system_name: system,
      ttl_seconds: 90,
    },
  });
  const all = await readPage(page, 'limit=25');
  expect(all.items.map((row) => row.id)).toEqual([first.id]);
  expect(all.truncated).toBe(false);
  expect(all.coverage).toMatchObject({
    scope: 'opted-in-managed-interfaces',
    configured_interfaces: 1,
    observed_interfaces: 1,
    max_entries: 256,
  });
  const excluded = await readPage(page, `interface_id=${fixture.lldp.excluded.id}`);
  expect(excluded).toMatchObject({ availability: 'unavailable', reason: 'LLDP_SCOPE_NOT_COVERED', items: [] });

  const repeatSend = Date.now();
  advertisement(capturePort, sourceMAC, system, 90);
  const refreshed = await receive(page, sourceMAC, repeatSend);
  expect(refreshed.id).toBe(first.id);
  expect(refreshed.lldp.ttl_seconds).toBe(90);
  expect(Date.parse(refreshed.lldp.last_seen_at)).toBeGreaterThan(
    Date.parse(first.lldp.last_seen_at),
  );

  await page.goto(`${href}?${scope}&limit=1`);
  const chassis = table(page).getByRole('link', {
    name: sourceMAC,
    exact: true,
  });
  await expect(chassis).toBeVisible();
  await expect(table(page)).toContainText(system);
  await expect(table(page)).toContainText('90 s');
  await expect(table(page)).toContainText(refreshed.lldp.last_seen_at);
  await capture(page, 'standard');
  const pending = page.waitForEvent('download');
  await page
    .getByRole('button', { name: 'Download this page as JSON', exact: true })
    .click();
  const downloaded = await (await pending).path();
  if (!downloaded) throw new Error('Expected the bounded LLDP page download');
  const exported: { page: LldpNeighborPage; query: Record<string, string> } =
    JSON.parse(readFileSync(downloaded, 'utf8'));
  expect(exported.page.items.map((row) => row.id)).toEqual([first.id]);
  expect(exported.query).toMatchObject({
    interface_id: capturePort.id,
    limit: '1',
  });

  await chassis.focus();
  await expect(chassis).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('heading', { name: system, exact: true }),
  ).toBeVisible();
  const links = page.getByRole('region', {
    name: 'LLDP Interface scope',
    exact: true,
  });
  for (const path of [
    `/interfaces/${capturePort.id}`,
    `/ports/${capturePort.portID}`,
    `/bridges/${capturePort.bridgeID}`,
  ]) {
    await expect(links.locator(`a[href="${path}"]`)).toBeVisible();
  }
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page.getByText('LLDP identity and source', { exact: true }).click();
  await expect(page.getByText(first.id, { exact: true })).toBeVisible();
  await capture(page, 'expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(
      page.getByRole('heading', { name: system, exact: true }),
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

  const shortMAC = '02:00:25:44:00:03';
  const shortSend = Date.now();
  advertisement(capturePort, shortMAC, 'short-lived-lldp-peer', 6);
  const brief = await receive(page, shortMAC, shortSend);
  expect(brief.lldp.ttl_seconds).toBe(6);
  await expect
    .poll(async () => (await readPage(page)).items.map((row) => row.id), {
      timeout: 15000,
    })
    .toEqual([first.id]);
  expect((await page.request.get(api('/' + brief.id))).status()).toBe(404);

  advertisement(capturePort, sourceMAC, system, 0);
  await expect
    .poll(async () => {
      const current = await readPage(page);
      return [current.availability, current.items.length];
    })
    .toEqual(['complete', 0]);
  expect(nativeLLDP()).toBe(ovsBefore);
  expect(writes).toEqual([]);
});

test('LLDP requires inventory and state authority in Standard and Expert', async ({
  page,
}) => {
  await login(page, 'browser-lldp-denied');
  await page.goto(href);
  const reply = await page.request.get(api('?limit=1'));
  expect(reply.status()).toBe(403);
  expect((await reply.json()).code).toBe('CAPABILITY_DENIED');
  for (const mode of ['Standard', 'Expert'] as const) {
    const toggle = page.getByRole('button', { name: mode, exact: true });
    await expect(toggle).toBeVisible();
    await expect(
      page.getByText('Permission denied', { exact: true }),
    ).toBeVisible();
    await expect(table(page)).toHaveCount(0);
    await expect(
      page.getByRole('button', {
        name: 'Download this page as JSON',
        exact: true,
      }),
    ).toHaveCount(0);
    if (mode === 'Standard') await toggle.click();
  }
  await capture(page, 'withheld');
});

test.afterAll(() => {
  execFileSync(
    'ovs-vsctl',
    [
      '--timeout=5',
      `--db=unix:${fixture.dbSocket}`,
      '--no-wait',
      '--if-exists',
      'del-br',
      fixture.lldp.bridge,
    ],
    { timeout: 7000 },
  );
  for (const port of [fixture.lldp.capture, fixture.lldp.excluded]) {
    const result = spawnSync('ip', ['link', 'del', port.interface], {
      timeout: 7000,
    });
    expect(result.status).toBe(0);
  }
  execFileSync('ip', ['netns', 'delete', fixture.lldp.namespace], {
    timeout: 7000,
  });
});
