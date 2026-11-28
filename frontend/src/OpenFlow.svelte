<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import type { OpenFlowReadPage, OpenFlowReadRow } from '../../clients/typescript/public-v1.generated';
  import { saveVerifiedArtifact } from './artifact-download';
  import { diagnosticTargets } from './diagnostics';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, errorText, navigate, refPath, type Model } from './model';
  import {
    canReadOpenFlow, canReadOpenFlowRawFields, matchesOpenFlowExport, matchesOpenFlowObservation, openFlowInventoryRoute,
    openFlowLocation, openFlowMaxRows, openFlowObservationPath, openFlowPageSize, openFlowReason,
    openFlowRoute, openFlowSnapshotAvailable, openFlowSnapshotPath, openFlowSnapshotRoute,
  } from './openflow';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  let filter = $state(untrack(() => {
    const location = openFlowLocation(model.query);
    return location.kind === 'inventory' ? location.filter : '';
  }));
  let bridge = $state('');
  let table = $state(0);
  let cookie = $state('');
  let limit = $state(50);
  let groups = $state(false);
  let rawFields = $state(false);
  let observing = $state(false);
  let exporting = $state(false);
  let expired = $state(false);
  let failure = $state('');
  let mounted = true;
  onDestroy(() => (mounted = false));

  const allowed = $derived(model.sessionReady && canReadOpenFlow(model.session));
  const rawAllowed = $derived(model.sessionReady && canReadOpenFlowRawFields(model.session));
  const location = $derived(openFlowLocation(model.query));
  const bridges = $derived(location.kind === 'inventory' && model.resource.status === 'ready' ? diagnosticTargets(model.resource.value, 'bridge') : null);
  const selectedBridge = $derived(bridges?.some(({ ref }) => ref.id === bridge) ?? false);
  const nextBridges = $derived(typeof model.resource.value?.next_cursor === 'string' ? model.resource.value.next_cursor : '');
  const page = $derived(location.kind === 'snapshot' && model.openFlowSnapshot.status === 'ready' ? model.openFlowSnapshot.value : null);
  const rawSnapshot = $derived(page?.evidence.query.raw_fields === true);
  const expiry = $derived(page ? Date.parse(page.snapshot.expires_at) : 0);

  $effect(() => {
    const timeLeft = expiry - Date.now();
    expired = !Number.isFinite(timeLeft) || timeLeft <= 0;
    if (!expired) {
      const timer = setTimeout(() => (expired = true), Math.min(timeLeft, 2_147_483_647));
      return () => clearTimeout(timer);
    }
  });

  $effect(() => { if (!rawAllowed) rawFields = false; });

  function requestContext(raw: boolean): () => boolean {
    const start = controller.state;
    const session = start.session;
    const permission = raw ? canReadOpenFlowRawFields : canReadOpenFlow;
    return () => {
      const current = controller.state;
      return mounted && current.sessionReady && current.path === start.path && current.query === start.query &&
        current.session?.principal_id === session?.principal_id && current.session?.csrf_token === session?.csrf_token &&
        current.session?.request_epochs.management === session?.request_epochs.management && permission(current.session);
    };
  }

  function filterBridges(event: SubmitEvent) {
    event.preventDefault();
    navigate(openFlowInventoryRoute(filter.trim()));
  }

  async function observe(event: SubmitEvent) {
    event.preventDefault();
    if (!allowed || (rawFields && !rawAllowed) || observing || location.kind !== 'inventory' || !selectedBridge) return;
    const scope = { bridge, table, cookie: cookie.trim(), limit, groups, rawFields };
    const path = openFlowObservationPath(scope);
    if (!path) {
      failure = 'INVALID_OPENFLOW_FORM';
      return;
    }
    const active = requestContext(scope.rawFields);
    observing = true;
    failure = '';
    try {
      const observed = await controller.api.read<OpenFlowReadPage>(path);
      if (!active()) return;
      if (!matchesOpenFlowObservation(observed, scope)) throw new Error('OPENFLOW_VIEW_MISMATCH');
      if (!openFlowSnapshotAvailable(observed)) throw new Error('OPENFLOW_SNAPSHOT_UNAVAILABLE');
      navigate(openFlowSnapshotRoute(observed.snapshot.id));
    } catch (error) {
      if (active()) failure = errorText(error);
    } finally {
      observing = false;
    }
  }

  async function exportSnapshot() {
    const original = page;
    if (!allowed || !original || (rawSnapshot && !rawAllowed) || exporting || !openFlowSnapshotAvailable(original)) return;
    const active = requestContext(rawSnapshot);
    const id = original.snapshot.id;
    exporting = true;
    failure = '';
    try {
      const exported = await controller.api.read<OpenFlowReadPage>(openFlowSnapshotPath(id, 'export'));
      if (!active()) return;
      const latest = controller.state.openFlowSnapshot.value;
      if (!latest || latest.snapshot.id !== id || latest.snapshot.instance_generation !== original.snapshot.instance_generation)
        throw new Error('OPENFLOW_SNAPSHOT_UNAVAILABLE');
      if (!matchesOpenFlowExport(exported, original) || (latest !== original && !matchesOpenFlowExport(exported, latest)))
        throw new Error('OPENFLOW_VIEW_MISMATCH');
      if (!openFlowSnapshotAvailable(exported)) throw new Error('OPENFLOW_SNAPSHOT_UNAVAILABLE');
      const bytes = new TextEncoder().encode(JSON.stringify(exported, null, 2) + '\n');
      saveVerifiedArtifact(bytes, `ovs-webui-openflow-${id}.json`, 'application/json');
    } catch (error) {
      if (active()) failure = errorText(error);
    } finally {
      exporting = false;
    }
  }

  function duration(row: OpenFlowReadRow): string {
    return `${row.duration_seconds}.${String(row.duration_nanoseconds).padStart(9, '0')}`;
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Switching</p>
    <h1>OpenFlow Viewer</h1>
    <p>Inspect bounded, read-only OpenFlow 1.3 snapshots for a Bridge.</p>
  </div>
  {#if allowed && location.kind !== 'inventory'}<Link href={openFlowRoute}>New observation</Link>{/if}
</header>

{#if !allowed}
  <p class="notice warning" role="status">Current permissions or connection do not allow OpenFlow observations.</p>
{:else if location.kind === 'invalid'}
  <p class="notice warning" role="alert">This OpenFlow snapshot link is invalid.</p>
{:else if location.kind === 'inventory'}
  <section class="panel" aria-labelledby="openflow-scope-heading">
    <h2 id="openflow-scope-heading">Observation scope</h2>
    <form class="actions" aria-label="Find an OpenFlow Bridge" onsubmit={filterBridges}>
      <label>Bridge name <input type="search" bind:value={filter} maxlength="128" /></label>
      <button disabled={observing}>Find Bridges</button>
    </form>
    <LoadNotice load={model.resource} />
    {#if model.resource.status === 'ready' && !bridges}<p role="status">Current Bridge inventory is unavailable.</p>{/if}
    {#if bridges && !bridges.length}<p role="status">No authorized Bridges match this page.</p>{/if}
    {#if location.cursor || nextBridges}
      <div class="actions">
        {#if location.cursor}<Link href={openFlowInventoryRoute(location.filter)}>First Bridge page</Link>{/if}
        {#if bridges && nextBridges}<Link href={openFlowInventoryRoute(location.filter, nextBridges)}>Next Bridge page</Link>{/if}
      </div>
    {/if}
    {#if bridges?.length}
      <form aria-label="Start OpenFlow observation" onsubmit={observe}>
        <fieldset disabled={observing}>
          <legend>Capture one snapshot</legend>
          <label for="openflow-bridge">Bridge</label>
          <select id="openflow-bridge" bind:value={bridge} required>
            <option value="">Select a Bridge</option>
            {#each bridges as target (target.ref.id)}<option value={target.ref.id}>{target.name}</option>{/each}
          </select>
          <label for="openflow-table">Table</label>
          <input id="openflow-table" type="number" min="0" max="254" step="1" required bind:value={table} />
          <label for="openflow-cookie">Cookie (optional decimal)</label>
          <input id="openflow-cookie" inputmode="numeric" maxlength="20" bind:value={cookie} />
          <label for="openflow-limit">Maximum flow rows</label>
          <input id="openflow-limit" type="number" min="1" max={openFlowMaxRows} step="1" required bind:value={limit} />
          <label><input type="checkbox" bind:checked={groups} /> Include Group and Meter configuration</label>
          {#if rawAllowed}<label><input type="checkbox" bind:checked={rawFields} /> Include raw Flow fields</label>{/if}
          <button class="primary" disabled={!selectedBridge}>{observing ? 'Observing…' : 'Observe OpenFlow'}</button>
        </fieldset>
      </form>
    {/if}
  </section>
{:else if rawSnapshot && !rawAllowed}
  <p class="notice warning" role="status">Current permissions do not allow raw fields from this OpenFlow snapshot.</p>
{:else if page && !expired}
  {@const meta = page.snapshot}
  {@const result = page.evidence}
  {@const query = result.query}
  {@const bridgeLink = refPath({ kind: 'bridge', id: meta.bridge_id })}
  <section class="panel" aria-labelledby="openflow-snapshot-heading">
    <h2 id="openflow-snapshot-heading">Original snapshot</h2>
    <p><Status value={result.field_coverage} />
      {rawSnapshot ? 'Retained match and instruction payloads appear as hexadecimal. Rule ownership is unknown.' :
        'Match values, action arguments and rule ownership are unavailable in this observation.'}
    </p>
    <dl>
      <dt>Bridge</dt><dd>{#if bridgeLink}<Link href={bridgeLink}>{meta.bridge_id}</Link>{:else}{meta.bridge_id}{/if}</dd>
      <dt>Table</dt><dd>{query.table}</dd>
      <dt>Cookie filter</dt><dd>{query.cookie ?? 'Any'}</dd>
      <dt>Requested row limit</dt><dd>{query.limit}</dd>
      <dt>Group and Meter configuration</dt><dd>{query.include_groups_meters ? 'Requested' : 'Not requested'}</dd>
      <dt>Observed</dt><dd><time datetime={meta.observed_at}>{meta.observed_at}</time></dd>
      <dt>Expires</dt><dd><time datetime={meta.expires_at}>{meta.expires_at}</time></dd>
      <dt>Rule ownership</dt><dd><Status value={result.ownership} /></dd>
      {#if expert}
        <dt>Source</dt><dd>{result.source}</dd>
        <dt>Bridge generation</dt><dd><code>{meta.instance_generation}</code></dd>
        <dt>Snapshot ID</dt><dd><code>{meta.id}</code></dd>
      {/if}
    </dl>
    {#if meta.truncated}<p class="notice warning" role="status">Some matching rows or metadata were outside this snapshot's limits.</p>{/if}
  </section>

  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll retained flow metadata.) -->
  <div class="table-scroll" role="region" aria-label="Retained OpenFlow rows" tabindex="0">
    <table>
      <caption>{meta.retained_rows} retained flow rows</caption>
      <thead><tr><th scope="col">Row</th><th scope="col">Table</th><th scope="col">Priority</th><th scope="col">Cookie</th><th scope="col">Packets</th><th scope="col">Bytes</th><th scope="col">Flow metadata</th></tr></thead>
      <tbody>
        {#each result.rows as row, index}
          <tr>
            <th scope="row">{page.offset + index + 1}</th><td>{row.table}</td><td>{row.priority}</td><td>{row.cookie}</td>
            <td>{row.packets ?? 'Unknown'}</td><td>{row.bytes ?? 'Unknown'}</td>
            <td>
              <details>
                <summary>Flow {page.offset + index + 1} details</summary>
                <p>Duration {duration(row)} s</p>
                <h3>{rawSnapshot ? 'Match fields' : 'Match field metadata'}</h3>
                <ul>{#each row.match_fields as field}
                  <li>Class 0x{field.class.toString(16)}, field {field.field}, {field.masked ? 'masked' : 'unmasked'}, {field.payload_bytes} bytes
                    {#if rawSnapshot && field.raw_payload_hex}<div>Raw payload (hex) <code class="raw-payload">{field.raw_payload_hex}</code></div>{/if}
                  </li>
                {:else}<li>No match field descriptors retained.</li>{/each}</ul>
                <h3>{rawSnapshot ? 'Instructions' : 'Instruction metadata'}</h3>
                <ul>{#each row.instructions as instruction}
                  <li>Type {instruction.type}, {instruction.bytes} bytes{#if instruction.action_types?.length} · Action types {instruction.action_types.join(', ')}{/if}
                    {#if rawSnapshot && instruction.raw_payload_hex}<div>Raw payload (hex) <code class="raw-payload">{instruction.raw_payload_hex}</code></div>{/if}
                  </li>
                {:else}<li>No instruction descriptors retained.</li>{/each}</ul>
                {#if row.metadata_truncated}<p>{rawSnapshot ? 'Some fields or payloads were omitted.' : 'Some metadata for this flow was omitted.'}</p>{/if}
              </details>
            </td>
          </tr>
        {:else}<tr><td colspan="7">No flow rows are present on this page of the retained snapshot.</td></tr>{/each}
      </tbody>
    </table>
  </div>

  <nav class="actions" aria-label="OpenFlow snapshot pages">
    {#if page.offset > 0}<Link href={openFlowSnapshotRoute(meta.id, Math.max(0, page.offset - openFlowPageSize))}>Previous page</Link>{/if}
    {#if page.next_offset !== undefined}<Link href={openFlowSnapshotRoute(meta.id, page.next_offset)}>Next page</Link>{/if}
    <button disabled={exporting || expired} onclick={exportSnapshot}>{exporting ? 'Reading original snapshot…' : 'Download original snapshot as JSON'}</button>
  </nav>

  {#if query.include_groups_meters}
    <section class="panel" aria-labelledby="openflow-aux-heading">
      <h2 id="openflow-aux-heading">Group and Meter configuration</h2>
      <p>Statistics <Status value={result.group_meter_statistics ?? 'unknown'} /></p>
      <h3>Groups <Status value={result.groups} /></h3>
      {#if expert && result.group_reason}<p><code>{result.group_reason}</code></p>{/if}
      {#each result.group_rows ?? [] as group (group.group_id)}
        <details>
          <summary>Group {group.group_id} · Type {group.type_code}</summary>
          <ul>{#each group.buckets as bucket}<li>Bucket weight {bucket.weight}{#if bucket.action_types.length} · Action types {bucket.action_types.join(', ')}{/if}</li>{:else}<li>No bucket descriptors retained.</li>{/each}</ul>
          {#if group.metadata_truncated}<p>Some group metadata was omitted.</p>{/if}
        </details>
      {/each}
      <h3>Meters <Status value={result.meters} /></h3>
      {#if expert && result.meter_reason}<p><code>{result.meter_reason}</code></p>{/if}
      {#each result.meter_rows ?? [] as meter (meter.meter_id)}
        <details>
          <summary>Meter {meter.meter_id} · Flags 0x{meter.flags.toString(16)}</summary>
          <ul>{#each meter.bands as band}<li>Band type {band.type_code}, rate {band.rate}, burst {band.burst}</li>{:else}<li>No meter-band descriptors retained.</li>{/each}</ul>
          {#if meter.metadata_truncated}<p>Some meter metadata was omitted.</p>{/if}
        </details>
      {/each}
    </section>
  {/if}
{:else if page && expired}
  <p class="notice warning" role="status">{openFlowReason('OPENFLOW_SNAPSHOT_UNAVAILABLE')}</p>
{:else}
  <LoadNotice load={{ ...model.openFlowSnapshot, error: model.openFlowSnapshot.error ? openFlowReason(model.openFlowSnapshot.error) : '' }} />
  {#if expert && model.openFlowSnapshot.error}<p><code>{model.openFlowSnapshot.error}</code></p>{/if}
{/if}

{#if allowed && failure}
  <p class="notice warning" role="alert">{failure === 'INVALID_OPENFLOW_FORM'
    ? 'Enter a table from 0 to 254, a row limit from 1 to 128, and an optional unsigned 64-bit decimal cookie.'
    : openFlowReason(failure)}</p>
  {#if expert}<p><code>{failure}</code></p>{/if}
{/if}

<style>
  .raw-payload {
    overflow-wrap: anywhere;
  }
</style>
