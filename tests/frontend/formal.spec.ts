import { test, expect } from '@playwright/test';
import type { Page, BrowserContext, Request } from '@playwright/test';
import type {
  InterfacePage,
  TopologyNode,
} from '../../clients/typescript/public-v1.generated';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { execFileSync, spawnSync } from 'node:child_process';
import { requestID } from '../../frontend/src/api';
import { AuthDiagnostics } from './auth-diagnostics';

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
const authDiagnostics = new WeakMap<BrowserContext, AuthDiagnostics>();
const stopAuthDiagnostics = new WeakMap<BrowserContext, () => Promise<void>>();
const authContexts = new Map<string, Set<BrowserContext>>();

function watchAuthContext(context: BrowserContext): AuthDiagnostics {
  const existing = authDiagnostics.get(context);
  if (existing) return existing;
  const diagnostics = new AuthDiagnostics(fixture.origin);
  authDiagnostics.set(context, diagnostics);
  const id = test.info().testId;
  const contexts = authContexts.get(id) ?? new Set<BrowserContext>();
  contexts.add(context);
  authContexts.set(id, contexts);
  const requests = new Map<Request, number>();
  const pending = new Set<Promise<void>>();
  const started = (request: Request) => {
    const ticket = diagnostics.begin(
      request.url(),
      request.method(),
      'browser',
    );
    if (ticket !== null) requests.set(request, ticket);
  };
  const failed = (request: Request) => {
    diagnostics.failed(requests.get(request) ?? null);
    requests.delete(request);
  };
  const responded = (reply: import('@playwright/test').Response) => {
    const ticket = requests.get(reply.request());
    if (ticket === undefined) return;
    requests.delete(reply.request());
    const task = (async () => {
      // Successful bodies contain credentials or protected resources: never
      // read them for diagnostics. Failure parsing never retains its payload.
      const code = reply.ok()
        ? undefined
        : (await reply.json().catch(() => null))?.code;
      diagnostics.response(ticket, reply.status(), code);
    })().catch(() => {
      diagnostics.failed(ticket);
    });
    pending.add(task);
    void task.finally(() => pending.delete(task));
  };
  context.on('request', started);
  context.on('response', responded);
  context.on('requestfailed', failed);
  stopAuthDiagnostics.set(context, async () => {
    context.off('request', started);
    context.off('response', responded);
    context.off('requestfailed', failed);
    await Promise.allSettled(pending);
  });
  return diagnostics;
}

test.beforeEach(async ({ context }) => {
  watchAuthContext(context);
});

test.afterEach(async ({ context }, info) => {
  const contexts = authContexts.get(info.testId) ?? new Set([context]);
  await Promise.all(
    [...contexts].map(async (context) => {
      await stopAuthDiagnostics.get(context)?.();
    }),
  );
  const path = info.outputPath('auth-diagnostics.json');
  mkdirSync(info.outputDir, { recursive: true });
  writeFileSync(
    path,
    JSON.stringify(
      {
        version: 1,
        contexts: [...contexts].map((context) =>
          authDiagnostics.get(context)?.snapshot(),
        ),
      },
      null,
      2,
    ) + '\n',
  );
  await info.attach('auth-diagnostics', {
    path,
    contentType: 'application/json',
  });
  authContexts.delete(info.testId);
});

async function patchItem(context: BrowserContext, name: string) {
  const values: InterfacePage = await get(
    context,
    `/interfaces?filter=${name}`,
  );
  const item = values.items.find((i) => i.name === name);
  if (!item)
    throw new Error('The exact patch fixture Interface is unavailable');
  return item;
}

async function observedPatchReason(context: BrowserContext, name: string) {
  const values: InterfacePage = await get(
    context,
    `/interfaces?filter=${name}`,
  );
  // OVSDB commit precedes asynchronous inventory publication. Absence is a
  // pending observation for this bounded read poll, not a failed assertion.
  return values.items.find((i) => i.name === name)?.patch_peer?.reason;
}

async function nativeTypeDepth(page: Page, mode: 'Standard' | 'Expert') {
  const toggle = page.getByRole('button', { name: /^(Standard|Expert)$/ });
  await expect(toggle).toBeVisible();
  if ((await toggle.textContent()) !== mode) await toggle.click();
  await expect(toggle).toHaveText(mode);
  await expect(toggle).toHaveAttribute(
    'aria-pressed',
    String(mode === 'Expert'),
  );
}

function createPatchPair(suffix: string) {
  vsctl(
    'add-br',
    `br-patch-${suffix}a`,
    '--',
    'set',
    'Bridge',
    `br-patch-${suffix}a`,
    'datapath_type=dummy',
    'fail_mode=secure',
    '--',
    'add-br',
    `br-patch-${suffix}b`,
    '--',
    'set',
    'Bridge',
    `br-patch-${suffix}b`,
    'datapath_type=dummy',
    'fail_mode=secure',
    '--',
    'add-port',
    `br-patch-${suffix}a`,
    `patch-${suffix}a`,
    '--',
    'set',
    'Interface',
    `patch-${suffix}a`,
    'type=patch',
    `options:peer=patch-${suffix}b`,
    '--',
    'add-port',
    `br-patch-${suffix}b`,
    `patch-${suffix}b`,
    '--',
    'set',
    'Interface',
    `patch-${suffix}b`,
    'type=patch',
    `options:peer=patch-${suffix}a`,
  );
}

function clearPatchPair(suffix: string) {
  vsctl(
    '--no-wait',
    '--if-exists',
    'del-br',
    `br-patch-${suffix}a`,
    '--',
    '--if-exists',
    'del-br',
    `br-patch-${suffix}b`,
  );
}

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

async function getReply(context: BrowserContext, path: string) {
  const url = fixture.origin + '/api/v1' + path;
  const diagnostics = watchAuthContext(context);
  const ticket = diagnostics.begin(url, 'GET', 'helper');
  try {
    const response = await context.request.get(url);
    const code =
      ticket === null || response.ok()
        ? undefined
        : (await response.json().catch(() => null))?.code;
    diagnostics?.response(ticket, response.status(), code);
    return response;
  } catch {
    diagnostics?.failed(ticket);
    // Playwright transport errors may include request headers in their call
    // logs. Keep credentials out of JUnit/error-context as well as our summary.
    throw new Error(
      'Formal helper transport failed; see bounded auth diagnostics.',
    );
  }
}
async function get(context: BrowserContext, path: string) {
  const response = await getReply(context, path);
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
async function login(page: Page, name = 'browser-admin', inventory = true) {
  watchAuthContext(page.context());
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
  if (inventory) await portPage(page, 'inv-p1');
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

async function policingEditor(page: Page) {
  const item = (
    await get(page.context(), '/interfaces?filter=pi-ui-police')
  ).items.find((i: { name: string }) => i.name === 'pi-ui-police');
  expect(item?.policing_editable).toBe(true);
  await page.goto(fixture.origin + `/interfaces/${item.management_id}`);
  await page
    .getByRole('link', { name: 'Edit ingress policing →', exact: true })
    .click();
  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'Edit ingress policing', exact: true }),
  ).toBeVisible();
  return item;
}
async function stagePolicing(page: Page, mode: string, rate?: string) {
  await page.getByLabel('Policing mode', { exact: true }).selectOption(mode);
  if (rate !== undefined)
    await page
      .getByLabel(mode === 'bandwidth' ? 'Rate (kbit/s)' : 'Rate (kpps)', {
        exact: true,
      })
      .fill(rate);
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
}
async function waitInstalledPolicing(
  context: BrowserContext,
  id: string,
  rate: string | null,
  packet: string | null,
) {
  await expect
    .poll(async () => {
      const item = await get(context, `/interfaces/${id}`);
      const sample = item.linux_ingress_policing;
      return {
        availability: sample.availability,
        actions: sample.actions.map(
          (a: {
            bytes_per_second: string | null;
            packets_per_second: string | null;
          }) => [a.bytes_per_second, a.packets_per_second],
        ),
      };
    })
    .toEqual({
      availability: 'known',
      actions: rate === null && packet === null ? [] : [[rate, packet]],
    });
}

test('spanning tree observes native intent and daemon status with shared identity and responsive depth', async ({
  page,
  context,
}) => {
  await login(page, 'browser-tree');
  const writes: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions)/.test(r.url())
    )
      writes.push(r.method());
  });
  vsctl(
    'add-br',
    'br-tree-ui',
    '--',
    'set',
    'Bridge',
    'br-tree-ui',
    'datapath_type=dummy',
    'rstp_enable=true',
    'other_config:rstp-priority=4096',
    '--',
    'add-port',
    'br-tree-ui',
    'tree-ui-p',
    '--',
    'set',
    'Interface',
    'tree-ui-p',
    'type=dummy',
    '--',
    'add-bond',
    'br-tree-ui',
    'tree-ui-bond',
    'tree-ui-b1',
    'tree-ui-b2',
    '--',
    'set',
    'Interface',
    'tree-ui-b1',
    'type=dummy',
    '--',
    'set',
    'Interface',
    'tree-ui-b2',
    'type=dummy',
  );
  try {
    await expect
      .poll(
        async () =>
          (await get(context, '/inventory/spanning-tree?filter=br-tree-ui'))
            .items.length,
      )
      .toBe(1);
    const row = (
      await get(context, '/inventory/spanning-tree?filter=br-tree-ui')
    ).items[0];
    const path = '/bridges/' + row.management_id + '/spanning-tree';
    await expect
      .poll(
        async () =>
          (await get(context, path)).spanning_tree.runtime.rstp_bridge_id
            .availability,
      )
      .toBe('known');
    await page.getByRole('link', { name: 'STP / RSTP', exact: true }).click();
    await page
      .getByRole('button', { name: 'Filter Bridges', exact: true })
      .click();
    await expect(page).toHaveURL(/\/switching\/spanning-tree\?limit=25$/);
    await page.getByLabel('Bridge name', { exact: true }).fill('br-tree-ui');
    await page
      .getByRole('button', { name: 'Filter Bridges', exact: true })
      .click();
    await page.getByRole('link', { name: 'br-tree-ui', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'br-tree-ui', level: 2, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText('excluded bond', { exact: false }).first(),
    ).toBeVisible();
    await expect(
      page.getByText('excluded internal', { exact: false }).first(),
    ).toBeVisible();
    await expect(
      page.getByText('Native default (unset)', { exact: true }).first(),
    ).toBeVisible();
    await expect(page.getByText('4096', { exact: true })).toBeVisible();
    await expect(
      page.getByRole('region', { name: 'Spanning tree parameter checks' }),
    ).toContainText('Basic parameter checks passed.');
    await screen(page, 'spanning-tree-standard');
    await nativeTypeDepth(page, 'Expert');
    await page
      .getByText('Native identity and configuration availability', {
        exact: true,
      })
      .click();
    await expect(
      page.getByText(row.management_id, { exact: true }),
    ).toBeVisible();
    await page
      .getByRole('link', { name: 'Review Bridge', exact: true })
      .focus();
    expect(await page.evaluate(() => document.activeElement?.textContent)).toBe(
      'Review Bridge',
    );
    await screen(page, 'spanning-tree-expert');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'spanning-tree-dark');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await page
      .getByRole('link', { name: 'Review Bridge', exact: true })
      .click();
    await page
      .getByRole('link', { name: 'Review STP / RSTP', exact: true })
      .click();
    await expect(page).toHaveURL(
      new RegExp('/switching/spanning-tree/' + row.management_id + '$'),
    );
    await nativeTypeDepth(page, 'Standard');
    await page.setViewportSize({ width: 820, height: 1180 });
    await screen(page, 'spanning-tree-tablet');
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(
      page.getByRole('heading', { name: 'STP / RSTP', exact: true }),
    ).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await screen(page, 'spanning-tree-mobile');
    expect(writes).toEqual([]);
    expect((await get(context, path)).allowed_operations).toEqual([]);
  } finally {
    vsctl('--if-exists', 'del-br', 'br-tree-ui');
  }
});

test('spanning tree basic parameter checks expose native caveats without granting configuration actions', async ({
  page,
  context,
}) => {
  await login(page, 'browser-tree-parameters');
  const writes: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions)/.test(r.url())
    )
      writes.push(r.method());
  });
  vsctl(
    'add-br',
    'br-tree-rules',
    '--',
    'set',
    'Bridge',
    'br-tree-rules',
    'datapath_type=dummy',
    'rstp_enable=true',
    'other_config:rstp-priority=4097',
  );
  try {
    await expect
      .poll(
        async () =>
          (await get(context, '/inventory/spanning-tree?filter=br-tree-rules'))
            .items.length,
      )
      .toBe(1);
    const row = (
      await get(context, '/inventory/spanning-tree?filter=br-tree-rules')
    ).items[0];
    const path = '/bridges/' + row.management_id + '/spanning-tree';
    await page.goto(
      fixture.origin + '/switching/spanning-tree/' + row.management_id,
    );
    const panel = page.getByRole('region', {
      name: 'Spanning tree parameter checks',
    });
    for (const mode of ['Standard', 'Expert'] as const) {
      await nativeTypeDepth(page, mode);
      await expect(panel).toContainText('Basic parameters need review.');
      await expect(panel).toContainText(
        'RSTP priority must be a multiple of 4096. OVS rounds other values down.',
      );
      await expect(panel).toContainText(
        'Configuration editing remains unavailable.',
      );
      const v = (await get(context, path)).spanning_tree.parameter_validation;
      expect(v.state).toBe('invalid');
      expect(v.checks.map((c: { code: string }) => c.code)).toEqual([
        'RSTP_PRIORITY_MULTIPLE_4096',
      ]);
      await expect(panel.locator('code')).toHaveCount(
        mode === 'Expert' ? 1 : 0,
      );
      await screen(page, `spanning-tree-parameters-${mode.toLowerCase()}`);
    }
    // The inactive protocol is assessed as well; no native default is stored.
    vsctl(
      'set',
      'Bridge',
      'br-tree-rules',
      'other_config:rstp-priority=4096',
      'other_config:stp-hello-time=10',
    );
    await expect
      .poll(async () =>
        (
          await get(context, path)
        ).spanning_tree.parameter_validation.checks.map(
          (c: { code: string }) => c.code,
        ),
      )
      .toEqual(['STP_MAX_AGE_HELLO_RELATION']);
    await page
      .getByRole('button', { name: 'Refresh spanning tree', exact: true })
      .click();
    await expect(panel).toContainText(
      'STP max age must be at least twice hello time plus two seconds.',
    );
    await expect(panel).toContainText('including inactive basic settings');
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(panel).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth + 1,
      ),
    ).toBe(true);
    await screen(page, 'spanning-tree-parameters-mobile');
    vsctl(
      'remove',
      'Bridge',
      'br-tree-rules',
      'other_config',
      'stp-hello-time',
    );
    await expect
      .poll(
        async () =>
          (await get(context, path)).spanning_tree.configuration[
            'stp-hello-time'
          ].availability,
      )
      .toBe('unset');
    await page.reload();
    await expect(panel).toContainText('Basic parameter checks passed.');
    const current = await get(context, path);
    expect(
      current.spanning_tree.configuration['stp-hello-time'].availability,
    ).toBe('unset');
    expect(
      current.spanning_tree.configuration['stp-hello-time'].value,
    ).toBeNull();
    expect(current.editable).toBe(false);
    expect(current.allowed_operations).toEqual([]);
    expect(current.spanning_tree.write_reason).toBe(
      'SPANNING_TREE_WRITE_GATE_PENDING',
    );
    expect(writes).toEqual([]);
  } finally {
    vsctl('--if-exists', 'del-br', 'br-tree-rules');
  }
});

test('spanning tree observer withholds intent and participation while native runtime remains readable', async ({
  page,
  context,
}) => {
  await login(page, 'browser-tree-observer');
  vsctl(
    'add-br',
    'br-tree-ro',
    '--',
    'set',
    'Bridge',
    'br-tree-ro',
    'datapath_type=dummy',
    'rstp_enable=true',
  );
  try {
    await expect
      .poll(
        async () =>
          (await get(context, '/inventory/spanning-tree?filter=br-tree-ro'))
            .items.length,
      )
      .toBe(1);
    const row = (
      await get(context, '/inventory/spanning-tree?filter=br-tree-ro')
    ).items[0];
    await expect
      .poll(
        async () =>
          (
            await get(
              context,
              '/bridges/' + row.management_id + '/spanning-tree',
            )
          ).spanning_tree.runtime.rstp_bridge_id.availability,
      )
      .toBe('known');
    await page.goto(
      fixture.origin + '/switching/spanning-tree/' + row.management_id,
    );
    await expect(
      page.getByRole('heading', { name: 'br-tree-ro', level: 2, exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText('Withheld', { exact: true }).first(),
    ).toBeVisible();
    const detail = await get(
      context,
      '/bridges/' + row.management_id + '/spanning-tree',
    );
    expect(detail.spanning_tree.protocol).toBe('unknown');
    expect(detail.spanning_tree.ownership).toBe('withheld');
    expect(detail.spanning_tree.parameter_validation.state).toBe('withheld');
    expect(detail.spanning_tree.parameter_validation.checks).toEqual([]);
    await expect(
      page.getByRole('region', { name: 'Spanning tree parameter checks' }),
    ).toContainText('Parameter checks are withheld with current permissions.');
    expect(
      detail.ports.every(
        (p: {
          spanning_tree: { rstp_participation: { availability: string } };
        }) => p.spanning_tree.rstp_participation.availability === 'withheld',
      ),
    ).toBe(true);
    await nativeTypeDepth(page, 'Expert');
    await expect(
      page.getByText('ovs-vswitchd-observation', { exact: false }).first(),
    ).toBeVisible();
    await screen(page, 'spanning-tree-withheld');
    expect(detail.editable).toBe(false);
  } finally {
    vsctl('--if-exists', 'del-br', 'br-tree-ro');
  }
});

test('spanning tree native conflict, unknown, external control, empty and retired identity remain explicit', async ({
  page,
  context,
}) => {
  await login(page, 'browser-tree-exceptions');
  vsctl(
    'add-br',
    'br-tree-ex',
    '--',
    'set',
    'Bridge',
    'br-tree-ex',
    'datapath_type=dummy',
    'stp_enable=true',
    'rstp_enable=true',
    'external_ids:ovn-owner=synthetic',
  );
  try {
    await expect
      .poll(
        async () =>
          (await get(context, '/inventory/spanning-tree?filter=br-tree-ex'))
            .items.length,
      )
      .toBe(1);
    const row = (
      await get(context, '/inventory/spanning-tree?filter=br-tree-ex')
    ).items[0];
    await page.goto(
      fixture.origin + '/switching/spanning-tree/' + row.management_id,
    );
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: 'STP and RSTP are both configured' }),
    ).toBeVisible();
    await expect(
      page.getByText('externally-controlled', { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText('Unknown', { exact: true }).first(),
    ).toBeVisible();
    await screen(page, 'spanning-tree-conflict');
    vsctl(
      'set',
      'Bridge',
      'br-tree-ex',
      'stp_enable=false',
      'rstp_enable=false',
    );
    await expect
      .poll(
        async () =>
          (
            await get(
              context,
              '/bridges/' + row.management_id + '/spanning-tree',
            )
          ).spanning_tree.protocol,
      )
      .toBe('disabled');
    await page
      .getByRole('button', { name: 'Refresh spanning tree', exact: true })
      .click();
    await expect(page.getByText('disabled', { exact: true })).toBeVisible();
    await screen(page, 'spanning-tree-disabled');
    vsctl('del-br', 'br-tree-ex');
    await expect
      .poll(async () =>
        (
          await getReply(
            context,
            '/bridges/' + row.management_id + '/spanning-tree',
          )
        ).status(),
      )
      .toBe(404);
    await page
      .getByRole('button', { name: 'Refresh spanning tree', exact: true })
      .click();
    await expect(page.getByText('NOT_FOUND', { exact: false })).toBeVisible();
    await screen(page, 'spanning-tree-retired');
    await page.goto(
      fixture.origin +
        '/switching/spanning-tree?filter=tree-result-does-not-exist',
    );
    await expect(
      page.getByText('No Bridges in this authorized result.', { exact: true }),
    ).toBeVisible();
    await screen(page, 'spanning-tree-empty');
  } finally {
    vsctl('--if-exists', 'del-br', 'br-tree-ex');
  }
});

test('spanning tree retains stale observations and clears protected state during manager outage', async ({
  page,
  context,
}) => {
  await login(page, 'browser-tree-outage');
  const bridge = (
    await get(context, '/inventory/spanning-tree?filter=br-inv')
  ).items.find((b: { name: string }) => b.name === 'br-inv');
  expect(bridge).toBeTruthy();
  await page.goto(
    fixture.origin + '/switching/spanning-tree/' + bridge.management_id,
  );
  await expect(
    page.getByRole('heading', { name: 'br-inv', level: 2, exact: true }),
  ).toBeVisible();
  execFileSync('ovs-appctl', ['-t', `${fixture.ovsDirectory}/db.ctl`, 'exit']);
  try {
    await expect(
      page.getByText(
        'Stale observation. Reported values are retained for review; current participation is Unknown.',
        { exact: true },
      ),
    ).toBeVisible();
    const stale = await get(
      context,
      '/bridges/' + bridge.management_id + '/spanning-tree',
    );
    expect(stale.source.freshness).toBe('stale');
    expect(stale.spanning_tree.parameter_validation.state).toBe('stale');
    expect(stale.spanning_tree.parameter_validation.checks).toEqual([]);
    await expect(
      page.getByRole('region', { name: 'Spanning tree parameter checks' }),
    ).toContainText('Parameter checks are unavailable for stale observations.');
    expect(
      stale.ports.every(
        (p: {
          spanning_tree: { rstp_participation: { availability: string } };
        }) => p.spanning_tree.rstp_participation.availability === 'unknown',
      ),
    ).toBe(true);
    await screen(page, 'spanning-tree-provider-stale');
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
  await expect
    .poll(
      async () =>
        (
          await get(
            context,
            '/bridges/' + bridge.management_id + '/spanning-tree',
          )
        ).source.freshness,
    )
    .toBe('fresh');
  unit('stop', 'mgrd');
  try {
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: 'Current authorization or connection' }),
    ).toBeVisible();
    await expect(
      page.getByRole('heading', { name: 'br-inv', level: 2, exact: true }),
    ).toHaveCount(0);
    await screen(page, 'spanning-tree-manager-unavailable');
  } finally {
    unit('start', 'mgrd');
  }
  await expect(
    page.getByRole('heading', { name: 'br-inv', level: 2, exact: true }),
  ).toBeVisible();
});

test('spanning tree inventory denial is enforced by the formal service and visible in both depths', async ({
  page,
  context,
}) => {
  await login(page, 'browser-tree-denied', false);
  expect((await getReply(context, '/inventory/spanning-tree')).status()).toBe(
    403,
  );
  await page.getByRole('link', { name: 'STP / RSTP', exact: true }).click();
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await nativeTypeDepth(page, 'Expert');
  await expect(
    page.getByText('Permission denied', { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('region', { name: 'Spanning tree Bridges', exact: true }),
  ).toHaveCount(0);
  await screen(page, 'spanning-tree-denied');
});

test('authentication diagnostics distinguish real browser admission from helper reads and signed-out rejection', async ({
  page,
  context,
}) => {
  const before = await getReply(context, '/session');
  expect(before.status()).toBe(401);
  await login(page, 'browser-auth-diagnostics');
  expect((await getReply(context, '/session')).status()).toBe(200);
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Sign in', exact: true }),
  ).toBeVisible();
  expect((await getReply(context, '/session')).status()).toBe(401);
  const entries = authDiagnostics.get(context)!.snapshot().entries;
  expect(
    entries.some(
      (entry) => entry.operation === 'create_session' && entry.status === 201,
    ),
  ).toBe(true);
  expect(
    entries.some(
      (entry) =>
        entry.operation === 'read_session' &&
        entry.source === 'browser' &&
        entry.status === 200,
    ),
  ).toBe(true);
  expect(
    entries.some(
      (entry) =>
        entry.operation === 'read_session' &&
        entry.source === 'helper' &&
        entry.status === 401,
    ),
  ).toBe(true);
  expect(
    entries.every(
      (entry) =>
        entry.code === null ||
        ['UNAUTHENTICATED', 'CREDENTIAL_EXPIRED_OR_REVOKED'].includes(
          entry.code,
        ),
    ),
  ).toBe(true);
});

test('native topology stages immutable creation, compares native images and recovers through shared Safe Apply', async ({
  page,
  context,
}) => {
  const account = 'browser-topology';
  await login(page, account);
  await clean(context);
  const topology = await get(context, '/inventory/topology');
  expect(topology.editable).toBe(true);
  const parent: TopologyNode = topology.nodes.find(
    (n: TopologyNode) => n.binding.table === 'Bridge' && n.name === 'br-ui-top',
  );
  const before = vsctl('get', 'Bridge', parent.name, 'ports');
  await page.goto(fixture.origin + '/switching/topology');
  await page
    .getByLabel('Existing object', { exact: true })
    .selectOption(parent.binding.management_id);
  await page
    .getByLabel('New immutable name', { exact: true })
    .fill('ti-ui-new');
  await page.getByRole('checkbox', { name: /I have reviewed/ }).check();
  await expect(
    page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
  ).toBeEnabled();
  await screen(page, 'topology-editor-standard');
  await nativeTypeDepth(page, 'Expert');
  await screen(page, 'topology-editor-expert');
  await nativeTypeDepth(page, 'Standard');
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  expect(vsctl('--if-exists', 'get', 'Port', 'ti-ui-new', '_uuid')).toBe('');
  const c = await get(context, '/candidate');
  const created: TopologyNode = c.intents[0].topology_change.after.find(
    (n: TopologyNode) =>
      n.binding.table === 'Interface' && n.name === 'ti-ui-new',
  );
  const diff = page.getByRole('region', {
    name: 'Native topology Diff',
    exact: true,
  });
  await expect(diff).toContainText('ti-ui-new');
  await expect(diff).toContainText('Not present');
  await expect(diff).toContainText('High-risk Safe Apply');
  await screen(page, 'topology-diff-standard');
  await nativeTypeDepth(page, 'Expert');
  await expect(diff).toContainText(created.binding.management_id);
  await screen(page, 'topology-diff-expert');
  await nativeTypeDepth(page, 'Standard');
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
    await screen(page, `topology-${device}-review`);
  }
  await page.setViewportSize({ width: 1280, height: 1000 });
  await validate(page);
  await prepareApply(page, account);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await awaiting(page);
  expect(vsctl('get', 'Interface', 'ti-ui-new', '_uuid')).toBe(
    created.binding.ovs_uuid,
  );
  await screen(page, 'topology-awaiting');
  const transaction = page.url().split('/').pop()!;
  await chooseDecision(page, 'Request rollback', 'rolled-back');
  expect(vsctl('--if-exists', 'get', 'Port', 'ti-ui-new', '_uuid')).toBe('');
  expect(vsctl('--if-exists', 'get', 'Interface', 'ti-ui-new', '_uuid')).toBe(
    '',
  );
  expect(vsctl('get', 'Bridge', parent.name, 'ports')).toBe(before);
  await screen(page, 'topology-rolled-back');
  const audit = await get(
    context,
    `/audit?object_id=${created.binding.management_id}`,
  );
  expect(
    audit.items.some(
      (r: Record<string, unknown>) =>
        r.transaction_id === transaction &&
        r.operation === 'safe-apply-state' &&
        r.result === 'rolled-back',
    ),
  ).toBe(true);
  await page.goto(
    fixture.origin + `/interfaces/${created.binding.management_id}`,
  );
  await expect(
    page.getByText('This Interface identity is no longer available.', {
      exact: false,
    }),
  ).toBeVisible();
  await page
    .getByRole('link', { name: 'Open Interface Audit', exact: true })
    .focus();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('region', { name: 'Evidence object scope' }),
  ).toContainText(created.binding.management_id);
  await screen(page, 'topology-retired-audit');
});

test('topology drift requires a fresh review and preserves the external change', async ({
  page,
  context,
}) => {
  await login(page, 'browser-topology');
  await clean(context);
  const topology = await get(context, '/inventory/topology');
  const port: TopologyNode = topology.nodes.find(
    (n: TopologyNode) => n.binding.table === 'Port' && n.name === 'ti-ui-a',
  );
  const destination: TopologyNode = topology.nodes.find(
    (n: TopologyNode) =>
      n.binding.table === 'Bridge' && n.name === 'br-ui-top-dst',
  );
  await page.goto(fixture.origin + '/switching/topology');
  await page.getByLabel('Operation', { exact: true }).selectOption('port.move');
  await page
    .getByLabel('Existing object', { exact: true })
    .selectOption(port.binding.management_id);
  await page
    .getByLabel('Destination Bridge', { exact: true })
    .selectOption(destination.binding.management_id);
  await page.getByRole('checkbox', { name: /I have reviewed/ }).check();
  await page
    .getByRole('button', { name: 'Stage in Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  vsctl(
    'set',
    'Port',
    port.name,
    'other_config:synthetic-topology-drift=external',
  );
  try {
    await expect(
      page.getByRole('heading', {
        name: 'Review and restage this topology change',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    expect(vsctl('port-to-br', port.name)).toBe('br-ui-top');
    expect(
      vsctl('get', 'Port', port.name, 'other_config:synthetic-topology-drift'),
    ).toBe('external');
    await screen(page, 'topology-drift-restage');
  } finally {
    await clean(context);
    vsctl(
      'remove',
      'Port',
      port.name,
      'other_config',
      'synthetic-topology-drift',
    );
  }
});

test('topology reader, withheld configuration and responsive editors cannot stage writes', async ({
  page,
  context,
  browser,
}) => {
  await login(page, 'browser-topology-reader');
  const topology = await get(context, '/inventory/topology');
  expect(topology.editable).toBe(false);
  const parent: TopologyNode = topology.nodes.find(
    (n: TopologyNode) => n.binding.table === 'Bridge' && n.name === 'br-ui-top',
  );
  const writes: string[] = [];
  page.on('request', (r) => {
    if (r.method() !== 'GET' && r.url().includes('/api/v1/candidate'))
      writes.push(r.method());
  });
  await page.goto(fixture.origin + '/switching/topology');
  await page
    .getByLabel('Existing object', { exact: true })
    .selectOption(parent.binding.management_id);
  for (const mode of ['Standard', 'Expert'] as const) {
    await nativeTypeDepth(page, mode);
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page
        .getByRole('status')
        .filter({ hasText: 'Workspace write permission is required.' }),
    ).toBeVisible();
    await screen(page, `topology-reader-${mode.toLowerCase()}`);
  }
  expect(writes).toEqual([]);
  const other = await browser.newContext({ ignoreHTTPSErrors: true });
  try {
    const observer = await other.newPage();
    await login(observer, 'browser-topology-observer');
    expect(
      (
        await other.request.get(fixture.origin + '/api/v1/inventory/topology')
      ).status(),
    ).toBe(403);
    await observer.goto(fixture.origin + '/switching/topology');
    await expect(
      observer.getByText('Permission denied', { exact: true }),
    ).toBeVisible();
    await expect(
      observer.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toHaveCount(0);
    await screen(observer, 'topology-configuration-withheld');
  } finally {
    await other.close();
  }
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page.getByLabel('Username', { exact: true })).toBeVisible();
  await login(page, 'browser-topology');
  await page.goto(fixture.origin + '/switching/topology');
  await page
    .getByLabel('Existing object', { exact: true })
    .selectOption(parent.binding.management_id);
  for (const [width, height, device] of [
    [900, 1000, 'tablet'],
    [390, 844, 'mobile'],
  ] as const) {
    await page.setViewportSize({ width, height });
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page
        .getByRole('status')
        .filter({ hasText: 'New topology changes require desktop.' }),
    ).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await screen(page, `topology-${device}-editor`);
  }
  await page.setViewportSize({ width: 1280, height: 1000 });
  unit('stop', 'mgrd');
  try {
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page
        .getByRole('alert')
        .filter({ hasText: 'Current authorization or connection' }),
    ).toBeVisible();
    await screen(page, 'topology-provider-unavailable');
  } finally {
    unit('start', 'mgrd');
  }
});

test('controlled ingress policing uses Candidate, native units, kernel proof and exact Safe Apply rollback', async ({
  page,
}) => {
  const account = 'browser-policing-edit';
  await login(page, account);
  await clean(page.context());
  const item = await policingEditor(page);
  try {
    await page
      .getByLabel('Policing mode', { exact: true })
      .selectOption('packets');
    await page.getByLabel('Rate (kpps)', { exact: true }).fill('1001');
    await page
      .getByRole('button', { name: 'Stage in Candidate', exact: true })
      .click();
    await expect(page.getByRole('alert')).toHaveText('Enter 1–1000 kpps.');
    await screen(page, 'interface-policing-edit-bounds');
    await stagePolicing(page, 'bandwidth', '1000');
    expect(
      vsctl('get', 'Interface', 'pi-ui-police', 'ingress_policing_rate'),
    ).toBe('0');
    const diff = page.getByRole('region', { name: 'Configuration Diff' });
    await expect(diff).toContainText('1000 kbit/s');
    await expect(diff).toContainText('OVS-selected default preserved');
    const standard = await diff.innerText();
    await screen(page, 'interface-policing-edit-standard');
    await nativeTypeDepth(page, 'Expert');
    expect(await diff.innerText()).toBe(standard);
    await screen(page, 'interface-policing-edit-expert');
    await nativeTypeDepth(page, 'Standard');
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
      await screen(page, `interface-policing-edit-${device}-review`);
    }
    await page.setViewportSize({ width: 1280, height: 1000 });
    await validate(page);
    await prepareApply(page, account);
    await page
      .getByRole('button', { name: 'Start Safe Apply', exact: true })
      .click();
    await awaiting(page);
    await waitInstalledPolicing(
      page.context(),
      item.management_id,
      '125000',
      null,
    );
    await screen(page, 'interface-policing-edit-awaiting');
    await chooseDecision(page, 'Request rollback', 'rolled-back');
    await waitInstalledPolicing(page.context(), item.management_id, null, null);
    expect(
      vsctl('get', 'Interface', 'pi-ui-police', 'ingress_policing_rate'),
    ).toBe('0');
    await screen(page, 'interface-policing-edit-rolled-back');
    await traceInterfaceEvidence(
      page,
      item.management_id,
      page.url().split('/').pop()!,
      'rolled-back',
    );
    await policingEditor(page);
    await stagePolicing(page, 'packets', '5');
    await validate(page);
    await prepareApply(page, account);
    await page
      .getByRole('button', { name: 'Start Safe Apply', exact: true })
      .click();
    await awaiting(page);
    await waitInstalledPolicing(
      page.context(),
      item.management_id,
      null,
      '5000',
    );
    await chooseDecision(page, 'Confirm configuration', 'confirmed');
    await screen(page, 'interface-policing-edit-confirmed');
    expect(
      vsctl('get', 'Interface', 'pi-ui-police', 'ingress_policing_rate'),
    ).toBe('0');
    expect(
      vsctl('get', 'Interface', 'pi-ui-police', 'ingress_policing_kpkts_rate'),
    ).toBe('5');
    expect(
      vsctl('get', 'Interface', 'pi-ui-police', 'ingress_policing_burst'),
    ).toBe('0');
    expect(
      vsctl('get', 'Interface', 'pi-ui-police', 'ingress_policing_kpkts_burst'),
    ).toBe('0');
    expect(vsctl('get', 'Interface', 'pi-ui-police', '_uuid')).toBe(
      item.ovs_uuid,
    );
  } finally {
    vsctl(
      'set',
      'Interface',
      'pi-ui-police',
      'ingress_policing_rate=0',
      'ingress_policing_kpkts_rate=0',
    );
  }
});

test('policing drift, read-only identities, permissions and responsive direct editor gates remain explicit', async ({
  page,
  browser,
}) => {
  await login(page, 'browser-policing-drift');
  await clean(page.context());
  const item = await policingEditor(page);
  try {
    await stagePolicing(page, 'bandwidth', '1000');
    vsctl('set', 'Interface', 'pi-ui-police', 'ingress_policing_rate=1800');
    await expect(
      page.getByRole('region', { name: 'Configuration Diff' }),
    ).toContainText('1800 kbit/s');
    await expect(
      page.getByRole('heading', {
        name: 'Review and restage this policing request',
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Validate Candidate', exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole('button', {
        name: 'Rebase reviewed choices',
        exact: true,
      }),
    ).toHaveCount(0);
    await screen(page, 'interface-policing-edit-drift');
    await clean(page.context());
    vsctl('set', 'Interface', 'pi-ui-police', 'ingress_policing_rate=0');
    await waitInstalledPolicing(page.context(), item.management_id, null, null);
    await policingEditor(page);
    for (const [width, height, device] of [
      [900, 1000, 'tablet'],
      [390, 844, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await page.reload();
      await expect(
        page.getByText('Use a desktop to prepare a configuration change.', {
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
      ).toBeDisabled();
      await screen(page, `interface-policing-edit-${device}-blocked`);
    }
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const reader = await context.newPage();
    try {
      await login(reader, 'browser-policing-reader');
      await reader.goto(
        fixture.origin + `/interfaces/${item.management_id}/policing`,
      );
      await expect(
        reader.getByText(
          'Current permissions do not allow Interface policing changes.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        reader.getByRole('button', { name: 'Stage in Candidate', exact: true }),
      ).toBeDisabled();
      await nativeTypeDepth(reader, 'Expert');
      await expect(
        reader.getByRole('button', { name: 'Stage in Candidate', exact: true }),
      ).toBeDisabled();
      await screen(reader, 'interface-policing-edit-reader');
    } finally {
      await context.close();
    }
  } finally {
    vsctl(
      'set',
      'Interface',
      'pi-ui-police',
      'ingress_policing_rate=0',
      'ingress_policing_kpkts_rate=0',
    );
  }
});

test('Interface selections combine immutable Bridge, native type and OVS state before natural pagination', async ({
  page,
  context,
}) => {
  await login(page, 'browser-interface-selection');
  const mutations: string[] = [];
  page.on('request', (request) => {
    if (
      request.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions|interfaces)/.test(request.url())
    )
      mutations.push(request.method());
  });
  const names = Array.from({ length: 12 }, (_, index) => `fs-ui-${12 - index}`);
  vsctl(
    'add-br',
    'br-filter-ui',
    '--',
    'set',
    'Bridge',
    'br-filter-ui',
    'datapath_type=dummy',
    ...names.flatMap((name) => [
      '--',
      'add-port',
      'br-filter-ui',
      name,
      '--',
      'set',
      'Interface',
      name,
      'type=dummy',
    ]),
  );
  try {
    vsctl(
      'add-br',
      'br-filter-alt',
      '--',
      'set',
      'Bridge',
      'br-filter-alt',
      'datapath_type=dummy',
      '--',
      'add-port',
      'br-filter-alt',
      'fs-ui-other',
      '--',
      'set',
      'Interface',
      'fs-ui-other',
      'type=dummy',
    );
    execFileSync(
      'ovs-appctl',
      [
        '-t',
        `${fixture.ovsDirectory}/switch.ctl`,
        'netdev-dummy/set-admin-state',
        'fs-ui-5',
        'down',
      ],
      { stdio: 'pipe' },
    );
    let bridgeID = '';
    await expect
      .poll(async () => {
        const bridges = await get(context, '/bridges?filter=br-filter-ui');
        bridgeID =
          bridges.items.find((b: { name: string }) => b.name === 'br-filter-ui')
            ?.management_id ?? '';
        if (!bridgeID) return [];
        const values = await get(
          context,
          `/interfaces?bridge_id=${bridgeID}&filter=fs-ui-&link_state=down`,
        );
        return values.items.map((item: { name: string }) => item.name);
      })
      .toEqual(['fs-ui-5']);
    await page.goto(fixture.origin + '/bridges?filter=br-filter-&limit=1');
    await page.reload();
    await expect(
      page.getByRole('heading', { name: 'Bridges', exact: true }),
    ).toBeVisible();
    let bridges = await get(context, '/bridges?filter=br-filter-&limit=1');
    const firstBridgeName = bridges.items[0].name;
    const seenBridges: string[] = [];
    const bridgeSnapshot = bridges.snapshot_id;
    for (let n = 0; n < 3; n++) {
      expect(bridges.items).toHaveLength(1);
      await expect(
        page.getByRole('link', { name: bridges.items[0].name, exact: true }),
      ).toBeVisible();
      seenBridges.push(bridges.items[0].name);
      expect(bridges.snapshot_id).toBe(bridgeSnapshot);
      if (!bridges.next_cursor) break;
      // Sealing has a fresh nonce: another GET has an equivalent but different
      // token. Exercise the cursor actually issued to this browser page.
      const nextLink = page.getByRole('link', {
        name: 'Next Bridges page',
        exact: true,
      });
      const browserCursor = new URL(
        (await nextLink.getAttribute('href'))!,
        fixture.origin,
      ).searchParams.get('cursor');
      expect(browserCursor).toBeTruthy();
      const next = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === '/api/v1/bridges' &&
          new URL(r.url()).searchParams.get('cursor') === browserCursor,
      );
      await page
        .getByRole('link', { name: 'Next Bridges page', exact: true })
        .click();
      const reply = await next;
      expect(reply.status()).toBe(200);
      expect(new URL(page.url()).searchParams.get('filter')).toBe('br-filter-');
      expect(new URL(page.url()).searchParams.get('limit')).toBe('1');
      bridges = await reply.json();
    }
    expect(seenBridges.sort()).toEqual(['br-filter-alt', 'br-filter-ui']);
    await expect(
      page.getByRole('link', { name: 'Next Bridges page', exact: true }),
    ).toHaveCount(0);
    await page
      .getByRole('link', { name: 'First Bridges page', exact: true })
      .click();
    expect(new URL(page.url()).searchParams.get('filter')).toBe('br-filter-');
    expect(new URL(page.url()).searchParams.get('limit')).toBe('1');
    expect(new URL(page.url()).searchParams.has('cursor')).toBe(false);
    await expect(
      page.getByRole('link', { name: firstBridgeName, exact: true }),
    ).toBeVisible();
    if (firstBridgeName !== 'br-filter-ui')
      await page
        .getByRole('link', { name: 'Next Bridges page', exact: true })
        .click();
    await expect(
      page.getByRole('link', { name: 'br-filter-ui', exact: true }),
    ).toBeVisible();
    await screen(page, 'interface-selection-bridge-picker');
    await page
      .getByRole('link', { name: 'Review Interfaces', exact: true })
      .click();
    expect(new URL(page.url()).searchParams.get('bridge_id')).toBe(bridgeID);
    await expect(page.getByLabel('Interface Bridge scope')).toContainText(
      'br-filter-ui',
    );
    await page.getByLabel('Interface name', { exact: true }).fill('fs-ui-');
    await page
      .getByLabel('Native type', { exact: true })
      .selectOption('native:dummy');
    await page.getByLabel('OVS link state', { exact: true }).selectOption('up');
    await page.getByLabel('Page size', { exact: true }).selectOption('10');
    // Wait for a real background refresh of the applied Bridge-only query.
    // It must not replace the four unsubmitted controls with applied values.
    await page.waitForResponse((r) => {
      const url = new URL(r.url());
      return (
        url.pathname === '/api/v1/interfaces' &&
        url.searchParams.get('bridge_id') === bridgeID &&
        !url.searchParams.has('native_type')
      );
    });
    await expect(
      page.getByLabel('Interface name', { exact: true }),
    ).toHaveValue('fs-ui-');
    await expect(page.getByLabel('Native type', { exact: true })).toHaveValue(
      'native:dummy',
    );
    await expect(
      page.getByLabel('OVS link state', { exact: true }),
    ).toHaveValue('up');
    await expect(page.getByLabel('Page size', { exact: true })).toHaveValue(
      '10',
    );
    const firstReply = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === '/api/v1/interfaces' &&
        new URL(r.url()).searchParams.get('native_type') === 'dummy',
    );
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    const reply = await firstReply;
    expect(reply.status()).toBe(200);
    let current = await reply.json();
    const expected = Array.from(
      { length: 12 },
      (_, n) => `fs-ui-${n + 1}`,
    ).filter((name) => name !== 'fs-ui-5');
    const observed: string[] = [];
    const identities = new Set<string>();
    const snapshot = current.snapshot_id;
    for (let n = 0; n < 12; n++) {
      const visibleNames = current.items.map(
        (item: { name: string }) => item.name,
      );
      const table = page.getByRole('region', {
        name: 'Interfaces inventory',
        exact: true,
      });
      await expect(table.getByRole('rowheader').getByRole('link')).toHaveText(
        visibleNames,
      );
      for (const item of current.items) {
        expect(item.bridge_ref.id).toBe(bridgeID);
        expect(item.fields.type.value).toBe('dummy');
        expect(item.fields.link_state.value).toEqual(['up']);
        expect(identities.has(item.management_id)).toBe(false);
        identities.add(item.management_id);
      }
      observed.push(...visibleNames);
      expect(current.snapshot_id).toBe(snapshot);
      if (!current.next_cursor) break;
      const next = page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname === '/api/v1/interfaces' &&
          new URL(r.url()).searchParams.get('cursor') === current.next_cursor,
      );
      await page
        .getByRole('button', { name: 'Next page', exact: true })
        .click();
      const response = await next;
      expect(response.status()).toBe(200);
      current = await response.json();
    }
    expect(observed).toEqual(expected);
    await expect(
      page.getByRole('button', { name: 'Next page', exact: true }),
    ).toBeDisabled();
    await page.getByRole('button', { name: 'First page', exact: true }).click();
    await expect(
      page.getByRole('link', { name: 'fs-ui-1', exact: true }),
    ).toBeVisible();
    await page.reload();
    await expect(page.getByLabel('Native type', { exact: true })).toHaveValue(
      'native:dummy',
    );
    await expect(
      page.getByLabel('OVS link state', { exact: true }),
    ).toHaveValue('up');
    await expect(
      page.getByRole('link', { name: 'fs-ui-1', exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Standard', exact: true }),
    ).toHaveAttribute('aria-pressed', 'false');
    await screen(page, 'interface-selection-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await expect(
      page.getByRole('button', { name: 'Expert', exact: true }),
    ).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByLabel('Native type', { exact: true })).toBeEnabled();
    await screen(page, 'interface-selection-expert');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    await page.getByRole('link', { name: 'fs-ui-1', exact: true }).focus();
    await page.keyboard.press('Enter');
    expect(new URL(page.url()).searchParams.get('bridge_id')).toBe(bridgeID);
    await page
      .getByRole('link', { name: 'Interfaces', exact: true })
      .last()
      .click();
    await expect(page.getByLabel('Native type', { exact: true })).toHaveValue(
      'native:dummy',
    );
    await expect(
      page.getByRole('link', { name: 'fs-ui-1', exact: true }),
    ).toBeVisible();
    for (const [width, height, device] of [
      [900, 1000, 'tablet'],
      [390, 844, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await screen(page, `interface-selection-${device}`);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    vsctl('--no-wait', 'set', 'Interface', 'fs-ui-12', 'type=""');
    await expect
      .poll(async () =>
        (
          await get(
            context,
            `/interfaces?bridge_id=${bridgeID}&filter=fs-ui-&native_type=`,
          )
        ).items.map((i: { name: string }) => i.name),
      )
      .toEqual(['fs-ui-12']);
    await page.getByLabel('Native type', { exact: true }).selectOption('empty');
    await page.getByLabel('OVS link state', { exact: true }).selectOption('');
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    await expect(
      page
        .getByRole('region', { name: 'Interfaces inventory', exact: true })
        .getByRole('rowheader')
        .getByRole('link'),
    ).toHaveText(['fs-ui-12']);
    expect(new URL(page.url()).searchParams.has('native_type')).toBe(true);
    expect(new URL(page.url()).searchParams.get('native_type')).toBe('');
    await screen(page, 'interface-selection-native-default');
    const futureType = 'future-native-' + 'x'.repeat(45);
    vsctl('--no-wait', 'set', 'Interface', 'fs-ui-12', `type=${futureType}`);
    await expect
      .poll(async () =>
        (
          await get(
            context,
            `/interfaces?bridge_id=${bridgeID}&filter=fs-ui-&native_type=${futureType}`,
          )
        ).items.map((i: { name: string }) => i.name),
      )
      .toEqual(['fs-ui-12']);
    await page
      .getByLabel('Native type', { exact: true })
      .selectOption('custom');
    await page
      .getByLabel('Exact native type', { exact: true })
      .fill(futureType);
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    await expect(
      page
        .getByRole('region', { name: 'Interfaces inventory', exact: true })
        .getByRole('rowheader')
        .getByRole('link'),
    ).toHaveText(['fs-ui-12']);
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await screen(page, 'interface-selection-custom-type');
    expect(mutations).toEqual([]);
  } finally {
    vsctl(
      '--if-exists',
      'del-br',
      'br-filter-ui',
      '--',
      '--if-exists',
      'del-br',
      'br-filter-alt',
    );
  }
});

test('Interface selection exceptions keep withheld, empty, stale and retired Bridge scopes explicit', async ({
  page,
  context,
}) => {
  await login(page, 'browser-interface-selection-observer');
  vsctl(
    'add-br',
    'br-scope-retire',
    '--',
    'set',
    'Bridge',
    'br-scope-retire',
    'datapath_type=dummy',
    '--',
    'add-port',
    'br-scope-retire',
    'scope-1',
    '--',
    'set',
    'Interface',
    'scope-1',
    'type=dummy',
  );
  try {
    let bridgeID = '';
    await expect
      .poll(async () => {
        const values = await get(context, '/bridges?filter=br-scope-retire');
        bridgeID =
          values.items.find(
            (b: { name: string }) => b.name === 'br-scope-retire',
          )?.management_id ?? '';
        return !!bridgeID;
      })
      .toBe(true);
    for (const type of ['dummy', '', 'future-native']) {
      const response = await getReply(
        context,
        `/interfaces?bridge_id=${bridgeID}&native_type=${type}`,
      );
      expect(response.status()).toBe(403);
      expect((await response.json()).code).toBe('CAPABILITY_DENIED');
    }
    await page.goto(
      fixture.origin + `/interfaces?bridge_id=${bridgeID}&native_type=dummy`,
    );
    await expect(
      page.getByLabel('Native type', { exact: true }),
    ).toBeDisabled();
    await expect(page.getByRole('status')).toContainText('Permission denied');
    await expect(
      page.getByRole('region', { name: 'Interfaces inventory', exact: true }),
    ).toHaveCount(0);
    await screen(page, 'interface-selection-withheld');
    await page
      .getByRole('button', { name: 'Clear filters', exact: true })
      .click();
    expect(new URL(page.url()).searchParams.has('native_type')).toBe(false);
    await expect(
      page.getByRole('region', { name: 'Interfaces inventory', exact: true }),
    ).toBeVisible();
    await page.goto(
      fixture.origin +
        `/interfaces?bridge_id=${bridgeID}&filter=absent-synthetic-interface`,
    );
    await expect(
      page.getByText('No Interfaces match this filter.', { exact: false }),
    ).toBeVisible();
    await screen(page, 'interface-selection-empty');
    await page.getByLabel('Interface name', { exact: true }).fill('scope-1');
    await page
      .getByRole('button', { name: 'Apply filter', exact: true })
      .click();
    await expect(
      page.getByRole('link', { name: 'scope-1', exact: true }),
    ).toBeVisible();
    execFileSync(
      'ovs-appctl',
      ['-t', `${fixture.ovsDirectory}/db.ctl`, 'exit'],
      { stdio: 'pipe' },
    );
    try {
      await expect(
        page.getByText('Stale observation.', { exact: false }),
      ).toBeVisible();
      expect(new URL(page.url()).searchParams.get('bridge_id')).toBe(bridgeID);
      await screen(page, 'interface-selection-stale');
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
    vsctl('del-br', 'br-scope-retire');
    await expect
      .poll(async () =>
        (await getReply(context, `/interfaces?bridge_id=${bridgeID}`)).status(),
      )
      .toBe(404);
    vsctl(
      'add-br',
      'br-scope-retire',
      '--',
      'set',
      'Bridge',
      'br-scope-retire',
      'datapath_type=dummy',
      '--',
      'add-port',
      'br-scope-retire',
      'scope-1',
      '--',
      'set',
      'Interface',
      'scope-1',
      'type=dummy',
    );
    let replacementID = '';
    await expect
      .poll(async () => {
        replacementID =
          (await get(context, '/bridges?filter=br-scope-retire')).items.find(
            (b: { management_id: string }) => b.management_id !== bridgeID,
          )?.management_id ?? '';
        return !!replacementID;
      })
      .toBe(true);
    await expect(
      page.getByText('The selected Bridge identity is no longer available.', {
        exact: false,
      }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get('bridge_id')).toBe(bridgeID);
    await expect(
      page.getByRole('region', { name: 'Interfaces inventory', exact: true }),
    ).toHaveCount(0);
    await screen(page, 'interface-selection-retired');
    await page
      .getByRole('button', { name: 'All Bridges', exact: true })
      .click();
    await expect(
      page.getByRole('link', { name: 'scope-1', exact: true }),
    ).toBeVisible();
    const replacement = (await get(context, '/interfaces?filter=scope-1'))
      .items[0];
    expect(replacement.bridge_ref.id).toBe(replacementID);
    expect(replacement.fields.type.availability).toBe('withheld');
  } finally {
    vsctl('--if-exists', 'del-br', 'br-scope-retire');
  }
});

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
    expect(new URL(page.url()).pathname).toBe(
      '/interfaces/' + iface.management_id,
    );
    expect(new URL(page.url()).searchParams.get('filter')).toBe('inv-p1');
    expect(new URL(page.url()).searchParams.get('limit')).toBe('10');
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
  const iface = (await get(context, '/interfaces?filter=inv-p1')).items.find(
    (i: { name: string }) => i.name === 'inv-p1',
  );
  expect(iface).toBeDefined();
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
  const readerContext = await context
    .browser()!
    .newContext({ ignoreHTTPSErrors: true });
  const readerPage = await readerContext.newPage();
  const readerErrors: string[] = [];
  readerPage.on('pageerror', (error) => readerErrors.push(error.message));
  await login(readerPage, 'browser-interface-config');
  await readerPage.goto(fixture.origin + '/interfaces/' + iface.management_id);
  await expect(
    configurationRow(readerPage, 'Ingress bandwidth limit'),
  ).toContainText('Disabled (0 kbit/s)');
  execFileSync('ovs-appctl', ['-t', `${fixture.ovsDirectory}/db.ctl`, 'exit']);
  try {
    await expect(
      page.getByText('Stale observation.', { exact: false }),
    ).toBeVisible();
    const stale = await get(context, `/interfaces/${iface.management_id}`);
    expect(stale.linux_device.reason).toBe('OVS_ASSOCIATION_STALE');
    expect(stale.linux_device.source.freshness).toBe('unavailable');
    expect(stale.linux_device.fields).toEqual({});
    await screen(page, 'interface-provider-stale');
    await expect(
      configurationRow(readerPage, 'Ingress bandwidth limit'),
    ).toContainText('Last observed: Disabled (0 kbit/s)');
    await screen(readerPage, 'interface-config-stale');
    const staleRules = await get(
      readerContext,
      `/interfaces/${iface.management_id}`,
    );
    expect(staleRules.linux_ingress_policing.reason).toBe(
      'OVS_ASSOCIATION_STALE',
    );
    expect(staleRules.linux_ingress_policing.actions).toEqual([]);
    await screen(readerPage, 'interface-policing-stale');
    for (const name of [
      'ofport_request',
      'ingress_policing_rate',
      'ingress_policing_burst',
      'ingress_policing_kpkts_rate',
      'ingress_policing_kpkts_burst',
    ]) {
      expect(stale.fields[name].availability).toBe('withheld');
      expect(stale.fields[name].value).toBeNull();
      expect(stale.fields[name].source.freshness).toBe('stale');
    }
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
    await readerContext.close();
    expect(readerErrors).toEqual([]);
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
    const replacementLink = page.getByRole('link', {
      name: 'if-retire',
      exact: true,
    });
    await expect(replacementLink).toBeVisible();
    const replacementURL = new URL(
      (await replacementLink.getAttribute('href'))!,
      fixture.origin,
    );
    expect(replacementURL.pathname).toBe(
      '/interfaces/' + replacement.management_id,
    );
    expect(replacementURL.searchParams.get('filter')).toBe('if-retire');
    expect(replacementURL.searchParams.get('limit')).toBe('25');
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

async function traceInterfaceEvidence(
  page: Page,
  identity: string,
  transaction: string,
  result: string,
  review = false,
) {
  const target = await get(page.context(), `/interfaces/${identity}`);
  expect(target.management_id).toBe(identity);
  const audit = await get(page.context(), `/audit?object_id=${identity}`);
  const record = audit.items.find(
    (r: Record<string, unknown>) =>
      r.transaction_id === transaction &&
      r.operation === 'safe-apply-state' &&
      r.result === result,
  );
  expect(record).toBeTruthy();
  expect(record.object_ref).toEqual({ kind: 'transaction', id: transaction });
  expect(record.related_object_refs).toContainEqual({
    kind: 'interface',
    id: identity,
  });
  const events = await get(page.context(), `/events?object_id=${identity}`);
  expect(
    events.items.some(
      (r: Record<string, unknown>) =>
        r.transaction_id === transaction && r.operation === 'job-created',
    ),
  ).toBe(true);
  await page.goto(fixture.origin + `/interfaces/${identity}`);
  await expect(
    page.getByRole('heading', { name: target.name, exact: true }),
  ).toBeVisible();
  const entry = page.getByRole('region', { name: 'Interface shared evidence' });
  for (const mode of ['Standard', 'Expert'] as const) {
    await nativeTypeDepth(page, mode);
    await expect(
      entry.getByRole('link', { name: 'Open Interface Audit', exact: true }),
    ).toHaveAttribute('href', `/operations/audit?object_id=${identity}`);
    if (review)
      await screen(page, `interface-evidence-entry-${mode.toLowerCase()}`);
  }
  await nativeTypeDepth(page, 'Standard');
  await entry
    .getByRole('link', { name: 'Open Interface Audit', exact: true })
    .focus();
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('heading', { name: 'Audit', exact: true }),
  ).toBeVisible();
  const scope = page.getByRole('region', { name: 'Evidence object scope' });
  await expect(scope).toContainText(identity);
  await expect(page.getByRole('row').nth(1).getByRole('link')).toBeVisible();
  if (review) {
    await screen(page, 'interface-evidence-audit-standard');
    await nativeTypeDepth(page, 'Expert');
    await expect(
      page.getByRole('columnheader', { name: 'Correlation', exact: true }),
    ).toBeVisible();
    await screen(page, 'interface-evidence-audit-expert');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-evidence-audit-dark');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await nativeTypeDepth(page, 'Standard');
    for (const [width, height, device] of [
      [900, 1000, 'tablet'],
      [390, 844, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height });
      await expect(scope).toContainText(identity);
      await expect(
        page.getByRole('button', { name: 'Start Safe Apply', exact: true }),
      ).toHaveCount(0);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await screen(page, `interface-evidence-audit-${device}`);
    }
    await page.setViewportSize({ width: 1280, height: 1000 });
    await page
      .getByLabel('Records per page', { exact: true })
      .selectOption('2');
    await expect(page.getByRole('row')).toHaveCount(3);
    const first = await page
      .getByRole('row')
      .nth(1)
      .getByRole('link')
      .getAttribute('href');
    await page.getByRole('button', { name: 'Next page', exact: true }).click();
    await expect
      .poll(() => new URL(page.url()).searchParams.get('cursor'))
      .toBeTruthy();
    await expect
      .poll(
        async () =>
          (
            await page
              .getByRole('row')
              .nth(1)
              .getByRole('link')
              .getAttribute('href')
          )?.split('?')[0],
      )
      .not.toBe(first!.split('?')[0]);
    await page.reload();
    await expect(scope).toContainText(identity);
    await expect(page.getByRole('row')).toHaveCount(3);
    await page.goBack();
    await expect(
      page.getByRole('row').nth(1).getByRole('link'),
    ).toHaveAttribute('href', first!);
    await page.goto(
      fixture.origin +
        `/operations/audit?object_id=${identity}&limit=2&cursor=expired`,
    );
    await expect(
      page.getByText('CURSOR_EXPIRED.', { exact: false }),
    ).toBeVisible();
    await expect(scope).toContainText(identity);
    await screen(page, 'interface-evidence-expired');
    await page
      .getByRole('button', { name: 'Refresh retained records', exact: true })
      .click();
    await expect(page.getByRole('row')).toHaveCount(3);
    expect(new URL(page.url()).searchParams.get('object_id')).toBe(identity);
  }
  if (review) {
    const unrelated = '99999999-9999-4999-8999-999999999999';
    await page.goto(
      fixture.origin + `/operations/audit/${record.id}?object_id=${unrelated}`,
    );
    await expect(
      page.getByText(
        'This record does not contain the selected object identity.',
        { exact: false },
      ),
    ).toBeVisible();
    await expect(
      page.getByRole('link', { name: /^Open transaction · / }),
    ).toHaveCount(0);
  }
  await page.goto(
    fixture.origin + `/operations/audit/${record.id}?object_id=${identity}`,
  );
  await page.reload();
  await expect(
    page.getByRole('heading', { name: 'Audit record', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('link', {
      name: `Open interface · ${identity.slice(0, 8)}`,
      exact: true,
    }),
  ).toHaveAttribute('href', `/interfaces/${identity}`);
  await expect(
    page.getByRole('link', {
      name: `Open transaction · ${transaction.slice(0, 8)}`,
      exact: true,
    }),
  ).toHaveAttribute('href', `/changes/transactions/${transaction}`);
  if (review) await screen(page, 'interface-evidence-record');
  await page.getByRole('link', { name: /^Open job · / }).click();
  await expect(
    page.getByRole('heading', { name: 'Jobs', exact: true }),
  ).toBeVisible();
  await page.goto(fixture.origin + `/operations/events?object_id=${identity}`);
  await expect(
    page.getByRole('heading', { name: 'Events', exact: true }),
  ).toBeVisible();
  await expect(scope).toContainText(identity);
  await expect(page.getByRole('row').nth(1).getByRole('link')).toBeVisible();
  if (review) await screen(page, 'interface-evidence-events');
}

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
    const rolledBack = page.url().split('/').pop()!;
    await traceInterfaceEvidence(
      page,
      before.management_id,
      rolledBack,
      'rolled-back',
    );
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
    await traceInterfaceEvidence(
      page,
      before.management_id,
      page.url().split('/').pop()!,
      'confirmed',
      true,
    );
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
  try {
    // Native empty is configuration, not proof of the actual automatic value.
    // A kernel MTU below the derived minimum can persist after clearing. Keep
    // this real exception gated, then prepare a proven automatic fixture.
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=1500');
    await waitMTUDefault(page, '1500', 1800);
    vsctl('clear', 'Interface', 'pi-ui-mtu', 'mtu_request');
    execFileSync('ip', ['link', 'set', 'dev', 'pi-ui-mtu', 'mtu', '1500'], {
      stdio: 'pipe',
    });
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
        request: [],
        actual: ['1500'],
        default: 1800,
        editable: false,
      });
    const unavailableList = await get(
      page.context(),
      '/interfaces?filter=pi-ui-mtu',
    );
    const unavailable = unavailableList.items.find(
      (i: { name: string }) => i.name === 'pi-ui-mtu',
    );
    await page.goto(
      fixture.origin + `/interfaces/${unavailable.management_id}/mtu`,
    );
    await expect(
      page.getByRole('button', { name: 'Stage in Candidate', exact: true }),
    ).toBeDisabled();
    expect(actualLinuxMTU()).toBe(1500);
    await expect(
      page.getByText(
        'Automatic MTU is not proven by the current device observation. Review the actual value and Bridge devices.',
        { exact: true },
      ),
    ).toBeVisible();
    await screen(page, 'interface-mtu-default-unapplied');
    vsctl('set', 'Interface', 'pi-ui-mtu', 'mtu_request=2400');
    await waitMTUDefault(page, '2400', 1800);
    vsctl('clear', 'Interface', 'pi-ui-mtu', 'mtu_request');
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

function linuxRow(page: Page, label: string) {
  return page
    .getByRole('region', { name: 'Linux device observations', exact: true })
    .getByRole('row')
    .filter({
      has: page.getByRole('rowheader', { name: label, exact: false }),
    });
}
function hostLink(name: string) {
  return JSON.parse(
    execFileSync('ip', ['-j', 'link', 'show', 'dev', name], {
      encoding: 'utf8',
    }),
  )[0];
}

function policingPanel(page: Page) {
  return page.getByRole('region', {
    name: 'Linux ingress policing',
    exact: true,
  });
}
const tc = (...args: string[]) =>
  execFileSync('tc', args, { encoding: 'utf8' }).trim();

test('Linux ingress policing separates real installed rules, pending configuration, partial coverage and identity changes', async ({
  page,
  context,
}) => {
  await login(page, 'browser-policing');
  const name = 'pol-ui',
    peer = 'pol-peer';
  const pid = Number(
    readFileSync(fixture.ovsDirectory + '/switch.pid', 'utf8').trim(),
  );
  let paused = false;
  const mutations: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions|interfaces)/.test(r.url())
    )
      mutations.push(r.method());
  });
  try {
    execFileSync('ip', [
      'link',
      'add',
      name,
      'type',
      'veth',
      'peer',
      'name',
      peer,
    ]);
    execFileSync('ip', ['link', 'set', name, 'up']);
    execFileSync('ip', ['link', 'set', peer, 'up']);
    vsctl(
      'add-port',
      'br-ui-parent',
      name,
      '--',
      'set',
      'Interface',
      name,
      'ingress_policing_rate=1000',
      'ingress_policing_kpkts_rate=0',
    );
    const iface = await kernelInterface(context, name);
    const path = '/interfaces/' + iface.management_id;
    await expect
      .poll(async () => {
        const detail = await get(context, path);
        return detail.linux_ingress_policing.actions.map(
          (a: { bytes_per_second: string; packets_per_second: string }) => [
            a.bytes_per_second,
            a.packets_per_second,
          ],
        );
      })
      .toContainEqual(['125000', null]);
    const initial = await get(context, path);
    expect(initial.linux_ingress_policing.availability).toBe('known');
    expect(initial.linux_ingress_policing.ifindex).toBe(hostLink(name).ifindex);
    expect(initial.linux_ingress_policing.source.authority).toBe(
      'linux-netlink-observation',
    );
    expect(initial.allowed_operations).toEqual([]);
    expect(
      JSON.parse(
        tc('-j', 'filter', 'show', 'dev', name, 'parent', 'ffff:'),
      ).some((f: { options?: { actions?: { kind: string }[] } }) =>
        f.options?.actions?.some((a) => a.kind === 'police'),
      ),
    ).toBe(true);
    await page.goto(fixture.origin + path);
    await expect(policingPanel(page)).toContainText('125000 bytes/s');
    await expect(policingPanel(page)).toContainText('Not reported');
    await screen(page, 'interface-policing-standard');
    await nativeTypeDepth(page, 'Expert');
    await expect(policingPanel(page)).toContainText('Filter evidence');
    await screen(page, 'interface-policing-expert');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-policing-dark');
    await nativeTypeDepth(page, 'Standard');
    for (const [width, label] of [
      [900, 'tablet'],
      [390, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height: 980 });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await expect(policingPanel(page).getByRole('button')).toHaveCount(0);
      await screen(page, 'interface-policing-' + label);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    tc(
      'filter',
      'add',
      'dev',
      name,
      'parent',
      'ffff:',
      'protocol',
      'all',
      'pref',
      '80',
      'matchall',
      'action',
      'pass',
    );
    const partial = await get(context, path);
    expect(partial.linux_ingress_policing.availability).toBe('partial');
    expect(partial.config_revision).toBe(initial.config_revision);
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(policingPanel(page)).toContainText('Partial coverage.');
    await expect(policingPanel(page)).toContainText('125000 bytes/s');
    await screen(page, 'interface-policing-partial');
    tc('filter', 'del', 'dev', name, 'parent', 'ffff:', 'pref', '80');
    process.kill(pid, 'SIGSTOP');
    paused = true;
    vsctl('--no-wait', 'set', 'Interface', name, 'ingress_policing_rate=2000');
    await expect
      .poll(
        async () =>
          (await get(context, path)).fields.ingress_policing_rate.value,
      )
      .toBe('2000');
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(
      configurationRow(page, 'Ingress bandwidth limit'),
    ).toContainText('2000 kbit/s');
    await expect(policingPanel(page)).toContainText('125000 bytes/s');
    await screen(page, 'interface-policing-config-pending');
    vsctl(
      '--no-wait',
      'set',
      'Interface',
      name,
      'ifindex=' + String(hostLink(name).ifindex + 100000),
    );
    await expect
      .poll(
        async () => (await get(context, path)).linux_ingress_policing.reason,
      )
      .toBe('LINUX_IFINDEX_MISMATCH');
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(policingPanel(page)).toContainText(
      'identities no longer match',
    );
    await expect(policingPanel(page)).not.toContainText('125000 bytes/s');
    await screen(page, 'interface-policing-identity-mismatch');
    vsctl(
      '--no-wait',
      'set',
      'Interface',
      name,
      'ifindex=' + String(hostLink(name).ifindex),
    );
    process.kill(pid, 'SIGCONT');
    paused = false;
    vsctl(
      'set',
      'Interface',
      name,
      'ingress_policing_rate=0',
      'ingress_policing_kpkts_rate=5',
    );
    await expect
      .poll(async () => {
        const v = (await get(context, path)).linux_ingress_policing;
        return v.actions.map(
          (a: {
            bytes_per_second: string | null;
            packets_per_second: string | null;
          }) => [a.bytes_per_second, a.packets_per_second],
        );
      })
      .toContainEqual([null, '5000']);
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(policingPanel(page)).toContainText('5000 packets/s');
    await expect(policingPanel(page)).toContainText('Not reported');
    await screen(page, 'interface-policing-packet');
    vsctl(
      'set',
      'Interface',
      name,
      'ingress_policing_rate=0',
      'ingress_policing_kpkts_rate=0',
    );
    await expect
      .poll(async () => {
        const v = (await get(context, path)).linux_ingress_policing;
        return [v.availability, v.actions.length];
      })
      .toEqual(['known', 0]);
    const refresh = page.getByRole('button', {
      name: 'Refresh resource',
      exact: true,
    });
    await refresh.focus();
    await page.keyboard.press('Enter');
    await expect(policingPanel(page)).toContainText(
      'No police action observed',
    );
    await screen(page, 'interface-policing-empty');
    vsctl(
      'set',
      'Interface',
      name,
      'ingress_policing_rate=1000',
      'ingress_policing_kpkts_rate=5',
    );
    await expect
      .poll(async () => {
        const v = await get(context, path);
        return [
          v.fields.ingress_policing_rate.value,
          v.fields.ingress_policing_kpkts_rate.value,
          v.linux_ingress_policing.actions.length,
        ];
      })
      .toEqual(['1000', '5', 0]);
    await refresh.click();
    await expect(
      configurationRow(page, 'Ingress bandwidth limit'),
    ).toContainText('1000 kbit/s');
    await expect(policingPanel(page)).toContainText(
      'No police action observed',
    );
    await expect(policingPanel(page)).toContainText(
      'does not establish that traffic is unrestricted',
    );
    await screen(page, 'interface-policing-conflicting-request');
    expect(mutations).toEqual([]);
  } finally {
    if (paused) process.kill(pid, 'SIGCONT');
    vsctl('--if-exists', 'del-port', 'br-ui-parent', name);
    spawnSync('ip', ['link', 'del', name]);
  }
});

test('Linux ingress policing withholds runtime configuration in both modes and marks unsupported bindings explicitly', async ({
  page,
  context,
  browser,
}) => {
  await login(page, 'browser-policing-observer');
  const iface = (await get(context, '/interfaces?filter=inv-p1')).items.find(
    (i: { name: string }) => i.name === 'inv-p1',
  );
  const detail = await get(context, '/interfaces/' + iface.management_id);
  expect(detail.linux_ingress_policing.availability).toBe('withheld');
  expect(detail.linux_ingress_policing.actions).toEqual([]);
  expect(detail.linux_ingress_policing.ifindex).toBeNull();
  await page.goto(fixture.origin + '/interfaces/' + iface.management_id);
  await expect(policingPanel(page)).toContainText(
    'Configuration read permission is required',
  );
  await screen(page, 'interface-policing-withheld-standard');
  await nativeTypeDepth(page, 'Expert');
  await expect(
    policingPanel(page).getByRole('region', {
      name: 'Linux ingress police actions',
      exact: true,
    }),
  ).toHaveCount(0);
  await screen(page, 'interface-policing-withheld-expert');
  const other = await browser.newContext({ ignoreHTTPSErrors: true });
  try {
    const reader = await other.newPage();
    await login(reader, 'browser-policing');
    await reader.goto(fixture.origin + '/interfaces/' + iface.management_id);
    await expect(policingPanel(reader)).toContainText(
      'No proven Linux device association',
    );
    await screen(reader, 'interface-policing-unsupported');
  } finally {
    await other.close();
  }
});
async function kernelInterface(context: BrowserContext, name: string) {
  let managementID = '';
  await expect
    .poll(async () => {
      const iface = (
        await get(context, '/interfaces?filter=' + name)
      ).items.find((i: { name: string }) => i.name === name) as
        | { management_id: string }
        | undefined;
      if (!iface) return null;
      managementID = iface.management_id;
      const detail = await get(context, '/interfaces/' + iface.management_id);
      return detail.linux_device?.availability;
    })
    .toBe('known');
  return get(context, '/interfaces/' + managementID);
}

test('Linux device detail separates real kernel carrier and MTU, hardware absence, depth and responsive review', async ({
  page,
  context,
}) => {
  await login(page, 'browser-linux-device');
  const name = 'plinux-ui',
    peer = 'plinux-peer';
  const mutations: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions|interfaces)/.test(r.url())
    )
      mutations.push(r.method());
  });
  try {
    execFileSync('ip', [
      'link',
      'add',
      name,
      'type',
      'veth',
      'peer',
      'name',
      peer,
    ]);
    vsctl('add-port', 'br-ui-parent', name);
    execFileSync('ip', ['link', 'set', name, 'up']);
    execFileSync('ip', ['link', 'set', peer, 'up']);
    const iface = await kernelInterface(context, name);
    expect(iface.linux_device.ifindex).toBe(hostLink(name).ifindex);
    expect(iface.linux_device.fields.mtu.value).toBe(hostLink(name).mtu);
    expect(iface.linux_device.fields.carrier.value).toBe(true);
    expect(iface.linux_device.source.provider_id).toBe('linux');
    expect(iface.fields.link_state.source.provider_id).toBe('ovsdb');
    expect(iface.linux_device.fields.pci_address.availability).toBe(
      'unavailable',
    );
    expect(iface.linux_device.fields.pci_address.value).toBeNull();
    expect(iface.allowed_operations).toEqual([]);
    await page.goto(fixture.origin + '/interfaces/' + iface.management_id);
    await expect(linuxRow(page, 'Linux carrier')).toContainText('Up (1)');
    await expect(linuxRow(page, 'Host PCI association')).toContainText(
      'Unavailable',
    );
    await expect(
      linuxRow(page, 'Host PCI association').locator('.badge'),
    ).toHaveAttribute('data-tone', 'neutral');
    await screen(page, 'interface-linux-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await expect(
      page.getByText('Linux association evidence', { exact: true }),
    ).toBeVisible();
    await screen(page, 'interface-linux-expert');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-linux-dark');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    for (const [width, label] of [
      [900, 'tablet'],
      [390, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height: 980 });
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await screen(page, 'interface-linux-' + label);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    execFileSync('ip', ['link', 'set', peer, 'down']);
    execFileSync('ip', ['link', 'set', name, 'mtu', '1800']);
    await expect
      .poll(async () => {
        const current = await get(
          context,
          '/interfaces/' + iface.management_id,
        );
        return [
          current.linux_device.fields.carrier.value,
          current.linux_device.fields.mtu.value,
          current.config_revision,
        ];
      })
      .toEqual([false, 1800, iface.config_revision]);
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .focus();
    await page.keyboard.press('Enter');
    await expect(linuxRow(page, 'Linux carrier')).toContainText('Down (0)');
    await expect(linuxRow(page, 'Linux device MTU')).toContainText(
      '1800 bytes',
    );
    await screen(page, 'interface-linux-carrier-down');
    expect(mutations).toEqual([]);
  } finally {
    vsctl('--if-exists', 'del-port', 'br-ui-parent', name);
    spawnSync('ip', ['link', 'del', name], { stdio: 'pipe' });
  }
});

function configurationRow(page: Page, label: string) {
  return page
    .getByRole('region', { name: 'Native configuration requests', exact: true })
    .getByRole('row')
    .filter({
      has: page.getByRole('rowheader', { name: label, exact: false }),
    });
}

const configurationColumns = [
  'ofport_request',
  'ingress_policing_rate',
  'ingress_policing_burst',
  'ingress_policing_kpkts_rate',
  'ingress_policing_kpkts_burst',
];

test('Interface configuration requests retain native units and empty values without implying runtime application', async ({
  page,
  context,
}) => {
  await login(page, 'browser-interface-config');
  const bridge = 'br-if-config',
    name = 'pif-config';
  const pid = Number(
    readFileSync(`${fixture.ovsDirectory}/switch.pid`, 'utf8'),
  );
  let paused = false;
  const mutations: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions|interfaces)/.test(r.url())
    )
      mutations.push(r.method());
  });
  vsctl(
    'add-br',
    bridge,
    '--',
    'set',
    'Bridge',
    bridge,
    'datapath_type=dummy',
    '--',
    'add-port',
    bridge,
    name,
    '--',
    'set',
    'Interface',
    name,
    'type=dummy',
    'ofport_request=24000',
    'ingress_policing_rate=1000',
    'ingress_policing_burst=0',
    'ingress_policing_kpkts_rate=5',
    'ingress_policing_kpkts_burst=0',
  );
  try {
    await expect
      .poll(async () => {
        const list = (await get(
          context,
          '/interfaces?filter=' + name,
        )) as InterfacePage;
        const iface = list.items.find((i) => i.name === name);
        return [
          iface?.fields?.ofport_request?.value,
          iface?.fields?.ofport?.value,
        ];
      })
      .toEqual([['24000'], ['24000']]);
    const list = (await get(
      context,
      '/interfaces?filter=' + name,
    )) as InterfacePage;
    const iface = list.items.find((i) => i.name === name);
    if (!iface?.fields)
      throw new Error('Native Interface configuration fields missing');
    const revision = iface.config_revision;
    for (const key of configurationColumns) {
      expect(iface.fields[key].availability).toBe('known');
      expect(iface.fields[key].source.authority).toBe('ovsdb-configuration');
      expect(iface.fields[key].editable).toBe(false);
    }
    expect(vsctl('get', 'Interface', name, 'ingress_policing_rate')).toBe(
      '1000',
    );
    await page.goto(fixture.origin + '/interfaces/' + iface.management_id);
    if (await page.getByRole('button', { name: 'Expert', exact: true }).count())
      await page.getByRole('button', { name: 'Expert', exact: true }).click();
    await expect(
      configurationRow(page, 'Requested OpenFlow port'),
    ).toContainText('24000');
    await expect(
      configurationRow(page, 'Ingress bandwidth limit'),
    ).toContainText('1000 kbit/s');
    await expect(
      configurationRow(page, 'Ingress bandwidth burst'),
    ).toContainText('Native default (0 kbit)');
    await expect(configurationRow(page, 'Ingress packet limit')).toContainText(
      '5 kpps',
    );
    await expect(configurationRow(page, 'Ingress packet burst')).toContainText(
      'Native default (0 kpackets)',
    );
    await screen(page, 'interface-config-standard');
    await page.getByRole('button', { name: 'Standard', exact: true }).click();
    await expect(
      configurationRow(page, 'Requested OpenFlow port'),
    ).toContainText('ovsdb-configuration');
    await screen(page, 'interface-config-expert');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-config-dark');
    await page.getByRole('button', { name: 'Expert', exact: true }).click();
    for (const [width, label] of [
      [900, 'tablet'],
      [390, 'mobile'],
    ] as const) {
      await page.setViewportSize({ width, height: 980 });
      await expect(
        configurationRow(page, 'Ingress bandwidth limit'),
      ).toContainText('1000 kbit/s');
      const region = page.getByRole('region', {
        name: 'Native configuration requests',
        exact: true,
      });
      await region.focus();
      await expect(region).toBeFocused();
      await page.keyboard.press('ArrowRight');
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
      ).toBe(true);
      await screen(page, 'interface-config-' + label);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    process.kill(pid, 'SIGSTOP');
    paused = true;
    vsctl('--no-wait', 'set', 'Interface', name, 'ofport_request=24001');
    await expect
      .poll(async () => {
        const current = await get(
          context,
          '/interfaces/' + iface.management_id,
        );
        return [
          current.fields.ofport_request.value,
          current.fields.ofport.value,
        ];
      })
      .toEqual([['24001'], ['24000']]);
    const pending = await get(context, '/interfaces/' + iface.management_id);
    expect(pending.config_revision).not.toBe(revision);
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(
      configurationRow(page, 'Requested OpenFlow port'),
    ).toContainText('24001');
    await expect(
      page
        .getByRole('region', { name: 'Native Interface fields', exact: true })
        .getByRole('row')
        .filter({
          has: page.getByRole('rowheader', {
            name: 'OpenFlow port',
            exact: true,
          }),
        }),
    ).toContainText('24000');
    await screen(page, 'interface-config-request-pending');
    process.kill(pid, 'SIGCONT');
    paused = false;
    vsctl(
      'clear',
      'Interface',
      name,
      'ofport_request',
      '--',
      'set',
      'Interface',
      name,
      'ingress_policing_rate=0',
      'ingress_policing_kpkts_rate=0',
    );
    await expect
      .poll(
        async () =>
          (await get(context, '/interfaces/' + iface.management_id)).fields
            .ofport_request.value,
      )
      .toEqual([]);
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(
      configurationRow(page, 'Requested OpenFlow port'),
    ).toContainText('Automatic allocation (native empty request)');
    await expect(
      configurationRow(page, 'Ingress bandwidth limit'),
    ).toContainText('Disabled (0 kbit/s)');
    await expect(configurationRow(page, 'Ingress packet limit')).toContainText(
      'Disabled (0 kpps)',
    );
    await screen(page, 'interface-config-empty-zero');
    expect(mutations).toEqual([]);
  } finally {
    if (paused) process.kill(pid, 'SIGCONT');
    vsctl('--if-exists', 'del-br', bridge);
  }
});

test('Interface configuration permission withholding remains identical in Standard and Expert', async ({
  page,
  context,
}) => {
  await login(page, 'browser-interface-config-observer');
  const bridge = 'br-if-cfg-view',
    name = 'pif-cfg-view';
  vsctl(
    'add-br',
    bridge,
    '--',
    'set',
    'Bridge',
    bridge,
    'datapath_type=dummy',
    '--',
    'add-port',
    bridge,
    name,
    '--',
    'set',
    'Interface',
    name,
    'type=dummy',
    'ofport_request=24100',
    'ingress_policing_rate=73',
    'ingress_policing_burst=8',
    'ingress_policing_kpkts_rate=5',
    'ingress_policing_kpkts_burst=3',
  );
  try {
    await expect
      .poll(async () => {
        const list = (await get(
          context,
          '/interfaces?filter=' + name,
        )) as InterfacePage;
        const iface = list.items.find((i) => i.name === name);
        return iface?.fields?.ofport?.value;
      })
      .toEqual(['24100']);
    const list = (await get(
      context,
      '/interfaces?filter=' + name,
    )) as InterfacePage;
    const iface = list.items.find((i) => i.name === name);
    if (!iface) throw new Error('Native Interface missing');
    const detail = await get(context, '/interfaces/' + iface.management_id);
    for (const key of configurationColumns) {
      expect(detail.fields[key].availability).toBe('withheld');
      expect(detail.fields[key].value).toBeNull();
      expect(detail.fields[key].reason).toBe('CONFIGURATION_WITHHELD');
      expect(detail.fields[key].editable).toBe(false);
    }
    await page.goto(fixture.origin + '/interfaces/' + iface.management_id);
    for (const label of ['standard', 'expert']) {
      if (
        label === 'standard' &&
        (await page
          .getByRole('button', { name: 'Expert', exact: true })
          .count())
      )
        await page.getByRole('button', { name: 'Expert', exact: true }).click();
      if (label === 'expert')
        await page
          .getByRole('button', { name: 'Standard', exact: true })
          .click();
      const region = page.getByRole('region', {
        name: 'Native configuration requests',
        exact: true,
      });
      await expect(region.getByText('Withheld', { exact: true })).toHaveCount(
        5,
      );
      await expect(region).not.toContainText('73 kbit/s');
      await screen(page, 'interface-config-withheld-' + label);
    }
    await expect(
      page.getByRole('link', { name: 'Edit MTU request →', exact: true }),
    ).toHaveCount(0);
  } finally {
    vsctl('--if-exists', 'del-br', bridge);
  }
});

test('Linux device identity mismatch, disappearance, unsupported types and withheld configuration stay explicit', async ({
  page,
  context,
}) => {
  await login(page, 'browser-linux-exceptions');
  const name = 'plinux-err',
    peer = 'plinux-epeer';
  const pid = Number(
    readFileSync(fixture.ovsDirectory + '/switch.pid', 'utf8').trim(),
  );
  let paused = false;
  try {
    execFileSync('ip', [
      'link',
      'add',
      name,
      'type',
      'veth',
      'peer',
      'name',
      peer,
    ]);
    vsctl('add-port', 'br-ui-parent', name);
    execFileSync('ip', ['link', 'set', name, 'up']);
    execFileSync('ip', ['link', 'set', peer, 'up']);
    const iface = await kernelInterface(context, name);
    expect(iface.fields.type.availability).toBe('withheld');
    expect(iface.fields.mtu_request.availability).toBe('withheld');
    expect(iface.linux_device.fields.carrier.value).toBe(true);
    await page.goto(fixture.origin + '/interfaces/' + iface.management_id);
    await expect(linuxRow(page, 'Linux carrier')).toContainText('Up (1)');
    await screen(page, 'interface-linux-withheld');
    process.kill(pid, 'SIGSTOP');
    paused = true;
    vsctl(
      '--no-wait',
      'set',
      'Interface',
      name,
      'ifindex=' + (iface.linux_device.ifindex + 1000),
    );
    await expect
      .poll(
        async () =>
          (await get(context, '/interfaces/' + iface.management_id))
            .linux_device.reason,
      )
      .toBe('LINUX_IFINDEX_MISMATCH');
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(
      page.getByText('Linux and OVS report different device identities.', {
        exact: false,
      }),
    ).toBeVisible();
    await expect(linuxRow(page, 'Linux carrier')).toContainText('Unavailable');
    await screen(page, 'interface-linux-identity-mismatch');
    vsctl(
      '--no-wait',
      'set',
      'Interface',
      name,
      'ifindex=' + iface.linux_device.ifindex,
    );
    execFileSync('ip', ['link', 'del', name]);
    await expect
      .poll(
        async () =>
          (await get(context, '/interfaces/' + iface.management_id))
            .linux_device.reason,
      )
      .toBe('LINUX_DEVICE_NOT_FOUND');
    await page
      .getByRole('button', { name: 'Refresh resource', exact: true })
      .click();
    await expect(
      page.getByText('The associated Linux device is not present.', {
        exact: true,
      }),
    ).toBeVisible();
    await screen(page, 'interface-linux-device-missing');
    // A same-name Linux replacement cannot inherit the original OVS ifindex.
    execFileSync('ip', [
      'link',
      'add',
      name,
      'type',
      'veth',
      'peer',
      'name',
      peer,
    ]);
    expect(hostLink(name).ifindex).not.toBe(iface.linux_device.ifindex);
    await expect
      .poll(
        async () =>
          (await get(context, '/interfaces/' + iface.management_id))
            .linux_device.reason,
      )
      .toBe('LINUX_IFINDEX_MISMATCH');
    process.kill(pid, 'SIGCONT');
    paused = false;
    vsctl('--if-exists', 'del-port', 'br-ui-parent', name);
    const response = await context.request.get(
      fixture.origin + '/api/v1/interfaces/' + iface.management_id,
    );
    await expect
      .poll(async () =>
        (
          await context.request.get(
            fixture.origin + '/api/v1/interfaces/' + iface.management_id,
          )
        ).status(),
      )
      .toBe(404);
    expect([200, 404]).toContain(response.status());
    const dummy = (await get(context, '/interfaces?filter=inv-p1')).items.find(
      (i: { name: string }) => i.name === 'inv-p1',
    );
    const unsupported = await get(
      context,
      '/interfaces/' + dummy.management_id,
    );
    expect(unsupported.linux_device.reason).toBe('OVS_DEVICE_BINDING_UNPROVEN');
    expect(unsupported.linux_device.fields).toEqual({});
    await page.goto(fixture.origin + '/interfaces/' + dummy.management_id);
    await expect(
      page.getByText('This Interface has no proven Linux device association.', {
        exact: false,
      }),
    ).toBeVisible();
    await screen(page, 'interface-linux-unsupported');
  } finally {
    if (paused) process.kill(pid, 'SIGCONT');
    vsctl('--if-exists', 'del-port', 'br-ui-parent', name);
    spawnSync('ip', ['link', 'del', name], { stdio: 'pipe' });
  }
});

test('native types and reciprocal patch configuration preserve exact navigation through exceptions and recreation', async ({
  page,
  context,
}) => {
  await login(page, 'browser-native-types');
  const mutations: string[] = [];
  page.on('request', (r) => {
    if (
      r.method() !== 'GET' &&
      /\/api\/v1\/(candidate|transactions|interfaces)/.test(r.url())
    )
      mutations.push(r.method());
  });
  createPatchPair('ui');
  try {
    await expect
      .poll(async () => await observedPatchReason(context, 'patch-uia'))
      .toBe('PATCH_RECIPROCAL_CONFIGURATION');
    const a = await patchItem(context, 'patch-uia');
    const b = await patchItem(context, 'patch-uib');
    expect(a.patch_peer?.peer_ref?.id).toBe(b.management_id);
    expect(a.patch_peer?.peer_port_ref).toEqual(b.port_ref);
    expect(a.patch_peer?.peer_bridge_ref).toEqual(b.bridge_ref);
    await page.goto(fixture.origin + `/interfaces/${a.management_id}`);
    const panel = page.getByRole('region', {
      name: 'Native type and associations',
    });
    await nativeTypeDepth(page, 'Standard');
    await expect(panel).toContainText('Reciprocal configuration observed.');
    await expect(panel).toContainText('does not prove packet forwarding');
    await expect(
      panel.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toHaveAttribute('href', `/interfaces/${b.management_id}`);
    await panel
      .getByRole('link', { name: 'Peer Interface →', exact: true })
      .focus();
    await expect(
      panel.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(
      fixture.origin + `/interfaces/${b.management_id}`,
    );
    await page.goto(fixture.origin + `/interfaces/${a.management_id}`);
    await nativeTypeDepth(page, 'Standard');
    await expect(
      panel.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toHaveAttribute('href', `/interfaces/${b.management_id}`);
    await screen(page, 'interface-native-types-standard');
    await nativeTypeDepth(page, 'Expert');
    await expect(panel).toContainText('PATCH_RECIPROCAL_CONFIGURATION');
    await screen(page, 'interface-native-types-expert');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await screen(page, 'interface-native-types-dark');
    await page
      .getByRole('button', { name: 'Toggle color theme', exact: true })
      .click();
    await nativeTypeDepth(page, 'Standard');
    for (const [name, width, height] of [
      ['tablet', 820, 1180],
      ['mobile', 390, 844],
    ] as const) {
      await page.setViewportSize({ width, height });
      await expect(
        panel.getByRole('link', { name: 'Peer Interface →', exact: true }),
      ).toBeVisible();
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth + 1,
        ),
      ).toBe(true);
      await screen(page, `interface-native-types-${name}`);
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    vsctl('set', 'Interface', 'patch-uib', 'options:peer=missing-ui-peer');
    await expect(panel).toContainText('does not point back');
    await expect(panel.getByRole('link')).toHaveCount(0);
    await screen(page, 'interface-patch-one-way');
    vsctl('set', 'Interface', 'patch-uib', 'options:peer=patch-uia');
    vsctl('set', 'Interface', 'patch-uia', 'options:peer=missing-ui-peer');
    await expect(panel).toContainText('absent from this inventory snapshot');
    await expect(panel.getByRole('link')).toHaveCount(0);
    await screen(page, 'interface-patch-missing');
    vsctl('set', 'Interface', 'patch-uia', 'options:peer=patch-uib');
    vsctl('--no-wait', 'set', 'Bridge', 'br-patch-uib', 'datapath_type=netdev');
    await expect(panel).toContainText('different Bridge datapath types');
    await expect(panel.getByRole('link')).toHaveCount(0);
    await screen(page, 'interface-patch-datapath-mismatch');
    vsctl('--no-wait', 'set', 'Bridge', 'br-patch-uib', 'datapath_type=dummy');
    await expect(
      panel.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toBeVisible();
    vsctl('del-port', 'br-patch-uib', 'patch-uib');
    await expect(panel).toContainText('absent from this inventory snapshot');
    vsctl(
      'add-port',
      'br-patch-uib',
      'patch-uib',
      '--',
      'set',
      'Interface',
      'patch-uib',
      'type=patch',
      'options:peer=patch-uia',
    );
    await expect
      .poll(async () => await observedPatchReason(context, 'patch-uia'))
      .toBe('PATCH_RECIPROCAL_CONFIGURATION');
    const replacement = await patchItem(context, 'patch-uib');
    expect(replacement.management_id).not.toBe(b.management_id);
    await expect(
      panel.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toHaveAttribute('href', `/interfaces/${replacement.management_id}`);
    await page.goto(fixture.origin + `/interfaces/${b.management_id}`);
    await expect(
      page.getByText('This Interface identity is no longer available.', {
        exact: false,
      }),
    ).toBeVisible();
    await screen(page, 'interface-patch-retired');
    await page.goto(fixture.origin + `/interfaces/${a.management_id}`);
    vsctl('--no-wait', 'set', 'Interface', 'patch-uia', 'type=future-native');
    await expect(panel).toContainText(
      'Unrecognized native type · future-native',
    );
    await expect(panel.getByRole('link')).toHaveCount(0);
    await screen(page, 'interface-native-types-unknown');
    vsctl('--no-wait', 'set', 'Interface', 'patch-uia', 'type=dpdk');
    await expect(panel).toContainText('DPDK · dpdk');
    await expect(panel).toContainText(
      'no hardware association, runtime readiness or tuning authority',
    );
    await screen(page, 'interface-native-types-dpdk');
    vsctl(
      '--no-wait',
      'clear',
      'Interface',
      'patch-uia',
      'options',
      '--',
      'set',
      'Interface',
      'patch-uia',
      'type=geneve',
      'options:remote_ip=flow',
      'options:key=flow',
    );
    await expect(panel).toContainText('Tunnel · geneve');
    await expect(panel).toContainText('depend on OpenFlow actions');
    await screen(page, 'interface-native-types-tunnel');
    expect(mutations).toEqual([]);
  } finally {
    clearPatchPair('ui');
  }
});

test('patch configuration stays withheld across modes and removes navigation when the real provider stops', async ({
  page,
  context,
}) => {
  await login(page, 'browser-patch-observer');
  createPatchPair('sec');
  const readerContext = await context
    .browser()!
    .newContext({ ignoreHTTPSErrors: true });
  const reader = await readerContext.newPage();
  try {
    await login(reader, 'browser-native-types');
    await expect
      .poll(async () => await observedPatchReason(readerContext, 'patch-seca'))
      .toBe('PATCH_RECIPROCAL_CONFIGURATION');
    const a = await patchItem(readerContext, 'patch-seca');
    await page.goto(fixture.origin + `/interfaces/${a.management_id}`);
    const hidden = await get(context, `/interfaces/${a.management_id}`);
    expect(hidden.patch_peer.availability).toBe('withheld');
    for (const key of ['peer_ref', 'peer_port_ref', 'peer_bridge_ref'])
      expect(hidden.patch_peer[key]).toBeNull();
    const panel = page.getByRole('region', {
      name: 'Native type and associations',
    });
    for (const mode of ['Standard', 'Expert'] as const) {
      await nativeTypeDepth(page, mode);
      await expect(panel).toContainText('Withheld');
      await expect(panel.getByRole('link')).toHaveCount(0);
      await expect(
        panel.getByRole('heading', {
          name: 'Configured patch peer',
          exact: true,
        }),
      ).toHaveCount(0);
      await screen(page, `interface-patch-withheld-${mode.toLowerCase()}`);
    }
    await reader.goto(fixture.origin + `/interfaces/${a.management_id}`);
    const visible = reader.getByRole('region', {
      name: 'Native type and associations',
    });
    await expect(
      visible.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toBeVisible();
    execFileSync('ovs-appctl', [
      '-t',
      `${fixture.ovsDirectory}/db.ctl`,
      'exit',
    ]);
    try {
      await expect(visible).toContainText('The association is not current.');
      await expect(visible).toContainText('Last observed: Patch');
      await expect(visible.getByRole('link')).toHaveCount(0);
      const stale = await get(readerContext, `/interfaces/${a.management_id}`);
      expect(stale.patch_peer.reason).toBe('PATCH_OBSERVATION_STALE');
      expect(stale.patch_peer.peer_ref).toBeNull();
      await screen(reader, 'interface-patch-stale');
      const stillHidden = await get(context, `/interfaces/${a.management_id}`);
      expect(stillHidden.patch_peer.reason).toBe('CONFIGURATION_WITHHELD');
      expect(stillHidden.patch_peer.peer_ref).toBeNull();
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
      visible.getByRole('link', { name: 'Peer Interface →', exact: true }),
    ).toBeVisible();
  } finally {
    await readerContext.close();
    clearPatchPair('sec');
  }
});

test('Interface evidence keeps withheld permissions, empty history and retired identities explicit', async ({
  page,
  browser,
}) => {
  await login(page, 'browser-evidence-reader');
  const other = await browser.newContext({ ignoreHTTPSErrors: true });
  const observer = await other.newPage();
  createPatchPair('ev');
  try {
    await login(observer, 'browser-evidence-observer');
    await expect
      .poll(async () => await observedPatchReason(page.context(), 'patch-eva'))
      .toBe('PATCH_RECIPROCAL_CONFIGURATION');
    const item = await patchItem(page.context(), 'patch-eva');
    await observer.goto(fixture.origin + `/interfaces/${item.management_id}`);
    const hidden = observer.getByRole('region', {
      name: 'Interface shared evidence',
    });
    for (const mode of ['Standard', 'Expert'] as const) {
      await nativeTypeDepth(observer, mode);
      await expect(hidden).toContainText(
        'Events unavailable with current authorization.',
      );
      await expect(hidden).toContainText(
        'Audit unavailable with current authorization.',
      );
      await expect(hidden.getByRole('link')).toHaveCount(0);
      await screen(
        observer,
        `interface-evidence-withheld-${mode.toLowerCase()}`,
      );
    }
    const denied = await other.request.get(
      fixture.origin + `/api/v1/audit?object_id=${item.management_id}`,
    );
    expect(denied.status()).toBe(403);
    await observer.goto(
      fixture.origin + `/operations/audit?object_id=${item.management_id}`,
    );
    await expect(
      observer.getByText('Permission denied', { exact: true }),
    ).toBeVisible();
    await expect(
      observer.getByText('No authorized retained records in this scope.', {
        exact: true,
      }),
    ).toHaveCount(0);
    await page.goto(
      fixture.origin + `/operations/audit?object_id=${item.management_id}`,
    );
    await expect(
      page.getByText('No authorized retained records in this scope.', {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByText(
        'An empty result does not prove that no change occurred.',
        { exact: false },
      ),
    ).toBeVisible();
    await screen(page, 'interface-evidence-empty');
    const oldID = item.management_id;
    vsctl(
      'del-port',
      'br-patch-eva',
      'patch-eva',
      '--',
      'add-port',
      'br-patch-eva',
      'patch-eva',
      '--',
      'set',
      'Interface',
      'patch-eva',
      'type=patch',
      'options:peer=patch-evb',
    );
    await expect
      .poll(async () => {
        const values: InterfacePage = await get(
          page.context(),
          '/interfaces?filter=patch-eva',
        );
        return (
          values.items.find((i) => i.name === 'patch-eva')?.management_id ??
          oldID
        );
      })
      .not.toBe(oldID);
    await page.goto(fixture.origin + `/interfaces/${oldID}`);
    await expect(
      page.getByText('This Interface identity is no longer available.', {
        exact: false,
      }),
    ).toBeVisible();
    const entry = page.getByRole('region', {
      name: 'Interface shared evidence',
    });
    await expect(
      entry.getByRole('link', { name: 'Open Interface Audit', exact: true }),
    ).toHaveAttribute('href', `/operations/audit?object_id=${oldID}`);
    await screen(page, 'interface-evidence-retired');
    await entry
      .getByRole('link', { name: 'Open Interface Audit', exact: true })
      .click();
    await page.reload();
    expect(new URL(page.url()).searchParams.get('object_id')).toBe(oldID);
    await expect(
      page.getByRole('region', { name: 'Evidence object scope' }),
    ).toContainText(oldID);
    unit('stop', 'mgrd');
    try {
      await expect(
        page
          .getByRole('alert')
          .filter({ hasText: 'Current authorization or connection' }),
      ).toBeVisible();
      await expect(
        page.getByRole('region', { name: 'Shared evidence records' }),
      ).toHaveCount(0);
      await screen(page, 'interface-evidence-unavailable');
    } finally {
      unit('start', 'mgrd');
    }
    await expect(
      page.getByText('No authorized retained records in this scope.', {
        exact: true,
      }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get('object_id')).toBe(oldID);
  } finally {
    await other.close();
    clearPatchPair('ev');
  }
});
