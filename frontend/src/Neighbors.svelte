<script lang="ts">
  import { untrack } from 'svelte';
  import type { Neighbor, NeighborPage } from '../../clients/typescript/public-v1.generated';
  import { controller, navigate, refPath, type Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { neighborFamily, neighborScopes, neighborsPath, neighborsReason, neighborsRoute } from './neighbors';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  const detail = $derived(model.path.startsWith(neighborsRoute + '/'));
  const observedItem = $derived(detail && model.resource.status === 'ready' ? model.resource.value as unknown as Neighbor | null : null);
  const item = $derived(observedItem?.source.freshness === 'fresh' ? observedItem : null);
  const page = $derived(!detail && model.resource.status === 'ready' ? model.resource.value as unknown as NeighborPage | null : null);
  const source = $derived(observedItem?.source ?? page?.source);
  const currentPage = $derived(page && model.resource.status === 'ready' && page.source?.freshness === 'fresh' &&
    (page.availability === 'complete' || page.availability === 'degraded'));
  const rows = $derived(currentPage && page ? page.items as Neighbor[] : []);
  let querySnapshot: string | undefined;
  let filter = $state(untrack(() => new URLSearchParams(model.query).get('filter') ?? ''));
  let family = $state('');
  let limit = $state('25');
  let scopes = $state({ bridge_id: '', port_id: '', interface_id: '' });

  $effect(() => {
    if (model.query === querySnapshot) return;
    querySnapshot = model.query;
    const params = new URLSearchParams(model.query);
    filter = params.get('filter') ?? '';
    family = params.get('family') ?? '';
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
    if (family) params.set('family', family);
    for (const [key] of neighborScopes) {
      if (scopes[key].trim()) params.set(key, scopes[key].trim());
    }
    navigate(neighborsPath(params.toString()));
  }

  function download() {
    if (!currentPage || !page) return;
    const query = new URLSearchParams(model.query);
    if (!query.has('limit')) query.set('limit', '25');
    const exported = {
      exported_at: new Date().toISOString(),
      query: Object.fromEntries(query),
      page,
    };
    const url = URL.createObjectURL(new Blob([JSON.stringify(exported, null, 2) + '\n'], { type: 'application/json' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = 'ovs-webui-neighbors-page.json';
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
</script>

{#if detail}
  <div class="breadcrumbs">
    <Link href={neighborsPath(model.query, new URLSearchParams(model.query).get('cursor'))}>Neighbors</Link>
    <span>› {item?.address ?? 'Neighbor'}</span>
  </div>
{/if}
<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility</p>
    <h1>{detail ? item?.address ?? 'Neighbor' : 'Neighbors'}</h1>
    <p>Review the Linux ARP and NDP cache on managed Interfaces.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh neighbors</button>
</header>

<LoadNotice load={model.resource} />
{#if model.resource.error === 'CURSOR_EXPIRED'}
  <p class="notice warning" role="status">The neighbor snapshot changed or expired. Restart from the first page to review this scope.</p>
  <Link href={neighborsPath(model.query)}>First neighbor page</Link>
{/if}
{#if detail && model.resource.error === 'NOT_FOUND'}
  <p class="notice warning" role="status">This neighbor identity is no longer in the current table. Review the current entries for its Interface.</p>
{/if}
{#if source}
  <div class="summary" role="region" aria-label="Neighbor observation source">
    <Status value={source.freshness} />
    <span>Source {source.authority}</span>
    {#if source.observed_at}<span>Sampled <time datetime={source.observed_at}>{source.observed_at}</time></span>{/if}
  </div>
  {#if source.freshness !== 'fresh'}
    <p class="notice warning" role="status">Current neighbor observations are unavailable.</p>
  {/if}
{/if}

{#if !detail}
  <form class="neighbor-filters" aria-label="Neighbor filters" onsubmit={search}>
    <div>
      <label for="neighbor-filter">IP, MAC, or Interface</label>
      <input id="neighbor-filter" type="search" bind:value={filter} maxlength="1024" />
    </div>
    {#each neighborScopes as [key, label] (key)}
      <div>
        <label for={`neighbor-${key}`}>{label}</label>
        <input id={`neighbor-${key}`} bind:value={scopes[key]} maxlength="36" placeholder="All" />
      </div>
    {/each}
    <div>
      <label for="neighbor-family">Address family</label>
      <select id="neighbor-family" bind:value={family}>
        <option value="">IPv4 and IPv6</option>
        <option value="ipv4">IPv4 / ARP</option>
        <option value="ipv6">IPv6 / NDP</option>
      </select>
    </div>
    <div>
      <label for="neighbor-limit">Page size</label>
      <select id="neighbor-limit" bind:value={limit}>
        {#each [1, 10, 25, 50] as size}<option value={String(size)}>{size}</option>{/each}
        {#if !['1', '10', '25', '50'].includes(limit)}<option value={limit}>{limit}</option>{/if}
      </select>
    </div>
    <button type="submit">Apply filter</button>
    <button type="button" onclick={() => navigate(neighborsPath(''))}>Clear filters</button>
  </form>
{/if}

{#if page}
  <div class="summary">
    <Status value={page.availability ?? 'unavailable'} />
    {#if currentPage}<span>{rows.length} entries on this page</span>{/if}
    {#if page.coverage}<span>{page.coverage.observed_interfaces} of {page.coverage.eligible_interfaces} eligible Interfaces observed</span>{/if}
  </div>
  {#if page.reason}<p class="notice warning" role="status">{neighborsReason(page.reason)}</p>{/if}
  {#if page.truncated && page.reason !== 'NEIGHBORS_TRUNCATED'}
    <p class="notice warning" role="status">The neighbor collection reached its entry limit. Some entries are omitted from this snapshot.</p>
  {/if}
  {#if currentPage}
    <p class="muted">Neighbor state describes the kernel cache at the sample time.</p>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll the neighbor cache.) -->
    <div class="table-scroll" role="region" aria-label="Linux ARP and NDP neighbors" tabindex="0">
      <table>
        <caption>Reported Linux ARP and NDP entries</caption>
        <thead><tr><th scope="col">Address</th><th scope="col">Family</th><th scope="col">MAC</th><th scope="col">Neighbor state</th><th scope="col">Interface</th><th scope="col">Bridge / Port</th></tr></thead>
        <tbody>
          {#each rows as row (row.id)}
            <tr>
              <th scope="row"><Link href={neighborsRoute + '/' + encodeURIComponent(row.id) + model.query}>{row.address ?? 'Unknown address'}</Link></th>
              <td>{neighborFamily(row.family)}</td>
              <td class="mono">{row.mac ?? 'Unreported'}</td>
              <td><Status value={row.state} neutral /></td>
              <td>
                {#if row.interface_ref && refPath(row.interface_ref)}<Link href={refPath(row.interface_ref)!}>{row.interface_name ?? 'Interface'}</Link>{:else}{row.interface_name ?? 'Interface unknown'}{/if}
                {#if expert}<small>ifindex {row.ifindex ?? 'Unknown'}</small>{/if}
              </td>
              <td>
                {#if row.bridge_ref && refPath(row.bridge_ref)}<Link href={refPath(row.bridge_ref)!}>{row.bridge_name ?? 'Bridge'}</Link>{:else}Bridge unknown{/if}
                <small>{#if row.port_ref && refPath(row.port_ref)}<Link href={refPath(row.port_ref)!}>{row.port_name ?? 'Port'}</Link>{:else}Port unknown{/if}</small>
              </td>
            </tr>
          {:else}
            <tr><td colspan="6">{page.availability === 'complete' && !page.truncated ? 'No ARP or NDP entries match this scope.' : 'No entries are available in the observed portion of this scope.'}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    <nav class="actions neighbor-pages" aria-label="Neighbor pages">
      <Link href={neighborsPath(model.query)}>First neighbor page</Link>
      {#if page.next_cursor}<Link href={neighborsPath(model.query, page.next_cursor)}>Next neighbor page</Link>{/if}
      <span>{page.next_cursor ? 'More entries in this snapshot.' : page.truncated ? 'End of the collected portion.' : 'End of this snapshot.'}</span>
      <button onclick={download}>Download this page as JSON</button>
    </nav>
    <p class="muted">The JSON download contains this page with its scope, sample time, coverage and pagination metadata.</p>
  {:else if source?.freshness === 'fresh'}
    <p class="notice warning" role="status">Current neighbor observations are unavailable.</p>
  {/if}
  {#if expert}
    <details>
      <summary>Neighbor coverage and source</summary>
      <dl>
        <dt>Snapshot</dt><dd class="mono">{page.snapshot_id}</dd>
        <dt>Generation</dt><dd class="mono">{page.instance_generation ?? 'Unknown'}</dd>
        <dt>Provider</dt><dd>{source?.provider_id ?? 'Unknown'}</dd>
        <dt>Confidence</dt><dd>{source?.confidence ?? 'Unknown'}</dd>
        <dt>Coverage scope</dt><dd>{page.coverage?.scope ?? 'Unknown'}</dd>
        <dt>Matching entries</dt><dd>{page.coverage?.matched_entries ?? 'Unknown'}</dd>
        <dt>Collection limit</dt><dd>{page.coverage?.max_entries ?? 'Unknown'}</dd>
        <dt>Collection truncated</dt><dd>{page.truncated ? 'Yes' : 'No'}</dd>
        <dt>Reason</dt><dd>{page.reason ?? 'None'}</dd>
      </dl>
    </details>
  {/if}
{:else if item}
  <section class="panel" aria-label="Neighbor observation">
    <h2>Neighbor cache entry</h2>
    <dl>
      <dt>Address</dt><dd class="mono">{item.address ?? 'Unknown'}</dd>
      <dt>Family</dt><dd>{neighborFamily(item.family)}</dd>
      <dt>MAC</dt><dd class="mono">{item.mac ?? 'Unreported'}</dd>
      <dt>Neighbor state</dt><dd><Status value={item.state} neutral /></dd>
      <dt>Linux ifindex</dt><dd>{item.ifindex ?? 'Unknown'}</dd>
    </dl>
  </section>
  <section class="panel" aria-label="Neighbor Interface scope">
    <h2>Bridge → Port → Interface</h2>
    <div class="topology">
      {#if item.bridge_ref && refPath(item.bridge_ref)}<Link href={refPath(item.bridge_ref)!}>{item.bridge_name ?? 'Bridge'}</Link>{:else}<span>Bridge unknown</span>{/if}
      <span>→</span>
      {#if item.port_ref && refPath(item.port_ref)}<Link href={refPath(item.port_ref)!}>{item.port_name ?? 'Port'}</Link>{:else}<span>Port unknown</span>{/if}
      <span>→</span>
      {#if item.interface_ref && refPath(item.interface_ref)}<Link href={refPath(item.interface_ref)!}>{item.interface_name ?? 'Interface'}</Link>{:else}<span>{item.interface_name ?? 'Interface unknown'}</span>{/if}
    </div>
    {#if item.interface_ref}<Link href={neighborsPath('interface_id=' + encodeURIComponent(item.interface_ref.id))}>Review this Interface's neighbors</Link>{/if}
  </section>
  {#if expert}
    <details>
      <summary>Neighbor identity and source</summary>
      <dl>
        <dt>Neighbor management ID</dt><dd class="mono">{item.id}</dd>
        <dt>Interface management ID</dt><dd class="mono">{item.interface_ref?.id ?? 'Unknown'}</dd>
        <dt>Provider</dt><dd>{item.source.provider_id}</dd>
        <dt>Authority</dt><dd>{item.source.authority}</dd>
        <dt>Confidence</dt><dd>{item.source.confidence}</dd>
      </dl>
    </details>
  {/if}
{/if}

<style>
  h1 { overflow-wrap: anywhere; }
  .neighbor-filters { display: flex; align-items: end; flex-wrap: wrap; gap: .75rem; margin-block: 1rem; }
  .neighbor-filters > div { display: grid; gap: .25rem; min-width: 0; }
  .neighbor-filters input { width: 13rem; max-width: 100%; }
  .neighbor-filters select { max-width: 100%; }
  .neighbor-pages { margin-block: 1rem; }
  th, td { vertical-align: top; }
  th:first-child, td:nth-child(3) { overflow-wrap: anywhere; min-width: 10rem; }
  @media (max-width: 600px) {
    .neighbor-filters > div { width: 100%; }
    .neighbor-filters input, .neighbor-filters select { width: 100%; }
  }
</style>
