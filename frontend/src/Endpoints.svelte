<script lang="ts">
  import { untrack } from 'svelte';
  import type { EndpointObservation, EndpointPage, Resource, ResourceRef } from '../../clients/typescript/public-v1.generated';
  import { controller, navigate, refPath, type Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { endpointReason, endpointSources, endpointsPath, endpointsRoute } from './endpoints';
  import { fdbPortResolution, fdbReason } from './fdb';
  import { lldpIdentifier, lldpReason } from './lldp';
  import { neighborFamily, neighborsReason } from './neighbors';

  let { model, expert }: { model: Model; expert: boolean } = $props();

  const detail = $derived(model.path.startsWith(endpointsRoute + '/'));
  const observedItem = $derived(detail && model.resource.status === 'ready' ? model.resource.value as Resource | null : null);
  const entry = $derived(observedItem?.source.freshness === 'fresh' ? observedItem.endpoint : undefined);
  const page = $derived(!detail && model.resource.status === 'ready' ? model.resource.value as unknown as EndpointPage | null : null);
  const currentPage = $derived(page?.freshness === 'fresh' && (page.availability === 'complete' || page.availability === 'degraded'));
  const rows = $derived(currentPage ? (page?.items ?? []) : []);
  const routeQuery = $derived(new URLSearchParams(model.query));
  const filtered = $derived(['mac', 'ip', 'vlan', 'bridge', 'port', 'filter'].some((key) => routeQuery.has(key)));
  const macPattern = '([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}|([0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2}';
  let querySnapshot: string | undefined;
  let queryInput = $state(untrack(() => filterInput(model.query)));
  let error = $state('');

  function filterInput(query: string) {
    const params = new URLSearchParams(query);
    return {
      mac: params.get('mac') ?? '',
      ip: params.get('ip') ?? '',
      vlan: params.get('vlan') ?? '',
      bridge: params.get('bridge') ?? '',
      port: params.get('port') ?? '',
      limit: params.get('limit') ?? '25',
    };
  }

  $effect(() => {
    if (model.query === querySnapshot) return;
    querySnapshot = model.query;
    queryInput = filterInput(model.query);
    error = '';
  });

  function search(event: SubmitEvent) {
    event.preventDefault();
    const vlan = queryInput.vlan.trim();
    if (vlan && (!/^[0-9]+$/.test(vlan) || Number(vlan) > 4095)) {
      error = 'Enter a VLAN from 0 to 4095.';
      return;
    }
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(queryInput)) {
      if (value.trim()) params.set(key, value.trim());
    }
    navigate(endpointsPath(params.toString()));
  }

  function label(endpoint: EndpointObservation) {
    if (endpoint.basis === 'lldp') {
      const advertisement = endpoint.evidence[0];
      return advertisement?.system_name || (advertisement?.chassis_id ? lldpIdentifier(advertisement.chassis_id) : 'LLDP observation');
    }
    return endpoint.mac ?? endpoint.addresses[0] ?? 'Endpoint observation';
  }

  function vlanPath(endpoint: EndpointObservation) {
    const params = new URLSearchParams({ vlan: String(endpoint.vlan) });
    if (endpoint.bridge_ref?.kind === 'bridge' && refPath(endpoint.bridge_ref)) params.set('bridge', endpoint.bridge_ref.id);
    if (endpoint.port_ref?.kind === 'port' && refPath(endpoint.port_ref)) params.set('port', endpoint.port_ref.id);
    return endpointsPath(params.toString());
  }

  function sourceReason(kind: keyof typeof endpointSources, reason: string) {
    if (reason === 'OVS_ASSOCIATION_STALE') return endpointReason(reason);
    if (kind === 'fdb') return fdbReason(reason);
    if (kind === 'neighbor') return neighborsReason(reason);
    return lldpReason(reason);
  }

  const evidenceLinks: Record<string, string> = {
    fdb_entry: 'FDB evidence',
    neighbor: 'ARP/NDP evidence',
    lldp_neighbor: 'LLDP evidence',
  };
</script>

{#snippet scope(kind: string, label: string, reference: ResourceRef | null | undefined, name: string | null | undefined)}
  {@const href = reference?.kind === kind ? refPath(reference) : null}
  {#if href}
    <Link {href}>{name ?? label}</Link>
  {:else if name || reference}
    <span>{name ?? reference?.id}</span>
    <small>Unverified {label} association</small>
  {:else}
    <span>Unknown</span>
  {/if}
{/snippet}

{#if detail}
  <div class="breadcrumbs">
    <Link href={endpointsPath(model.query, routeQuery.get('cursor'))}>Endpoints</Link>
    <span>› {entry ? label(entry) : 'Endpoint observation'}</span>
  </div>
{/if}

<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility</p>
    <h1>{detail ? entry ? label(entry) : 'Endpoint observation' : 'Endpoints'}</h1>
    {#if !detail}<p>Review endpoint observations from FDB, ARP/NDP and LLDP by source and managed scope.</p>{/if}
  </div>
  <button onclick={() => controller.refresh()}>Refresh endpoints</button>
</header>

<LoadNotice load={{ ...model.resource, error: endpointReason(model.resource.error) }} />
{#if model.resource.error === 'CURSOR_EXPIRED'}
  <p class="notice warning" role="status">The endpoint snapshot changed or expired. Start at the first page to review this scope.</p>
  <Link href={endpointsPath(model.query)}>First page</Link>
{/if}
{#if detail && model.resource.error === 'NOT_FOUND'}
  <p class="notice warning" role="status">This identity is absent from the current endpoint observations.</p>
{/if}

{#if !detail && model.resource.status !== 'denied'}
  <form class="endpoint-filters" aria-label="Endpoint filters" onsubmit={search} oninput={() => error = ''}>
    <div>
      <label for="endpoint-mac">MAC</label>
      <input id="endpoint-mac" type="search" bind:value={queryInput.mac} maxlength="17" pattern={macPattern} placeholder="All" />
    </div>
    <div>
      <label for="endpoint-ip">IP address</label>
      <input id="endpoint-ip" type="search" bind:value={queryInput.ip} maxlength="45" placeholder="IPv4 or IPv6" />
    </div>
    <div>
      <label for="endpoint-vlan">VLAN</label>
      <input id="endpoint-vlan" inputmode="numeric" bind:value={queryInput.vlan} maxlength="4" placeholder="All" />
    </div>
    <div>
      <label for="endpoint-bridge">Bridge</label>
      <input id="endpoint-bridge" bind:value={queryInput.bridge} maxlength="36" placeholder="Management ID or all" />
    </div>
    <div>
      <label for="endpoint-port">Port</label>
      <input id="endpoint-port" bind:value={queryInput.port} maxlength="36" placeholder="Management ID or all" />
    </div>
    <div>
      <label for="endpoint-limit">Page size</label>
      <select id="endpoint-limit" bind:value={queryInput.limit}>
        {#each [1, 10, 25, 50] as size}
          <option value={String(size)}>{size}</option>
        {/each}
        {#if !['1', '10', '25', '50'].includes(queryInput.limit)}
          <option value={queryInput.limit}>{queryInput.limit}</option>
        {/if}
      </select>
    </div>
    <button type="submit">Apply filters</button>
    <button type="button" onclick={() => navigate(endpointsPath(''))}>Clear filters</button>
  </form>
  {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
{/if}

{#if page}
  <div class="summary">
    <Status value={page.availability ?? 'unavailable'} />
    {#if page.freshness}<Status value={page.freshness} />{/if}
    {#if currentPage && page.matched !== undefined}<span>{page.matched} {page.matched === 1 ? 'observation' : 'observations'} in the retained snapshot</span>{/if}
  </div>
  {#if page.truncated}
    <p class="notice warning" role="status">The observation limit was reached. Some endpoint evidence may be missing.</p>
  {/if}
  {#if currentPage}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll the endpoint observations.) -->
    <div class="table-scroll" role="region" aria-label="Endpoint observations" tabindex="0">
      <table>
        <caption>Observations grouped by source and verified scope</caption>
        <thead><tr><th scope="col">Observation</th><th scope="col">IP addresses</th><th scope="col">Bridge / Port</th><th scope="col">VLAN</th><th scope="col">Evidence</th></tr></thead>
        <tbody>
          {#each rows as row (row.id)}
            {@const endpoint = row.endpoint}
            {#if endpoint}
              <tr>
                <th scope="row">
                  <Link href={endpointsRoute + '/' + encodeURIComponent(row.id) + model.query}>{label(endpoint)}</Link>
                  <small>{endpointSources[endpoint.basis]}</small>
                </th>
                <td>
                  {#each endpoint.addresses as address (address)}
                    <small class="mono">{address}</small>
                  {:else}
                    <span>Unreported</span>
                  {/each}
                </td>
                <td>
                  <div>{@render scope('bridge', 'Bridge', endpoint.bridge_ref, endpoint.bridge_name)}</div>
                  <div>{@render scope('port', 'Port', endpoint.port_ref, endpoint.port_name)}</div>
                  {#if expert && endpoint.interface_ref}
                    <div>{@render scope('interface', 'Interface', endpoint.interface_ref, endpoint.interface_name)}</div>
                  {/if}
                </td>
                <td>{#if endpoint.vlan !== undefined}<Link href={vlanPath(endpoint)}>VLAN {endpoint.vlan}</Link>{:else}Unreported{/if}</td>
                <td>
                  {endpoint.evidence_total}
                  {#if endpoint.evidence_truncated}<small>{endpoint.evidence.length} retained here</small>{/if}
                </td>
              </tr>
            {/if}
          {:else}
            <tr><td colspan="5">
              {#if page.availability !== 'complete' || page.truncated}
                No endpoint observations are available in the observed portion of this scope.
              {:else if filtered}
                No endpoint observations match these filters.
              {:else}
                The observed scope has no endpoint observations.
              {/if}
            </td></tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if page.next_cursor || routeQuery.has('cursor')}
      <nav class="actions" aria-label="Endpoint pages">
        <Link href={endpointsPath(model.query)}>First page</Link>
        {#if page.next_cursor}<Link href={endpointsPath(model.query, page.next_cursor)}>Next page</Link>{/if}
      </nav>
    {/if}
  {:else}
    <p class="notice warning" role="status">{page.freshness === 'stale' ? 'The endpoint observations are stale.' : 'Current endpoint observations are unavailable for this scope.'}</p>
  {/if}
  <section class="panel" aria-label="Source coverage">
    <h2>Source coverage</h2>
    <div class="source-overview">
      {#each page.sources ?? [] as coverage (coverage.kind)}
        <div class="source-summary">
          <h3>{endpointSources[coverage.kind]}</h3>
          <p class="source-status">
            <Status value={coverage.availability} />
            <Status value={coverage.source.freshness} />
          </p>
          <p>{coverage.observed} of {coverage.eligible} {coverage.scope_unit === 'bridge' ? 'Bridges' : 'Interfaces'} observed</p>
          {#if coverage.source.observed_at}
            <p class="muted">Sampled <time datetime={coverage.source.observed_at}>{coverage.source.observed_at}</time></p>
          {/if}
          {#if coverage.scope_reason}<p>{endpointReason(coverage.scope_reason)}</p>{/if}
          {#if coverage.reason}<p>{sourceReason(coverage.kind, coverage.reason)}</p>{/if}
          {#if coverage.truncated}<p>Retained observations reached the source limit.</p>{/if}
          {#if expert}
            <dl>
              <dt>Provider</dt><dd>{coverage.source.provider_id}</dd>
              <dt>Authority</dt><dd>{coverage.source.authority}</dd>
              <dt>Confidence</dt><dd>{coverage.source.confidence}</dd>
            </dl>
          {/if}
        </div>
      {/each}
    </div>
    {#if expert}
      <details>
        <summary>Snapshot identity</summary>
        <dl>
          <dt>Snapshot</dt><dd class="mono">{page.snapshot_id}</dd>
          <dt>Generation</dt><dd class="mono">{page.instance_generation ?? 'Unknown'}</dd>
        </dl>
      </details>
    {/if}
  </section>
{:else if detail && observedItem && !entry}
  <p class="notice warning" role="status">Current endpoint evidence is unavailable.</p>
{:else if entry && observedItem}
  <section class="panel" aria-label="Endpoint scope">
    <h2>Observed scope</h2>
    <dl>
      <dt>Source</dt><dd>{endpointSources[entry.basis]} <Status value={observedItem.source.freshness} /></dd>
      <dt>MAC</dt><dd class="mono">{entry.mac ?? 'Unreported'}</dd>
      <dt>IP addresses</dt>
      <dd>
        {#each entry.addresses as address (address)}
          <span class="mono observed-address">{address}</span>
        {:else}Unreported{/each}
      </dd>
      <dt>Bridge</dt><dd>{@render scope('bridge', 'Bridge', entry.bridge_ref, entry.bridge_name)}</dd>
      <dt>Port</dt><dd>{@render scope('port', 'Port', entry.port_ref, entry.port_name)}</dd>
      <dt>Interface</dt><dd>{@render scope('interface', 'Interface', entry.interface_ref, entry.interface_name)}</dd>
      <dt>VLAN</dt><dd>{#if entry.vlan !== undefined}<Link href={vlanPath(entry)}>VLAN {entry.vlan}</Link>{:else}Unreported{/if}</dd>
    </dl>
  </section>
  <section class="panel" aria-label="Endpoint evidence">
    <h2>Endpoint evidence</h2>
    {#if entry.evidence_truncated}
      <p class="notice warning" role="status">Showing {entry.evidence.length} of {entry.evidence_total} retained observations for this endpoint.</p>
    {/if}
    {#each entry.evidence as evidence (evidence.ref.kind + '/' + evidence.ref.id)}
      {@const caption = evidenceLinks[evidence.ref.kind]}
      {@const href = caption ? refPath(evidence.ref) : null}
      <article class="evidence-item">
        <h3>
          {#if href}<Link {href}>{caption}</Link>
          {:else}<span>Source evidence</span>{/if}
        </h3>
        <p class="source-status"><Status value={evidence.state} neutral /><Status value={evidence.source.freshness} /></p>
        {#if !href}<p class="mono">Reference {evidence.ref.kind}/{evidence.ref.id} is unavailable.</p>{/if}
        <dl>
          {#if evidence.source.observed_at}
            <dt>Sampled</dt><dd><time datetime={evidence.source.observed_at}>{evidence.source.observed_at}</time></dd>
          {/if}
          {#if evidence.mac}<dt>Reported MAC</dt><dd class="mono">{evidence.mac}</dd>{/if}
          {#if evidence.ip}<dt>Reported IP</dt><dd class="mono">{evidence.ip}{#if evidence.family} ({neighborFamily(evidence.family)}){/if}</dd>{/if}
          {#if evidence.vlan !== undefined}<dt>Reported VLAN</dt><dd>{evidence.vlan}</dd>{/if}
          {#if evidence.ofport !== undefined}<dt>Native port</dt><dd>{evidence.ofport}</dd>{/if}
          {#if evidence.port_name}<dt>Reported native name</dt><dd>{evidence.port_name}</dd>{/if}
          {#if evidence.port_resolution && evidence.port_resolution !== 'resolved'}
            <dt>Port association</dt><dd>{fdbPortResolution(evidence.port_resolution)}</dd>
          {/if}
          {#if evidence.chassis_id}<dt>Advertised chassis</dt><dd class="mono">{lldpIdentifier(evidence.chassis_id)}</dd>{/if}
          {#if evidence.lldp_port_id}<dt>Advertised port</dt><dd class="mono">{lldpIdentifier(evidence.lldp_port_id)}</dd>{/if}
          {#if evidence.system_name}<dt>Advertised system</dt><dd>{evidence.system_name}</dd>{/if}
          {#if evidence.last_seen_at}
            <dt>{evidence.ref.kind === 'fdb_entry' ? 'Last seen estimate' : 'Last seen'}</dt>
            <dd><time datetime={evidence.last_seen_at}>{evidence.last_seen_at}</time></dd>
          {/if}
          {#if expert}
            {#if evidence.ttl_seconds !== undefined}<dt>Advertised TTL (s)</dt><dd>{evidence.ttl_seconds}</dd>{/if}
            <dt>Provider</dt><dd>{evidence.source.provider_id}</dd>
            <dt>Authority</dt><dd>{evidence.source.authority}</dd>
            <dt>Confidence</dt><dd>{evidence.source.confidence}</dd>
          {/if}
        </dl>
      </article>
    {/each}
  </section>
{/if}

<style>
  h1, dd, .breadcrumbs, .observed-address { overflow-wrap: anywhere; }
  .endpoint-filters { display: flex; align-items: end; flex-wrap: wrap; gap: .75rem; margin-block: 1rem; }
  .endpoint-filters > div { display: grid; gap: .25rem; min-width: 0; }
  .endpoint-filters input { width: 13rem; max-width: 100%; }
  .endpoint-filters select { max-width: 100%; }
  .source-overview { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 15rem), 1fr)); gap: .75rem; }
  .source-summary { min-width: 0; border: 1px solid var(--border); border-radius: 6px; padding: .75rem; overflow-wrap: anywhere; }
  .source-summary h3, .evidence-item h3 { margin-top: 0; }
  .source-status { display: flex; align-items: center; flex-wrap: wrap; gap: .4rem; }
  .evidence-item + .evidence-item { margin-top: 1rem; padding-top: 1rem; border-top: 1px solid var(--border); }
  .table-scroll table { min-width: 43rem; }
  .observed-address { display: block; }
  @media (max-width: 600px) {
    .endpoint-filters > div { width: 100%; }
    .endpoint-filters input, .endpoint-filters select { width: 100%; }
  }
</style>
