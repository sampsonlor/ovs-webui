<script lang="ts">
  import type { QoSObservation, QoSRate, Resource } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';

  type Entry = Resource & { native_qos: QoSObservation; ovs_uuid?: string };
  let { model, expert }: { model: Model; expert: boolean } = $props();
  const base = '/switching/qos';
  const bindingText = {
    bound: 'One bound Port',
    shared: 'Shared across Ports',
    unbound: 'Unbound',
    unknown: 'Port binding unknown',
  };
  const rateState = { configured: 'Unknown', unset: 'Unset', unknown: 'Unknown', 'not-applicable': 'Not applicable' };
  let detail = $derived(model.path !== base);
  let entry = $derived(detail ? (model.resource.value as Entry | null) : null);
  let observation = $derived(entry?.native_qos);
  let items = $derived((model.resource.value?.items as Entry[] | undefined) ?? []);
  let cursor = $derived(model.resource.value?.next_cursor);

  function rate(value: QoSRate) {
    return value.bps === null ? rateState[value.state] : `${value.bps} bit/s`;
  }
  function discipline(value: string | null) {
    return value === null ? 'Unknown' : value || 'No scheduler type configured';
  }
  function nextPage(token: string) {
    const query = new URLSearchParams(model.query);
    query.set('cursor', token);
    return `${base}?${query}`;
  }
</script>

<section aria-label="Native QoS and Queues">
  <p class="eyebrow">Switching</p>
  <h1>QoS &amp; Queues</h1>
  <p>Review egress QoS, Port bindings and native Queue configuration.</p>
  {#if detail}<p><Link href={base}>← QoS &amp; Queues</Link></p>{/if}
  <LoadNotice load={model.resource} />
  {#if model.resource.status === 'ready'}
    {#if !detail}
      {#if items.length > 0}
        <div class="table-scroll">
          <table>
            <caption>Observed QoS configurations</caption>
            <thead><tr><th scope="col">QoS</th><th scope="col">Discipline</th><th scope="col">Port binding</th><th scope="col">Queues</th><th scope="col">Observation</th></tr></thead>
            <tbody>
              {#each items as item (item.id)}
                <tr>
                  <th scope="row"><Link href={`${base}/${item.id}`}>QoS · {item.id.slice(0, 8)}</Link></th>
                  <td>{discipline(item.native_qos.discipline)}</td>
                  <td>{bindingText[item.native_qos.binding_state]}</td>
                  <td>{item.native_qos.queue_count ?? 'Unknown'}</td>
                  <td><Status value={item.source.freshness} /></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p>{new URLSearchParams(model.query).has('filter') ? 'No matching QoS configurations in this observation.' : 'No QoS configurations in this observation.'}</p>
      {/if}
      {#if typeof cursor === 'string'}<p><Link href={nextPage(cursor)}>Next QoS page →</Link></p>{/if}
    {:else if entry && observation}
      <div class="page-heading">
        <h2>QoS · {entry.id.slice(0, 8)}</h2>
        <Status value={entry.source.freshness} />
      </div>
      <section class="panel">
        <h3>Native configuration</h3>
        <dl>
          <dt>Discipline</dt><dd>{discipline(observation.discipline)}</dd>
          <dt>Configured maximum rate</dt><dd>{rate(observation.max_rate)}</dd>
          <dt>Port binding</dt><dd>{bindingText[observation.binding_state]}</dd>
          <dt>Observed at</dt><dd>{entry.source.observed_at ?? 'Unknown'}</dd>
          {#if expert}
            <dt>Management ID</dt><dd>{entry.id}</dd>
            <dt>Native QoS UUID</dt><dd>{entry.ovs_uuid ?? 'Unknown'}</dd>
          {/if}
        </dl>
        {#if observation.ports?.length}
          <ul>
            {#each observation.ports as port (port.port_ref.id)}
              <li><Link href={`/ports/${port.port_ref.id}`}>{port.name}</Link></li>
            {/each}
          </ul>
        {/if}
        {#if observation.ports_truncated}
          <p>Showing the first 32 of {observation.port_count ?? 'an unknown number of'} bound Ports.</p>
        {/if}
      </section>
      <section class="panel">
        <h3>Queue mapping</h3>
        {#if observation.queues === null}
          <p>Queue mapping is unavailable in this observation.</p>
        {:else if observation.queues.length === 0}
          <p>No explicit Queue mappings. Queue 0 is the default for traffic without an explicit queue selection.</p>
        {:else}
          <div class="table-scroll">
            <table>
              <caption>Queue numbers within this QoS configuration</caption>
              <thead><tr><th scope="col">Queue</th><th scope="col">Minimum rate</th><th scope="col">Maximum rate</th><th scope="col">DSCP</th>{#if expert}<th scope="col">Management ID</th>{/if}</tr></thead>
              <tbody>
                {#each observation.queues as queue (queue.queue_id)}
                  <tr>
                    <th scope="row">{queue.queue_id}{queue.queue_id === 0 ? ' (default)' : ''}</th>
                    <td>{rate(queue.min_rate)}</td>
                    <td>{rate(queue.max_rate)}</td>
                    <td>{queue.dscp ?? (queue.dscp_state === 'unset' ? 'Unset' : 'Unknown')}</td>
                    {#if expert}<td>{queue.queue_ref.id}</td>{/if}
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
      <section class="panel">
        <h3>Runtime evidence</h3>
        <p>Scheduler installation and Queue counters are unavailable in this view. Queue mappings alone do not show which traffic uses each Queue.</p>
      </section>
    {/if}
  {/if}
</section>
