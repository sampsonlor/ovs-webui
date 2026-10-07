<script lang="ts">
  import { untrack } from 'svelte';
  import type { Interface } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import { controller } from './model';
  import { policingEditReason, policingNumber } from './policy';
  import Link from './Link.svelte';
  let {item, model, desktop}: {item:Interface; model:Model; desktop:boolean} = $props();
  const original = untrack(() => item);
  const byteRate = policingNumber(original.fields?.ingress_policing_rate?.value) ?? 0;
  const packetRate = policingNumber(original.fields?.ingress_policing_kpkts_rate?.value) ?? 0;
  let mode = $state<'disabled' | 'bandwidth' | 'packets'>(byteRate ? 'bandwidth' : packetRate ? 'packets' : 'disabled');
  let requested = $state(String(byteRate || packetRate || ''));
  let error = $state('');
  const unavailable = $derived(policingEditReason(item, model.session, desktop) ||
    (item.management_id !== original.management_id || item.ovs_uuid !== original.ovs_uuid || item.instance_generation !== original.instance_generation || item.config_revision !== original.config_revision || item.port_ref.id !== original.port_ref.id || item.bridge_ref?.id !== original.bridge_ref?.id
      ? 'Live configuration or attachment changed. Return to the Interface and review the current values.' : ''));
  async function save(event:SubmitEvent) {
    event.preventDefault();
    error = '';
    if (unavailable || model.busy || model.pending || !model.sessionReady) return;
    const rate = mode === 'disabled' ? 0 : Number(requested);
    if (mode !== 'disabled' && (!/^[1-9][0-9]*$/.test(requested) || !Number.isSafeInteger(rate) || rate < 1 || rate > (mode === 'bandwidth' ? 1000000 : 1000))) {
      error = mode === 'bandwidth' ? 'Enter 1–1000000 kbit/s.' : 'Enter 1–1000 kpps.';
      return;
    }
    await controller.stagePolicing(item, {mode, rate});
  }
</script>

<div class="breadcrumbs"><Link href="/interfaces">Interfaces</Link><span>›</span><Link href={`/interfaces/${item.management_id}`}>{item.name}</Link><span>› Ingress policing</span></div>
<header class="page-heading"><div><p class="eyebrow">Switching / Interface / Policing intent</p><h1>Edit ingress policing</h1><p>Prepare a change for {item.name}. Review its Diff, then use Safe Apply.</p></div></header>
<div class="notice"><strong>Current native configuration</strong><p>Bandwidth: {policingNumber(item.fields?.ingress_policing_rate?.value) ?? 'Unknown'} kbit/s · packets: {policingNumber(item.fields?.ingress_policing_kpkts_rate?.value) ?? 'Unknown'} kpps.</p><p>Configuration and installed Linux rules are separate observations. Neither measures actual traffic limiting.</p></div>
{#if unavailable}<p class="notice warning" role="status">{unavailable}</p>{/if}
<form class="panel form-panel" onsubmit={save}>
  <fieldset disabled={!!unavailable || model.busy || !!model.pending || !model.sessionReady}>
    <legend>Internal Interface ingress policing</legend>
    <label for="policing-mode">Policing mode</label><select id="policing-mode" bind:value={mode}><option value="disabled">Disabled</option><option value="bandwidth">Bandwidth</option><option value="packets">Packet rate</option></select>
    {#if mode !== 'disabled'}
      <label for="policing-rate">{mode === 'bandwidth' ? 'Rate (kbit/s)' : 'Rate (kpps)'}</label><input id="policing-rate" inputmode="numeric" required bind:value={requested} aria-describedby="policing-help" />
      <p id="policing-help">{mode === 'bandwidth' ? '1–1000000 kbit/s. 1 kbit/s = 1000 bits per second.' : '1–1000 kpps. 1 kpps = 1000 packets per second.'} The other rate is set to zero.</p>
    {:else}<p class="notice" role="status">Both rates will be zero. The server must prove that no ingress qdisc remains before confirmation.</p>{/if}
    <p>Both native bursts remain zero, preserving OVS-selected defaults. Custom bursts require a separate review.</p>
    <p class="notice warning">This Interface is explicitly assigned to exclusive ingress management. Changing policing can disrupt traffic and the management path. The server checks supported software rules before dispatch, confirmation and rollback.</p>
    <div class="actions"><button type="submit" class="primary">Stage in Candidate</button><Link href={`/interfaces/${item.management_id}`}>Cancel</Link></div>
  </fieldset>
  {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
</form>
