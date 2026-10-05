<script lang="ts">
  import type { Interface } from '../../clients/typescript/public-v1.generated';
  import { linuxObservation, linuxReason } from './interface-observation';
  import Status from './Status.svelte';
  let { item, expert }: { item: Interface; expert: boolean } = $props();
  const device = $derived(item.linux_device);
  const fields = [['carrier', 'Linux carrier'], ['operstate', 'Linux operational state'], ['mtu', 'Linux device MTU'], ['speed_mbps', 'Linux link speed'], ['duplex', 'Linux duplex'], ['pci_address', 'Host PCI association'], ['driver', 'Host device driver'], ['vendor_id', 'PCI vendor ID'], ['device_id', 'PCI device ID'], ['numa_node', 'NUMA node']];
</script>

<section class="panel" aria-labelledby="linux-device-heading">
  <h2 id="linux-device-heading">Linux device observations</h2>
  <p>Linux carrier and device state are separate from the OVS observations above. Hardware association requires host evidence.</p>
  <div class="summary"><Status value={device?.availability ?? 'unavailable'} /><span>{device?.source.observed_at ? `Sampled ${device.source.observed_at}` : 'Not sampled'}</span><Status value={device?.source.freshness ?? 'unavailable'} /></div>
  {#if device?.availability !== 'known'}<p class="notice warning" role="status">{linuxReason(item)}</p>{/if}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the observations.) -->
  <div class="table-scroll" tabindex="0" role="region" aria-label="Linux device observations"><table><thead><tr><th>Field</th><th>Host value</th><th>Availability</th></tr></thead><tbody>{#each fields as [key, label]}<tr><th scope="row">{label}{#if expert}<small>{key}</small>{/if}</th><td>{linuxObservation(item, key)}</td><td><Status value={device?.availability === 'known' ? device.fields[key]?.availability ?? 'unavailable' : 'unavailable'} /></td></tr>{/each}</tbody></table></div>
  {#if expert}<details><summary>Linux association evidence</summary><dl><dt>Matched ifindex</dt><dd>{device?.ifindex ?? 'Not proven'}</dd><dt>Provider</dt><dd>{device?.source.provider_id ?? 'Unavailable'}</dd><dt>Authority</dt><dd>{device?.source.authority ?? 'Unknown'}</dd><dt>Confidence</dt><dd>{device?.source.confidence ?? 'unknown'}</dd></dl><pre>{JSON.stringify(device, null, 2)}</pre></details>{/if}
</section>
