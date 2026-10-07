<script lang="ts">
  import type { Interface } from '../../clients/typescript/public-v1.generated';
  import { policingReady, policingSummary, policingRate } from './interface-policing';
  import Status from './Status.svelte';
  let { item, expert }: { item: Interface; expert: boolean } = $props();
  const value = $derived(item.linux_ingress_policing);
  const ready = $derived(policingReady(item));
</script>

<section class="panel" aria-labelledby="interface-policing-heading">
  <h2 id="interface-policing-heading">Linux ingress policing</h2>
  <p>Installed kernel rules are separate from the requested OVS rates above. This view does not measure traffic effectiveness or establish who installed a rule.</p>
  <div class="summary"><Status value={value?.availability ?? 'unavailable'} /><span>{ready && value?.source.observed_at ? `Sampled ${value.source.observed_at}` : 'Not sampled'}</span></div>
  <p class:notice={!ready || value?.availability === 'partial'} class:warning={!ready || value?.availability === 'partial'} role="status">{policingSummary(item)}</p>
  {#if ready}
    <p>{value?.filter_count ?? 'Unknown'} ingress filter records observed. Kernel rate units are bytes/s and packets/s; configured OVS units remain kbit/s and kpps.</p>
    {#if value?.actions.length}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll rule values.) -->
      <div class="table-scroll" tabindex="0" role="region" aria-label="Linux ingress police actions"><table><thead><tr><th>Action</th><th>Byte rate</th><th>Packet rate</th><th>Exceed / conform</th>{#if expert}<th>Filter evidence</th>{/if}</tr></thead><tbody>{#each value.actions as action, i}<tr><th scope="row">Police {i+1}</th><td>{policingRate(action,'bytes_per_second')}</td><td>{policingRate(action,'packets_per_second')}</td><td>{action.exceed_action} / {action.conform_action}</td>{#if expert}<td>{action.filter_kind}<small>Priority {action.priority} 路 {action.handle}</small><small>Police index {action.index}</small></td>{/if}</tr>{/each}</tbody></table></div>
    {/if}
  {/if}
  <p class="muted">Coverage: Linux tc ingress only. Burst conversion, egress, XDP, userspace / DPDK and hardware enforcement are not established. Rule collection is a diagnostic observation, not an atomic snapshot or Apply proof.</p>
  {#if expert}<details><summary>Ingress rule observation evidence</summary><dl><dt>Matched ifindex</dt><dd>{ready ? value?.ifindex : 'Not proven'}</dd><dt>Authority</dt><dd>{value?.source.authority ?? 'Unknown'}</dd><dt>Freshness</dt><dd>{value?.source.freshness ?? 'unavailable'}</dd><dt>Confidence</dt><dd>{value?.source.confidence ?? 'unknown'}</dd></dl><pre>{JSON.stringify(value,null,2)}</pre></details>{/if}
</section>
