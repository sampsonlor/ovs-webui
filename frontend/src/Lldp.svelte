<script lang="ts">
  import { untrack } from 'svelte';
  import type { LldpNeighborPage, Resource } from '../../clients/typescript/public-v1.generated';
  import { controller, navigate, refPath, type Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { neighborScopes } from './neighbors';
  import { lldpIdentifier, lldpPath, lldpReason, lldpRoute } from './lldp';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  const detail = $derived(model.path.startsWith(lldpRoute + '/'));
  const observedItem = $derived(detail && model.resource.status === 'ready' ? model.resource.value as Resource | null : null);
  const item = $derived(observedItem?.source.freshness === 'fresh' ? observedItem : null);
  const advertisement = $derived(item?.lldp);
  const page = $derived(!detail && model.resource.status === 'ready' ? model.resource.value as unknown as LldpNeighborPage | null : null);
  const source = $derived(observedItem?.source ?? page?.source);
  const currentPage = $derived(page && page.source?.freshness === 'fresh' &&
    (page.availability === 'complete' || page.availability === 'degraded'));
  const rows = $derived(currentPage && page ? page.items : []);
  let querySnapshot: string | undefined;
  let filter = $state(untrack(() => new URLSearchParams(model.query).get('filter') ?? ''));
  let limit = $state('25');
  let scopes = $state({ bridge_id: '', port_id: '', interface_id: '' });

  $effect(() => {
    if (model.query === querySnapshot) return;
    querySnapshot = model.query;
    const params = new URLSearchParams(model.query);
    filter = params.get('filter') ?? '';
    limit = params.get('limit') ?? '25';
    scopes = {
      bridge_id: params.get('bridge_id') ?? '',
      port_id: params.get('port_id') ?? '',
      interface_id: params.get('interface_id') ?? '',
    };
  });

  function search(event: SubmitEvent) {
    event.preventDefault();
    const params = new URLSearchParams({ limit });
    if (filter.trim()) params.set('filter', filter.trim());
    for (const [key] of neighborScopes) {
      if (scopes[key].trim()) params.set(key, scopes[key].trim());
    }
    navigate(lldpPath(params.toString()));
  }

  function download() {
    if (!currentPage || !page) return;
    const query = new URLSearchParams(model.query);
    if (!query.has('limit')) query.set('limit', '25');
    const exported = { exported_at: new Date().toISOString(), query: Object.fromEntries(query), page };
    const url = URL.createObjectURL(new Blob([JSON.stringify(exported, null, 2) + '\n'], { type: 'application/json' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = 'ovs-webui-lldp-page.json';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
</script>

{#if detail}
  <div class="breadcrumbs">
    <Link href={lldpPath(model.query, new URLSearchParams(model.query).get('cursor'))}>LLDP</Link>
    <span>› {advertisement?.system_name || 'LLDP neighbor'}</span>
  </div>
{/if}
<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility</p>
    <h1>{detail ? advertisement?.system_name || 'LLDP neighbor' : 'LLDP'}</h1>
    <p>Review LLDP advertisements received on opted-in managed Interfaces.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh LLDP</button>
</header>

<LoadNotice load={model.resource} />
{#if model.resource.error === 'CURSOR_EXPIRED'}
  <p class="notice warning" role="status">The LLDP snapshot changed or expired. Restart from the first page to review this scope.</p>
  <Link href={lldpPath(model.query)}>First LLDP page</Link>
{/if}
{#if detail && model.resource.error === 'NOT_FOUND'}
  <p class="notice warning" role="status">This LLDP advertisement is no longer retained. Review the current capture for its Interface.</p>
{/if}
{#if source}
  <div class="summary" role="region" aria-label="LLDP observation source">
    <Status value={source.freshness} />
    <span>Source {source.authority}</span>
    {#if source.observed_at}<span>Sampled <time datetime={source.observed_at}>{source.observed_at}</time></span>{/if}
  </div>
  {#if source.freshness !== 'fresh'}
    <p class="notice warning" role="status">Current LLDP observations are unavailable.</p>
  {/if}
{/if}

{#if !detail}
  <form class="lldp-filters" aria-label="LLDP filters" onsubmit={search}>
    <div><label for="lldp-filter">Find LLDP advertisements</label><input id="lldp-filter" type="search" bind:value={filter} maxlength="1024" /></div>
    {#each neighborScopes as [key, label] (key)}
      <div>
        <label for={`lldp-${key}`}>{label}</label>
        <input id={`lldp-${key}`} bind:value={scopes[key]} maxlength="36" placeholder="All" />
      </div>
    {/each}
    <div>
      <label for="lldp-limit">Page size</label>
      <select id="lldp-limit" bind:value={limit}>
        {#each [1, 10, 25, 50] as size}<option value={String(size)}>{size}</option>{/each}
        {#if !['1', '10', '25', '50'].includes(limit)}<option value={limit}>{limit}</option>{/if}
      </select>
    </div>
    <button type="submit">Apply filter</button>
    <button type="button" onclick={() => navigate(lldpPath(''))}>Clear filters</button>
  </form>
{/if}

{#if page}
  <div class="summary">
    <Status value={page.availability ?? 'unavailable'} />
    {#if currentPage}<span>{rows.length} advertisements on this page</span>{/if}
    {#if page.coverage}<span>{page.coverage.observed_interfaces} of {page.coverage.configured_interfaces} opted-in Interfaces observed</span>{/if}
  </div>
  {#if page.reason}<p class="notice warning" role="status">{lldpReason(page.reason)}</p>{/if}
  {#if page.truncated && page.reason !== 'LLDP_CAPTURE_TRUNCATED'}
    <p class="notice warning" role="status">The LLDP capture reached its entry limit. Some advertisements are omitted from this snapshot.</p>
  {/if}
  {#if currentPage}
    <p class="muted">Last seen is the received advertisement time. TTL is the lifetime advertised by the sender.</p>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll received LLDP advertisements.) -->
    <div class="table-scroll" role="region" aria-label="LLDP advertisements" tabindex="0">
      <table class="lldp-table">
        <caption>Received LLDP advertisements</caption>
        <thead><tr><th scope="col">Chassis ID</th><th scope="col">Remote port ID</th><th scope="col">System</th><th scope="col">Interface</th><th scope="col">Bridge / Port</th><th scope="col">Advertised TTL</th><th scope="col">Last seen</th></tr></thead>
        <tbody>
          {#each rows as row (row.id)}
            {@const received = row.lldp}
            <tr>
              <th scope="row">
                <Link href={lldpRoute + '/' + encodeURIComponent(row.id) + model.query}>{lldpIdentifier(received?.chassis_id)}</Link>
                {#if expert && received}<small>Subtype {received.chassis_id.subtype} · {received.chassis_id.encoding}</small>{/if}
              </th>
              <td>{lldpIdentifier(received?.port_id)}{#if expert && received}<small>Subtype {received.port_id.subtype} · {received.port_id.encoding}</small>{/if}</td>
              <td>{received?.system_name || 'Unreported'}</td>
              <td>
                {#if row.interface_ref && refPath(row.interface_ref)}<Link href={refPath(row.interface_ref)!}>{row.interface_name ?? 'Interface'}</Link>{:else}{row.interface_name ?? 'Interface unknown'}{/if}
                {#if expert}<small>ifindex {row.ifindex ?? 'Unknown'}</small>{/if}
              </td>
              <td>
                {#if row.bridge_ref && refPath(row.bridge_ref)}<Link href={refPath(row.bridge_ref)!}>{row.bridge_name ?? 'Bridge'}</Link>{:else}Bridge unknown{/if}
                <small>{#if row.port_ref && refPath(row.port_ref)}<Link href={refPath(row.port_ref)!}>{row.port_name ?? 'Port'}</Link>{:else}Port unknown{/if}</small>
              </td>
              <td>{received ? received.ttl_seconds + ' s' : 'Unreported'}</td>
              <td>{#if received}<time datetime={received.last_seen_at}>{received.last_seen_at}</time>{:else}Unreported{/if}</td>
            </tr>
          {:else}
            <tr><td colspan="7">{page.availability === 'complete' && !page.truncated ? 'No LLDP advertisements observed within this capture scope.' : 'No advertisements are available in the observed portion of this capture scope.'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    <nav class="actions lldp-pages" aria-label="LLDP pages">
      <Link href={lldpPath(model.query)}>First LLDP page</Link>
      {#if page.next_cursor}<Link href={lldpPath(model.query, page.next_cursor)}>Next LLDP page</Link>{/if}
      <span>{page.next_cursor ? 'More advertisements in this snapshot.' : page.truncated ? 'End of the captured portion.' : 'End of this snapshot.'}</span>
      <button onclick={download}>Download this page as JSON</button>
    </nav>
    <p class="muted">The JSON download contains this page with its capture scope, observation times, coverage and pagination metadata.</p>
  {:else if source?.freshness === 'fresh'}
    <p class="notice warning" role="status">Current LLDP observations are unavailable.</p>
  {/if}
  {#if expert}
    <details>
      <summary>LLDP coverage and source</summary>
      <dl>
        <dt>Snapshot</dt><dd class="mono">{page.snapshot_id}</dd>
        <dt>Generation</dt><dd class="mono">{page.instance_generation ?? 'Unknown'}</dd>
        <dt>Provider</dt><dd>{source?.provider_id ?? 'Unknown'}</dd>
        <dt>Confidence</dt><dd>{source?.confidence ?? 'Unknown'}</dd>
        <dt>Capture scope</dt><dd>{page.coverage?.scope ?? 'Unknown'}</dd>
        <dt>Matching advertisements</dt><dd>{page.coverage?.matched_entries ?? 'Unknown'}</dd>
        <dt>Capture limit</dt><dd>{page.coverage?.max_entries ?? 'Unknown'}</dd>
        <dt>Capture truncated</dt><dd>{page.truncated ? 'Yes' : 'No'}</dd>
        <dt>Reason</dt><dd>{page.reason ?? 'None'}</dd>
      </dl>
    </details>
  {/if}
{:else if item && advertisement}
  <section class="panel" aria-label="LLDP advertisement">
    <h2>Received advertisement</h2>
    <dl>
      <dt>Chassis ID</dt><dd class="mono">{lldpIdentifier(advertisement.chassis_id)}</dd>
      <dt>Remote port ID</dt><dd class="mono">{lldpIdentifier(advertisement.port_id)}</dd>
      <dt>System</dt><dd>{advertisement.system_name || 'Unreported'}</dd>
      <dt>Advertised TTL</dt><dd>{advertisement.ttl_seconds} s</dd>
      <dt>Last seen</dt><dd><time datetime={advertisement.last_seen_at}>{advertisement.last_seen_at}</time></dd>
    </dl>
  </section>
  <section class="panel" aria-label="LLDP Interface scope">
    <h2>Bridge → Port → Interface</h2>
    <div class="topology">
      {#if item.bridge_ref && refPath(item.bridge_ref)}<Link href={refPath(item.bridge_ref)!}>{item.bridge_name ?? 'Bridge'}</Link>{:else}<span>Bridge unknown</span>{/if}
      <span>→</span>
      {#if item.port_ref && refPath(item.port_ref)}<Link href={refPath(item.port_ref)!}>{item.port_name ?? 'Port'}</Link>{:else}<span>Port unknown</span>{/if}
      <span>→</span>
      {#if item.interface_ref && refPath(item.interface_ref)}<Link href={refPath(item.interface_ref)!}>{item.interface_name ?? 'Interface'}</Link>{:else}<span>{item.interface_name ?? 'Interface unknown'}</span>{/if}
    </div>
    {#if item.interface_ref}<Link href={lldpPath('interface_id=' + encodeURIComponent(item.interface_ref.id))}>Review this Interface's LLDP capture</Link>{/if}
  </section>
  {#if expert}
    <details>
      <summary>LLDP identity and source</summary>
      <dl>
        <dt>Advertisement management ID</dt><dd class="mono">{item.id}</dd>
        <dt>Interface management ID</dt><dd class="mono">{item.interface_ref?.id ?? 'Unknown'}</dd>
        <dt>Linux ifindex</dt><dd>{item.ifindex ?? 'Unknown'}</dd>
        <dt>Chassis ID subtype / encoding</dt><dd>{advertisement.chassis_id.subtype} / {advertisement.chassis_id.encoding}</dd>
        <dt>Remote port ID subtype / encoding</dt><dd>{advertisement.port_id.subtype} / {advertisement.port_id.encoding}</dd>
        <dt>Provider</dt><dd>{item.source.provider_id}</dd>
        <dt>Authority</dt><dd>{item.source.authority}</dd>
        <dt>Confidence</dt><dd>{item.source.confidence}</dd>
      </dl>
    </details>
  {/if}
{/if}

<style>
  h1 { overflow-wrap: anywhere; }
  .lldp-filters { display: flex; align-items: end; flex-wrap: wrap; gap: .75rem; margin-block: 1rem; }
  .lldp-filters > div { display: grid; gap: .25rem; min-width: 0; }
  .lldp-filters input { width: 13rem; max-width: 100%; }
  .lldp-filters select { max-width: 100%; }
  .lldp-pages { margin-block: 1rem; }
  .lldp-table { min-width: 72rem; }
  th, td { vertical-align: top; overflow-wrap: anywhere; }
  .lldp-table th:first-child, .lldp-table td:nth-child(2) { min-width: 10rem; }
  @media (max-width: 600px) {
    .lldp-filters > div { width: 100%; }
    .lldp-filters input, .lldp-filters select { width: 100%; }
  }
</style>
