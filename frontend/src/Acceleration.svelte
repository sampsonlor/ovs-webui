<script lang="ts">
  import type { RuntimeAccelerationEvidence } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, type Load } from './model';
  import { observationLabel, observationReason } from './management-observation';
  import { dpdkRoute, offloadRoute, type AccelerationKind } from './acceleration';

  let {
    load, kind, expert,
  }: { load: Load<RuntimeAccelerationEvidence>; kind: AccelerationKind; expert: boolean } = $props();

  const evidence = $derived(load.status === 'ready' ? load.value : undefined);
  const ovs = $derived(evidence?.ovs.state === 'ready' ? evidence.ovs.observation : undefined);
  const host = $derived(evidence?.host);
  type Sample = { state: string; value?: string | number | boolean | number[]; reason_code?: string };
  type Field = { label: string; sample: Sample };
  const configuration = $derived(ovs && (kind === 'dpdk'
    ? [
        { label: 'DPDK initialization setting', sample: ovs.dpdk.init_mode },
        { label: 'Configured socket memory (MiB)', sample: ovs.dpdk.socket_memory_mib },
        { label: 'Configured PMD CPU count', sample: ovs.dpdk.configured_pmd_cpus },
      ]
    : [
        { label: 'Configured hardware offload', sample: ovs.offload.configured },
        { label: 'Configured TC policy', sample: ovs.offload.tc_policy },
      ]));
  const runtime = $derived(ovs && (kind === 'dpdk'
    ? [
        { label: 'OVS-reported initialization', sample: ovs.dpdk.reported_initialized },
        { label: 'OVS-reported DPDK version', sample: ovs.dpdk.reported_version },
        { label: 'Runtime PMD threads', sample: ovs.dpdk.pmd_threads },
        { label: 'Runtime RX queues', sample: ovs.dpdk.rx_queues },
        { label: 'Process NUMA affinity', sample: ovs.dpdk.numa_affinity },
      ]
    : [
        { label: 'Runtime hardware support', sample: ovs.offload.hardware_support },
        { label: 'Runtime hardware flows', sample: ovs.offload.hardware_flows },
        { label: 'Observed software fallback', sample: ovs.offload.software_fallback },
      ]));

  function format(value: string | number | boolean | number[] | undefined): string {
    if (value === true) return 'Yes';
    if (value === false) return 'No';
    if (Array.isArray(value)) return value.join(', ');
    return value === undefined ? 'Unknown' : String(value);
  }

  function poolSize(size: number): string {
    return size === 2048 ? '2 MiB' : size === 1048576 ? '1 GiB' : `${size} KiB`;
  }
</script>

{#snippet fieldValue(field: Sample)}
  {#if field.value !== undefined}
    {format(field.value)} <small>{observationLabel(field.state)}</small>
  {:else}<Status value={field.state} />{/if}
  {#if expert && field.reason_code}<small><code>{field.reason_code}</code></small>{/if}
{/snippet}

{#snippet observation(fields: Field[] | undefined, source: string | undefined)}
  <p><Status value={evidence?.ovs.state} /></p>
  {#if fields && ovs}
    <p>Sampled <time datetime={ovs.observed_at}>{ovs.observed_at}</time></p>
    {#if expert}
      <p>Source <code>{source}</code></p>
      <p>Generation <code>{ovs.instance_generation}</code></p>
    {/if}
    <dl>
      {#each fields as field (field.label)}
        <dt>{field.label}</dt><dd>{@render fieldValue(field.sample)}</dd>
      {/each}
    </dl>
  {:else if evidence?.ovs.reason_code}
    <p>{observationReason(evidence.ovs.reason_code)}</p>
    {#if expert}<p><code>{evidence.ovs.reason_code}</code></p>{/if}
  {/if}
{/snippet}

<header class="page-heading">
  <div>
    <p class="eyebrow">Visibility</p>
    <h1>{kind === 'dpdk' ? 'DPDK' : 'Hardware Offload'}</h1>
    <p>{kind === 'dpdk' ? 'Review DPDK settings, runtime observations, and Linux huge pages.' : 'Review hardware offload settings and runtime observations.'}</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh observations</button>
</header>

<LoadNotice {load} />
{#if evidence}
  <section class="panel" aria-labelledby="acceleration-config-heading">
    <h2 id="acceleration-config-heading">OVSDB configuration</h2>
    {@render observation(configuration, ovs?.configuration_source)}
  </section>
  <section class="panel" aria-labelledby="acceleration-runtime-heading">
    <h2 id="acceleration-runtime-heading">OVS-reported runtime</h2>
    {@render observation(runtime, ovs?.reported_runtime_source)}
  </section>

  {#if kind === 'dpdk'}
    <section class="panel" aria-labelledby="acceleration-host-heading">
      <h2 id="acceleration-host-heading">Linux global memory and NUMA</h2>
      {#if host}
        <p><Status value={host.state} /></p>
        {#if host.observed_at}<p>Sampled <time datetime={host.observed_at}>{host.observed_at}</time></p>{/if}
        {#if expert}<p>Source <code>{host.source}</code></p>{/if}
        {#if host.reason_code}<p>{observationReason(host.reason_code)}</p>{/if}
        {#if host.state === 'observed' || host.state === 'partial'}
          <dl><dt>Online NUMA nodes</dt><dd>{@render fieldValue(host.online_numa_nodes)}</dd></dl>
          {#each host.huge_pages as pool (pool.size_kib)}
            <h3>{poolSize(pool.size_kib)} huge pages</h3>
            <dl>
              <dt>Persistent pages</dt><dd>{@render fieldValue(pool.persistent_pages)}</dd>
              <dt>Free pages</dt><dd>{@render fieldValue(pool.free_pages)}</dd>
              {#if expert}
                <dt>Reserved pages</dt><dd>{@render fieldValue(pool.reserved_pages)}</dd>
                <dt>Surplus pages</dt><dd>{@render fieldValue(pool.surplus_pages)}</dd>
              {/if}
            </dl>
          {/each}
        {/if}
      {:else}
        <p>Current Linux host evidence is unavailable.</p>
      {/if}
    </section>
  {/if}

  <nav class="actions" aria-label="Related acceleration observations">
    <Link href={kind === 'dpdk' ? offloadRoute : dpdkRoute}>
      {kind === 'dpdk' ? 'Hardware Offload' : 'DPDK'}
    </Link>
  </nav>
{/if}
