<script lang="ts">
  import { untrack } from 'svelte';
  import type { FdbEntryPage, Resource } from '../../clients/typescript/public-v1.generated';
  import { controller, navigate, refPath, type Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { fdbPath, fdbPortResolution, fdbReason, fdbRoute } from './fdb';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  const detail = $derived(model.path.startsWith(fdbRoute + '/'));
  const observedItem = $derived(detail && model.resource.status === 'ready' ? model.resource.value as Resource | null : null);
  const item = $derived(observedItem?.source.freshness === 'fresh' ? observedItem : null);
  const entry = $derived(item?.fdb);
  const page = $derived(!detail && model.resource.status === 'ready' ? model.resource.value as unknown as FdbEntryPage | null : null);
  const freshness = $derived(observedItem?.source.freshness ?? page?.freshness);
  const authority = $derived(observedItem?.source.authority ?? page?.source);
  const observedAt = $derived(observedItem?.source.observed_at ?? page?.observed_at);
  const currentPage = $derived(page && page.freshness === 'fresh' &&
    (page.availability === 'available' || page.availability === 'degraded'));
  const rows = $derived(currentPage && page ? page.items : []);
  const truncated = $derived(page?.truncated || page?.coverage?.truncated);
  let querySnapshot: string | undefined;
  let filters = $state(untrack(() => {
    const params = new URLSearchParams(model.query);
    return { mac: params.get('mac') ?? '', vlan: params.get('vlan') ?? '', bridge: params.get('bridge') ?? '', port: params.get('port') ?? '' };
  }));
  let limit = $state('25');
  let error = $state('');

  $effect(() => {
    if (model.query === querySnapshot) return;
    querySnapshot = model.query;
    const params = new URLSearchParams(model.query);
    limit = params.get('limit') ?? '25';
    filters = { mac: params.get('mac') ?? '', vlan: params.get('vlan') ?? '', bridge: params.get('bridge') ?? '', port: params.get('port') ?? '' };
    error = '';
  });

  function search(event: SubmitEvent) {
    event.preventDefault();
    const vlan = filters.vlan.trim();
    if (vlan && (!/^[0-9]+$/.test(vlan) || Number(vlan) > 4095)) {
      error = 'Enter a VLAN from 0 to 4095.';
      return;
    }
    const params = new URLSearchParams({ limit });
    for (const [key, value] of Object.entries(filters)) if (value.trim()) params.set(key, value.trim());
    navigate(fdbPath(params.toString()));
  }

  function download() {
    if (!currentPage || !page) return;
    const query = new URLSearchParams(model.query);
    if (!query.has('limit')) query.set('limit', '25');
    const exported = { exported_at: new Date().toISOString(), query: Object.fromEntries(query), page };
    const url = URL.createObjectURL(new Blob([JSON.stringify(exported, null, 2) + '\n'], { type: 'application/json' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = 'ovs-webui-fdb-page.json';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
</script>

{#if detail}
  <div class="breadcrumbs">
    <Link href={fdbPath(model.query, new URLSearchParams(model.query).get('cursor'))}>FDB</Link>
    <span>› {entry?.mac ?? 'FDB entry'}</span>
  </div>
{/if}
<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility</p>
    <h1>{detail ? entry?.mac ?? 'FDB entry' : 'FDB'}</h1>
    <p>Review the native forwarding database on managed Bridges.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh FDB</button>
</header>

<LoadNotice load={{
  ...model.resource,
  error: model.resource.error.startsWith('FDB_') ? fdbReason(model.resource.error) : model.resource.error,
}} />
{#if model.resource.error === 'CURSOR_EXPIRED'}
  <p class="notice warning" role="status">The FDB snapshot changed or expired. Restart from the first page to review this scope.</p>
  <Link href={fdbPath(model.query)}>First FDB page</Link>
{/if}
{#if detail && model.resource.error === 'NOT_FOUND'}
  <p class="notice warning" role="status">This FDB entry is no longer in the current observation. Review the current entries for its Bridge.</p>
{/if}
{#if authority}
  <div class="summary" role="region" aria-label="FDB observation source">
    <Status value={freshness} />
    <span>Source {authority}</span>
    {#if observedAt}<span>Sampled <time datetime={observedAt}>{observedAt}</time></span>{/if}
  </div>
  {#if freshness !== 'fresh'}<p class="notice warning" role="status">Current FDB observations are unavailable.</p>{/if}
{/if}

{#if !detail}
  <form class="fdb-filters" aria-label="FDB filters" onsubmit={search} oninput={() => error = ''}>
    <div><label for="fdb-mac">MAC</label><input id="fdb-mac" type="search" bind:value={filters.mac} maxlength="17" pattern="[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}" placeholder="All" /></div>
    <div><label for="fdb-vlan">VLAN</label><input id="fdb-vlan" inputmode="numeric" bind:value={filters.vlan} maxlength="4" placeholder="All" /></div>
    <div><label for="fdb-bridge">Bridge</label><input id="fdb-bridge" bind:value={filters.bridge} maxlength="36" placeholder="Management ID or all" /></div>
    <div><label for="fdb-port">Port</label><input id="fdb-port" bind:value={filters.port} maxlength="36" placeholder="Management ID or all" /></div>
    <div>
      <label for="fdb-limit">Page size</label>
      <select id="fdb-limit" bind:value={limit}>
        {#each [1, 10, 25, 50] as size}<option value={String(size)}>{size}</option>{/each}
        {#if !['1', '10', '25', '50'].includes(limit)}<option value={limit}>{limit}</option>{/if}
      </select>
    </div>
    <button type="submit">Apply filter</button>
    <button type="button" onclick={() => navigate(fdbPath(''))}>Clear filters</button>
  </form>
  {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
{/if}

{#if page}
  <div class="summary">
    <Status value={page.availability ?? 'unavailable'} />
    {#if currentPage}<span>{rows.length} entries on this page</span>{/if}
    {#if page.coverage}<span>{page.coverage.bridges_sampled} of {page.coverage.bridges_total} Bridges sampled</span>{/if}
  </div>
  {#if page.reason}<p class="notice warning" role="status">{fdbReason(page.reason)}</p>{/if}
  {#if truncated && page.reason !== 'truncated'}<p class="notice warning" role="status">Some native FDB entries were omitted from this observation.</p>{/if}
  {#if currentPage}
    <p class="muted">Last seen estimates use the reported age at sample time. Port links identify the associated logical OVS Port.</p>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll native forwarding entries.) -->
    <div class="table-scroll" role="region" aria-label="Forwarding database" tabindex="0">
      <table class="fdb-table">
        <caption>Observed native FDB entries</caption>
        <thead><tr><th scope="col">MAC</th><th scope="col">VLAN</th><th scope="col">Bridge</th><th scope="col">Native port</th><th scope="col">Logical Port</th><th scope="col">Age (s)</th><th scope="col">Last seen estimate</th></tr></thead>
        <tbody>
          {#each rows as row (row.id)}
            {@const native = row.fdb}
            <tr>
              <th scope="row"><Link href={fdbRoute + '/' + encodeURIComponent(row.id) + model.query}>{native?.mac ?? 'Unknown MAC'}</Link></th>
              <td>{native?.vlan ?? 'Unknown'}</td>
              <td>{#if native?.bridge_ref && refPath(native.bridge_ref)}<Link href={refPath(native.bridge_ref)!}>{native.bridge_name}</Link>{:else}{native?.bridge_name ?? 'Bridge unknown'}{/if}</td>
              <td>{native?.ofport ?? 'Unreported'}</td>
              <td>
                {#if native?.port_resolution === 'resolved' && native.port_ref && refPath(native.port_ref)}
                  <Link href={refPath(native.port_ref)!}>{native.port_name ?? 'Port'}</Link>
                {:else}
                  <span>{fdbPortResolution(native?.port_resolution)}</span>
                  {#if native?.port_name}<small>Reported native name {native.port_name}</small>{/if}
                {/if}
              </td>
              <td>{native?.age_seconds ?? 'Unreported'}</td>
              <td>{#if native?.last_seen_at}<time datetime={native.last_seen_at}>{native.last_seen_at}</time>{:else}Unknown{/if}</td>
            </tr>
          {:else}
            <tr><td colspan="7">
              {#if page.availability === 'available' && !truncated}
                {page.coverage?.entries_total === 0 ? 'The observed native FDB is empty.' : 'No FDB entries match this scope.'}
              {:else}
                No entries are available in the observed portion of this scope.
              {/if}
            </td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    <nav class="actions fdb-pages" aria-label="FDB pages">
      <Link href={fdbPath(model.query)}>First FDB page</Link>
      {#if page.next_cursor}<Link href={fdbPath(model.query, page.next_cursor)}>Next FDB page</Link>{/if}
      <span>{page.next_cursor ? 'More entries in this snapshot.' : truncated ? 'End of the observed portion.' : 'End of this snapshot.'}</span>
      <button onclick={download}>Download this page as JSON</button>
    </nav>
    <p class="muted">The JSON download contains this page with its scope, sample time, coverage and pagination metadata.</p>
  {:else if freshness === 'fresh'}
    <p class="notice warning" role="status">Current FDB observations are unavailable.</p>
  {/if}
  {#if expert}
    <details>
      <summary>FDB coverage and source</summary>
      <dl>
        <dt>Snapshot</dt><dd class="mono">{page.snapshot_id}</dd>
        <dt>Generation</dt><dd class="mono">{page.instance_generation ?? 'Unknown'}</dd>
        <dt>Native entries sampled</dt><dd>{page.coverage?.entries_sampled ?? 'Unknown'}</dd>
        <dt>Native entries total</dt><dd>{page.coverage?.entries_total ?? 'Unknown'}</dd>
        <dt>Observation truncated</dt><dd>{truncated ? 'Yes' : 'No'}</dd>
        <dt>Reason</dt><dd>{page.reason ?? 'None'}</dd>
      </dl>
    </details>
  {/if}
{:else if item && entry}
  <section class="panel" aria-label="FDB entry">
    <h2>Native FDB entry</h2>
    <dl>
      <dt>MAC</dt><dd class="mono">{entry.mac}</dd>
      <dt>VLAN</dt><dd>{entry.vlan}</dd>
      <dt>Native port</dt><dd>{entry.ofport ?? 'Unreported'}</dd>
      <dt>Age (s)</dt><dd>{entry.age_seconds ?? 'Unreported'}</dd>
      <dt>Last seen estimate</dt><dd>{#if entry.last_seen_at}<time datetime={entry.last_seen_at}>{entry.last_seen_at}</time>{:else}Unknown{/if}</dd>
    </dl>
    <p class="muted">Last seen is estimated from the reported age at sample time.</p>
  </section>
  <section class="panel" aria-label="FDB Port scope">
    <h2>Bridge → Port</h2>
    <div class="topology">
      {#if entry.bridge_ref && refPath(entry.bridge_ref)}<Link href={refPath(entry.bridge_ref)!}>{entry.bridge_name}</Link>{:else}<span>{entry.bridge_name}</span>{/if}
      <span>→</span>
      {#if entry.port_resolution === 'resolved' && entry.port_ref && refPath(entry.port_ref)}<Link href={refPath(entry.port_ref)!}>{entry.port_name ?? 'Port'}</Link>{:else}<span>{fdbPortResolution(entry.port_resolution)}</span>{/if}
    </div>
    {#if entry.bridge_ref}<Link href={fdbPath('bridge=' + encodeURIComponent(entry.bridge_ref.id))}>Review this Bridge's FDB</Link>{/if}
  </section>
  {#if expert}
    <details>
      <summary>FDB identity and source</summary>
      <dl>
        <dt>Entry management ID</dt><dd class="mono">{item.id}</dd>
        <dt>Port resolution</dt><dd>{entry.port_resolution}</dd>
        <dt>Provider</dt><dd>{item.source.provider_id}</dd>
        <dt>Authority</dt><dd>{item.source.authority}</dd>
        <dt>Confidence</dt><dd>{item.source.confidence}</dd>
      </dl>
    </details>
  {/if}
{/if}

<style>
  h1 { overflow-wrap: anywhere; }
  .fdb-filters { display: flex; align-items: end; flex-wrap: wrap; gap: .75rem; margin-block: 1rem; }
  .fdb-filters > div { display: grid; gap: .25rem; min-width: 0; }
  .fdb-filters input { width: 13rem; max-width: 100%; }
  .fdb-filters select { max-width: 100%; }
  .fdb-pages { margin-block: 1rem; }
  .fdb-table { min-width: 68rem; }
  th, td { vertical-align: top; overflow-wrap: anywhere; }
  @media (max-width: 600px) {
    .fdb-filters > div { width: 100%; }
    .fdb-filters input, .fdb-filters select { width: 100%; }
  }
</style>
