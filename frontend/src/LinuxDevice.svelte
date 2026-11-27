<script lang="ts">
  import type {
    Interface,
    LinuxHardwareField,
  } from '../../clients/typescript/public-v1.generated';
  import { linuxObservation, linuxReason } from './interface-observation';
  import Status from './Status.svelte';
  let { item, expert }: { item: Interface; expert: boolean } = $props();
  const device = $derived(item.linux_device);
  const fields = [['carrier', 'Linux carrier'], ['operstate', 'Linux operational state'], ['mtu', 'Linux device MTU'], ['speed_mbps', 'Linux link speed'], ['duplex', 'Linux duplex'], ['pci_address', 'Host PCI association'], ['driver', 'Host device driver'], ['vendor_id', 'PCI vendor ID'], ['device_id', 'PCI device ID'], ['numa_node', 'NUMA node']];
  const hardware = $derived(
    device?.availability === 'known' &&
      device.source.freshness === 'fresh' &&
      device.source.confidence === 'proven'
      ? device.hardware
      : undefined,
  );
  const hardwareFields = [
    ['pci_function', 'PCI function'],
    ['switch_port', 'Switch port type'],
    ['connector', 'Connector medium'],
  ] as const;
  const hardwareLabels: Record<string, string> = {
    'sr-iov-pf': 'SR-IOV physical function (PF)',
    'sr-iov-vf': 'SR-IOV virtual function (VF)',
    'physical-port': 'Physical switch port',
    'pci-pf-representor': 'PCI PF representor',
    'pci-vf-representor': 'PCI VF representor',
    'pci-sf-representor': 'PCI SF representor',
    'twisted-pair': 'Twisted pair',
    aui: 'AUI',
    mii: 'MII',
    bnc: 'BNC',
    fibre: 'Fiber',
    'direct-attach': 'Direct attach',
  };

  function hardwareValue(field: LinuxHardwareField | undefined): string {
    if (
      field?.availability !== 'known' ||
      field.source.freshness !== 'fresh' ||
      field.source.confidence !== 'proven' ||
      !field.value
    )
      return 'Unknown';
    return hardwareLabels[field.value] ?? field.value;
  }
</script>

<section class="panel" aria-labelledby="linux-device-heading">
  <h2 id="linux-device-heading">Linux device observations</h2>
  <p>The shared host sample reports Linux carrier and device state. OVS reports its link state separately.</p>
  <div class="summary"><Status value={device?.availability ?? 'unavailable'} /><span>{device?.source.observed_at ? `Sampled ${device.source.observed_at}` : 'Not sampled'}</span><Status value={device?.source.freshness ?? 'unavailable'} /></div>
  {#if device?.availability !== 'known'}<p class="notice warning" role="status">{linuxReason(item)}</p>{/if}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the observations.) -->
  <div class="table-scroll" tabindex="0" role="region" aria-label="Linux device observations"><table><thead><tr><th>Field</th><th>Host value</th><th>Availability</th></tr></thead><tbody>{#each fields as [key, label]}<tr><th scope="row">{label}{#if expert}<small>{key}</small>{/if}</th><td>{linuxObservation(item, key)}</td><td><Status value={device?.availability === 'known' ? device.fields[key]?.availability ?? 'unavailable' : 'unavailable'} neutral={device?.availability === 'known' && device.fields[key]?.reason === 'LINUX_HARDWARE_ASSOCIATION_UNPROVEN'} /></td></tr>{/each}
    {#each hardwareFields as [key, label]}
      {@const field = hardware?.[key]}
      {@const value = hardwareValue(field)}
      <tr>
        <th scope="row">{label}{#if expert}<small>{key}</small>{/if}</th>
        <td>
          {value}
          {#if expert && field}
            <small>{field.source.provider_id} · {field.source.authority}</small>
            <small>{field.source.observed_at ? `Sampled ${field.source.observed_at}` : 'Not sampled'}</small>
          {/if}
        </td>
        <td><Status value={value === 'Unknown' ? 'unknown' : 'known'} neutral /></td>
      </tr>
    {/each}
  </tbody></table></div>
  {#if expert}<details><summary>Linux association evidence</summary><dl><dt>Matched ifindex</dt><dd>{device?.ifindex ?? 'Not proven'}</dd><dt>Provider</dt><dd>{device?.source.provider_id ?? 'Unavailable'}</dd><dt>Authority</dt><dd>{device?.source.authority ?? 'Unknown'}</dd><dt>Confidence</dt><dd>{device?.source.confidence ?? 'unknown'}</dd></dl><pre>{JSON.stringify(device, null, 2)}</pre></details>{/if}
</section>
