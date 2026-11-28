<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { VLANConfigurationExport } from '../../clients/typescript/public-v1.generated';
  import { saveVerifiedArtifact } from './artifact-download';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import { controller, errorText, navigate, refPath, type Model } from './model';
  import {
    canExportVlans, canStageVlans, currentVlanTransferInventory, hasExactVlanFields, readVlanTransferFile, verifyVlanExport,
    vlanExportURL, vlanTransferError, vlanTransferMappings, vlanTransferMaxBytes, vlanTransferMaxPorts,
    vlanTransferQuery, vlanTransferRoute, vlanTransferTarget, vlanTransferURL,
    type LoadedVlanTransfer, type TransferTarget, type VlanTransferMode,
  } from './vlan-transfer';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  let filter = $state('');
  let exported = $state<Record<string, TransferTarget>>({});
  let destinations = $state<Record<string, TransferTarget>>({});
  let source = $state<LoadedVlanTransfer | null>(null);
  let filename = $state('');
  let remap = $state(false);
  let mode = $state<VlanTransferMode>('merge');
  let reviewed = $state('');
  let exportError = $state('');
  let importError = $state('');
  let downloading = $state(false);
  let readingFile = $state(false);
  let previousGeneration = '';
  let previousSession = '';
  let previousQuery: string | undefined;
  let fileSequence = 0;
  let mounted = true;
  onDestroy(() => (mounted = false));

  const canExport = $derived(model.sessionReady && canExportVlans(model.session));
  const canStage = $derived(model.sessionReady && canStageVlans(model.session));
  const inventory = $derived(model.vlanTransferPorts.status === 'ready' ? currentVlanTransferInventory(model.vlanTransferPorts.value) : null);
  const generation = $derived(inventory?.instance_generation ?? '');
  const targets = $derived(inventory?.items.flatMap((port) => {
    const target = vlanTransferTarget(port, generation);
    return target ? [target] : [];
  }) ?? []);
  const scope = $derived(vlanTransferQuery(model.query));
  const chosen = $derived(Object.values(exported));
  const candidate = $derived(model.workspace.status === 'ready' ? model.workspace.value?.candidate : null);
  const sameGeneration = $derived(!!source && source.document.instance_generation === generation);
  const exact = $derived(mode === 'exact-restore');
  const manual = $derived(exact || !sameGeneration || remap);
  const mappings = $derived(source && inventory ? vlanTransferMappings(source.document, generation, manual, destinations, mode) : null);
  const reviewKey = $derived(candidate && mappings ? `${candidate.revision}|${generation}|${mode}|${JSON.stringify(mappings)}` : '');
  const blocked = $derived(model.busy || !!model.pending);

  $effect(() => {
    if (model.query === previousQuery) return;
    previousQuery = model.query;
    filter = scope.filter;
  });
  $effect(() => {
    if (!generation) return;
    if (previousGeneration && previousGeneration !== generation) {
      exported = {};
      destinations = {};
      reviewed = '';
    }
    previousGeneration = generation;
  });
  $effect(() => {
    const active = model.session;
    const identity = active ? `${active.principal_id}|${active.csrf_token}|${active.request_epochs.workspace}` : '';
    if (previousSession && previousSession !== identity) {
      fileSequence++;
      readingFile = false;
      source = null;
      filename = '';
      exported = {};
      destinations = {};
      reviewed = '';
      remap = false;
      mode = 'merge';
    }
    previousSession = identity;
  });

  function stillCurrent(permission: typeof canExportVlans, pinQuery = false): () => boolean {
    const start = controller.state;
    const session = start.session;
    return () => {
      const current = controller.state;
      return mounted && current.path === vlanTransferRoute && current.sessionReady && (!pinQuery || current.query === start.query) &&
        current.session?.principal_id === session?.principal_id && current.session?.csrf_token === session?.csrf_token &&
        current.session?.request_epochs.workspace === session?.request_epochs.workspace && permission(current.session);
    };
  }

  function search(event: SubmitEvent) {
    event.preventDefault();
    navigate(vlanTransferURL(filter.trim()));
  }

  function toggleExport(target: TransferTarget, checked: boolean) {
    if (!checked) delete exported[target.id];
    else if (chosen.length < vlanTransferMaxPorts) exported[target.id] = target;
  }

  async function download() {
    if (!canExport || !inventory || downloading || chosen.some((port) => port.generation !== generation)) return;
    const ids = chosen.map((port) => port.id);
    const path = vlanExportURL(ids);
    if (!path) return;
    const expected = generation;
    const active = stillCurrent(canExportVlans, true);
    downloading = true;
    exportError = '';
    try {
      const envelope = await controller.api.read<VLANConfigurationExport>(path);
      const contents = await verifyVlanExport(envelope.document_base64, envelope.sha256, ids, expected);
      if (!active() || generation !== expected) return;
      saveVerifiedArtifact(contents, 'ovs-webui-port-vlan.json', 'application/json');
    } catch (error) {
      if (active()) exportError = errorText(error);
    } finally {
      downloading = false;
    }
  }

  async function chooseFile(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    const sequence = ++fileSequence;
    input.value = '';
    source = null;
    filename = '';
    destinations = {};
    remap = false;
    reviewed = '';
    importError = '';
    readingFile = false;
    if (!file || !canStage) return;
    if (!file.size || file.size > vlanTransferMaxBytes) {
      importError = 'CONFIG_DOCUMENT_LIMIT';
      return;
    }
    const active = stillCurrent(canStageVlans);
    readingFile = true;
    try {
      const loaded = readVlanTransferFile(new Uint8Array(await file.arrayBuffer()));
      if (!active() || sequence !== fileSequence) return;
      source = loaded;
      filename = file.name;
    } catch (error) {
      if (active() && sequence === fileSequence) importError = errorText(error);
    } finally {
      if (sequence === fileSequence) readingFile = false;
    }
  }

  function selectDestination(sourceID: string, targetID: string) {
    const target = targets.find((port) => port.id === targetID);
    if (target) destinations[sourceID] = target;
    else delete destinations[sourceID];
    reviewed = '';
  }

  function changeMode(event: Event) {
    mode = (event.currentTarget as HTMLSelectElement).value === 'exact-restore' ? 'exact-restore' : 'merge';
    destinations = {};
    reviewed = '';
  }

  function stage(event: SubmitEvent) {
    event.preventDefault();
    if (!canStage || !candidate || !source || !mappings || !reviewKey || reviewed !== reviewKey || blocked) return;
    void controller.stageVlanTransfer({ document_base64: source.encoded, expected_generation: generation, mappings,
      ...(exact ? { mode } : {}) }, candidate.revision);
    reviewed = '';
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Administration</p>
    <h1>Configuration Transfer</h1>
    <p>Export managed Port VLAN settings or stage a reviewed transfer in Candidate.</p>
  </div>
</header>

{#if canExport || canStage}
  <section class="panel" aria-labelledby="transfer-ports-heading">
    <h2 id="transfer-ports-heading">Current Port inventory</h2>
    <p>Select standalone Ports with a system or internal Interface. The server verifies the current Port and member scope.</p>
    <form class="actions" onsubmit={search}>
      <label>Port name <input type="search" maxlength="128" bind:value={filter} /></label>
      <button>Find Ports</button>
    </form>
    <LoadNotice load={model.vlanTransferPorts} />
    {#if model.vlanTransferPorts.status === 'ready' && !inventory}<p role="status">Current Port inventory evidence is unavailable.</p>{/if}
    {#if inventory && !targets.length}<p role="status">No standalone Ports match this page.</p>{/if}
    {#if scope.cursor || inventory?.next_cursor}
      <nav class="actions" aria-label="Port inventory pages">
        {#if scope.cursor}<Link href={vlanTransferURL(scope.filter)}>First Port page</Link>{/if}
        {#if inventory?.next_cursor}<Link href={vlanTransferURL(scope.filter, inventory.next_cursor)}>Next Port page</Link>{/if}
      </nav>
    {/if}
    {#if expert && inventory}<p>Current generation <code>{generation}</code></p>{/if}
  </section>
{:else}
  <p class="notice warning" role="status">Current permissions or connection do not allow configuration transfer.</p>
{/if}

{#if canExport}
  <section class="panel" aria-labelledby="vlan-export-heading">
    <h2 id="vlan-export-heading">Export Port VLAN settings</h2>
    <fieldset>
      <legend>Choose up to {vlanTransferMaxPorts} Ports</legend>
      {#each targets as target (target.id)}
        <label><input type="checkbox" checked={!!exported[target.id]} disabled={downloading || (!exported[target.id] && chosen.length >= vlanTransferMaxPorts)}
          onchange={(event) => toggleExport(target, event.currentTarget.checked)} /> {target.name}</label>
        {#if expert}<small><code>{target.id}</code></small>{/if}
      {/each}
    </fieldset>
    {#if chosen.length}
      <ul aria-label="Selected export Ports">
        {#each chosen as target (target.id)}<li>{target.name} <code>{target.id}</code> <button disabled={downloading} onclick={() => toggleExport(target, false)}>Remove {target.name}</button></li>{/each}
      </ul>
    {/if}
    <button class="primary" disabled={!inventory || !chosen.length || downloading} onclick={download}>{downloading ? 'Verifying export…' : 'Download VLAN JSON'}</button>
    {#if exportError}<p class="notice warning" role="alert">{vlanTransferError(exportError)}</p>{#if expert}<p><code>{exportError}</code></p>{/if}{/if}
  </section>
{/if}

{#if canStage}
  <section class="panel" aria-labelledby="vlan-import-heading">
    <h2 id="vlan-import-heading">{exact ? 'Stage Port VLAN Exact Restore' : 'Stage Port VLAN merge'}</h2>
    <div class="import-mode">
      <label for="vlan-transfer-mode">Import mode</label>
      <select id="vlan-transfer-mode" value={mode} disabled={blocked} onchange={changeMode}>
        <option value="merge">Merge</option>
        <option value="exact-restore">Exact Restore</option>
      </select>
    </div>
    <label for="vlan-transfer-file">Port VLAN JSON file (up to 24 KiB)</label>
    <input id="vlan-transfer-file" type="file" accept=".json,application/json" disabled={readingFile || blocked} onchange={chooseFile} />
    {#if importError}<p class="notice warning" role="alert">{vlanTransferError(importError)}</p>{/if}
    {#if source}
      <h3>Loaded configuration</h3>
      <p>{filename} · {source.document.ports.length} Ports · {source.bytes.toLocaleString()} bytes</p>
      {#if exact}
        <p>Exact Restore sets the explicit VLAN mode, tag and trunks for each reviewed destination. Review the resulting Diff and Validation in Candidate.</p>
        {#if !hasExactVlanFields(source.document)}<p role="status">Exact Restore requires a valid explicit VLAN mode, tag and trunk list for every source Port.</p>{/if}
      {:else}
        <p>Only fields listed in the file are staged. Review the resulting Diff and Validation in Candidate.</p>
      {/if}
      <dl>
        <dt>Source generation</dt><dd><code>{source.document.instance_generation}</code></dd>
        {#if expert}<dt>Schema digest</dt><dd><code>{source.document.schema_digest}</code></dd>{/if}
      </dl>
      {#if exact}
        <p>Choose a current destination Port and review its member for each source Port.</p>
      {:else if sameGeneration}
        <label><input type="checkbox" bind:checked={remap} onchange={() => (reviewed = '')} /> Review explicit destination mappings</label>
        {#if !remap}<p>The merge uses the original Port IDs in this generation.</p>{/if}
      {:else if generation}
        <p>The source generation differs. Choose and review a destination for each source Port.</p>
      {/if}

      <form onsubmit={stage}>
        {#each source.document.ports as entry (entry.binding.management_id)}
          {@const sourceID = entry.binding.management_id}
          {@const picked = destinations[sourceID]}
          <fieldset>
            <legend>Source Port <code>{sourceID}</code></legend>
            <dl>
              <dt>Source member</dt><dd><code>{entry.member.management_id}</code></dd>
              <dt>Port shape</dt><dd>{entry.shape === 'standalone-system' ? 'Standalone system' : 'Standalone internal'}</dd>
              <dt>VLAN mode</dt><dd>{entry.vlan.vlan_mode ?? 'Unchanged'}</dd>
              <dt>Tag</dt><dd>{entry.vlan.tag === undefined ? 'Unchanged' : entry.vlan.tag[0] ?? 'Clear tag'}</dd>
              <dt>Trunks</dt><dd>{entry.vlan.trunks === undefined ? 'Unchanged' : entry.vlan.trunks.length ? entry.vlan.trunks.join(', ') : 'Empty trunk set (all VLANs in trunk mode)'}</dd>
            </dl>
            {#if manual}
              <label for={`destination-${sourceID}`}>Destination Port for {sourceID}</label>
              <select id={`destination-${sourceID}`} value={picked?.id ?? ''} disabled={!inventory || blocked}
                onchange={(event) => selectDestination(sourceID, event.currentTarget.value)}>
                <option value="">Select current Port</option>
                {#if picked && !targets.some((port) => port.id === picked.id)}<option value={picked.id}>{picked.name} · {picked.id}</option>{/if}
                {#each targets as port (port.id)}<option value={port.id}>{port.name} · {port.id}</option>{/each}
              </select>
              {#if picked}<p>Destination member <code>{picked.member}</code></p>{/if}
            {:else}
              <p>Destination Port <code>{sourceID}</code></p>
            {/if}
          </fieldset>
        {/each}
        {#if !candidate}
          <p>Open a current Candidate before staging this merge. <Link href="/changes/candidate">Review Candidate</Link></p>
        {:else}
          {@const candidateLink = refPath({ kind: 'candidate', id: candidate.id })}
          {#if candidateLink}<p>Destination <Link href={candidateLink}>current Candidate</Link>{#if expert} · Revision <code>{candidate.revision}</code>{/if}</p>{/if}
        {/if}
        {#if manual && inventory && !mappings && (!exact || hasExactVlanFields(source.document))}<p>Choose one distinct destination Port for every source Port.</p>{/if}
        {#if model.message?.startsWith('CONFIG_')}<p class="notice warning">{vlanTransferError(model.message)}</p>{/if}
        <label><input type="checkbox" checked={!!reviewKey && reviewed === reviewKey} disabled={!reviewKey || blocked}
          onchange={(event) => (reviewed = event.currentTarget.checked ? reviewKey : '')} /> {exact ? 'I reviewed the VLAN fields, destination Ports and members.' : 'I reviewed the VLAN fields and destination Ports.'}</label>
        <button class="primary" disabled={!reviewKey || reviewed !== reviewKey || blocked}>{exact ? 'Stage VLAN Exact Restore' : 'Stage VLAN Merge'}</button>
      </form>
    {/if}
  </section>
{/if}

<style>
  .import-mode {
    margin: 16px 0;
  }

  .import-mode label {
    margin: 0;
  }
</style>
