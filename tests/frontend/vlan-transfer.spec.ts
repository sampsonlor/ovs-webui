import { test, expect, type Page, type Response } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import type {
  Accepted,
  Candidate,
  Port,
  Session,
  Transaction,
  Validation,
  VLANConfigurationExport,
  VLANConfigurationImportCommand,
  Workspace,
} from '../../clients/typescript/public-v1.generated';
import type { VlanTransferDocument } from '../../frontend/src/vlan-transfer';
import { requestID } from '../../frontend/src/api';

const path = process.env.OVS_FRONTEND_FIXTURE;
if (!path)
  throw new Error('VLAN transfer requires the real isolated Go/OVS fixture.');
const fixture = JSON.parse(readFileSync(path, 'utf8')) as {
  origin: string;
  accounts: Record<string, string>;
  dbSocket: string;
  vlanTransfer: { id: string; port: string };
};
const account = 'browser-vlan-transfer';
const exportPath = '/configuration/vlan-export';
const importPath = '/configuration/vlan-imports';

function apiReply(method: string, endpoint: string) {
  return (response: Response) =>
    response.request().method() === method &&
    new URL(response.url()).pathname === '/api/v1' + endpoint;
}

async function apiRead<T>(page: Page, endpoint: string): Promise<T> {
  const response = await page
    .context()
    .request.get(fixture.origin + '/api/v1' + endpoint);
  expect(response.status(), `GET ${endpoint}`).toBe(200);
  return response.json();
}

function vsctl(...args: string[]) {
  return execFileSync(
    'ovs-vsctl',
    ['--timeout=5', `--db=unix:${fixture.dbSocket}`, ...args],
    {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  ).trim();
}

function nativeState() {
  return {
    port: vsctl(
      '--format=json',
      '--columns=_uuid,name,interfaces,vlan_mode,tag,trunks,cvlans,external_ids',
      'list',
      'Port',
      fixture.vlanTransfer.port,
    ),
    nextConfig: vsctl('get', 'Open_vSwitch', '.', 'next_cfg'),
  };
}

test('native Port VLAN export and reviewed import stage through Candidate and Validation only', async ({
  page,
}) => {
  const { id, port } = fixture.vlanTransfer;
  await page.goto(fixture.origin + '/admin/configuration-transfer');
  await page.getByLabel('Username', { exact: true }).fill(account);
  await page
    .getByLabel('Password', { exact: true })
    .fill(fixture.accounts[account]);
  const login = page.waitForResponse(apiReply('POST', '/sessions'));
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await login).status()).toBe(201);
  await expect(
    page.getByRole('heading', { name: 'Configuration Transfer', exact: true }),
  ).toBeVisible();

  const initial = (await apiRead<Workspace>(page, '/workspace')).candidate!;
  expect(initial.intents).toEqual([]);
  const currentPort = await apiRead<Port>(page, `/ports/${id}`);
  expect(currentPort.interface_refs).toHaveLength(1);
  const native = nativeState();
  const portUUID = vsctl('get', 'Port', port, '_uuid');
  const memberUUID = vsctl('get', 'Interface', port, '_uuid');
  const oldTag = Number(vsctl('get', 'Port', port, 'tag'));
  expect(oldTag).toBe(10);
  const newTag = 21;
  const browserCommands: string[] = [];
  page.on('request', (request) => {
    const route = new URL(request.url());
    if (
      route.origin === fixture.origin &&
      route.pathname.startsWith('/api/v1/') &&
      ['POST', 'PATCH', 'PUT', 'DELETE'].includes(request.method())
    )
      browserCommands.push(route.pathname);
  });

  await page.getByLabel('Port name', { exact: true }).fill(port);
  await page.getByRole('button', { name: 'Find Ports', exact: true }).click();
  const exportPanel = page.getByRole('region', {
    name: 'Export Port VLAN settings',
    exact: true,
  });
  await exportPanel.getByRole('checkbox', { name: port, exact: true }).check();
  const [exportResponse, download] = await Promise.all([
    page.waitForResponse(apiReply('GET', exportPath)),
    page.waitForEvent('download', { timeout: 15_000 }),
    exportPanel
      .getByRole('button', { name: 'Download VLAN JSON', exact: true })
      .click(),
  ]);
  expect(exportResponse.status()).toBe(200);
  expect(new URL(exportResponse.url()).searchParams.get('ports')).toBe(id);
  expect(download.suggestedFilename()).toBe('ovs-webui-port-vlan.json');
  const data = readFileSync((await download.path())!);
  const envelope: VLANConfigurationExport = await exportResponse.json();
  expect(data.toString('base64')).toBe(envelope.document_base64);
  expect(createHash('sha256').update(data).digest('hex')).toBe(envelope.sha256);
  const document: VlanTransferDocument = JSON.parse(data.toString('utf8'));
  expect(document.format).toBe('ovs-webui.port-vlan.v1');
  expect(document.schema_digest).toMatch(/^[0-9a-f]{64}$/);
  expect(document.instance_generation).toBe(currentPort.instance_generation);
  expect(document.ports).toEqual([
    {
      binding: {
        management_id: id,
        ovs_uuid: portUUID,
        table: 'Port',
        instance_generation: document.instance_generation,
      },
      member: {
        management_id: currentPort.interface_refs[0].id,
        ovs_uuid: memberUUID,
        table: 'Interface',
        instance_generation: document.instance_generation,
      },
      shape: 'standalone-internal',
      vlan: { vlan_mode: 'access', tag: [oldTag], trunks: [] },
    },
  ]);
  expect(data.toString('utf8')).not.toContain('transfer-opaque');

  const file = page.getByLabel('Port VLAN JSON file (up to 24 KiB)', {
    exact: true,
  });
  const stage = page.getByRole('button', {
    name: 'Stage VLAN Merge',
    exact: true,
  });
  const upload = (name: string, body: VlanTransferDocument) =>
    file.setInputFiles({
      name,
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(body)),
    });
  await upload('invalid.json', {
    ...document,
    ports: [{ ...document.ports[0], vlan: { tag: [4095] } }],
  });
  const importPanel = page.getByRole('region', {
    name: 'Stage Port VLAN merge',
    exact: true,
  });
  await expect(importPanel.getByRole('alert')).toHaveText(
    'The file is invalid for the managed Port VLAN format.',
  );
  await expect(stage).toHaveCount(0);

  const foreign = structuredClone(document);
  foreign.instance_generation = randomUUID();
  foreign.ports[0].binding.instance_generation = foreign.instance_generation;
  foreign.ports[0].member.instance_generation = foreign.instance_generation;
  foreign.ports[0].vlan = { tag: [newTag] };
  await upload('foreign-generation.json', foreign);
  await expect(
    page.getByText(
      'The source generation differs. Choose and review a destination for each source Port.',
    ),
  ).toBeVisible();
  await expect(stage).toBeDisabled();
  expect(browserCommands).toEqual([]);

  const session = await apiRead<Session>(page, '/session');
  const request = requestID();
  const rejected = await page
    .context()
    .request.post(fixture.origin + '/api/v1' + importPath, {
      headers: {
        Origin: fixture.origin,
        'X-OVS-CSRF-Token': session.csrf_token,
        'Idempotency-Key': request,
        'X-OVS-Request-Epoch': session.request_epochs.workspace,
        'If-Match': `"${initial.revision}"`,
      },
      data: {
        request_id: request,
        expected_generation: document.instance_generation,
        mappings: [],
        document_base64: Buffer.from(JSON.stringify(foreign)).toString(
          'base64',
        ),
      } satisfies VLANConfigurationImportCommand,
    });
  expect(rejected.status()).toBe(422);
  expect(await rejected.json()).toMatchObject({
    code: 'CONFIG_MAPPING_REQUIRED',
  });
  expect(
    (await apiRead<Workspace>(page, '/workspace')).candidate,
  ).toMatchObject({
    id: initial.id,
    revision: initial.revision,
    intents: [],
  });
  expect(nativeState()).toEqual(native);

  document.ports[0].vlan = { tag: [newTag] };
  await upload('tag-only-merge.json', document);
  await expect(
    page.getByText('The merge uses the original Port IDs in this generation.'),
  ).toBeVisible();
  await page
    .getByRole('checkbox', {
      name: 'Review explicit destination mappings',
      exact: true,
    })
    .check();
  await expect(stage).toBeDisabled();
  await page
    .getByLabel(`Destination Port for ${id}`, { exact: true })
    .selectOption(id);
  await page
    .getByRole('checkbox', {
      name: 'I reviewed the VLAN fields and destination Ports.',
      exact: true,
    })
    .check();
  const stageReply = page.waitForResponse(apiReply('POST', importPath));
  await stage.click();
  const stagedResponse = await stageReply;
  expect(stagedResponse.status()).toBe(200);
  expect(await stagedResponse.request().headerValue('if-match')).toBe(
    `"${initial.revision}"`,
  );
  const command: VLANConfigurationImportCommand = stagedResponse
    .request()
    .postDataJSON();
  expect(command.expected_generation).toBe(document.instance_generation);
  expect(command.mappings).toEqual([{ source_id: id, destination_id: id }]);
  expect(
    JSON.parse(Buffer.from(command.document_base64, 'base64').toString('utf8')),
  ).toEqual(document);
  const staged: Candidate = await stagedResponse.json();
  expect(staged.id).toBe(initial.id);
  expect(staged.revision).not.toBe(initial.revision);
  expect(staged.intents).toHaveLength(1);
  expect(staged.intents[0]).toMatchObject({
    operation: 'port.vlan.set',
    object: { management_id: id, ovs_uuid: portUUID },
    value: { vlan_mode: 'access', tag: newTag, trunks: [] },
  });
  await expect(page).toHaveURL(
    fixture.origin + `/changes/candidates/${staged.id}`,
  );
  await expect(
    page.getByRole('heading', { name: 'Candidate Workspace', exact: true }),
  ).toBeVisible();
  expect(nativeState()).toEqual(native);

  const validateReply = page.waitForResponse(apiReply('POST', '/validations'));
  await page
    .getByRole('button', { name: 'Validate Candidate', exact: true })
    .click();
  const validate = await validateReply;
  expect(validate.status()).toBe(202);
  const receipt: Accepted = await validate.json();
  expect(receipt.resource_ref.kind).toBe('validation');
  await expect(page).toHaveURL(
    fixture.origin + `/changes/validations/${receipt.resource_ref.id}`,
  );
  await expect(
    page.getByRole('heading', { name: 'Diff & Validation', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText('Usable for this revision', { exact: true }),
  ).toBeVisible();
  const result = await apiRead<Validation>(
    page,
    `/validations/${receipt.resource_ref.id}`,
  );
  expect(result).toMatchObject({
    candidate_id: staged.id,
    candidate_revision: staged.revision,
    usable: true,
  });
  expect(result.diff).toContainEqual(
    expect.objectContaining({
      object: expect.objectContaining({
        management_id: id,
        ovs_uuid: portUUID,
      }),
      field: 'tag',
      before: oldTag,
      after: newTag,
      current: oldTag,
    }),
  );
  expect(browserCommands).toEqual([
    '/api/v1' + importPath,
    '/api/v1/validations',
  ]);
  expect(nativeState()).toEqual(native);
});

test('native Port VLAN Exact Restore requires the reviewed member and stages a complete field set', async ({
  page,
}) => {
  const { id, port } = fixture.vlanTransfer;
  const user = 'browser-vlan-restore';
  await page.goto(fixture.origin + '/admin/configuration-transfer');
  await page.getByLabel('Username', { exact: true }).fill(user);
  await page.getByLabel('Password', { exact: true }).fill(fixture.accounts[user]);
  const login = page.waitForResponse(apiReply('POST', '/sessions'));
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await login).status()).toBe(201);

  const before = (await apiRead<Workspace>(page, '/workspace')).candidate!;
  expect(before.intents).toEqual([]);
  const native = nativeState();
  const destination = await apiRead<Port>(page, `/ports/${id}`);
  expect(destination.interface_refs).toHaveLength(1);
  const member = destination.interface_refs[0].id;
  const generation = destination.instance_generation;
  const archive = await apiRead<VLANConfigurationExport>(page, `${exportPath}?ports=${id}`);
  const document: VlanTransferDocument = JSON.parse(Buffer.from(archive.document_base64, 'base64').toString('utf8'));
  expect(document.ports[0].vlan).toMatchObject({ vlan_mode: 'access', tag: [10], trunks: [] });
  document.ports[0].vlan.tag = [22];
  const proposed = [{ source_id: id, destination_id: id, destination_member_id: member }];
  const session = await apiRead<Session>(page, '/session');
  const submit = (input: VlanTransferDocument, mappings: VLANConfigurationImportCommand['mappings']) => {
    const request = requestID();
    return page.context().request.post(fixture.origin + '/api/v1' + importPath, {
      headers: {
        Origin: fixture.origin,
        'X-OVS-CSRF-Token': session.csrf_token,
        'Idempotency-Key': request,
        'X-OVS-Request-Epoch': session.request_epochs.workspace,
        'If-Match': `"${before.revision}"`,
      },
      data: {
        request_id: request,
        mode: 'exact-restore',
        expected_generation: generation,
        mappings,
        document_base64: Buffer.from(JSON.stringify(input)).toString('base64'),
      } satisfies VLANConfigurationImportCommand,
    });
  };
  const missing = await submit(document, []);
  expect(missing.status()).toBe(422);
  expect(await missing.json()).toMatchObject({ code: 'CONFIG_MAPPING_REQUIRED' });
  const stale = await submit(document, [{ ...proposed[0], destination_member_id: randomUUID() }]);
  expect(stale.status()).toBe(409);
  expect(await stale.json()).toMatchObject({ code: 'CONFIG_DESTINATION_MEMBER_CHANGED' });
  const partial = structuredClone(document);
  delete partial.ports[0].vlan.trunks;
  const incomplete = await submit(partial, proposed);
  expect(incomplete.status()).toBe(422);
  expect(await incomplete.json()).toMatchObject({ code: 'CONFIG_EXACT_FIELDS_REQUIRED' });
  expect((await apiRead<Workspace>(page, '/workspace')).candidate).toMatchObject({ id: before.id, revision: before.revision, intents: [] });
  expect(nativeState()).toEqual(native);

  await page.getByLabel('Port name', { exact: true }).fill(port);
  await page.getByRole('button', { name: 'Find Ports', exact: true }).click();
  await page.getByLabel('Import mode', { exact: true }).selectOption('exact-restore');
  await page.getByLabel('Port VLAN JSON file (up to 24 KiB)', { exact: true }).setInputFiles({
    name: 'complete-port-vlan.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(document)),
  });
  const exact = page.getByRole('region', { name: 'Stage Port VLAN Exact Restore', exact: true });
  const stage = exact.getByRole('button', { name: 'Stage VLAN Exact Restore', exact: true });
  await expect(stage).toBeDisabled();
  await exact.getByLabel(`Destination Port for ${id}`, { exact: true }).selectOption(id);
  await expect(exact.getByText(`Destination member ${member}`, { exact: true })).toBeVisible();
  await expect(stage).toBeDisabled();
  await exact.getByRole('checkbox', { name: 'I reviewed the VLAN fields, destination Ports and members.', exact: true }).check();
  const staging = page.waitForResponse(apiReply('POST', importPath));
  await stage.click();
  const stagedResponse = await staging;
  expect(stagedResponse.status()).toBe(200);
  const body = stagedResponse.request().postDataJSON() as VLANConfigurationImportCommand;
  expect(body).toMatchObject({ mode: 'exact-restore', expected_generation: generation, mappings: proposed });
  const staged: Candidate = await stagedResponse.json();
  expect(staged.id).toBe(before.id);
  expect(staged.intents).toHaveLength(1);
  expect(staged.intents[0]).toMatchObject({
    operation: 'port.vlan.set', object: { management_id: id }, value: { vlan_mode: 'access', tag: 22, trunks: [] },
  });
  expect(nativeState()).toEqual(native);

  const validating = page.waitForResponse(apiReply('POST', '/validations'));
  await page.getByRole('button', { name: 'Validate Candidate', exact: true }).click();
  const validation = await validating;
  expect(validation.status()).toBe(202);
  const accepted: Accepted = await validation.json();
  await expect(page.getByText('Usable for this revision', { exact: true })).toBeVisible();
  const outcome = await apiRead<Validation>(page, `/validations/${accepted.resource_ref.id}`);
  expect(outcome).toMatchObject({ candidate_id: staged.id, candidate_revision: staged.revision, usable: true });
  expect(outcome.diff).toContainEqual(expect.objectContaining({ object: expect.objectContaining({ management_id: id }), field: 'tag', before: 10, after: 22 }));
  expect(nativeState()).toEqual(native);
});

test('native Port VLAN Exact Restore reaches shared Safe Apply and restores the exact native Port on rollback', async ({
  page,
}) => {
  const { id, port } = fixture.vlanTransfer;
  const user = 'browser-vlan-rollback';
  const password = fixture.accounts[user];
  await page.goto(fixture.origin + '/admin/configuration-transfer');
  await page.getByLabel('Username', { exact: true }).fill(user);
  await page.getByLabel('Password', { exact: true }).fill(password);
  const login = page.waitForResponse(apiReply('POST', '/sessions'));
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  expect((await login).status()).toBe(201);
  await expect(
    page.getByRole('heading', { name: 'Configuration Transfer', exact: true }),
  ).toBeVisible();

  const before = (await apiRead<Workspace>(page, '/workspace')).candidate!;
  expect(before.intents).toEqual([]);
  const native = nativeState();
  const memberUUID = vsctl('get', 'Interface', port, '_uuid');
  const destination = await apiRead<Port>(page, `/ports/${id}`);
  expect(destination.interface_refs).toHaveLength(1);
  const member = destination.interface_refs[0].id;
  const generation = destination.instance_generation;
  const original = await apiRead<VLANConfigurationExport>(page, `${exportPath}?ports=${id}`);
  const document: VlanTransferDocument = JSON.parse(
    Buffer.from(original.document_base64, 'base64').toString('utf8'),
  );
  expect(document.ports[0]).toMatchObject({
    binding: { management_id: id },
    member: { management_id: member, ovs_uuid: memberUUID },
    vlan: { vlan_mode: 'access', tag: [10], trunks: [] },
  });
  const restoredTag = 23;
  document.ports[0].vlan.tag = [restoredTag];

  const commands: string[] = [];
  page.on('request', (request) => {
    const route = new URL(request.url());
    if (
      route.origin === fixture.origin &&
      route.pathname.startsWith('/api/v1/') &&
      ['POST', 'PATCH', 'PUT', 'DELETE'].includes(request.method())
    )
      commands.push(route.pathname);
  });
  await page.getByLabel('Port name', { exact: true }).fill(port);
  await page.getByRole('button', { name: 'Find Ports', exact: true }).click();
  await page.getByLabel('Import mode', { exact: true }).selectOption('exact-restore');
  await page.getByLabel('Port VLAN JSON file (up to 24 KiB)', { exact: true }).setInputFiles({
    name: 'reviewed-safe-apply.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(document)),
  });
  const exact = page.getByRole('region', { name: 'Stage Port VLAN Exact Restore', exact: true });
  await exact.getByLabel(`Destination Port for ${id}`, { exact: true }).selectOption(id);
  await expect(exact.getByText(`Destination member ${member}`, { exact: true })).toBeVisible();
  await exact.getByRole('checkbox', {
    name: 'I reviewed the VLAN fields, destination Ports and members.',
    exact: true,
  }).check();
  const staging = page.waitForResponse(apiReply('POST', importPath));
  await exact.getByRole('button', { name: 'Stage VLAN Exact Restore', exact: true }).click();
  const stageReply = await staging;
  expect(stageReply.status()).toBe(200);
  expect(stageReply.request().postDataJSON()).toMatchObject({
    mode: 'exact-restore',
    expected_generation: generation,
    mappings: [{ source_id: id, destination_id: id, destination_member_id: member }],
  });
  const staged: Candidate = await stageReply.json();
  expect(staged).toMatchObject({
    id: before.id,
    instance_generation: generation,
    intents: [expect.objectContaining({
      operation: 'port.vlan.set',
      object: { management_id: id, ovs_uuid: document.ports[0].binding.ovs_uuid },
      value: { vlan_mode: 'access', tag: restoredTag, trunks: [] },
    })],
  });

  const validating = page.waitForResponse(apiReply('POST', '/validations'));
  await page.getByRole('button', { name: 'Validate Candidate', exact: true }).click();
  const validationReply = await validating;
  expect(validationReply.status()).toBe(202);
  const validationReceipt: Accepted = await validationReply.json();
  expect(validationReceipt.resource_ref.kind).toBe('validation');
  const validationID = validationReceipt.resource_ref.id;
  await expect(page.getByText('Usable for this revision', { exact: true })).toBeVisible();
  const reviewed = await apiRead<Validation>(page, `/validations/${validationID}`);
  expect(reviewed).toMatchObject({
    candidate_id: staged.id,
    candidate_revision: staged.revision,
    instance_generation: generation,
    usable: true,
    execution_ready: true,
  });
  expect(reviewed.diff).toContainEqual(expect.objectContaining({
    object: expect.objectContaining({ management_id: id }),
    field: 'tag',
    before: 10,
    after: restoredTag,
  }));

  await page.getByLabel('Verify your password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Verify identity', exact: true }).click();
  await expect(page.getByText('Identity verified.', { exact: false })).toBeVisible();
  await page.getByLabel('Change reason', { exact: true }).fill('Synthetic VLAN Exact Restore rollback acceptance');
  await page.getByRole('checkbox', { name: /^I reviewed this Diff/ }).check();
  const start = page.getByRole('button', { name: 'Start Safe Apply', exact: true });
  await expect(start).toBeEnabled();
  expect(commands).toEqual(['/api/v1' + importPath, '/api/v1/validations', '/api/v1/session/reauthentication']);
  expect(nativeState()).toEqual(native);

  const starting = page.waitForResponse(apiReply('POST', '/transactions'));
  await start.click();
  const applyReply = await starting;
  expect(applyReply.status()).toBe(202);
  expect(applyReply.request().postDataJSON()).toMatchObject({
    mode: 'safe-apply',
    candidate_id: staged.id,
    candidate_revision: staged.revision,
    validation_id: validationID,
  });
  const admitted: Accepted = await applyReply.json();
  expect(admitted.resource_ref.kind).toBe('transaction');
  const transactionID = admitted.resource_ref.id;
  const transactionPath = `/transactions/${transactionID}`;
  const transactionURL = fixture.origin + '/changes' + transactionPath;
  await expect(page).toHaveURL(transactionURL);
  await expect(page.getByTestId('safe-state')).toHaveText('awaiting-confirmation');
  const applied = await apiRead<Transaction>(page, transactionPath);
  expect(applied).toMatchObject({
    id: transactionID,
    instance_generation: generation,
    candidate_id: staged.id,
    candidate_revision: staged.revision,
    validation_id: validationID,
    job_ref: { kind: 'job', id: admitted.job_id },
    knowledge: 'known',
    commit_outcome: 'committed',
    applied_outcome: 'applied',
    safe_apply: 'awaiting-confirmation',
    applied_evidence: { provider_id: 'ovsdb', instance_generation: generation, state: 'proven' },
  });
  expect(applied.allowed_actions).toContain('rollback');
  expect(vsctl('get', 'Port', port, 'tag')).toBe(String(restoredTag));
  expect(BigInt(nativeState().nextConfig)).toBeGreaterThan(BigInt(native.nextConfig));

  const decisionPath = transactionPath + '/decisions';
  const rollback = page.getByRole('button', { name: 'Request rollback', exact: true });
  await expect(rollback).toBeEnabled();
  const deciding = page.waitForResponse(apiReply('POST', decisionPath));
  await rollback.click();
  const decision = await deciding;
  expect(decision.status()).toBe(202);
  expect(decision.request().postDataJSON()).toMatchObject({ decision: 'rollback' });
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');
  await page.reload();
  await expect(page).toHaveURL(transactionURL);
  await expect(page.getByTestId('safe-state')).toHaveText('rolled-back');

  const recovered = await apiRead<Transaction>(page, transactionPath);
  expect(recovered).toMatchObject({
    id: transactionID,
    instance_generation: generation,
    phase: 'settled',
    safe_apply: 'rolled-back',
    recovery_trigger: 'authorized-rollback-requested',
    recovery_reason: 'rollback-applied-management-reachable',
    rollback_evidence: {
      commit_outcome: 'committed',
      applied_outcome: 'applied',
      target_next_cfg: expect.stringMatching(/^\d+$/),
      observed_cur_cfg: expect.stringMatching(/^\d+$/),
      observed_at: expect.any(String),
    },
    job_ref: applied.job_ref,
    correlation_id: applied.correlation_id,
    allowed_actions: [],
  });
  expect(BigInt(recovered.sequence)).toBeGreaterThan(BigInt(applied.sequence));
  expect(vsctl('get', 'Port', port, 'tag')).toBe('10');
  expect(vsctl('get', 'Interface', port, '_uuid')).toBe(memberUUID);
  const after = nativeState();
  expect(after.port).toBe(native.port);
  expect(BigInt(after.nextConfig)).toBeGreaterThan(BigInt(native.nextConfig));
  expect(commands).toEqual([
    '/api/v1' + importPath,
    '/api/v1/validations',
    '/api/v1/session/reauthentication',
    '/api/v1/transactions',
    '/api/v1' + decisionPath,
  ]);
});
