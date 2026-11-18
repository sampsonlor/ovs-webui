<script lang="ts">
  import { untrack } from 'svelte';
  import type {
    Resource,
    TelemetryExporter,
    TelemetryPage,
    TelemetrySetting,
  } from '../../clients/typescript/public-v1.generated';
  import { controller, navigate, type Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';

  let { model, expert }: { model: Model; expert: boolean } = $props();

  const route = '/visibility/telemetry';
  const protocols = [['sflow', 'sFlow'], ['netflow', 'NetFlow'], ['ipfix', 'IPFIX']] as const;
  const names: Record<string, string> = {
    sampling: 'Sampling rate',
    polling: 'Polling interval (s)',
    active_timeout: 'Active timeout (s)',
    header: 'Sample header size (bytes)',
    agent: 'sFlow agent device',
    engine_type: 'Engine type',
    engine_id: 'Engine ID',
    add_id_to_interface: 'Include engine ID in Interface numbers',
    obs_domain_id: 'Observation domain ID',
    obs_point_id: 'Observation point ID',
    cache_active_timeout: 'Cache active timeout (s)',
    cache_max_flows: 'Cache flow limit',
    stats_interval: 'Statistics interval (s)',
    template_interval: 'Template interval (s)',
    'enable-tunnel-sampling': 'Tunnel sampling',
    'enable-input-sampling': 'Input sampling',
    'enable-output-sampling': 'Output sampling',
  };
  const basic = ['sampling', 'polling', 'active_timeout', 'obs_domain_id', 'obs_point_id'];
  const states: Record<TelemetryExporter['state'], string> = {
    configured: 'Configured',
    unconfigured: 'Not configured',
    unsupported: 'Unsupported',
    unknown: 'Unknown',
  };
  const reasons: Record<string, string> = {
    TELEMETRY_REFERENCE_CONFIGURED: 'OVSDB reports an attached exporter.',
    TELEMETRY_REFERENCE_UNSET: 'The native Bridge reference is empty.',
    TELEMETRY_SCHEMA_UNSUPPORTED: 'This exporter is unavailable with the observed OVSDB schema.',
    TELEMETRY_REFERENCE_UNKNOWN: 'The native Bridge reference could not be verified.',
    TELEMETRY_EXPORTER_ROW_UNAVAILABLE: 'The referenced exporter record is unavailable.',
  };

  const detail = $derived(model.path.startsWith(route + '/'));
  const item = $derived(detail ? (model.resource.value as unknown as Resource | null) : null);
  const page = $derived(!detail ? (model.resource.value as unknown as TelemetryPage | null) : null);
  const source = $derived(item?.source ?? page?.source);
  const stale = $derived(source?.freshness === 'stale');
  let filter = $state(untrack(() => new URLSearchParams(model.query).get('filter') ?? ''));
  let previousQuery: string | undefined;

  $effect(() => {
    if (model.query !== previousQuery) {
      filter = new URLSearchParams(model.query).get('filter') ?? '';
      previousQuery = model.query;
    }
  });

  function bridgeName(bridge: Resource) {
    return typeof bridge.name === 'string' && bridge.name ? bridge.name : bridge.id;
  }

  function filterPage(event: SubmitEvent) {
    event.preventDefault();
    const query = new URLSearchParams({ limit: '25' });
    if (filter.trim()) query.set('filter', filter.trim());
    navigate(route + '?' + query);
  }

  function pagePath(cursor?: string) {
    const query = new URLSearchParams(model.query);
    if (!query.has('limit')) query.set('limit', '25');
    if (cursor) query.set('cursor', cursor);
    else query.delete('cursor');
    return route + '?' + query;
  }

  function settings(exporter: TelemetryExporter, showNative: boolean) {
    const reported = new Map(Object.entries(exporter.settings));
    return Object.entries(names).flatMap(([name, label]) => {
      const field = reported.get(name);
      return field && (showNative || basic.includes(name)) ? [{name, label, field}] : [];
    });
  }

  function settingValue(field: TelemetrySetting) {
    if (field.state === 'unset') return 'Unset';
    if (field.state === 'unsupported') return 'Unsupported';
    if (field.state !== 'known' || field.value === null) return 'Unknown';
    if (typeof field.value === 'boolean') return field.value ? 'On' : 'Off';
    return String(field.value);
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility</p>
    <h1>Telemetry</h1>
    <p>Review sFlow, NetFlow and IPFIX configured on each Bridge.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh telemetry</button>
</header>

<LoadNotice load={model.resource} />
{#if stale}
  <p class="notice warning" role="status">Stale telemetry observation. Refresh to check the current configuration.</p>
{/if}
{#if page || item}
  <p class="notice">Read-only Bridge exporter configuration. <span>Collector delivery is unverified.</span></p>
{/if}

{#if page}
  <form class="filter-bar" onsubmit={filterPage}>
    <label for="telemetry-filter">Bridge name</label>
    <input id="telemetry-filter" type="search" bind:value={filter} maxlength="1024" />
    <button type="submit">Filter Bridges</button>
  </form>
  {#if source}
    <p class="muted">
      Observation <Status value={source.freshness} neutral />
      {#if source.observed_at}<time datetime={source.observed_at}>{source.observed_at}</time>{/if}
    </p>
  {/if}
  <section class="bridge-list" aria-label="Telemetry Bridges">
    {#each page.items as bridge (bridge.id)}
      <article class="panel">
        <h2><Link href={`${route}/${encodeURIComponent(bridge.id)}`}>{bridgeName(bridge)}</Link></h2>
        <dl>
          {#each protocols as [protocol, label]}
            {@const exporter = bridge.native_telemetry?.exporters[protocol]}
            <dt>{label}</dt>
            <dd>
              <Status value={exporter ? states[exporter.state] : 'Unknown'} neutral />
              {#if exporter?.state === 'configured'}
                <span class="muted">{exporter.target_count === null ? 'Target count unknown' : `${exporter.target_count} collector ${exporter.target_count === 1 ? 'target' : 'targets'}`}</span>
              {/if}
            </dd>
          {/each}
        </dl>
      </article>
    {:else}
      <p class="notice">No Bridges match this telemetry view.</p>
    {/each}
  </section>
  {#if page.next_cursor || new URLSearchParams(model.query).has('cursor')}
    <nav class="actions" aria-label="Telemetry pages">
      <Link href={pagePath()}>First telemetry page</Link>
      {#if page.next_cursor}<Link href={pagePath(page.next_cursor)}>Next telemetry page</Link>{/if}
    </nav>
  {/if}
{:else if item}
  <section class="panel" aria-label="Bridge telemetry overview">
    <h2>{bridgeName(item)}</h2>
    <div class="actions">
      <Link href={`/bridges/${encodeURIComponent(item.id)}`}>Review Bridge</Link>
      <Link href={route}>All telemetry Bridges</Link>
    </div>
    <dl>
      <dt>Observation</dt><dd><Status value={item.source.freshness} neutral /></dd>
      <dt>Observed at</dt>
      <dd>
        {#if item.source.observed_at}<time datetime={item.source.observed_at}>{item.source.observed_at}</time>{:else}Unknown{/if}
      </dd>
      {#if expert}
        <dt>Provider</dt><dd>{item.source.provider_id}</dd>
        <dt>Authority</dt><dd>{item.source.authority}</dd>
        <dt>Bridge management ID</dt><dd class="mono">{item.id}</dd>
      {/if}
    </dl>
  </section>
  {#if item.native_telemetry}
    <section class="columns" aria-label="Bridge exporters">
      {#each protocols as [protocol, label]}
        {@const exporter = item.native_telemetry.exporters[protocol]}
        <article class="panel" aria-labelledby={`telemetry-${protocol}`}>
          <h3 id={`telemetry-${protocol}`}>{label}</h3>
          <Status value={states[exporter.state]} neutral />
          {#if protocol === 'ipfix'}<p class="muted">Bridge sampling</p>{/if}
          {#if exporter.state === 'configured'}
            <dl>
              <dt>Collector targets</dt>
              <dd>
                {#if exporter.targets_state === 'known' || exporter.targets_state === 'limited'}
                  {#if exporter.targets?.length}
                    <ul class="targets">
                      {#each exporter.targets as target (target)}<li class="mono">{target}</li>{/each}
                    </ul>
                  {:else if exporter.target_count === 0}None configured{:else}Unknown{/if}
                  {#if exporter.targets_state === 'limited'}
                    <span class="muted">Showing {exporter.targets?.length ?? 0} of {exporter.target_count ?? 'unknown'} targets.</span>
                  {/if}
                {:else}Unknown{/if}
              </dd>
              {#each settings(exporter, expert) as {name, label, field} (name)}
                <dt>{label}</dt><dd>{settingValue(field)}</dd>
              {/each}
            </dl>
          {/if}
          {#if expert}
            <dl>
              <dt>Reason</dt><dd>{reasons[exporter.reason] ?? exporter.reason}</dd>
              {#if exporter.native_uuid}<dt>Native UUID</dt><dd class="mono">{exporter.native_uuid}</dd>{/if}
            </dl>
          {/if}
        </article>
      {/each}
    </section>
  {:else}
    <p class="notice warning" role="status">Exporter configuration is unavailable for this Bridge.</p>
  {/if}
{:else if detail}
  <Link href={route}>All telemetry Bridges</Link>
{:else if new URLSearchParams(model.query).has('cursor')}
  <Link href={pagePath()}>First telemetry page</Link>
{/if}

<style>
  .filter-bar { display: flex; align-items: center; flex-wrap: wrap; gap: .7rem; margin: 1rem 0; }
  .filter-bar input { min-width: min(100%, 12rem); }
  .bridge-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 19rem), 1fr)); gap: 1rem; }
  .bridge-list article { margin-bottom: 0; }
  nav.actions { margin-top: 1rem; }
  dd > span.muted { display: block; margin-top: .35rem; }
  .targets { margin: 0; padding: 0; list-style: none; }
</style>
