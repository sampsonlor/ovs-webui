<script lang="ts">
  import type { Model } from './model';
  import { controller, refPath } from './model';
  import { interfaceFields, field, availability, observation, deviceObservation, pciAssociation } from './interface-observation';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import LinuxDevice from './LinuxDevice.svelte';
  import InterfaceConfiguration from './InterfaceConfiguration.svelte';
  import InterfaceNativeType from './InterfaceNativeType.svelte';
  import InterfacePolicing from './InterfacePolicing.svelte';
  import { has, mtuEditReason, policingEditReason } from './policy';
  let { model, expert, desktop }: { model: Model; expert: boolean; desktop: boolean } = $props();
  const item = $derived(model.interface.value);
  const identity = $derived(item?.management_id ?? model.path.split('/')[2]);
  const deviceFields = [['driver_name', 'Driver'], ['driver_version', 'Driver version'], ['firmware_version', 'Firmware version'], ['bus_info', 'Reported bus'], ['numa_id', 'NUMA node'], ['if_type', 'Reported device type']];
</script>

<div class="breadcrumbs"><Link href="/interfaces">Interfaces</Link><span>› {item?.name ?? 'Interface'}</span></div>
<header class="page-heading"><div><p class="eyebrow">Switching / Interface detail</p><h1>{item?.name ?? 'Interface'}</h1><p>Native configuration and observations for this exact Interface identity.</p></div><button onclick={() => controller.refresh()}>Refresh resource</button></header>
<LoadNotice load={model.interface} />
{#if model.interface.error === 'NOT_FOUND'}<p class="notice warning">This Interface identity is no longer available. A same-name replacement has its own identity.</p>{/if}
{#if refPath({kind:'interface', id:identity})}
  <section class="panel" aria-label="Interface shared evidence"><h2>Events & Audit</h2><p>Review shared records for this exact Interface identity, including associated MTU and policing execution and Safe Apply evidence. Retained records may not cover older changes.</p><div class="actions">{#each [['events', 'Events', 'events.read'], ['audit', 'Audit', 'audit.read']] as [collection, label, capability]}{#if model.sessionReady && has(model.session, capability)}<Link href={`/operations/${collection}?object_id=${encodeURIComponent(identity)}`}>Open Interface {label}</Link>{:else}<span>{label} unavailable with current authorization.</span>{/if}{/each}</div></section>
{/if}
{#if item}
  <div class="summary"><Status value={item.source.freshness} /><span>Observed {item.source.observed_at ?? 'Unknown'}</span><Status value={item.source.confidence} /></div>
  {#if item.source.freshness !== 'fresh'}<p class="notice warning" role="status">Stale observation. Refresh before relying on these values.</p>{/if}
  <section class="panel"><h2>Bridge → Port → Interface</h2><div class="topology">{#if item.bridge_ref && refPath(item.bridge_ref)}<Link href={refPath(item.bridge_ref)!}>Bridge · {item.bridge_ref.id.slice(0, 8)}</Link>{:else}<span>Bridge unknown</span>{/if}<span>→</span><Link href={`/ports/${item.port_ref.id}`}>Port · {item.port_ref.id.slice(0, 8)}</Link><span>→</span><strong>{item.name}</strong></div><p>{item.local_interface === true ? 'Local internal Interface.' : item.port_kind === 'bond' ? 'This Interface is a member of a Bond Port.' : 'This Interface belongs to a single-interface Port.'}</p></section>
  <section class="panel"><h2>Native Interface fields</h2><p>Observed MTU is the current device value. Requested MTU is a separate configuration value.</p>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the native fields.) -->
    <div class="table-scroll" tabindex="0" role="region" aria-label="Native Interface fields"><table><thead><tr><th>Field</th><th>Native value</th><th>Availability</th>{#if expert}<th>Source / observation</th>{/if}</tr></thead><tbody>{#each interfaceFields as [name, label]}{@const nativeSource = field(item, name)?.source}<tr><th scope="row">{label}{#if expert}<small>{name}</small>{/if}</th><td>{observation(item, name)}</td><td><Status value={availability(item, name)} /></td>{#if expert}<td>{nativeSource?.provider_id ?? 'Unavailable'} · {nativeSource?.authority ?? 'Unknown'}<small>{nativeSource?.freshness ?? 'Unavailable'} · {nativeSource?.confidence ?? 'Unknown'}</small><small>{nativeSource?.observed_at ?? 'Not observed'}</small></td>{/if}</tr>{/each}</tbody></table></div>
  </section>
  <InterfaceConfiguration {item} {expert} />
  <InterfacePolicing {item} {expert} />
  <section class="panel" aria-label="Policing configuration change"><h2>Policing change</h2><p>Ingress authority: {typeof item.policing_ownership === 'string' ? item.policing_ownership : 'Unknown'}.</p>{#if !policingEditReason(item, model.session, desktop)}<Link href={`/interfaces/${item.management_id}/policing`}>Edit ingress policing →</Link>{:else}<p class="muted">{policingEditReason(item, model.session, desktop)}</p>{/if}</section>
  <InterfaceNativeType {item} {expert} />
  <LinuxDevice {item} {expert} />
  <div class="columns"><section class="panel"><h2>OVS reported device status</h2><dl>{#each deviceFields as [key, label]}<dt>{label}</dt><dd>{deviceObservation(item, key)}</dd>{/each}<dt>Reported PCI bus</dt><dd>{pciAssociation(item)}</dd></dl><p class="muted">Source: OVS Interface status. Reported bus and driver hints are independent of the host association above.</p></section>
    <section class="panel"><h2>Configuration context</h2><dl><dt>Ownership</dt><dd>{typeof item.ownership === 'string' ? item.ownership : 'Unknown'}</dd><dt>MTU authority</dt><dd>{typeof item.mtu_ownership === 'string' ? item.mtu_ownership : 'Unknown'}</dd></dl>{#if !mtuEditReason(item, model.session, desktop)}<Link href={`/interfaces/${item.management_id}/mtu`}>Edit MTU request →</Link>{:else}<p class="muted">{mtuEditReason(item, model.session, desktop)}</p>{/if}<p>Review supported Port changes through its Candidate workflow.</p><Link href={`/ports/${item.port_ref.id}`}>Review owning Port →</Link>{#if expert}<h3>Observed safe options</h3><Status value={availability(item, 'options')} />{#if availability(item, 'options') === 'known'}<pre>{JSON.stringify(item.options, null, 2)}</pre>{:else}<p>{observation(item, 'options')}</p>{/if}<p class="muted">Only the reported safe option subset is shown. An empty subset does not prove that all native options are empty.</p>{/if}</section></div>
  {#if expert}<details open><summary>Native identity and evidence</summary><dl><dt>Management ID</dt><dd class="mono">{item.management_id}</dd><dt>OVS UUID</dt><dd class="mono">{item.ovs_uuid}</dd><dt>Generation</dt><dd class="mono">{item.instance_generation}</dd><dt>Configuration revision</dt><dd class="mono">{item.config_revision}</dd></dl><pre>{JSON.stringify(item.fields, null, 2)}</pre></details>{/if}
{/if}
