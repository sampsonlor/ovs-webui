import { test, expect } from '@playwright/test';
import type { Page, BrowserContext } from '@playwright/test';
import { readFileSync, mkdirSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { requestID } from '../../frontend/src/api';

type Fixture = {
  origin: string;
  accounts: Record<string, string>;
  units: Record<string, string>;
  dbSocket: string;
  ovsDirectory: string;
  database: string;
};
if (!process.env.OVS_FRONTEND_FIXTURE)
  throw new Error(
    'This suite requires the real isolated Go/OVS fixture. No mock fallback.',
  );
const fixture: Fixture = JSON.parse(
  readFileSync(process.env.OVS_FRONTEND_FIXTURE, 'utf8'),
);
const vsctl = (...args: string[]) =>
  execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    { encoding: 'utf8' },
  ).trim();
const unit = (action: string, name: string) =>
  execFileSync('systemctl', [action, fixture.units[name]], { stdio: 'pipe' });
const evidence = 'test-results/frontend-evidence';

async function mtuEditor(page: Page) {
  const list = await get(page.context(), '/interfaces?filter=pi-ui-mtu');
  const item = list.items.find((i: { name: string }) => i.name === 'pi-ui-mtu');
  expect(item?.mtu_editable).toBe(true);
  await page.goto(fixture.origin + `/interfaces/${item.management_id}`);
  await page
    .getByRole('link', { name: 'Edit MTU request →', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Edit MTU request', exact: true }),
  ).toBeVisible();
  return item;
}

async function stageMTU(page: Page, requested: string) {
  await page.getByLabel('MTU request (bytes)', { exact: true }).fill(requested);
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
}

async function waitMTUDefault(
  page: Page,
  request: string | null,
  value: number,
) {
  await expect
    .poll(async () => {
      const list = await get(page.context(), '/interfaces?filter=pi-ui-mtu');
      const item = list.items.find(
        (i: { name: string }) => i.name === 'pi-ui-mtu',
      );
      return {
        request: item?.fields?.mtu_request?.value,
        actual: item?.fields?.mtu?.value,
        default: item?.mtu_default,
        editable: item?.mtu_editable,
      };
    })
    .toEqual({
      request: request === null ? [] : [request],
      actual: [request ?? String(value)],
      default: value,
      editable: true,
    });
}

function actualLinuxMTU() {
  return JSON.parse(
    execFileSync('ip', ['-j', 'link', 'show', 'dev', 'pi-ui-mtu'], {
      encoding: 'utf8',
    }),
  )[0].mtu;
}

async function stageAutomaticMTU(page: Page) {
  await page.getByLabel('MTU mode', { exact: true }).selectOption('automatic');
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
}

async function get(context: BrowserContext, path: string) {
  const response = await context.request.get(fixture.origin + '/api/v1' + path);
  expect(response.status(), path).toBe(200);
  return response.json();
}
async function portPage(page: Page, name: string) {
  // Inventory is bounded by both row count and bytes. A synthetic object is
  // not guaranteed to be on page one, regardless of the configured page size.
  for (let attempt = 0; attempt < 8; attempt++) {
    await expect(
      page.getByRole('region', { name: 'Ports inventory', exact: true }),
    ).toBeVisible();
    const target = page.getByRole('link', { name, exact: true });
    if (await target.count()) {
      await expect(target).toBeVisible();
      return;
    }
    const reply = page.waitForResponse((r) => {
      const url = new URL(r.url());
      return url.pathname === '/api/v1/ports' && url.searchParams.has('cursor');
    });
    await page.getByRole('button', { name: 'Next page', exact: true }).click();
    const response = await reply;
    if (response.status() !== 200) {
      expect(response.status()).toBe(410);
      expect((await response.json()).code).toBe('CURSOR_EXPIRED');
      await expect(page.getByRole('status')).toContainText('CURSOR_EXPIRED');
      await page
        .getByRole('button', { name: 'Refresh inventory', exact: true })
        .click();
    }
  }
  throw new Error(`Port ${name} was not found through bounded inventory pages`);
}
async function openPort(page: Page, name: string) {
  await page.goto(fixture.origin + '/ports');
  await portPage(page, name);
  await page.getByRole('link', { name, exact: true }).click();
}
async function command(
  context: BrowserContext,
  path: string,
  body: Record<string, unknown>,
  method = 'POST',
  revision?: string,
) {
  const session = await get(context, '/session');
  const request = requestID();
  const response = await context.request.fetch(
    fixture.origin + '/api/v1' + path,
    {
      method,
      data: { ...body, request_id: request },
      headers: {
        Origin: fixture.origin,
        'X-OVS-CSRF-Token': session.csrf_token,
        'Idempotency-Key': request,
        'X-OVS-Request-Epoch':
          session.request_epochs[
            path === '/candidate' ? 'workspace' : 'management'
          ],
        ...(revision ? { 'If-Match': `"${revision}"` } : {}),
      },
    },
  );
  expect(response.ok(), `Command ${path} returned ${response.status()}`).toBe(
    true,
  );
  return response.json();
}
async function login(page: Page, name = 'browser-admin') {
  await page.goto(fixture.origin + '/ports');
  await page.getByLabel('Username', { exact: true }).fill(name);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[name]);
  const loginReply = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' && r.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const response = await loginReply;
  const problem = response.status() === 201 ? null : await response.json();
  const code =
    typeof problem?.code === 'string' && /^[A-Z_]{1,80}$/.test(problem.code)
      ? problem.code
      : '';
  expect(response.status(), `Session creation ${code}`).toBe(201);
  // Diagnose browser cookie admission without retaining cookies, credentials or
  // successful session payloads in the test report.
  const admitted = await page.evaluate(async () => {
    const r = await fetch('/api/v1/session', {
      credentials: 'same-origin',
      cache: 'no-store',
    });
    const p: unknown = r.ok ? null : await r.json();
    return {
      status: r.status,
      code:
        p !== null &&
        typeof p === 'object' &&
        'code' in p &&
        typeof p.code === 'string' &&
        /^[A-Z_]{1,80}$/.test(p.code)
          ? p.code
          : '',
    };
  });
  expect(admitted).toEqual({ status: 200, code: '' });
  await expect(
    page.getByRole('heading', { name: 'Ports', exact: true }),
  ).toBeVisible();
  await portPage(page, 'inv-p1');
}
async function clean(context: BrowserContext) {
  const c = await get(context, '/candidate');
  if (c.intents.length)
    await command(
      context,
      '/candidate',
      { operation: 'discard' },
      'PATCH',
      c.revision,
    );
}
async function stage(page: Page, tag: string) {
  await openPort(page, 'inv-p1');
  await page
    .getByRole('link', { name: 'Edit VLAN intent →', exact: true })
    .click();
  await page
    .getByRole('combobox', { name: 'VLAN mode', exact: true })
    .selectOption('access');
  await page.getByLabel('VLAN tag', { exact: true }).fill(tag);
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
}
async function validate(page: Page) {
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Diff & Validation', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Usable for this revision', { exact: true }),
  ).toBeVisible();
}
async function prepareApply(page: Page, name = 'browser-admin') {
  await page
    .getByLabel('Verify your password', { exact: true })
    .fill(fixture.accounts[name]);
  await page
    .getByRole('button', { name: 'Verify identity', exact: true })
    .click();
  await expect(
    page.getByText('Identity verified.', { exact: false }),
  ).toBeVisible();
  await page
    .getByLabel('Change reason', { exact: true })
    .fill('Synthetic formal frontend acceptance');
  await page.getByRole('checkbox').check();
  await expect(
    page.getByRole('button', { name: 'Start Safe Apply', exact: true }),
  ).toBeEnabled();
}
async function awaiting(page: Page) {
  await expect(page.getByTestId('safe-state')).toHaveText(
    'awaiting-confirmation',
  );
  await expect(
    page.getByRole('button', { name: 'Confirm configuration', exact: true }),
  ).toBeEnabled();
}
async function chooseDecision(page: Page, name: string, state: string) {
  for (let attempt = 0; attempt < 4; attempt++) {
    const reply = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/decisions'),
    );
    await page.getByRole('button', { name, exact: true }).click();
    const response = await reply;
    if (response.status() === 202) {
      await expect(page.getByTestId('safe-state')).toHaveText(state);
      return;
    }
    // Repeat the human review only after an explicit non-execution guarantee.
    const problem = await response.json();
    expect(response.status()).toBe(409);
    expect(problem.code).toBe('TRANSACTION_VERSION_CHANGED');
    expect(problem.command_effect).toBe('not-started');
    await expect(
      page.getByRole('button', { name: 'Recover original request' }),
    ).toHaveCount(0);
    await page
      .getByRole('button', { name: 'Refresh evidence', exact: true })
      .click();
  }
  throw new Error('Transaction did not stabilize for an explicit decision');
}
async function screen(page: Page, name: string) {
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: `${evidence}/${name}.png`, fullPage: true });
}
const pageErrors = new WeakMap<Page, string[]>();

test('native Interfaces support snapshot filters, pagination, depth and responsive observation', async ({
  page,
  context,
}) => {
  await login(page, 'browser-interfaces');
  const mutations: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions|interfaces)/.test(r.url())
    )
      mutations.push(r.method());
  });
  const names = Array.from(
    { length: 11 },
    (_, i) => `if-observe-${String(i).padStart(2, '0')}`,
  );
  vsctl(
    'add-br',
    'br-if-ui',
    '--',
    'set',
    'Bridge',
    'br-if-ui',
    'datapath_type=dummy',
    ...names.flatMap((name) => [
      '--',
      'add-port',
      'br-if-ui',
      name,
      '--',
      'set',
      'Interface',
      name,
      'type=dummy',
    ]),
  );
  try {
    vsctl('set', 'Interface', 'inv-p1', 'mtu_request=1500');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await page.getByRole('link', { name: 'Interfaces', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Interfaces', exact: true }),
    ).toBeVisible();
    await page.getByLabel('Page size', { exact: true }).selectOption('10');
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    await expect(
      page.getByRole('button', { name: 'Next page', exact: true }),
    ).toBeEnabled();
    await screen(page, 'interfaces-expert');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    await screen(page, 'interfaces-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    let paged = false;
    for (let attempt = 0; attempt < 3; attempt++) {
      const nextPage = page.waitForResponse(
        (response) =>
          response.url().includes('/api/v1/interfaces?') &&
          response.url().includes('cursor='),
      );
      await page
        .getByRole('button', { name: 'Next page', exact: true })
        .click();
      const response = await nextPage;
      if (response.status() === 200) {
        paged = true;
        break;
      }
      // Real daemon observations can invalidate the snapshot while reviewing
      // mode screenshots. Exercise explicit UI recovery; never ignore errors
      // or accept pagination without a successful snapshot-bound second page.
      expect(response.status()).toBe(410);
      expect((await response.json()).code).toBe('CURSOR_EXPIRED');
      await expect(
        page.getByText('The page snapshot changed or expired.', {
          exact: false,
        }),
      ).toBeVisible();
      await page
        .getByRole('button', { name: 'First page', exact: true })
        .click();
      await expect(
        page.getByRole('button', { name: 'Next page', exact: true }),
      ).toBeEnabled();
    }
    expect(
      paged,
      'Successful snapshot-bound second page after explicit recovery',
    ).toBe(true);
    await expect(
      page.getByRole('region', { name: 'Interfaces inventory', exact: true }),
    ).toBeVisible();
    vsctl('set', 'Interface', 'inv-p1', 'mtu_request=1600');
    await expect(
      page.getByText('The page snapshot changed or expired.', { exact: false }),
    ).toBeVisible();
    await screen(page, 'interfaces-cursor-expired');
    await page.getByRole('button', { name: 'First page', exact: true }).click();
    await expect(
      page.getByRole('region', { name: 'Interfaces inventory', exact: true }),
    ).toBeVisible();
    await page.getByLabel('Interface name', { exact: true }).fill('inv-p1');
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    await expect(
      page.getByRole('link', { name: 'inv-p1', exact: true }),
    ).toBeVisible();
    const filtered = await get(context, '/interfaces?filter=inv-p1');
    expect(filtered.items).toHaveLength(1);
    const iface = filtered.items[0];
    expect(iface.fields.mtu_request.value).toEqual(['1600']);
    await page.getByRole('link', { name: 'inv-p1', exact: true }).click();
    await expect(page).toHaveURL(
      fixture.origin + '/interfaces/' + iface.management_id,
    );
    await expect(
      page.getByRole('heading', {
        name: 'Native Interface fields',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole('row').filter({
        has: page.getByRole('rowheader', {
          name: 'Requested MTU',
          exact: false,
        }),
      }),
    ).toContainText('1600');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    await screen(page, 'interface-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-expert-dark');
    for (const [width, name] of [
      [900, 'interface-tablet'],
      [390, 'interface-mobile'],
    ] as const) {
      await page.setViewportSize({ width, height: 980 });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await screen(page, name);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page
      .getByRole('link', { name: 'Review owning Port →', exact: true })
      .focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(
      fixture.origin + '/ports/' + iface.port_ref.id,
    );
    expect(mutations).toEqual([]);
  } finally {
    vsctl('clear', 'Interface', 'inv-p1', 'mtu_request');
    vsctl('del-br', 'br-if-ui');
  }
});

test('Interface configuration withholding, empty filters, stale provider and retired identities remain explicit', async ({
  page,
  context,
}) => {
  await login(page, 'browser-interface-observer');
  await page.goto(fixture.origin + '/interfaces');
  await page
    .getByLabel('Interface name', { exact: true })
    .fill('synthetic-no-interface');
  await page.getByRole('button', { name: 'Apply filter', exact: true }).click();
  await expect(
    page.getByText('No Interfaces match this filter.', { exact: false }),
  ).toBeVisible();
  await screen(page, 'interfaces-empty');
  await page.getByLabel('Interface name', { exact: true }).fill('inv-p1');
  await page.getByRole('button', { name: 'Apply filter', exact: true }).click();
  await page.getByRole('link', { name: 'inv-p1', exact: true }).click();
  await expect(
    page.getByRole('row').filter({
      has: page.getByRole('rowheader', { name: 'Native type', exact: true }),
    }),
  ).toContainText('Withheld');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    page.getByRole('row').filter({
      has: page.getByRole('rowheader', {
        name: 'Requested MTU',
        exact: false,
      }),
    }),
  ).toContainText('Withheld');
  await screen(page, 'interface-withheld');
  execFileSync('ovs-appctl', ['-t', `${fixture.ovsDirectory}/db.ctl`, 'exit']);
  try {
    await expect(
      page.getByText('Stale observation.', { exact: false }),
    ).toBeVisible();
    await screen(page, 'interface-provider-stale');
  } finally {
    execFileSync(
      'ovsdb-server',
      [
        fixture.database,
        `--remote=punix:${fixture.dbSocket}`,
        `--pidfile=${fixture.ovsDirectory}/db.pid`,
        `--unixctl=${fixture.ovsDirectory}/db.ctl`,
        '--detach',
        '--no-chdir',
        '--overwrite-pidfile',
      ],
      {
        env: {
          ...process.env,
          OVS_RUNDIR: fixture.ovsDirectory,
          OVS_LOGDIR: fixture.ovsDirectory,
          OVS_DBDIR: fixture.ovsDirectory,
        },
        stdio: 'pipe',
      },
    );
  }
  await expect(
    page.getByText('Stale observation.', { exact: false }),
  ).toHaveCount(0);
  vsctl(
    'add-port',
    'br-inv',
    'if-retire',
    '--',
    'set',
    'Interface',
    'if-retire',
    'type=dummy',
  );
  try {
    await expect
      .poll(
        async () =>
          (await get(context, '/interfaces?filter=if-retire')).items.length,
      )
      .toBe(1);
    const old = (await get(context, '/interfaces?filter=if-retire')).items[0];
    await page.goto(fixture.origin + '/interfaces/' + old.management_id);
    await expect(
      page.getByRole('heading', { name: 'if-retire', exact: true }),
    ).toBeVisible();
    vsctl('del-port', 'br-inv', 'if-retire');
    vsctl(
      'add-port',
      'br-inv',
      'if-retire',
      '--',
      'set',
      'Interface',
      'if-retire',
      'type=dummy',
    );
    await expect(
      page.getByText('This Interface identity is no longer available.', {
        exact: false,
      }),
    ).toBeVisible();
    await screen(page, 'interface-retired');
    const replacement = (await get(context, '/interfaces?filter=if-retire'))
      .items[0];
    expect(replacement.management_id).not.toBe(old.management_id);
    expect(replacement.ovs_uuid).not.toBe(old.ovs_uuid);
    await page.goto(fixture.origin + '/interfaces');
    await page.getByLabel('Interface name', { exact: true }).fill('if-retire');
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    await expect(
      page.getByRole('link', { name: 'if-retire', exact: true }),
    ).toHaveAttribute('href', '/interfaces/' + replacement.management_id);
  } finally {
    vsctl('--if-exists', 'del-port', 'br-inv', 'if-retire');
  }
});

test('QinQ editor preserves TPID, reviews all customer VLANs and safely restores native fields', async ({
  page,
  context,
}) => {
  const account = 'browser-qinq';
  await login(page, account);
  await clean(context);
  const original = vsctl('get', 'Port', 'inv-p1', '_uuid');
  await openPort(page, 'inv-p1');
  await page
    .getByRole('link', { name: 'Edit VLAN intent →', exact: true })
    .click();
  await page
    .getByRole('combobox', { name: 'VLAN mode', exact: true })
    .selectOption('dot1q-tunnel');
  await page.getByLabel('Service VLAN tag', { exact: true }).fill('200');
  await page.getByLabel('Customer VLANs', { exact: true }).fill('30, 30');
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(page.getByRole('alert')).toContainText('unique');
  await page.getByLabel('Customer VLANs', { exact: true }).fill('');
  await expect(page.getByText('Service TPID:', { exact: false })).toContainText(
    '802.1ad',
  );
  await screen(page, 'qinq-editor');
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('cell', { name: '∅ · all customer VLANs', exact: true }),
  ).toBeVisible();
  expect(vsctl('get', 'Port', 'inv-p1', 'vlan_mode')).toBe('access');
  const c = await get(context, '/candidate');
  expect(c.intents[0].qinq_context.ethertype).toBe(null);
  await screen(page, 'qinq-standard');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page
    .getByText('QinQ native dependency evidence', { exact: true })
    .click();
  await screen(page, 'qinq-expert');
  for (const [width, height, device] of [
    [900, 1000, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await screen(page, 'qinq-' + device);
  }
  await page.setViewportSize({ width: 1280, height: 1000 });
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  expect(vsctl('get', 'Port', 'inv-p1', 'vlan_mode')).toBe('dot1q-tunnel');
  expect(vsctl('get', 'Port', 'inv-p1', 'cvlans')).toBe('[]');
  await screen(page, 'qinq-awaiting');
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  expect(vsctl('get', 'Port', 'inv-p1', 'vlan_mode')).toBe('access');
  expect(vsctl('get', 'Port', 'inv-p1', 'tag')).toBe('10');
  expect(vsctl('get', 'Port', 'inv-p1', '_uuid')).toBe(original);
  await screen(page, 'qinq-rolled-back');
});

test('managed internal Port deletion preserves its parent and restores fresh child identities', async ({
  page,
  context,
}) => {
  const account = 'browser-internal-delete';
  await login(page, account);
  await clean(context);
  const parent = (
    await get(context, '/bridges?filter=br-ui-parent')
  ).items.find((b: { name: string }) => b.name === 'br-ui-parent');
  expect(parent).toBeTruthy();
  const parentBinding = {
    management_id: parent.management_id,
    ovs_uuid: parent.ovs_uuid,
    table: 'Bridge',
    instance_generation: parent.instance_generation,
  };
  const originalMembers = vsctl('get', 'Bridge', 'br-ui-parent', 'ports');
  const localPort = vsctl('get', 'Port', 'br-ui-parent', '_uuid');
  const localInterface = vsctl('get', 'Interface', 'br-ui-parent', '_uuid');
  let c = await get(context, '/candidate');
  await command(
    context,
    '/candidate',
    {
      operation: 'stage',
      intents: [
        {
          intent_id: crypto.randomUUID(),
          operation: 'port.create-internal',
          name: 'pi-ui-delete',
          vlan_id: 20,
          object: parentBinding,
        },
      ],
    },
    'PATCH',
    c.revision,
  );
  c = await get(context, '/candidate');
  const original = c.intents[0].object;
  await page.goto(fixture.origin + '/changes/candidate');
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  await chooseDecision(page, 'Confirm configuration', 'confirmed');
  c = await get(context, '/candidate');
  await command(
    context,
    '/candidate',
    {
      operation: 'stage',
      intents: [
        {
          intent_id: crypto.randomUUID(),
          operation: 'port.delete-internal',
          object: original,
        },
      ],
    },
    'PATCH',
    c.revision,
  );
  c = await get(context, '/candidate');
  const replacement = c.intents[0].internal_port_deletion.replacement.port;
  expect(replacement.management_id).not.toBe(original.management_id);
  expect(vsctl('get', 'Port', 'pi-ui-delete', '_uuid')).toBe(original.ovs_uuid);
  await page.goto(fixture.origin + '/changes/candidate');
  await expect(
    page.getByText('Delete internal access Port and Interface', {
      exact: true,
    }),
  ).toBeVisible();
  const diff = page.getByRole('region', { name: 'Configuration Diff' });
  await expect(diff).toContainText(
    'br-ui-parent → pi-ui-delete · internal · access VLAN 20',
  );
  await expect(diff.getByRole('link')).toHaveCount(0);
  const standard = await diff.innerText();
  await screen(page, 'internal-port-delete-standard');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page
    .getByText('Original and reserved replacement identities', { exact: true })
    .click();
  await expect(page.locator('pre')).toContainText(replacement.management_id);
  expect(await diff.innerText()).toBe(standard);
  await screen(page, 'internal-port-delete-expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  for (const [width, height, device] of [
    [900, 1000, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    await screen(page, 'internal-port-delete-' + device + '-review');
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
  }
  await page.setViewportSize({ width: 1280, height: 1000 });
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  await expect(page.getByTestId('identity-replacements')).toContainText(
    'reserved',
  );
  await expect(
    page.getByRole('link', { name: 'Open restored Port' }),
  ).toHaveCount(0);
  expect(vsctl('--if-exists', 'get', 'Port', 'pi-ui-delete', '_uuid')).toBe('');
  expect(vsctl('get', 'Bridge', 'br-ui-parent', 'ports')).toBe(originalMembers);
  await screen(page, 'internal-port-delete-awaiting-confirmation');
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  await expect(page.getByTestId('identity-replacements')).toContainText(
    'restored',
  );
  await screen(page, 'internal-port-delete-rolled-back');
  const transaction = await get(
    context,
    new URL(page.url()).pathname.replace('/changes', ''),
  );
  expect(transaction.identity_replacements).toHaveLength(2);
  expect(
    transaction.identity_replacements.every(
      (r: { state: string }) => r.state === 'restored',
    ),
  ).toBe(true);
  const listed = await get(context, '/transactions');
  expect(
    listed.items.find((r: { id: string }) => r.id === transaction.id)
      .identity_replacements,
  ).toEqual(transaction.identity_replacements);
  expect(
    (
      await context.request.get(
        fixture.origin + '/api/v1/ports/' + original.management_id,
      )
    ).status(),
  ).toBe(404);
  expect(vsctl('get', 'Port', 'pi-ui-delete', '_uuid')).toBe(
    replacement.ovs_uuid,
  );
  expect(vsctl('get', 'Bridge', 'br-ui-parent', '_uuid')).toBe(parent.ovs_uuid);
  expect(vsctl('get', 'Port', 'br-ui-parent', '_uuid')).toBe(localPort);
  expect(vsctl('get', 'Interface', 'br-ui-parent', '_uuid')).toBe(
    localInterface,
  );
  expect(vsctl('get', 'Port', 'pi-ui-delete', 'tag')).toBe('20');
  expect(
    (await get(context, '/bridges/' + parent.management_id)).ovs_uuid,
  ).toBe(parent.ovs_uuid);
  await page.getByRole('link', { name: 'Open restored Port' }).click();
  await expect(page).toHaveURL(
    fixture.origin + '/ports/' + replacement.management_id,
  );
  await expect(
    page.getByRole('heading', { name: 'pi-ui-delete', exact: true }),
  ).toBeVisible();
});

test('internal access Port preserves its parent through responsive review and guarded rollback', async ({
  page,
  context,
}) => {
  const account = 'browser-internal-port';
  await login(page, account);
  await clean(context);
  const bridges = await get(context, '/bridges?filter=br-ui-parent');
  const parent = bridges.items.find(
    (b: { name: string }) => b.name === 'br-ui-parent',
  );
  expect(parent).toBeTruthy();
  const binding = {
    management_id: parent.management_id,
    ovs_uuid: parent.ovs_uuid,
    table: 'Bridge',
    instance_generation: parent.instance_generation,
  };
  const before = vsctl('get', 'Bridge', 'br-ui-parent', 'ports');
  let c = await get(context, '/candidate');
  await command(
    context,
    '/candidate',
    {
      operation: 'stage',
      intents: [
        {
          intent_id: crypto.randomUUID(),
          operation: 'port.create-internal',
          object: binding,
          name: 'pi-ui-create',
          vlan_id: 20,
        },
      ],
    },
    'PATCH',
    c.revision,
  );
  c = await get(context, '/candidate');
  const created = c.intents[0];
  expect(vsctl('--if-exists', 'get', 'Port', 'pi-ui-create', '_uuid')).toBe('');
  await page.goto(fixture.origin + '/changes/candidate');
  await expect(
    page.getByText('New internal access Port and Interface', { exact: true }),
  ).toBeVisible();
  const diff = page.getByRole('region', { name: 'Configuration Diff' });
  await expect(diff).toContainText(
    'br-ui-parent → pi-ui-create · internal · access VLAN 20',
  );
  await expect(diff.getByRole('link')).toHaveCount(0);
  const standard = await diff.innerText();
  await screen(page, 'internal-port-standard');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page
    .getByText('Parent binding and new object identities', { exact: true })
    .click();
  await expect(page.locator('pre')).toContainText(
    created.internal_port_creation.interface.management_id,
  );
  expect(await diff.innerText()).toBe(standard);
  await screen(page, 'internal-port-expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  for (const [width, height, device] of [
    [900, 1000, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await screen(page, 'internal-port-' + device + '-review');
  }
  await page.setViewportSize({ width: 1280, height: 1000 });
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  expect(vsctl('get', 'Port', 'pi-ui-create', 'tag')).toBe('20');
  expect(vsctl('get', 'Port', 'pi-ui-create', '_uuid')).toBe(
    created.object.ovs_uuid,
  );
  expect(vsctl('get', 'Bridge', 'br-ui-parent', '_uuid')).toBe(parent.ovs_uuid);
  await screen(page, 'internal-port-awaiting');
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  expect(vsctl('--if-exists', 'get', 'Port', 'pi-ui-create', '_uuid')).toBe('');
  expect(
    vsctl('--if-exists', 'get', 'Interface', 'pi-ui-create', '_uuid'),
  ).toBe('');
  expect(vsctl('get', 'Bridge', 'br-ui-parent', 'ports')).toBe(before);
  expect(
    (
      await context.request.get(
        fixture.origin + '/api/v1/ports/' + created.object.management_id,
      )
    ).status(),
  ).toBe(404);
  expect(
    (await get(context, '/bridges/' + parent.management_id)).ovs_uuid,
  ).toBe(parent.ovs_uuid);
  await screen(page, 'internal-port-rolled-back');
});

test('managed Bridge deletion reviews identity changes and restores fresh objects', async ({
  page,
  context,
}) => {
  const account = 'browser-bridge-delete';
  await login(page, account);
  await clean(context);
  let c = await get(context, '/candidate');
  await command(
    context,
    '/candidate',
    {
      operation: 'stage',
      intents: [
        {
          intent_id: crypto.randomUUID(),
          operation: 'bridge.create-isolated',
          name: 'br-ui-delete',
        },
      ],
    },
    'PATCH',
    c.revision,
  );
  c = await get(context, '/candidate');
  const original = c.intents[0].object;
  await page.goto(fixture.origin + '/changes/candidate');
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  await chooseDecision(page, 'Confirm configuration', 'confirmed');
  c = await get(context, '/candidate');
  await command(
    context,
    '/candidate',
    {
      operation: 'stage',
      intents: [
        {
          intent_id: crypto.randomUUID(),
          operation: 'bridge.delete-isolated',
          object: original,
        },
      ],
    },
    'PATCH',
    c.revision,
  );
  c = await get(context, '/candidate');
  const replacement = c.intents[0].bridge_deletion.replacement.bridge;
  expect(replacement.management_id).not.toBe(original.management_id);
  expect(vsctl('get', 'Bridge', 'br-ui-delete', '_uuid')).toBe(
    original.ovs_uuid,
  );
  await page.goto(fixture.origin + '/changes/candidate');
  await expect(
    page.getByText('Rollback recreates with new identities', { exact: true }),
  ).toBeVisible();
  const diff = page.getByRole('region', { name: 'Configuration Diff' });
  const standard = await diff.innerText();
  await screen(page, 'bridge-delete-standard');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await page
    .getByText('Original and reserved replacement identities', { exact: true })
    .click();
  await expect(page.locator('pre')).toContainText(replacement.management_id);
  expect(await diff.innerText()).toBe(standard);
  await screen(page, 'bridge-delete-expert');
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  for (const [width, height, device] of [
    [900, 1000, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    await screen(page, 'bridge-delete-' + device + '-review');
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
  }
  await page.setViewportSize({ width: 1280, height: 1000 });
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  await expect(page.getByTestId('identity-replacements')).toContainText(
    'reserved',
  );
  await expect(
    page.getByRole('link', { name: 'Open restored Bridge' }),
  ).toHaveCount(0);
  expect(vsctl('--if-exists', 'get', 'Bridge', 'br-ui-delete', '_uuid')).toBe(
    '',
  );
  await screen(page, 'bridge-delete-awaiting-confirmation');
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  await expect(page.getByTestId('identity-replacements')).toContainText(
    'restored',
  );
  await screen(page, 'bridge-delete-rolled-back');
  const transaction = await get(
    context,
    new URL(page.url()).pathname.replace('/changes', ''),
  );
  expect(transaction.identity_replacements).toHaveLength(3);
  expect(
    transaction.identity_replacements.every(
      (r: { state: string }) => r.state === 'restored',
    ),
  ).toBe(true);
  const listed = await get(context, '/transactions');
  expect(
    listed.items.find((r: { id: string }) => r.id === transaction.id)
      .identity_replacements,
  ).toEqual(transaction.identity_replacements);
  expect(
    (
      await context.request.get(
        fixture.origin + '/api/v1/bridges/' + original.management_id,
      )
    ).status(),
  ).toBe(404);
  expect(vsctl('get', 'Bridge', 'br-ui-delete', '_uuid')).toBe(
    replacement.ovs_uuid,
  );
  await page.getByRole('link', { name: 'Open restored Bridge' }).click();
  await expect(page).toHaveURL(
    fixture.origin + '/bridges/' + replacement.management_id,
  );
  await expect(
    page.getByRole('heading', { name: 'br-ui-delete', exact: true }),
  ).toBeVisible();
});

test('isolated Bridge staged by API uses shared responsive Diff, Safe Apply and rollback', async ({
  page,
  context,
}) => {
  await login(page, 'browser-bridge');
  await clean(context);
  const c = await get(context, '/candidate');
  await command(
    context,
    '/candidate',
    {
      operation: 'stage',
      intents: [
        {
          intent_id: crypto.randomUUID(),
          operation: 'bridge.create-isolated',
          name: 'br-ui-create',
        },
      ],
    },
    'PATCH',
    c.revision,
  );
  expect(vsctl('--if-exists', 'get', 'Bridge', 'br-ui-create', '_uuid')).toBe(
    '',
  );
  await page.goto(fixture.origin + '/changes/candidate');
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  const diff = page.getByRole('region', { name: 'Configuration Diff' });
  await expect(diff).toContainText('br-ui-create');
  const standard = await diff.innerText();
  await screen(page, 'bridge-create-standard');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  expect(await diff.innerText()).toBe(standard);
  await screen(page, 'bridge-create-expert');
  await page.setViewportSize({ width: 900, height: 1000 });
  await expect(
    page.getByRole('button', { name: 'Validate Candidate', exact: true }),
  ).toBeDisabled();
  await screen(page, 'bridge-create-tablet-review');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole('button', { name: 'Validate Candidate', exact: true }),
  ).toBeDisabled();
  await screen(page, 'bridge-create-mobile-review');
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.setViewportSize({ width: 1280, height: 1000 });
  await validate(page);
  await prepareApply(page, 'browser-bridge');
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  const inventory = await get(context, '/bridges?filter=br-ui-create');
  const bridge = inventory.items.find(
    (b: { name: string }) => b.name === 'br-ui-create',
  );
  expect(bridge?.port_refs).toHaveLength(1);
  expect(vsctl('get', 'Interface', 'br-ui-create', 'ofport')).toBe('65534');
  await screen(page, 'bridge-create-awaiting-confirmation');
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  expect(vsctl('--if-exists', 'get', 'Bridge', 'br-ui-create', '_uuid')).toBe(
    '',
  );
  expect(vsctl('--if-exists', 'get', 'Port', 'br-ui-create', '_uuid')).toBe('');
  expect(
    vsctl('--if-exists', 'get', 'Interface', 'br-ui-create', '_uuid'),
  ).toBe('');
  await screen(page, 'bridge-create-rolled-back');
});
test.beforeEach(async ({ page }) => {
  const errors: string[] = [];
  pageErrors.set(page, errors);
  page.on('pageerror', (e) => errors.push(e.message));
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', () => {
      throw new Error('CSP violation');
    });
  });
});
test.afterEach(async ({ page }) => {
  expect(pageErrors.get(page)).toEqual([]);
});

test('real login, native identity, approved depth and responsive responsibilities', async ({
  page,
  context,
}) => {
  await login(page);
  await clean(context);
  await screen(page, 'ports-standard-light');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await expect(
    page.getByRole('columnheader', { name: 'Identity / source', exact: true }),
  ).toBeVisible();
  await page
    .getByRole('button', { name: 'Toggle color theme', exact: true })
    .click();
  await screen(page, 'ports-expert-dark');
  await openPort(page, 'inv-p2');
  await expect(page.getByText(/QinQ · service VLAN 200/)).toBeVisible();
  await expect(
    page.getByRole('link', { name: 'Edit VLAN intent →' }),
  ).toHaveCount(0);
  await page.getByRole('link', { name: /^Bridge ·/ }).click();
  await expect(
    page.getByRole('heading', { name: 'Bridge', exact: true }),
  ).toBeVisible();
  const bridgeURL = page.url();
  await page.reload();
  expect(page.url()).toBe(bridgeURL);
  await openPort(page, 'inv-p1');
  const portURL = page.url();
  await page.reload();
  expect(page.url()).toBe(portURL);
  await page.setViewportSize({ width: 900, height: 1000 });
  await expect(
    page.getByText('Use a desktop to prepare a configuration change.'),
  ).toBeVisible();
  await screen(page, 'port-tablet');
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Expert', exact: true }).click();
  await screen(page, 'port-mobile');
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.setViewportSize({ width: 1280, height: 1100 });
  await page.evaluate(() => {
    document.documentElement.style.fontSize = '200%';
    document.body.style.fontSize = '28px';
  });
  await screen(page, 'port-large-text');
  expect(
    await page
      .locator('.brand-mark')
      .evaluate(
        (el) =>
          el.scrollHeight <= el.clientHeight &&
          el.scrollWidth <= el.clientWidth,
      ),
  ).toBe(true);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.reload();
  await page.keyboard.press('Tab');
  await expect(
    page.getByRole('link', { name: 'Skip to content' }),
  ).toBeFocused();
  const outline = await page
    .getByRole('link', { name: 'Skip to content' })
    .evaluate((el) => getComputedStyle(el).outlineWidth);
  expect(parseFloat(outline)).toBeGreaterThanOrEqual(3);
});

test('Candidate to Safe Apply recovers a lost real admission reply, refreshes and links Job / Audit', async ({
  page,
  context,
}) => {
  await login(page);
  await clean(context);
  await stage(page, '40');
  expect(vsctl('get', 'Port', 'inv-p1', 'tag')).not.toBe('40');
  await validate(page);
  await screen(page, 'validation-diff');
  await prepareApply(page);
  let posts = 0;
  await page.route('**/api/v1/transactions', async (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    posts++;
    const actual = await route.fetch();
    expect(actual.status()).toBe(202);
    await route.abort('failed');
  });
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await expect(
    page.getByRole('button', { name: 'Recover original request' }),
  ).toBeVisible();
  await screen(page, 'lost-reply-unknown');
  const secondTab = await context.newPage();
  await secondTab.goto(page.url());
  await expect(
    secondTab.getByRole('button', { name: 'Recover original request' }),
  ).toBeVisible();
  await expect(
    secondTab.getByRole('button', { name: 'Start Safe Apply', exact: true }),
  ).toBeDisabled();
  await page.reload();
  let releaseReceipt!: () => void;
  const holdReceipt = new Promise<void>((r) => {
    releaseReceipt = r;
  });
  let fetchedReceipt!: () => void;
  const receiptFetched = new Promise<void>((r) => {
    fetchedReceipt = r;
  });
  await page.route('**/api/v1/requests/**', async (route) => {
    const response = await route.fetch();
    fetchedReceipt();
    await holdReceipt;
    await route.fulfill({ response });
  });
  try {
    await page
      .getByRole('button', { name: 'Recover original request' })
      .click();
    await receiptFetched;
    await secondTab
      .getByRole('button', { name: 'Recover original request' })
      .click();
    await expect(
      secondTab.getByText('Original outcome remains unknown:', {
        exact: false,
      }),
    ).toContainText('REQUEST_IN_PROGRESS_IN_ANOTHER_TAB');
  } finally {
    releaseReceipt();
  }
  await awaiting(page);
  await secondTab.close();
  expect(posts).toBe(1);
  const transactionURL = page.url();
  const t = await get(
    context,
    '/transactions/' + transactionURL.split('/').pop(),
  );
  const currentSession = await get(context, '/session');
  const staleID = requestID();
  const stale = await context.request.post(
    fixture.origin + '/api/v1/transactions/' + t.id + '/decisions',
    {
      data: {
        request_id: staleID,
        decision: 'confirm',
        expected_sequence: String(BigInt(t.sequence) - BigInt(1)),
      },
      headers: {
        Origin: fixture.origin,
        'X-OVS-CSRF-Token': currentSession.csrf_token,
        'Idempotency-Key': staleID,
        'X-OVS-Request-Epoch': currentSession.request_epochs.management,
      },
    },
  );
  expect(stale.status()).toBe(409);
  const staleProblem = await stale.json();
  expect(staleProblem.code).toBe('TRANSACTION_VERSION_CHANGED');
  expect(staleProblem.command_effect).toBe('not-started');
  expect(staleProblem.request_id).toBe(staleID);
  await page.reload();
  await awaiting(page);
  expect(page.url()).toBe(transactionURL);
  await screen(page, 'safe-apply-awaiting');
  await chooseDecision(page, 'Confirm configuration', 'confirmed');
  expect(vsctl('get', 'Port', 'inv-p1', 'tag')).toBe('40');
  await expect
    .poll(async () => (await get(context, '/candidate')).intents.length)
    .toBe(0);
  await page.getByRole('link', { name: 'Execution Job', exact: true }).click();
  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'Jobs', exact: true }),
  ).toBeVisible();
  const audit = await get(context, '/audit?correlation_id=' + t.correlation_id);
  expect(audit.items.length).toBeGreaterThan(0);
  await page.goto(fixture.origin + '/operations/audit/' + audit.items[0].id);
  await expect(page.getByText(t.correlation_id, { exact: true })).toBeVisible();
  await screen(page, 'audit-correlated');
  const stored = await page.evaluate(() => JSON.stringify(localStorage));
  expect(stored).not.toContain('csrf_token');
  expect(stored).not.toContain(fixture.accounts['browser-admin']);
});

test('real external writer exposes three-way conflict and explicit snapshot-bound rebase', async ({
  page,
  context,
}) => {
  await login(page);
  await clean(context);
  await stage(page, '50');
  vsctl('set', 'Port', 'inv-p1', 'tag=41');
  await expect(
    page.getByRole('heading', { name: 'Resolve against this snapshot' }),
  ).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Validate Candidate', exact: true }),
  ).toBeDisabled();
  await screen(page, 'candidate-real-conflict');
  await page
    .getByRole('combobox', { name: /^Port / })
    .selectOption('keep-mine');
  // A new external snapshot needs a fresh human choice, not the previous consent.
  const currentTag = page
    .getByRole('region', { name: 'Configuration Diff' })
    .getByRole('row')
    .filter({ has: page.getByRole('rowheader').filter({ hasText: 'tag' }) })
    .getByRole('cell')
    .nth(1);
  vsctl('set', 'Port', 'inv-p1', 'tag=42');
  await expect(currentTag).toHaveText('42');
  await expect(page.getByRole('combobox', { name: /^Port / })).toHaveValue('');
  await expect(
    page.getByRole('button', { name: 'Rebase reviewed choices' }),
  ).toBeDisabled();
  vsctl('set', 'Port', 'inv-p1', 'tag=41');
  // Wait for this same native value in the visible three-way Diff before choosing.
  await expect(currentTag).toHaveText('41');
  await page
    .getByRole('combobox', { name: /^Port / })
    .selectOption('keep-mine');
  await page.getByRole('button', { name: 'Rebase reviewed choices' }).click();
  await expect(
    page.getByRole('button', { name: 'Validate Candidate', exact: true }),
  ).toBeEnabled();
  const c = await get(context, '/candidate');
  expect(c.intents[0].before.tag).toBe(41);
  expect(c.intents[0].value.tag).toBe(50);
  expect(vsctl('get', 'Port', 'inv-p1', 'tag')).toBe('41');
  await clean(context);
});

test('real provider outage and manager outage remain stale / unavailable', async ({
  page,
  context,
}) => {
  await login(page);
  await clean(context);
  execFileSync('ovs-appctl', ['-t', `${fixture.ovsDirectory}/db.ctl`, 'exit']);
  try {
    await expect(
      page.getByText('Stale observation.', { exact: false }),
    ).toBeVisible();
    await screen(page, 'ports-provider-stale');
    await page.getByRole('link', { name: 'inv-p1', exact: true }).click();
    await expect(
      page.getByRole('link', { name: 'Edit VLAN intent →' }),
    ).toHaveCount(0);
  } finally {
    execFileSync(
      'ovsdb-server',
      [
        fixture.database,
        `--remote=punix:${fixture.dbSocket}`,
        `--pidfile=${fixture.ovsDirectory}/db.pid`,
        `--unixctl=${fixture.ovsDirectory}/db.ctl`,
        '--detach',
        '--no-chdir',
        '--overwrite-pidfile',
      ],
      {
        env: {
          ...process.env,
          OVS_RUNDIR: fixture.ovsDirectory,
          OVS_LOGDIR: fixture.ovsDirectory,
          OVS_DBDIR: fixture.ovsDirectory,
        },
        stdio: 'pipe',
      },
    );
  }
  await expect(
    page.getByRole('link', { name: 'Edit VLAN intent →' }),
  ).toBeVisible();
  unit('stop', 'mgrd');
  try {
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: 'Current authorization or connection' }),
    ).toBeVisible();
    await screen(page, 'manager-unavailable');
  } finally {
    unit('start', 'mgrd');
  }
  await expect(
    page.getByRole('link', { name: 'Edit VLAN intent →' }),
  ).toBeVisible();
});

test('reader depth never grants edit rights; revoked permissions fence an already-returned response', async ({
  page,
  browser,
}) => {
  await login(page, 'browser-reader');
  await page.getByRole('button', { name: 'Standard', exact: true }).click();
  await openPort(page, 'inv-p1');
  await expect(
    page.getByText('Current permissions do not allow VLAN changes.'),
  ).toBeVisible();
  await screen(page, 'reader-expert');
  const adminContext = await browser.newContext({ ignoreHTTPSErrors: true });
  const adminPage = await adminContext.newPage();
  await login(adminPage);
  const peerContext = await browser.newContext({ ignoreHTTPSErrors: true });
  const peer = await peerContext.newPage();
  await login(peer, 'browser-revoke');
  let release!: () => void;
  const held = new Promise<void>((r) => {
    release = r;
  });
  let fetched!: () => void;
  const fetchedOnce = new Promise<void>((r) => {
    fetched = r;
  });
  await peer.route('**/api/v1/ports', async (route) => {
    const response = await route.fetch();
    fetched();
    await held;
    await route.fulfill({ response });
  });
  await peer.getByRole('button', { name: 'Refresh inventory' }).click();
  await fetchedOnce;
  const users = await get(adminContext, '/users');
  const target = users.items.find(
    (u: { username: string }) => u.username === 'browser-revoke',
  );
  const adminSession = await get(adminContext, '/session');
  const elevated = await adminContext.request.post(
    fixture.origin + '/api/v1/session/reauthentication',
    {
      data: { password: fixture.accounts['browser-admin'] },
      headers: {
        Origin: fixture.origin,
        'X-OVS-CSRF-Token': adminSession.csrf_token,
      },
    },
  );
  expect(elevated.ok()).toBe(true);
  await command(
    adminContext,
    '/users/' + target.id,
    { disabled: true, role_ids: target.role_ids },
    'PATCH',
    target.revision,
  );
  // Navigation invalidates the in-flight batch before its old authorized body is released.
  await peer.getByRole('link', { name: 'Administration', exact: true }).click();
  release();
  await expect(
    peer.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  await expect(
    peer.getByRole('link', { name: 'inv-p1', exact: true }),
  ).toHaveCount(0);
  await screen(peer, 'revoked-session');
  await peerContext.close();
  await adminContext.close();
});

test('existing Safe Apply supports tablet review and mobile rollback without new transactions', async ({
  page,
  context,
}) => {
  await login(page);
  await clean(context);
  await stage(page, '60');
  await validate(page);
  await prepareApply(page);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  await page.setViewportSize({ width: 900, height: 1000 });
  await screen(page, 'safe-apply-tablet');
  await expect(
    page.getByRole('button', { name: 'Confirm configuration', exact: true }),
  ).toBeEnabled();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole('button', { name: 'Confirm configuration', exact: true }),
  ).toBeDisabled();
  await screen(page, 'safe-apply-mobile');
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  expect(vsctl('get', 'Port', 'inv-p1', 'tag')).toBe('41');
});

test('closing the browser leaves the actual 120-second server deadline and rollback authoritative', async ({
  page,
  context,
}) => {
  test.setTimeout(190000);
  await login(page, 'browser-deadline');
  await clean(context);
  await stage(page, '70');
  await validate(page);
  await prepareApply(page, 'browser-deadline');
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  const url = page.url(),
    id = url.split('/').pop();
  const original = await get(context, '/transactions/' + id);
  expect(
    Date.parse(original.confirmation_deadline) -
      Date.parse(original.server_time),
  ).toBeGreaterThan(100000);
  await page.close();
  await expect
    .poll(async () => (await get(context, '/transactions/' + id)).safe_apply, {
      timeout: 145000,
      intervals: [2000],
    })
    .toBe('rolled-back');
  const restored = await get(context, '/transactions/' + id);
  expect(restored.confirmation_deadline).toBe(original.confirmation_deadline);
  expect(vsctl('get', 'Port', 'inv-p1', 'tag')).toBe('41');
  const reopened = await context.newPage();
  await reopened.goto(url);
  await expect(reopened.getByTestId('safe-state')).toHaveText('rolled-back');
  await expect(
    reopened.getByRole('button', {
      name: 'Confirm configuration',
      exact: true,
    }),
  ).toBeDisabled();
  await screen(reopened, 'server-deadline-rollback');
});

test('explicit internal Interface MTU follows responsive Candidate review, actual device proof and exact rollback', async ({
  page,
}) => {
  const account = 'browser-mtu';
  await login(page, account);
  await clean(page.context());
  const before = await mtuEditor(page);
  const originalPort = vsctl('get', 'Port', 'pi-ui-mtu', '_uuid');
  const originalBridge = vsctl('get', 'Bridge', 'br-ui-parent', '_uuid');
  try {
    await page.getByLabel('MTU request (bytes)', { exact: true }).fill('65536');
    await page
      .getByRole('button', { name: 'Stage in Candidate', exact: true })
      .click();
    await expect(page.getByRole('alert')).toHaveText(
      'Enter one MTU request from 576 to 65535 bytes.',
    );
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe('1500');
    await screen(page, 'interface-mtu-editor');
    await stageMTU(page, '2000');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe('1500');
    const diff = page.getByRole('region', { name: 'Configuration Diff' });
    await expect(diff).toContainText('mtu_request');
    await expect(
      diff.getByRole('cell', { name: '1500', exact: true }),
    ).toHaveCount(2);
    await expect(
      diff.getByRole('cell', { name: '2000', exact: true }),
    ).toHaveCount(1);
    const standard = await diff.innerText();
    await screen(page, 'interface-mtu-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    expect(await diff.innerText()).toBe(standard);
    await screen(page, 'interface-mtu-expert');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    for (const [width, height, device] of [
      [900, 1000, 'tablet'],
      [390, 844, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await expect(
        page.getByRole('button', { name: 'Validate Candidate', exact: true }),
      ).toBeDisabled();
      await screen(page, `interface-mtu-${device}-review`);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
    }
    await page.setViewportSize({ width: 1280, height: 1000 });
    await validate(page);
    await prepareApply(page, account);
    await page
      .getByRole('button', { name: 'Start Safe Apply', exact: true })
      .click();
    await awaiting(page);
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu')).toBe('2000');
    expect(
      JSON.parse(
        execFileSync('ip', ['-j', 'link', 'show', 'dev', 'pi-ui-mtu'], {
          encoding: 'utf8',
        }),
      )[0].mtu,
    ).toBe(2000);
    await screen(page, 'interface-mtu-awaiting-confirmation');
    vsctl(
      'set',
      'Interface',
      'pi-ui-mtu',
      'external_ids:unrelated=preserve',
      'other_config:opaque=preserve',
    );
    await expect
      .poll(
        async () =>
          (await get(page.context(), `/interfaces/${before.management_id}`))
            .fields.external_ids.value.unrelated,
      )
      .toBe('preserve');
    await chooseDecision(page, 'Request rollback', 'rolled-back');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe('1500');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu')).toBe('1500');
    expect(
      JSON.parse(
        execFileSync('ip', ['-j', 'link', 'show', 'dev', 'pi-ui-mtu'], {
          encoding: 'utf8',
        }),
      )[0].mtu,
    ).toBe(1500);
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', '_uuid')).toBe(
      before.ovs_uuid,
    );
    expect(vsctl('get', 'Port', 'pi-ui-mtu', '_uuid')).toBe(originalPort);
    expect(vsctl('get', 'Bridge', 'br-ui-parent', '_uuid')).toBe(
      originalBridge,
    );
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'other_config')).toBe(
      '{opaque=preserve}',
    );
    expect(
      vsctl('get', 'Interface', 'pi-ui-mtu', 'external_ids:unrelated'),
    ).toBe('preserve');
    await screen(page, 'interface-mtu-rolled-back');
    await mtuEditor(page);
    await stageMTU(page, '2200');
    await validate(page);
    await prepareApply(page, account);
    await page
      .getByRole('button', { name: 'Start Safe Apply', exact: true })
      .click();
    await awaiting(page);
    await chooseDecision(page, 'Confirm configuration', 'confirmed');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu')).toBe('2200');
    await screen(page, 'interface-mtu-confirmed');
  } finally {
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=1500');
  }
});

test('MTU drift, hidden native options, reader permissions and mobile direct edits remain gated', async ({
  page,
  browser,
}) => {
  await login(page, 'browser-mtu-drift');
  await clean(page.context());
  const item = await mtuEditor(page);
  try {
    await stageMTU(page, '2000');
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=1800');
    await expect(
      page.getByRole('region', { name: 'Configuration Diff' }),
    ).toContainText('1800');
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole('button', {
        name: 'Rebase reviewed choices',
        exact: true,
      }),
    ).toHaveCount(0);
    await expect(
      page.getByRole('heading', {
        name: 'Review and restage this MTU request',
        exact: true,
      }),
    ).toBeVisible();
    await screen(page, 'interface-mtu-drift');
    await clean(page.context());
    vsctl('set', 'Interface', 'pi-ui-mtu', 'options:unpublished=synthetic');
    await expect
      .poll(
        async () =>
          (await get(page.context(), `/interfaces/${item.management_id}`))
            .mtu_editable,
      )
      .toBe(false);
    const hidden = await get(
      page.context(),
      `/interfaces/${item.management_id}`,
    );
    expect(hidden.options).toEqual({});
    await page.goto(fixture.origin + `/interfaces/${item.management_id}/mtu`);
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-mtu-unavailable-dark');
    vsctl('clear', 'Interface', 'pi-ui-mtu', 'options');
    await expect
      .poll(
        async () =>
          (await get(page.context(), `/interfaces/${item.management_id}`))
            .mtu_editable,
      )
      .toBe(true);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(fixture.origin + `/interfaces/${item.management_id}/mtu`);
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByText('Use a desktop to prepare a configuration change.', {
        exact: true,
      }),
    ).toBeVisible();
    await screen(page, 'interface-mtu-mobile-editor');
    const reader = await browser.newContext({ ignoreHTTPSErrors: true });
    try {
      const readerPage = await reader.newPage();
      await login(readerPage, 'browser-mtu-reader');
      const observed = await get(reader, `/interfaces/${item.management_id}`);
      expect(observed.mtu_editable).toBe(false);
      expect(observed.fields.mtu_request.editable).toBe(false);
      await readerPage.goto(
        fixture.origin + `/interfaces/${item.management_id}/mtu`,
      );
      await expect(
        readerPage.getByRole('button', {
          name: 'Stage in Candidate',
          exact: true,
        }),
      ).toBeDisabled();
      await expect(
        readerPage.getByText(
          'Current permissions do not allow Interface MTU changes.',
          { exact: true },
        ),
      ).toBeVisible();
      await screen(readerPage, 'interface-mtu-reader');
    } finally {
      await reader.close();
    }
  } finally {
    vsctl('clear', 'Interface', 'pi-ui-mtu', 'options');
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=1500');
  }
});

test('automatic MTU original survives responsive review and exact native-empty rollback', async ({
  page,
}) => {
  const account = 'browser-mtu-defaults';
  await login(page, account);
  await clean(page.context());
  vsctl('set', 'Interface', 'pi-ui-mtu-peer', 'mtu_request=1800');
  vsctl('clear', 'Interface', 'pi-ui-mtu', 'mtu_request');
  try {
    await waitMTUDefault(page, null, 1800);
    const before = await mtuEditor(page);
    const originalPort = vsctl('get', 'Port', 'pi-ui-mtu', '_uuid');
    const originalBridge = vsctl('get', 'Bridge', 'br-ui-parent', '_uuid');
    await screen(page, 'interface-mtu-default-editor');
    await stageMTU(page, '2400');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe('[]');
    const diff = page.getByRole('region', { name: 'Configuration Diff' });
    await expect(diff).toContainText('Automatic MTU dependency (bytes)');
    await expect(
      diff.getByRole('cell', {
        name: 'Automatic · empty native request',
        exact: true,
      }),
    ).toHaveCount(2);
    await expect(
      diff.getByRole('cell', { name: '1800', exact: true }),
    ).toHaveCount(3);
    const standard = await diff.innerText();
    await screen(page, 'interface-mtu-default-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    expect(await diff.innerText()).toBe(standard);
    await screen(page, 'interface-mtu-default-expert');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    for (const [width, height, device] of [
      [900, 1000, 'tablet'],
      [390, 844, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await expect(
        page.getByRole('button', { name: 'Validate Candidate', exact: true }),
      ).toBeDisabled();
      await screen(page, `interface-mtu-default-${device}-review`);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
    }
    await page.setViewportSize({ width: 1280, height: 1000 });
    await validate(page);
    await prepareApply(page, account);
    await page
      .getByRole('button', { name: 'Start Safe Apply', exact: true })
      .click();
    await awaiting(page);
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu')).toBe('2400');
    expect(actualLinuxMTU()).toBe(2400);
    await screen(page, 'interface-mtu-default-awaiting-confirmation');
    await chooseDecision(page, 'Request rollback', 'rolled-back');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe('[]');
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu')).toBe('1800');
    expect(actualLinuxMTU()).toBe(1800);
    expect(vsctl('get', 'Interface', 'pi-ui-mtu', '_uuid')).toBe(
      before.ovs_uuid,
    );
    expect(vsctl('get', 'Port', 'pi-ui-mtu', '_uuid')).toBe(originalPort);
    expect(vsctl('get', 'Bridge', 'br-ui-parent', '_uuid')).toBe(
      originalBridge,
    );
    await screen(page, 'interface-mtu-default-rolled-back');
  } finally {
    vsctl('set', 'Interface', 'pi-ui-mtu-peer', 'mtu_request=1800');
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=1500');
  }
});

test('clear MTU confirms the actual automatic value, restores explicit originals and fences changed dependencies', async ({
  page,
}) => {
  const account = 'browser-mtu-default-drift';
  await login(page, account);
  await clean(page.context());
  vsctl('set', 'Interface', 'pi-ui-mtu-peer', 'mtu_request=1800');
  vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=2400');
  try {
    await waitMTUDefault(page, '2400', 1800);
    const item = await mtuEditor(page);
    expect(item.mtu_clearable).toBe(true);
    for (const decision of ['rollback', 'confirm']) {
      if (decision === 'confirm') await mtuEditor(page);
      await stageAutomaticMTU(page);
      expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe(
        '2400',
      );
      const diff = page.getByRole('region', { name: 'Configuration Diff' });
      await expect(diff).toContainText('Automatic MTU dependency (bytes)');
      await expect(
        diff.getByRole('cell', {
          name: 'Automatic · empty native request',
          exact: true,
        }),
      ).toHaveCount(1);
      if (decision === 'rollback')
        await screen(page, 'interface-mtu-clear-standard');
      await validate(page);
      await prepareApply(page, account);
      await page
        .getByRole('button', { name: 'Start Safe Apply', exact: true })
        .click();
      await awaiting(page);
      expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe('[]');
      expect(actualLinuxMTU()).toBe(1800);
      await chooseDecision(
        page,
        decision === 'rollback' ? 'Request rollback' : 'Confirm configuration',
        decision === 'rollback' ? 'rolled-back' : 'confirmed',
      );
      expect(vsctl('get', 'Interface', 'pi-ui-mtu', 'mtu_request')).toBe(
        decision === 'rollback' ? '2400' : '[]',
      );
      expect(actualLinuxMTU()).toBe(decision === 'rollback' ? 2400 : 1800);
      expect(vsctl('get', 'Interface', 'pi-ui-mtu', '_uuid')).toBe(
        item.ovs_uuid,
      );
      await screen(
        page,
        `interface-mtu-clear-${decision === 'rollback' ? 'rolled-back' : 'confirmed'}`,
      );
    }
    await waitMTUDefault(page, null, 1800);
    await mtuEditor(page);
    await stageMTU(page, '2400');
    vsctl('set', 'Interface', 'pi-ui-mtu-peer', 'mtu_request=2600');
    await expect(
      page.getByRole('region', { name: 'Configuration Diff' }),
    ).toContainText('2600');
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole('heading', {
        name: 'Review and restage this MTU request',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', {
        name: 'Rebase reviewed choices',
        exact: true,
      }),
    ).toHaveCount(0);
    await screen(page, 'interface-mtu-default-drift');
    await clean(page.context());
    vsctl('clear', 'Interface', 'pi-ui-mtu-peer', 'mtu_request');
    await expect
      .poll(
        async () =>
          (await get(page.context(), `/interfaces/${item.management_id}`))
            .mtu_editable,
      )
      .toBe(false);
    const unavailable = await get(
      page.context(),
      `/interfaces/${item.management_id}`,
    );
    expect(unavailable.fields.mtu_request.value).toEqual([]);
    expect(unavailable.mtu_default).toBeUndefined();
    await page.goto(fixture.origin + `/interfaces/${item.management_id}/mtu`);
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByText(
        'Default MTU needs proven, stable Bridge devices and at least one contributor.',
        { exact: true },
      ),
    ).toBeVisible();
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-mtu-default-unavailable-dark');
  } finally {
    vsctl('set', 'Interface', 'pi-ui-mtu-peer', 'mtu_request=1800');
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=1500');
  }
});
