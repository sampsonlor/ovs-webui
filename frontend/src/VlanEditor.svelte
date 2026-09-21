<script lang="ts">
  import { untrack } from 'svelte';
  import type { Model } from './model';
  import { controller } from './model';
  import type { Port, VlanInput } from '../../clients/typescript/public-v1.generated';
  import { editReason, standardModes, vlanNumbers, vlanText } from './policy';
  import Link from './Link.svelte';
  let { port, model, desktop }: { port: Port; model: Model; desktop: boolean } = $props();
  const original = untrack(() => port);
  let mode = $state(original.vlan.native?.vlan_mode ?? '');
  let tag = $state(String(original.vlan.native?.tag ?? ''));
  let trunks = $state(original.vlan.native?.trunks.join(', ') ?? '');
  let error = $state('');
  const reason = $derived(editReason(port, model.session, desktop));
  const changed = $derived(original.config_revision !== port.config_revision);
  const unavailable = $derived(reason || (changed ? 'Live configuration changed while this form was open. Return to the Port and review the new value.' : ''));
  async function save(event: SubmitEvent) {
    event.preventDefault(); error = '';
    if (unavailable || model.busy || model.pending || !model.sessionReady) return;
    try {
      if (!standardModes.some((m) => m === mode)) throw new Error('Select a supported VLAN mode.');
      const ids = mode === 'access' ? [] : vlanNumbers(trunks);
      const tagNumber = mode === 'trunk' ? null : vlanNumbers(tag);
      if (tagNumber !== null && tagNumber.length !== 1) throw new Error('Enter one VLAN tag from 1 to 4094.');
      const value = { vlan_mode: mode, tag: tagNumber?.[0] ?? null, trunks: ids, cvlans: [] } as VlanInput;
      await controller.stage(port, value);
    } catch (e) { error = e instanceof Error ? e.message : 'Unable to stage VLAN intent.'; }
  }
</script>
<div class="breadcrumbs"><Link href="/ports">Ports</Link><span>›</span><Link href={`/ports/${port.management_id}`}>{port.name}</Link><span>› VLAN</span></div>
<header class="page-heading"><div><p class="eyebrow">Switching / Port / VLAN intent</p><h1>Edit VLAN</h1><p>Prepare a change for {port.name}. Live configuration changes only after validation and Safe Apply.</p></div></header>
<div class="notice"><strong>Current native value</strong><p>{vlanText(port.vlan.native)}</p></div>
{#if unavailable}<div class="notice warning" role="status">{unavailable}</div>{/if}
<form class="panel form-panel" onsubmit={save}>
  <fieldset disabled={!!unavailable || model.busy || !!model.pending || !model.sessionReady}>
    <legend>VLAN configuration</legend>
    <label>VLAN mode<select bind:value={mode}>
      {#if !standardModes.some((m) => m === mode)}<option value={mode}>{mode || 'Native default (preserved)'}</option>{/if}
      {#each standardModes as option}<option value={option} disabled={!Array.isArray(port.vlan_modes) || !port.vlan_modes.includes(option)}>{option}</option>{/each}
    </select></label>
    {#if mode !== 'trunk'}<label>VLAN tag<input inputmode="numeric" required bind:value={tag} placeholder="1–4094" /></label>{/if}
    {#if mode !== 'access'}<label>Trunk VLANs<input bind:value={trunks} placeholder="10, 20, 30" aria-describedby="trunks-help" /></label>
      <p id="trunks-help">An empty list permits all VLANs. This native OVS meaning is retained in the Diff.</p>{/if}
    <p>CVLANs and advanced or unknown native values are preserved as read-only in this slice.</p>
    <div class="actions"><button type="submit" class="primary">Stage in Candidate</button><Link href={`/ports/${port.management_id}`}>Cancel</Link></div>
  </fieldset>
  {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
</form>
