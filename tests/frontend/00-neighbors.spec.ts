import { expect, test, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import { createConnection } from 'node:net';
import type {
  Interface,
  InterfacePage,
  Neighbor,
  NeighborPage,
} from '../../clients/typescript/public-v1.generated';

const fixturePath = process.env.OVS_FRONTEND_FIXTURE;
if (!fixturePath) throw new Error('Requires actual isolated Go/OVS services.');
const fixture: {
  origin: string;
  dbSocket: string;
  accounts: Record<string, string>;
} = JSON.parse(readFileSync(fixturePath, 'utf8'));
const href = fixture.origin + '/visibility/neighbors';

const bridge = 'br-neighbor-ui';
const managed = 'nb-ui-l3';
const uplink = 'nb-ui-sw';
const peer = 'nb-ui-peer';
const peerNamespace = 'ovs-neighbor-ui-peer';
const unmanaged = 'nb-ui-unm';
const unmanagedPeer = 'nb-ui-unm-p';
const peerMAC = '02:00:24:44:25:02';
const localIPv4 = '192.0.2.201';
const localIPv6 = '2001:db8:44:25::1';
const ipv4 = '192.0.2.202';
const ipv6 = '2001:db8:44:25::2';
const unresolved = '192.0.2.203';
const hiddenAddress = '198.51.100.206';
const authority = 'linux-rtnetlink-neighbors';

function run(program: string, ...args: string[]): string {
  return execFileSync(program, args, {
    encoding: 'utf8',
    timeout: 7000,
  }).trim();
}

const ovs = ['--timeout=5', `--db=unix:${fixture.dbSocket}`];
const ip = (...args: string[]) => run('ip', ...args);
const peerIP = (...args: string[]) => ip('-n', peerNamespace, ...args);

function prepareFixture(cleanup: (() => void)[]) {
  run(
    'ovs-vsctl',
    ...ovs,
    'add-br',
    bridge,
    '--',
    'set',
    'Bridge',
    bridge,
    'datapath_type=system',
    '--',
    'add-port',
    bridge,
    managed,
    '--',
    'set',
    'Interface',
    managed,
    'type=internal',
  );
  cleanup.push(() => {
    run('ovs-vsctl', ...ovs, '--if-exists', 'del-br', bridge);
  });
  ip('netns', 'add', peerNamespace);
  cleanup.push(() => {
    ip('netns', 'delete', peerNamespace);
  });
  ip('link', 'add', uplink, 'type', 'veth', 'peer', 'name', peer);
  cleanup.push(() => {
    ip('link', 'del', uplink);
  });
  ip('link', 'set', 'dev', peer, 'netns', peerNamespace);
  run('ovs-vsctl', ...ovs, 'add-port', bridge, uplink);
  ip('link', 'set', 'dev', uplink, 'up');
  ip('link', 'set', 'dev', managed, 'up');
  peerIP('link', 'set', 'dev', peer, 'address', peerMAC);
  peerIP('link', 'set', 'dev', peer, 'up');
  ip('-4', 'addr', 'add', `${localIPv4}/29`, 'dev', managed);
  peerIP('-4', 'addr', 'add', `${ipv4}/29`, 'dev', peer);
  run('sysctl', '-q', '-w', `net.ipv6.conf.${managed}.disable_ipv6=0`);
  ip(
    'netns',
    'exec',
    peerNamespace,
    'sysctl',
    '-q',
    '-w',
    `net.ipv6.conf.${peer}.disable_ipv6=0`,
  );
  ip('-6', 'addr', 'add', `${localIPv6}/64`, 'nodad', 'dev', managed);
  peerIP('-6', 'addr', 'add', `${ipv6}/64`, 'nodad', 'dev', peer);

  ip('link', 'add', unmanaged, 'type', 'veth', 'peer', 'name', unmanagedPeer);
  cleanup.push(() => {
    ip('link', 'del', unmanaged);
  });
  ip('link', 'set', 'dev', unmanaged, 'up');
  ip('link', 'set', 'dev', unmanagedPeer, 'up');
  ip(
    '-4',
    'neigh',
    'replace',
    hiddenAddress,
    'lladdr',
    '02:00:24:44:25:03',
    'nud',
    'permanent',
    'dev',
    unmanaged,
  );
}

type KernelNeighbor = { dst: string; lladdr?: string; state: string[] };

function kernelNeighbor(device: string, address: string, family: '-4' | '-6') {
  const rows: KernelNeighbor[] = JSON.parse(
    ip(family, '-j', 'neigh', 'show', 'dev', device),
  );
  return rows.find((row) => row.dst === address);
}

function reachPeer(family: 4 | 6, localAddress: string, host: string) {
  // The isolated peer refuses a closed TCP port. Its reply proves packet
  // forwarding and triggers normal ARP or NDP resolution on the fixture.
  return new Promise<string>((resolve) => {
    const socket = createConnection({ family, localAddress, host, port: 9 });
    socket.once('error', (error: NodeJS.ErrnoException) =>
      resolve(error.code ?? 'error'),
    );
    socket.once('connect', () => {
      socket.destroy();
      resolve('connected');
    });
    socket.setTimeout(1000, () => {
      socket.destroy();
      resolve('timeout');
    });
  });
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
  await page.goto(href);
}

async function read<T>(page: Page, path: string): Promise<T> {
  const reply = await page.request.get(`${fixture.origin}/api/v1${path}`);
  expect(reply.status(), path).toBe(200);
  return reply.json();
}

function table(page: Page) {
  return page.getByRole('region', {
    name: 'Linux ARP and NDP neighbors',
    exact: true,
  });
}

async function capture(page: Page, label: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: `test-results/frontend-evidence/neighbors-${label}.png`,
    fullPage: true,
  });
}

test('Native ARP and NDP use one shared read-only snapshot with scoped navigation, paging and export', async ({
  page,
}) => {
  const cleanup: (() => void)[] = [];
  const cleanupErrors: unknown[] = [];
  try {
    prepareFixture(cleanup);
    await login(page, 'browser-neighbors-observer');
    const writes: string[] = [];
    page.on('request', (request) => {
      if (
        request.method() !== 'GET' &&
        /\/api\/v1\/(neighbors|candidate|transactions)/.test(request.url())
      ) {
        writes.push(request.method());
      }
    });
    await expect.poll(() => reachPeer(4, localIPv4, ipv4)).toBe('ECONNREFUSED');
    await expect.poll(() => reachPeer(6, localIPv6, ipv6)).toBe('ECONNREFUSED');
    expect(kernelNeighbor(managed, ipv4, '-4')?.lladdr).toBe(peerMAC);
    expect(kernelNeighbor(managed, ipv6, '-6')?.lladdr).toBe(peerMAC);
    expect(kernelNeighbor(unmanaged, hiddenAddress, '-4')?.lladdr).toBe(
      '02:00:24:44:25:03',
    );
    ip('-4', 'neigh', 'replace', unresolved, 'nud', 'failed', 'dev', managed);
    const failed = kernelNeighbor(managed, unresolved, '-4');
    expect(failed?.state).toContain('FAILED');
    expect(failed?.lladdr).toBeUndefined();

    let binding: Interface | undefined;
    await expect
      .poll(async () => {
        const catalog = await read<InterfacePage>(
          page,
          `/interfaces?filter=${managed}`,
        );
        binding = catalog.items.find((row) => row.name === managed);
        return !!binding?.bridge_ref?.id;
      })
      .toBe(true);
    const intf = binding?.management_id;
    const port = binding?.port_ref.id;
    const parent = binding?.bridge_ref?.id;
    if (!intf || !port || !parent)
      throw new Error(
        'Expected exact managed Bridge → Port → Interface binding',
      );
    const scope = `interface_id=${intf}&port_id=${port}&bridge_id=${parent}`;
    const scopedPath = `/neighbors?${scope}`;
    let observed: NeighborPage | undefined;
    await expect
      .poll(
        async () => {
          observed = await read<NeighborPage>(page, scopedPath);
          return [ipv4, ipv6, unresolved].every((address) =>
            observed?.items.some((item) => item.address === address),
          );
        },
        { timeout: 20000 },
      )
      .toBe(true);
    if (!observed) throw new Error('Expected scoped native observations');
    expect(observed.source).toMatchObject({
      provider_id: 'linux',
      authority,
      freshness: 'fresh',
      observed_at: expect.any(String),
    });
    expect(observed.coverage).toMatchObject({
      scope: 'managed-linux-interfaces',
      max_entries: 2048,
    });
    expect(observed.truncated).toBe(false);
    const index = JSON.parse(ip('-j', 'link', 'show', 'dev', managed))[0]
      .ifindex;
    for (const [address, family, mac] of [
      [ipv4, 'ipv4', peerMAC],
      [ipv6, 'ipv6', peerMAC],
      [unresolved, 'ipv4', null],
    ]) {
      const item = observed.items.find((row) => row.address === address);
      expect(item).toMatchObject({
        address,
        family,
        mac,
        ifindex: index,
        interface_name: managed,
        interface_ref: { kind: 'interface', id: intf },
        port_ref: { kind: 'port', id: port },
        bridge_ref: { kind: 'bridge', id: parent },
        source: { authority, observed_at: observed.source?.observed_at },
        allowed_actions: [],
      });
    }
    expect(
      observed.items.find((row) => row.address === unresolved)?.state,
    ).toBe('failed');
    const iface = await read<Interface>(page, `/interfaces/${intf}`);
    expect(iface.source.provider_id).toBe('ovsdb');
    expect(iface.fields?.type.availability).toBe('withheld');
    expect(
      (await read<NeighborPage>(page, `/neighbors?filter=${hiddenAddress}`))
        .items,
    ).toEqual([]);

    // The dynamic entries above prove ARP and NDP learning. Keep those same
    // fixture rows stable while exercising snapshot-bound navigation.
    for (const [family, address] of [
      ['-4', ipv4],
      ['-6', ipv6],
    ]) {
      ip(
        family,
        'neigh',
        'replace',
        address,
        'lladdr',
        peerMAC,
        'nud',
        'permanent',
        'dev',
        managed,
      );
    }
    await expect
      .poll(
        async () => {
          const first = await read<NeighborPage>(page, scopedPath);
          const next = await read<NeighborPage>(page, scopedPath);
          return [
            first.source?.freshness,
            first.items.find((item) => item.address === ipv4)?.state,
            first.items.find((item) => item.address === ipv6)?.state,
            first.snapshot_id === next.snapshot_id,
            first.source?.observed_at === next.source?.observed_at,
          ];
        },
        { timeout: 20000 },
      )
      .toEqual(['fresh', 'permanent', 'permanent', true, true]);

    await page.goto(`${href}?${scope}&family=ipv4&limit=1`);
    await expect(
      page.getByRole('heading', { name: 'Neighbors', exact: true }),
    ).toBeVisible();
    await expect(
      table(page).getByRole('link', { name: ipv4, exact: true }),
    ).toBeVisible();
    await expect(
      table(page).getByRole('link', { name: unresolved, exact: true }),
    ).toHaveCount(0);
    await capture(page, 'standard');
    const pendingDownload = page.waitForEvent('download');
    await page
      .getByRole('button', { name: 'Download this page as JSON', exact: true })
      .click();
    const download = await pendingDownload;
    const downloadedPath = await download.path();
    if (!downloadedPath)
      throw new Error('Expected bounded Neighbor page export');
    const exported: { page: NeighborPage; query: Record<string, string> } =
      JSON.parse(readFileSync(downloadedPath, 'utf8'));
    expect(exported.page.items.map((item) => item.address)).toEqual([ipv4]);
    expect(exported.page.next_cursor).toEqual(expect.any(String));
    expect(exported.page.source?.authority).toBe(authority);
    expect(exported.query).toMatchObject({
      limit: '1',
      family: 'ipv4',
      interface_id: intf,
      port_id: port,
      bridge_id: parent,
    });
    await page
      .getByRole('link', { name: 'Next neighbor page', exact: true })
      .click();
    const unresolvedRow = table(page)
      .getByRole('row')
      .filter({
        has: page.getByRole('link', { name: unresolved, exact: true }),
      });
    await expect(unresolvedRow).toContainText('Unreported');
    await expect(
      table(page).getByRole('link', { name: ipv4, exact: true }),
    ).toHaveCount(0);
    await capture(page, 'failed');

    await page.getByLabel('IP, MAC, or Interface', { exact: true }).fill(ipv6);
    await page
      .getByLabel('Address family', { exact: true })
      .selectOption('ipv6');
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    const addressLink = table(page).getByRole('link', {
      name: ipv6,
      exact: true,
    });
    const entry = observed.items.find((row) => row.address === ipv6);
    if (!entry) throw new Error('Expected a native NDP entry');
    await expect(addressLink).toHaveAttribute(
      'href',
      new RegExp(`^/visibility/neighbors/${entry.id}(?:\\?|$)`),
    );
    await addressLink.focus();
    await expect(addressLink).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(
      page.getByRole('heading', { name: ipv6, exact: true }),
    ).toBeVisible();
    const detail = await read<Neighbor>(page, `/neighbors/${entry.id}`);
    expect(detail).toMatchObject({
      address: ipv6,
      mac: peerMAC,
      interface_ref: { id: intf },
      allowed_actions: [],
    });
    const links = page.getByRole('region', {
      name: 'Neighbor Interface scope',
      exact: true,
    });
    for (const path of [
      `/interfaces/${intf}`,
      `/ports/${port}`,
      `/bridges/${parent}`,
    ]) {
      await expect(links.locator(`a[href="${path}"]`)).toBeVisible();
    }
    await expect(
      page.getByText('Neighbor identity and source', { exact: true }),
    ).toHaveCount(0);
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await page
      .getByText('Neighbor identity and source', { exact: true })
      .click();
    await expect(
      page.locator('[aria-label="Neighbor observation source"]'),
    ).toContainText(authority);
    await capture(page, 'expert');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    for (const width of [820, 390]) {
      await page.setViewportSize({ width, height: 844 });
      await expect(
        page.getByRole('heading', { name: ipv6, exact: true }),
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
    await page.goto(`${href}?${scope}&filter=2001%3Adb8%3A44%3A25%3A%3Affff`);
    await expect(
      page.getByText('No ARP or NDP entries match this scope.', {
        exact: true,
      }),
    ).toBeVisible();
    await capture(page, 'empty');
    expect(writes).toEqual([]);
  } finally {
    for (const remove of cleanup.reverse()) {
      try {
        remove();
      } catch (error) {
        cleanupErrors.push(error);
      }
    }
  }
  expect(cleanupErrors, 'Retire disposable neighbor fixtures').toEqual([]);
});

test('Neighbors require both inventory and state authority in Standard and Expert', async ({
  page,
}) => {
  await login(page, 'browser-neighbors-denied');
  const reply = await page.request.get(
    `${fixture.origin}/api/v1/neighbors?limit=1`,
  );
  expect(reply.status()).toBe(403);
  expect((await reply.json()).code).toBe('CAPABILITY_DENIED');
  for (const mode of ['Standard', 'Expert'] as const) {
    await expect(
      page.getByRole('button', { name: mode, exact: true }),
    ).toBeVisible();
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
    if (mode === 'Standard')
      await page.getByRole('button', { name: mode, exact: true }).click();
  }
  await capture(page, 'withheld');
});
