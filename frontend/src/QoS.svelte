<script lang="ts">
  import type { LinuxQoSObservation, QoSObservation, QoSRate, Resource } from '../../clients/typescript/public-v1.generated';
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
  let runtime = $derived(entry?.linux_qos);
  let installed = $derived(runtime?.installed_rates);
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
  const runtimeMessages: Record<string, string> = {
    QOS_SHARED_PORT_BINDING: 'A single Port binding is required for scheduler and Queue observation.',
    QOS_PORT_UNBOUND: 'This QoS configuration has no bound Port.',
    QOS_DEVICE_BINDING_UNPROVEN: 'The Port could not be associated with a single Linux interface.',
    QOS_DISCIPLINE_UNSUPPORTED: 'Runtime observation supports Linux HTB and HFSC.',
    QOS_QUEUE_MAPPING_UNKNOWN: 'The native Queue mapping could not be verified.',
    QOS_RUNTIME_QUEUE_LIMIT: 'The Queue mapping exceeds the runtime observation limit.',
    QOS_QUEUE_ID_UNSUPPORTED: 'A configured Queue number is outside the Linux scheduler range.',
    QOS_PORT_BINDING_UNKNOWN: 'The Port bindings could not be verified.',
    LINUX_QOS_CLASSES_INCOMPLETE: 'Some expected Queue classes were not observed.',
    LINUX_QOS_COUNTERS_INCOMPLETE: 'Some Queue counters were not reported by Linux.',
    LINUX_QOS_ROOT_OFFLOADED: 'The kernel reports an offloaded scheduler.',
    OVS_ASSOCIATION_STALE: 'Wait for a fresh OVS observation of the Port and Interface.',
    QOS_ASSOCIATION_CHANGED: 'The OVS association changed during the kernel read.',
  };
  const schedulerLabels: Record<LinuxQoSObservation['scheduler'], string> = {
    observed: 'Observed', 'not-observed': 'Root scheduler not observed',
    different: 'Kernel scheduler differs from the supported layout', unavailable: 'Unavailable',
  };
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
        <h3>Linux egress scheduler</h3>
        {#if runtime}
          <p>{schedulerLabels[runtime.scheduler]}{runtime.root_kind ? ` · ${runtime.root_kind}` : ''}</p>
          {#if runtimeMessages[runtime.reason]}
            <p>{runtimeMessages[runtime.reason]}</p>
          {:else if runtime.scheduler === 'unavailable'}
            <p>Current kernel evidence is unavailable.</p>
          {/if}
          {#if expert && runtime.source.observed_at}
            <p>Sampled at {runtime.source.observed_at}</p>
          {/if}
          {#if installed}
            <dl><dt>Installed maximum rate</dt><dd>{installed.maximum_bps} bit/s</dd></dl>
            <!-- svelte-ignore a11y_no_noninteractive_tabindex (The scroll container must be reachable by keyboard.) -->
            <div class="table-scroll" role="region" aria-label="Installed Linux Queue rates" tabindex="0">
              <table>
                <caption>Installed Linux Queue rates</caption>
                <thead><tr><th scope="col">Queue</th><th scope="col">Minimum rate</th><th scope="col">Maximum rate</th></tr></thead>
                <tbody>
                  {#each installed.queues as queue (queue.queue_id)}
                    <tr>
                      <th scope="row">{queue.queue_id}{queue.queue_id === 0 ? ' (default)' : ''}</th>
                      <td>{queue.minimum_bps} bit/s</td>
                      <td>{queue.maximum_bps} bit/s</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {:else if runtime.scheduler === 'observed'}
            <p>Installed class rates are unavailable.</p>
          {/if}
          {#if runtime.queues.length}
            <!-- svelte-ignore a11y_no_noninteractive_tabindex (The scroll container must be reachable by keyboard.) -->
            <div class="table-scroll" role="region" aria-label="Linux Queue counters" tabindex="0">
              <table>
                <caption>Cumulative Linux Queue counters</caption>
                <thead><tr><th scope="col">Queue</th><th scope="col">Bytes</th><th scope="col">Packets</th><th scope="col">Drops</th></tr></thead>
                <tbody>
                  {#each runtime.queues as queue (queue.queue_id)}
                    <tr>
                      <th scope="row">{queue.queue_id}{queue.queue_id === 0 ? ' (default)' : ''}</th>
                      <td>{queue.bytes ?? 'Unreported'}</td><td>{queue.packets ?? 'Unreported'}</td><td>{queue.drops ?? 'Unreported'}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        {:else}
          <p>Current kernel evidence is unavailable.</p>
        {/if}
      </section>
    {/if}
  {/if}
</section>
