<script lang="ts">
  import { untrack } from 'svelte';
  import type {
    Statistic,
    StatisticPage,
  } from '../../clients/typescript/public-v1.generated';
  import { controller, navigate, refPath, type Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import {
    counterValue,
    rateFor,
    rateValue,
    sampleState,
    statisticCounters,
    statisticScopes,
    statisticsPath,
    statisticsReason,
    statisticsRoute,
  } from './statistics';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  type StatisticsPage = StatisticPage & { items: Statistic[] };
  const byteRateKeys = ['rx_bytes', 'tx_bytes'] as const;
  const detail = $derived(model.path.startsWith(statisticsRoute + '/'));
  const item = $derived(detail ? model.resource.value as unknown as Statistic | null : null);
  const page = $derived(!detail ? model.resource.value as unknown as StatisticsPage | null : null);
  const statistics = $derived(item?.statistics);
  const source = $derived(statistics?.source ?? item?.source ?? page?.source);
  const history = $derived(statistics?.history ?? []);
  const maxByteRate = $derived(Math.max(0, ...history.flatMap(sample => [sample.rates.rx_bytes ?? 0, sample.rates.tx_bytes ?? 0])));
  let querySnapshot: string | undefined;
  let filter = $state(untrack(() => new URLSearchParams(model.query).get('filter') ?? ''));
  let limit = $state('25');
  let scopes = $state({ bridge_id: '', port_id: '', interface_id: '' });
  const scopeParams = $derived(new URLSearchParams(model.query));

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
    for (const [key] of statisticScopes) if (scopes[key].trim()) params.set(key, scopes[key].trim());
    navigate(statisticsPath(params.toString()));
  }

  function detailPath(id: string) {
    return statisticsRoute + '/' + encodeURIComponent(id) + model.query;
  }

  function timeLabel(value: string) {
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date);
  }

  function width(value: number | null) {
    return value !== null && Number.isFinite(value) && value >= 0 && maxByteRate > 0
      ? value / maxByteRate * 100 : 0;
  }

</script>

{#if detail}
  <div class="breadcrumbs">
    <Link href={statisticsPath(model.query, scopeParams.get('cursor'))}>Statistics</Link>
    <span>› {item?.name ?? 'Interface statistics'}</span>
  </div>
{/if}
<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility / Statistics</p>
    <h1>{detail ? item?.name ?? 'Interface statistics' : 'Statistics'}</h1>
    <p>Review traffic reported for each Interface.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh statistics</button>
</header>

<LoadNotice load={model.resource} />
{#if model.resource.error === 'CURSOR_EXPIRED'}
  <p class="notice warning">The page snapshot changed or expired. Restart from the first page to review the selected scope.</p>
{/if}
{#if model.resource.error === 'NOT_FOUND' && detail}
  <p class="notice warning">This Interface identity is no longer available. A same-name replacement has its own statistics.</p>
{/if}
{#if source}
  <div class="summary">
    <Status value={source.freshness} />
    <span>Source {source.authority}</span>
    {#if source.observed_at}<span>Observed <time datetime={source.observed_at}>{source.observed_at}</time></span>{/if}
  </div>
  {#if source.freshness === 'stale'}
    <p class="notice warning" role="status">Stale statistics. Displayed counters are the last retained observation. Current traffic rates are unavailable.</p>
  {:else if source.freshness !== 'fresh'}
    <p class="notice warning" role="status">A current statistics observation is unavailable.</p>
  {/if}
{/if}

{#if !detail}
  <form class="statistics-filters" onsubmit={search}>
    <div><label for="statistics-name">Interface name</label><input id="statistics-name" type="search" bind:value={filter} maxlength="1024" /></div>
    {#each statisticScopes as [key, label] (key)}
      <div><label for={`statistics-${key}`}>{label}</label><input id={`statistics-${key}`} bind:value={scopes[key]} maxlength="36" placeholder="All" /></div>
    {/each}
    <div><label for="statistics-limit">Page size</label><select id="statistics-limit" bind:value={limit}>{#each [10, 25, 50] as size}<option value={String(size)}>{size}</option>{/each}{#if !['10', '25', '50'].includes(limit)}<option value={limit}>{limit}</option>{/if}</select></div>
    <button type="submit">Apply filter</button>
    <button type="button" onclick={() => navigate(statisticsPath(''))}>Clear filters</button>
  </form>
  <p class="muted">Port and Bridge scopes show individual member Interfaces.</p>
{/if}

{#if page}
  <div class="summary"><span>{page.items.length} Interfaces on this page</span><Status value={page.availability} /></div>
  {#if page.reason}<p class="notice warning" role="status">{statisticsReason(page.reason)}</p>{/if}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll traffic observations.) -->
  <div class="table-scroll" role="region" aria-label="Interface statistics" tabindex="0">
    <table>
      <caption>Reported cumulative Interface traffic</caption>
      <thead><tr><th>Interface</th><th>Bridge / Port</th><th>Received</th><th>Transmitted</th><th>Errors / drops</th><th>Sample</th></tr></thead>
      <tbody>
        {#each page.items as row (row.id)}
          {@const sample = row.statistics}
          <tr>
            <th scope="row"><Link href={detailPath(row.id)}>{row.name}</Link></th>
            <td>
              {#if row.bridge_ref && refPath(row.bridge_ref)}<Link href={refPath(row.bridge_ref)!}>{row.bridge_name ?? 'Bridge'}</Link>{:else}Bridge unknown{/if}
              <small>{#if row.port_ref && refPath(row.port_ref)}<Link href={refPath(row.port_ref)!}>{row.port_name ?? 'Port'}</Link>{:else}Port unknown{/if}</small>
            </td>
            <td>{counterValue(sample?.counters.rx_bytes)} bytes<small>{counterValue(sample?.counters.rx_packets)} packets</small><small>{rateValue(rateFor(sample, 'rx_bytes'))} bytes/s</small></td>
            <td>{counterValue(sample?.counters.tx_bytes)} bytes<small>{counterValue(sample?.counters.tx_packets)} packets</small><small>{rateValue(rateFor(sample, 'tx_bytes'))} bytes/s</small></td>
            <td>RX {counterValue(sample?.counters.rx_errors)} / {counterValue(sample?.counters.rx_dropped)}<small>TX {counterValue(sample?.counters.tx_errors)} / {counterValue(sample?.counters.tx_dropped)}</small></td>
            <td>
              <Status value={sample?.state ?? 'unknown'} neutral />
              {#if sample?.source.freshness !== 'fresh'}<Status value={sample?.source.freshness ?? 'unknown'} />{/if}
              <small>{#if sample?.sampled_at}<time datetime={sample.sampled_at}>{timeLabel(sample.sampled_at)}</time>{:else}Not sampled{/if}</small>
              {#if sample && (sample.reset || sample.discontinuity || sample.coalesced)}<small>{sampleState(sample)}</small>{/if}
              {#if expert && sample?.reason}<small class="mono">{sample.reason}</small>{/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="6">No Interfaces match this statistics scope.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
  <nav class="actions statistics-pages" aria-label="Statistics pages">
    <Link href={statisticsPath(model.query)}>First statistics page</Link>
    {#if page.next_cursor}<Link href={statisticsPath(model.query, page.next_cursor)}>Next statistics page</Link>{/if}
    <span>{page.truncated ? 'More Interfaces exist in this snapshot.' : 'End of this snapshot.'}</span>
  </nav>
  {#if expert}
    <details><summary>Statistics coverage and snapshot</summary><p class="mono">Snapshot {page.snapshot_id} · generation {page.instance_generation ?? 'Unknown'}</p><pre>{JSON.stringify(page.coverage, null, 2)}</pre></details>
  {/if}
{:else if item}
  <section class="panel" aria-label="Statistics Interface scope">
    <h2>Bridge → Port → Interface</h2>
    <div class="topology">
      {#if item.bridge_ref && refPath(item.bridge_ref)}<Link href={refPath(item.bridge_ref)!}>{item.bridge_name ?? 'Bridge'}</Link>{:else}<span>Bridge unknown</span>{/if}
      <span>→</span>
      {#if item.port_ref && refPath(item.port_ref)}<Link href={refPath(item.port_ref)!}>{item.port_name ?? 'Port'}</Link>{:else}<span>Port unknown</span>{/if}
      <span>→</span><strong>{item.name}</strong>
    </div>
    <div class="actions"><Link href={refPath(item.interface_ref) ?? '/interfaces'}>Review Interface</Link></div>
  </section>
  {#if statistics}
    <p class="notice">OVS publishes counters periodically. Rates average changes between manager observations, which are collected every {statistics.interval_ms / 1000} seconds. The underlying datapath capture time is unavailable.</p>
    {#if statistics.reason}
      <p class="notice warning" role="status">{statisticsReason(statistics.reason)}</p>
    {:else if statistics.state !== 'observed'}
      <p class="notice warning" role="status">{statistics.reason ? statisticsReason(statistics.reason) : statistics.state === 'unsupported' ? 'Traffic statistics are unsupported by the current source.' : 'Traffic counters are unavailable in the current observation.'}</p>
    {:else if statistics.reason}
      <p class="notice" role="status">{statisticsReason(statistics.reason)}</p>
    {/if}
    {#if statistics.reset || statistics.discontinuity || statistics.coalesced}
      <p class="notice warning" role="status">{sampleState(statistics)}. Read the next continuous sample before relying on a rate.</p>
    {/if}
    <section class="panel">
      <h2>Traffic counters</h2>
      <p class="muted">Counters are cumulative. Unknown values were unreported.</p>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll exact counter values.) -->
      <div class="table-scroll" role="region" aria-label="Traffic counters" tabindex="0">
        <table>
          <caption>Cumulative Interface counters</caption>
          <thead><tr><th>Counter</th><th>Reported value</th><th>Average rate</th></tr></thead>
          <tbody>
            {#each statisticCounters as [key, label, unit] (key)}
              <tr><th scope="row">{label}{#if expert}<small class="mono">{key}</small>{/if}</th><td class="mono">{counterValue(statistics.counters[key])} {unit}</td><td>{key in statistics.rates ? rateValue(rateFor(statistics, key)) + ' ' + unit + '/s' : '—'}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="muted">Sampled {#if statistics.sampled_at}<time datetime={statistics.sampled_at}>{statistics.sampled_at}</time>{:else}Unknown{/if}</p>
    </section>
    <section class="panel">
      <h2>Recent traffic trend</h2>
      <p class="muted">Up to {statistics.retention_seconds / 60} minutes of shared memory samples. A manager restart clears this history.</p>
      {#if history.length}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll the trend table.) -->
        <div class="table-scroll trend-scroll" role="region" aria-label="Recent traffic trend" tabindex="0">
          <table class="trend-table">
            <caption>Recent traffic samples</caption>
            <thead><tr><th>Observed at</th><th>Receive (bytes/s)</th><th>Transmit (bytes/s)</th><th>Receive (packets/s)</th><th>Transmit (packets/s)</th><th>Sample state</th></tr></thead>
            <tbody>
              {#each history as sample, index (sample.sampled_at + '/' + index)}
                <tr>
                  <th scope="row"><time datetime={sample.sampled_at} title={sample.sampled_at}>{timeLabel(sample.sampled_at)}</time></th>
                  {#each byteRateKeys as key (key)}
                    {@const rate = sample.rates[key]}
                    <td class="trend-rate">
                      <span class="rate-bar" class:transmit={key === 'tx_bytes'} style:width={width(rate) + '%'} aria-hidden="true"></span>
                      <span class="rate-number">{rateValue(rate)}</span>
                    </td>
                  {/each}
                  <td>{rateValue(sample.rates.rx_packets)}</td><td>{rateValue(sample.rates.tx_packets)}</td>
                  <td>{sampleState(sample)}{#if expert && sample.discontinuity}<small class="mono">{sample.discontinuity}</small>{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p role="status">Waiting for shared counter samples. Two continuous observations are needed for a rate.</p>
      {/if}
    </section>
    {#if expert}
      <details><summary>Statistics identity and source</summary>
        <dl><dt>Interface management ID</dt><dd class="mono">{item.interface_ref.id}</dd><dt>Native UUID</dt><dd class="mono">{item.native_uuid}</dd><dt>Provider generation</dt><dd class="mono">{item.instance_generation ?? 'Unknown'}</dd><dt>Provider</dt><dd>{statistics.source.provider_id}</dd><dt>Authority</dt><dd>{statistics.source.authority}</dd><dt>Confidence</dt><dd>{statistics.source.confidence}</dd><dt>Retained samples</dt><dd>{statistics.sample_count}</dd><dt>Reason</dt><dd>{statistics.reason ?? 'None'}</dd><dt>Discontinuity</dt><dd>{statistics.discontinuity ?? 'None'}</dd></dl>
      </details>
    {/if}
  {:else}
    <p class="notice warning" role="status">Traffic counters are unavailable for this Interface.</p>
  {/if}
{/if}

<style>
  .statistics-filters { display: flex; align-items: end; flex-wrap: wrap; gap: .75rem; margin-block: 1rem; }
  .statistics-filters > div { display: grid; gap: .25rem; min-width: 0; }
  .statistics-filters input { width: 13rem; max-width: 100%; }
  .statistics-filters input[type="search"] { width: 15rem; }
  .statistics-pages { margin-block: 1rem; }
  .trend-scroll { max-height: 26rem; }
  td, th { vertical-align: top; }
  .trend-rate { min-width: 9rem; position: relative; }
  .rate-bar { position: absolute; inset-inline-start: 0; bottom: .35rem; height: .25rem; background: var(--accent, #0b7664); opacity: .7; max-width: 100%; }
  .rate-bar.transmit { background: #317ba3; }
  .rate-number { position: relative; display: block; padding-bottom: .35rem; }
  .trend-table td:last-child { white-space: normal; min-width: 9rem; }
  @media (max-width: 600px) {
    .statistics-filters > div { width: 100%; }
    .statistics-filters input { width: 100%; }
    .statistics-filters input[type="search"] { width: 100%; }
  }
</style>
