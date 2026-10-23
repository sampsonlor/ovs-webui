import { test, expect, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdirSync } from 'node:fs';

if(!process.env.OVS_FRONTEND_FIXTURE) throw new Error('Requires actual isolated Go/OVS services.');
const fixture=JSON.parse(readFileSync(process.env.OVS_FRONTEND_FIXTURE,'utf8'));
const href=fixture.origin+'/switching/isolation/'+fixture.isolation.management_id;
const vs=(...args:string[])=>execFileSync('ovs-vsctl',['--timeout=5',`--db=unix:${fixture.dbSocket}`,...args],{encoding:'utf8'}).trim();
async function login(page:Page,user:string) {
  await page.goto(href);
  await page.getByLabel('Username',{exact:true}).fill(user);
  await page.getByLabel('Password',{exact:true}).fill(fixture.accounts[user]);
  const reply=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/api/v1/sessions'));
  await page.getByRole('button',{name:'Sign in',exact:true}).click();
  expect((await reply).status()).toBe(201);
  await page.goto(href);
  await expect(page.getByRole('heading',{name:'iso-ui-data',exact:true})).toBeVisible();
}
async function screen(page:Page,name:string) {
  mkdirSync('test-results/frontend-evidence',{recursive:true});
  await page.screenshot({path:'test-results/frontend-evidence/isolation-'+name+'.png',fullPage:true});
}
async function decision(page:Page,name:string,state:string) {
  for(let review=0;review<4;review++) {
    const reply=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/decisions'));
    await page.getByRole('button',{name,exact:true}).click();
    const response=await reply;
    if(response.status()===202) { await expect(page.getByTestId('safe-state')).toHaveText(state);return; }
    const problem=await response.json();
    expect(response.status()).toBe(409);expect(problem.code).toBe('TRANSACTION_VERSION_CHANGED');expect(problem.command_effect).toBe('not-started');
    await expect(page.getByRole('button',{name:'Recover original request'})).toHaveCount(0);
    await page.getByRole('button',{name:'Refresh evidence',exact:true}).click();
  }
  throw new Error('Explicit decision did not stabilize');
}
async function apply(page:Page,desired:'protected'|'unprotected') {
  await page.goto(href);
  await page.getByLabel('Desired isolation',{exact:true}).selectOption(desired);
  await page.getByRole('button',{name:'Stage isolation change',exact:true}).click();
  await expect(page.getByRole('heading',{name:'Candidate Workspace',exact:true})).toBeVisible();
  await expect(page.getByRole('table',{name:'Original → Current → Yours · values supplied by the server'})).toContainText('protected');
  await page.getByRole('button',{name:'Validate Candidate',exact:true}).click();
  await expect(page.getByRole('heading',{name:'Diff & Validation',exact:true})).toBeVisible();
  await expect(page.getByText('Protected ports cannot communicate with other protected ports on this Bridge. Review management connectivity before applying.',{exact:true})).toBeVisible();
  await expect(page.getByText('PROTECTED_PORT_NATIVE_SEMANTICS_REVIEW_REQUIRED',{exact:true})).toHaveCount(0);
  await screen(page,'validation-'+desired);
  await page.getByLabel('Change reason',{exact:true}).fill('Synthetic native protected Port review');
  await page.getByRole('checkbox').check();
  await page.getByRole('button',{name:'Start Safe Apply',exact:true}).click();
  await expect(page).toHaveURL(/\/changes\/transactions\/[0-9a-f-]+$/);
  await expect(page.getByTestId('safe-state')).toHaveText('awaiting-confirmation');
  expect(vs('get','Port','iso-ui-data','protected')).toBe(String(desired==='protected'));
}

test('native isolation review keeps read permission, depth and mobile responsibility separate',async({page})=>{
  await login(page,'browser-isolation-reader');
  await page.goto(fixture.origin+'/switching/isolation?filter=iso-ui-data&limit=1');
  await expect(page.getByRole('table',{name:'Observed ports in this page'})).toContainText('iso-ui-data');
  await screen(page,'list');
  await page.getByRole('link',{name:'iso-ui-data',exact:true}).click();
  await expect(page.getByRole('heading',{name:'iso-ui-data',exact:true})).toBeVisible();
  await expect(page.getByText('Configuration unavailable:',{exact:false})).toBeVisible();
  await expect(page.getByText('Your current permissions allow review but do not allow isolation changes.',{exact:false})).toBeVisible();
  await expect(page.getByRole('button',{name:'Stage isolation change',exact:true})).toHaveCount(0);
  await screen(page,'reader-standard');
  await page.getByRole('button',{name:'Standard',exact:true}).click();
  await screen(page,'reader-expert');
  await page.getByRole('button',{name:'Toggle color theme',exact:true}).click();
  await screen(page,'reader-dark');
  for(const [width,height,label] of [[900,1100,'tablet'],[390,844,'mobile']] as const) {
    await page.setViewportSize({width,height});
    await expect(page.getByText('Review mode. Use a desktop to prepare a change.',{exact:true})).toBeVisible();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await screen(page,label);
  }
});

test('native protected Port uses shared confirmation and exact Safe Apply recovery',async({page})=>{
  await login(page,'browser-isolation');
  await screen(page,'editor');
  await apply(page,'protected');
  await decision(page,'Confirm configuration','confirmed');
  expect(vs('get','Port','iso-ui-data','protected')).toBe('true');
  await screen(page,'confirmed');
  await apply(page,'unprotected');
  await decision(page,'Request rollback','rolled-back');
  expect(vs('get','Port','iso-ui-data','protected')).toBe('true');
  const settled=vs('get','Open_vSwitch','.','next_cfg');
  await page.getByRole('button',{name:'Refresh evidence',exact:true}).click();
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  expect(vs('get','Open_vSwitch','.','next_cfg')).toBe(settled);
  await screen(page,'recovered');
  vs('set','Port','iso-ui-data','protected=false');
  const external=vs('get','Open_vSwitch','.','next_cfg');
  await page.goto(fixture.origin+'/changes/candidate');
  await expect(page.getByRole('region',{name:'Isolation change needs review',exact:true})).toBeVisible();
  await expect(page.getByText('Live isolation changed after this draft was captured. Discard this intent and review the current Port.',{exact:true})).toBeVisible();
  await expect(page.getByRole('button',{name:'Validate Candidate',exact:true})).toBeDisabled();
  await expect(page.getByRole('button',{name:'Rebase reviewed choices',exact:true})).toHaveCount(0);
  expect(vs('get','Port','iso-ui-data','protected')).toBe('false');
  expect(vs('get','Open_vSwitch','.','next_cfg')).toBe(external);
  await screen(page,'conflict');
});
