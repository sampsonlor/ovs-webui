import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import type {
  Statistic,
  StatisticPage,
  Statistics,
  StreamEnvelope,
  Subscription,
} from '../../clients/typescript/public-v1.generated';

type ObservedStatistic = Statistic & { statistics: Statistics };

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  ovsDirectory: string;
  accounts: Record<string, string>;
} = JSON.parse(readFileSync(fixturePath, 'utf8'));
const href = fixture.origin + '/visibility/statistics';

function vsctl(...args: string[]) {
  return execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    { encoding: 'utf8' },
  ).trim();
}

async function login(page: Page, account: string) {
  expect((await page.goto(href))?.status()).toBe(200);
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const response = page.waitForResponse(
    (reply) =>
      reply.request().method() === 'POST' &&
      reply.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await response).status()).toBe(201);
  await page.goto(href);
}

async function read(page: Page, id: string): Promise<ObservedStatistic> {
  const reply = await page.request.get(
    fixture.origin + '/api/v1/statistics/' + id,
  );
  expect(reply.status()).toBe(200);
  const value: Statistic = await reply.json();
  expect(value.statistics).toBeDefined();
  return value as ObservedStatistic;
}

async function selected(page: Page): Promise<ObservedStatistic> {
  const reply = await page.request.get(
    fixture.origin + '/api/v1/statistics?filter=inv-p1',
  );
  expect(reply.status()).toBe(200);
  const result: StatisticPage = await reply.json();
  const row = result.items.find((item) => item.name === 'inv-p1');
  expect(row).toBeDefined();
  expect(row?.statistics).toBeDefined();
  return row as ObservedStatistic;
}

async function capture(page: Page, name: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: `test-results/frontend-evidence/statistics-${name}.png`,
    fullPage: true,
  });
}

test('Native Statistics shares exact Interface counters and RAM trends through responsive authenticated reads', async ({
  page,
}) => {
  const subscriptions: Subscription[] = [];
  const notifications: StreamEnvelope[] = [];
  let opened = 0;
  let closed = 0;
  page.on('websocket', (socket) => {
    if (new URL(socket.url()).pathname !== '/api/v1/stream') return;
    opened++;
    socket.on('framesent', ({ payload }) => {
      if (typeof payload === 'string') subscriptions.push(JSON.parse(payload));
    });
    socket.on('framereceived', ({ payload }) => {
      if (typeof payload === 'string') notifications.push(JSON.parse(payload));
    });
    socket.on('close', () => {
      closed++;
    });
  });
  await login(page, 'browser-statistics-observer');
  const writes: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions)/.test(request.url())
    ) {
      writes.push(request.method());
    }
  });
  await expect(
    page.getByRole('heading', { name: 'Statistics', exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Interface name', { exact: true })
    .fill('synthetic-no-statistics-match');
  await page.getByRole('button', { name: 'Apply filter', exact: true }).click();
  await expect(
    page.getByText('No Interfaces match this statistics scope.', {
      exact: true,
    }),
  ).toBeVisible();
  await page.getByLabel('Interface name', { exact: true }).fill('inv-p1');
  await page.getByRole('button', { name: 'Apply filter', exact: true }).click();
  const region = page.getByRole('region', {
    name: 'Interface statistics',
    exact: true,
  });
  const link = region.locator('a[href^="/visibility/statistics/"]');
  await expect(link).toHaveCount(1);
  const row = await selected(page);
  expect(row.id).toBe(row.interface_ref.id);
  expect(row.native_uuid).toBe(vsctl('get', 'Interface', 'inv-p1', '_uuid'));
  const detail = fixture.origin + '/api/v1/statistics/' + row.id;
  await expect
    .poll(
      async () => (await read(page, row.id)).statistics.counters.rx_packets,
      { timeout: 20000 },
    )
    .not.toBeNull();
  const before = await read(page, row.id);
  const initialPackets = BigInt(before.statistics.counters.rx_packets!);
  const packet =
    '02000000000202000000000108060001080006040001020000000001c6126d01000000000000c6126d02';
  execFileSync('ovs-appctl', [
    '-t',
    fixture.ovsDirectory + '/switch.ctl',
    'netdev-dummy/receive',
    'inv-p1',
    packet,
    packet,
    packet,
  ]);
  await expect
    .poll(
      async () => {
        const latest = await read(page, row.id);
        return (
          latest.statistics.counters.rx_packets !== null &&
          BigInt(latest.statistics.counters.rx_packets) >=
            initialPackets + BigInt(3)
        );
      },
      { timeout: 25000 },
    )
    .toBe(true);
  await page
    .getByRole('button', { name: 'Refresh statistics', exact: true })
    .click();
  await expect(region).toContainText(
    (await read(page, row.id)).statistics.counters.rx_packets + ' packets',
  );
  await capture(page, 'list');
  expect(opened).toBe(0);
  await link.focus();
  await expect(link).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(new RegExp('/visibility/statistics/' + row.id));
  await expect.poll(() => subscriptions).toEqual([
    { resources: [{ kind: 'statistic', id: row.id }], cursor: null },
  ]);
  await expect.poll(() => notifications[0]?.type).toBe('resync.required');

  const detailBefore = await read(page, row.id);
  const priorCount = BigInt(detailBefore.statistics.counters.rx_packets!);
  const previousHints = notifications.length;
  const updatedDetail = page.waitForResponse(
    async (response) => {
      if (
        response.url() !== detail ||
        response.request().method() !== 'GET' ||
        response.status() !== 200 ||
        !notifications.slice(previousHints).some(
          (hint) =>
            hint.type === 'state.coalesced' &&
            hint.resource_ref?.kind === 'statistic' &&
            hint.resource_ref.id === row.id &&
            !!hint.resource_version,
        )
      )
        return false;
      const latest: ObservedStatistic = await response.json();
      const count = latest.statistics.counters.rx_packets;
      return count !== null && BigInt(count) >= priorCount + BigInt(2);
    },
    { timeout: 25000 },
  );
  execFileSync('ovs-appctl', [
    '-t',
    fixture.ovsDirectory + '/switch.ctl',
    'netdev-dummy/receive',
    'inv-p1',
    packet,
    packet,
  ]);
  const current: ObservedStatistic = await (await updatedDetail).json();
  expect(current.statistics.interval_ms).toBe(5000);
  expect(current.statistics.retention_seconds).toBe(300);
  expect(current.statistics.sample_count).toBeLessThanOrEqual(60);
  expect(current.statistics.history?.length).toBeGreaterThan(0);
  expect(current.statistics.history?.length).toBeLessThanOrEqual(60);
  await expect(
    page.getByText('Source ovsdb-statistics', { exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByRole('region', { name: 'Traffic counters', exact: true })
      .getByRole('row', { name: /^Received packets/ })
      .getByRole('cell')
      .first(),
  ).toHaveText(current.statistics.counters.rx_packets + ' packets');
  await expect(
    page.getByRole('region', { name: 'Recent traffic trend', exact: true }),
  ).toBeVisible();
  await expect(page.getByText('Native UUID', { exact: true })).toHaveCount(0);
  await expect(
    page.getByRole('button', { name: /stage|apply|configure/i }),
  ).toHaveCount(0);
  await capture(page, 'standard');

  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page
    .getByText('Statistics identity and source', { exact: true })
    .click();
  await expect(page.getByText('Native UUID', { exact: true })).toBeVisible();
  await expect(page.getByText(row.native_uuid!, { exact: true })).toBeVisible();
  await capture(page, 'expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  for (const width of [820, 390]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(
      page.getByRole('heading', { name: 'Traffic counters', exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole('link', { name: 'Review Interface', exact: true }),
    ).toBeVisible();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
  }
  const timestampLayout = await page
    .getByRole('region', { name: 'Recent traffic trend', exact: true })
    .locator('tbody th time')
    .first()
    .evaluate((timestamp) => {
      const range = document.createRange();
      range.selectNodeContents(timestamp);
      return {
        height: range.getBoundingClientRect().height,
        fragments: range.getClientRects().length,
        lineHeight: Number.parseFloat(getComputedStyle(timestamp).lineHeight),
      };
    });
  expect(timestampLayout.height).toBeGreaterThan(0);
  expect(timestampLayout.fragments).toBe(1);
  expect(timestampLayout.height).toBeLessThanOrEqual(
    timestampLayout.lineHeight + 1,
  );
  await capture(page, 'mobile');
  expect(writes).toEqual([]);
  expect((await page.request.get(detail)).status()).toBe(200);
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Statistics', exact: true })).toBeVisible();
  await expect.poll(() => closed).toBe(opened);
});

test('Statistics permission denial and unavailable provider views withhold counter detail', async ({
  page,
  browser,
}) => {
  const denied = await browser.newPage({ ignoreHTTPSErrors: true });
  try {
    await login(denied, 'browser-statistics-denied');
    await expect(
      denied.getByText('Permission denied', { exact: true }),
    ).toBeVisible();
    expect(
      (
        await denied.request.get(fixture.origin + '/api/v1/statistics')
      ).status(),
    ).toBe(403);
    await expect(
      denied.getByRole('region', { name: 'Interface statistics', exact: true }),
    ).toHaveCount(0);
    await capture(denied, 'withheld');
  } finally {
    await denied.close();
  }
  await login(page, 'browser-statistics-observer');
  const row = await selected(page);
  const path = fixture.origin + '/api/v1/statistics/' + row.id;
  await page.goto(href + '/' + row.id);
  await expect(
    page.getByRole('region', { name: 'Traffic counters', exact: true }),
  ).toBeVisible();
  // The native daemon fixture covers actual OVSDB outage and recovery.
  // This bounded response exercises the browser's unavailable projection.
  await page.route(path, (route) =>
    route.fulfill({
      status: 503,
      json: { code: 'STATISTICS_UNAVAILABLE', status: 503 },
    }),
  );
  await page
    .getByRole('button', { name: 'Refresh statistics', exact: true })
    .click();
  await expect(
    page.getByRole('status').getByText('Unavailable', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Traffic counters', exact: true }),
  ).toHaveCount(0);
  await capture(page, 'unavailable');
  await page.unroute(path);
  await page
    .getByRole('button', { name: 'Refresh statistics', exact: true })
    .click();
  await expect(
    page.getByRole('region', { name: 'Traffic counters', exact: true }),
  ).toBeVisible();
});
