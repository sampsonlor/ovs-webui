import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';

if (!process.env.OVS_FRONTEND_FIXTURE)
  throw new Error('Requires actual isolated Go/OVS services.');
const fixture = JSON.parse(
  readFileSync(process.env.OVS_FRONTEND_FIXTURE, 'utf8'),
);
const base = fixture.origin + '/switching/mirrors';
const href = base + '/' + fixture.mirror.management_id;
const vs = (...args: string[]) =>
  execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    { encoding: 'utf8' },
  ).trim();
async function login(page: Page, user: string) {
  await page.goto(href);
  await page.getByLabel('Username', { exact: true }).fill(user);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[user]);
  const reply = page.waitForResponse(
    (r) =>
      r.request().method() === 'POST' && r.url().endsWith('/api/v1/sessions'),
  );
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await reply).status()).toBe(201);
  await page.goto(href);
}
async function screen(page: Page, name: string) {
  mkdirSync('test-results/frontend-evidence', { recursive: true });
  await page.screenshot({
    path: 'test-results/frontend-evidence/mirror-' + name + '.png',
    fullPage: true,
  });
}
async function decision(page: Page, name: string, state: string) {
  for (let review = 0; review < 4; review++) {
    const reply = page.waitForResponse(
      (r) => r.request().method() === 'POST' && r.url().endsWith('/decisions'),
    );
    await page.getByRole('button', { name, exact: true }).click();
    const response = await reply;
    if (response.status() === 202) {
      await expect(page.getByTestId('safe-state')).toHaveText(state);
      return;
    }
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
  throw new Error('Explicit decision did not stabilize');
}
async function apply(page: Page, label: string) {
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('table', {
      name: 'Original → Current → Yours · immutable objects and native settings',
    }),
  ).toContainText('Mirror');
  await screen(page, 'candidate-' + label);
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  await expect(
    page.getByRole('heading', { name: 'Diff & Validation', exact: true }),
  ).toBeVisible();
  await page
    .getByLabel('Change reason', { exact: true })
    .fill('Synthetic native Mirror review');
  await page.getByRole('checkbox').check();
  await screen(page, 'validation-' + label);
  await page
    .getByRole('button', { name: 'Start Safe Apply', exact: true })
    .click();
  await expect(page).toHaveURL(/\/changes\/transactions\/[0-9a-f-]+$/);
  await expect(page.getByTestId('safe-state')).toHaveText(
    'awaiting-confirmation',
  );
}

test('native Mirror review separates direction, permission, native counters and stale observations', async ({ page }) => {
  await login(page, 'browser-mirror-reader');
  await expect(page.getByRole('heading', { name: 'mi-ui-data', exact: true })).toBeVisible();
  await expect(page.getByText('Your current permissions allow review but do not allow Mirror changes.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Stage Mirror change', exact: true })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Native copy counters', exact: true })).toBeVisible();
  await screen(page, 'reader-standard');
  await page.goto(base+'?filter=mi-ui-data&limit=1');
  await expect(page.getByRole('table', { name:'Observed native Mirror sessions in this page' })).toContainText('mi-ui-data');
  await screen(page,'list');
  await page.goto(base+'?filter=synthetic-no-mirror-match');
  await expect(page.getByText('No Mirror sessions in this observation.',{exact:true})).toBeVisible();
  await screen(page,'empty');
  await page.goto(href);
  await expect(page.getByRole('heading',{name:'mi-ui-data',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Standard',exact:true}).click();
  await expect(page.getByText('Mirror selectors, output_port / output_vlan and snaplen; Bridge.mirrors membership',{exact:true})).toBeVisible();
  await screen(page,'expert');
  await page.getByRole('button',{name:'Toggle color theme',exact:true}).click();
  await screen(page,'dark');
  for(const [width,height,label] of [[900,1100,'tablet'],[390,844,'mobile']] as const){
    await page.setViewportSize({width,height});
    await expect(page.getByText('Review mode. Use a desktop to prepare a new Mirror change.',{exact:true})).toBeVisible();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await screen(page,label);
  }
  await page.setViewportSize({width:1440,height:1000});
  execFileSync('ovs-appctl',['-t',`${fixture.ovsDirectory}/db.ctl`,'exit']);
  try {
    await expect(page.getByText('Values are last observed. Configuration changes require a fresh observation.',{exact:true})).toBeVisible();
    await expect(page.getByText('Wait for a fresh native observation before preparing a Mirror change.',{exact:true})).toBeVisible();
    await screen(page,'stale');
  } finally {
    execFileSync('ovsdb-server',[fixture.database,`--remote=punix:${fixture.dbSocket}`,`--pidfile=${fixture.ovsDirectory}/db.pid`,`--unixctl=${fixture.ovsDirectory}/db.ctl`,'--detach','--no-chdir','--overwrite-pidfile'],{env:{...process.env,OVS_RUNDIR:fixture.ovsDirectory,OVS_LOGDIR:fixture.ovsDirectory,OVS_DBDIR:fixture.ovsDirectory},stdio:'pipe'});
  }
  await expect(page.getByText('Observation: fresh',{exact:false})).toBeVisible();
  await page.getByRole('button',{name:'Sign out',exact:true}).click();
  await login(page,'browser-mirror-observer');
  await expect(page.getByText('Permission denied',{exact:true})).toBeVisible();
  await expect(page.getByRole('heading',{name:'mi-ui-data',exact:true})).toHaveCount(0);
  await screen(page,'withheld');
});

test('native Mirror CRUD shares review, preserves native selectors and recovers a fresh session identity', async ({page})=>{
  await login(page,'browser-mirror');
  await expect(page.getByRole('button',{name:'Stage Mirror change',exact:true})).toBeVisible();
  await screen(page,'editor');
  await page.goto(base+'/new');
  await page.getByLabel('Bridge',{exact:true}).selectOption(fixture.mirrorBridge.management_id);
  await page.getByLabel('New immutable session name',{exact:true}).selectOption('mi-ui-new');
  await page.getByLabel('Select all Bridge Ports in both directions',{exact:true}).check();
  await page.getByLabel('VLAN filter (optional, comma separated)',{exact:true}).fill('100');
  await page.getByLabel('Copy destination type',{exact:true}).selectOption('vlan');
  await page.getByLabel('RSPAN VLAN',{exact:true}).fill('200');
  await page.getByLabel('RSPAN VLAN',{exact:true}).focus();await page.keyboard.press('Tab');
  await expect(page.getByLabel('Snap length in bytes (optional)',{exact:true})).toBeFocused();
  await page.getByLabel('I reviewed the selectors, copy destination and management connectivity.',{exact:true}).check();
  await screen(page,'creation');
  await page.getByRole('button',{name:'Stage Mirror change',exact:true}).click();
  await apply(page,'create');
  expect(vs('get','Mirror','mi-ui-new','select_all')).toBe('true');
  expect(vs('get','Mirror','mi-ui-new','select_vlan')).toBe('[100]');
  expect(vs('get','Mirror','mi-ui-new','output_vlan')).toBe('[200]');
  await decision(page,'Confirm configuration','confirmed');
  await page.goto(href);
  await expect(page.getByRole('button',{name:'Stage Mirror change',exact:true})).toBeVisible();
  const columns=['select_all','select_src_port','select_dst_port','select_vlan','output_port','output_vlan','snaplen','external_ids'];
  const original=vs('get','Mirror','mi-ui-data',...columns);
  const originalFields=vs('get','Mirror','mi-ui-data',...columns.slice(0,-1));
  const oldID=vs('get','Mirror','mi-ui-data','_uuid');
  await page.getByRole('group',{name:'Traffic entering source Ports',exact:true}).getByLabel('mi-ui-src',{exact:true}).uncheck();
  await page.getByRole('group',{name:'Traffic leaving destination Ports',exact:true}).getByLabel('mi-ui-dst',{exact:true}).check();
  await page.getByLabel('Copy destination type',{exact:true}).selectOption('vlan');
  await page.getByLabel('RSPAN VLAN',{exact:true}).fill('200');
  await page.getByLabel('Snap length in bytes (optional)',{exact:true}).fill('128');
  await page.getByLabel('I reviewed the selectors, copy destination and management connectivity.',{exact:true}).check();
  await page.getByRole('button',{name:'Stage Mirror change',exact:true}).click();
  await apply(page,'configure');
  expect(vs('get','Mirror','mi-ui-data','select_src_port')).toBe('[]');
  expect(vs('get','Mirror','mi-ui-data','output_vlan')).toBe('[200]');
  await decision(page,'Request rollback','rolled-back');
  expect(vs('get','Mirror','mi-ui-data',...columns)).toBe(original);
  await screen(page,'configuration-recovered');
  await page.goto(fixture.origin+'/changes/candidate');
  await page.getByRole('button',{name:'Discard intents…',exact:true}).click();
  const discarded=page.waitForResponse(r=>r.request().method()==='PATCH' && r.url().endsWith('/api/v1/candidate'));
  await page.getByRole('button',{name:'Confirm discard',exact:true}).click();
  expect((await discarded).status()).toBe(200);
  await expect(page.getByText('0 staged intents',{exact:true})).toBeVisible();
  await page.goto(href);
  await page.getByLabel('I reviewed the Mirror deletion and affected monitoring.',{exact:true}).check();
  await page.getByRole('button',{name:'Stage Mirror deletion',exact:true}).click();
  await apply(page,'delete');
  expect(vs('--if-exists','get','Mirror',oldID,'_uuid')).toBe('');
  await decision(page,'Request rollback','rolled-back');
  expect(vs('get','Mirror','mi-ui-data','_uuid')).not.toBe(oldID);
  expect(vs('get','Mirror','mi-ui-data',...columns.slice(0,-1))).toBe(originalFields);
  expect(vs('get','Mirror','mi-ui-data','external_ids:mirror-foreign')).toBe('preserve');
  const settled=vs('get','Open_vSwitch','.','next_cfg');
  await page.getByRole('button',{name:'Refresh evidence',exact:true}).click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  expect(vs('get','Open_vSwitch','.','next_cfg')).toBe(settled);
  await screen(page,'deletion-recovered');
  await page.goto(href);
  await expect(page.getByText('Unavailable',{exact:true})).toBeVisible();
  await expect(page.getByText('NOT_FOUND. Refresh to read current evidence.',{exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'Stage Mirror change',exact:true})).toHaveCount(0);
  await screen(page,'retired-identity');
  await expect(page.getByText('NOT_FOUND. Refresh to read current evidence.',{exact:true})).toBeVisible();
  await page.goto(base+'?filter=mi-ui-data');
  await page.getByRole('link',{name:'mi-ui-data',exact:true}).click();
  await expect(page.getByText('Current local ownership of the session, Bridge and affected Ports is required before preparing a change.',{exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'Stage Mirror change',exact:true})).toHaveCount(0);
  await screen(page,'fresh-identity-requires-grant');
});
