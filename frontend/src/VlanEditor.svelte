<script lang="ts">
  import { untrack } from 'svelte';
  import type { Model } from './model';
  import { controller } from './model';
  import type { Port, VlanInput } from '../../clients/typescript/public-v1.generated';
  import { editReason, standardModes, vlanModes, vlanNumbers, vlanText } from './policy';
  import Link from './Link.svelte';
  let { port, model, desktop }: { port: Port; model: Model; desktop: boolean } = $props();
  const original = untrack(() => port);
  let mode = $state(original.vlan.native?.vlan_mode ?? '');
  let tag = $state(String(original.vlan.native?.tag ?? ''));
  let trunks = $state(original.vlan.native?.trunks.join(', ') ?? '');
  let cvlans = $state(original.vlan.native?.cvlans.join(', ') ?? '');
  let error = $state('');
  const reason = $derived(editReason(port, model.session, desktop));
  const changed = $derived(original.config_revision !== port.config_revision);
  const unavailable = $derived(
    reason ||
      (changed
        ? 'Live configuration changed while this form was open. Return to the Port and review the new value.'
        : ''),
  );
  async function save(event: SubmitEvent) {
    event.preventDefault();
    error = '';
    if (unavailable || model.busy || model.pending || !model.sessionReady) return;
    try {
      if (!vlanModes.some((m) => m === mode) || (mode === 'dot1q-tunnel' && port.qinq_editable !== true))
        throw new Error('Select a supported VLAN mode.');
      const ids = mode === 'access' || mode === 'dot1q-tunnel' ? [] : vlanNumbers(trunks);
      const tagNumber = mode === 'trunk' ? null : vlanNumbers(tag);
      if (tagNumber !== null && tagNumber.length !== 1)
        throw new Error('Enter one VLAN tag from 1 to 4094.');
      const value = {
        vlan_mode: mode,
        tag: tagNumber?.[0] ?? null,
        trunks: ids,
        cvlans: mode === 'dot1q-tunnel' ? vlanNumbers(cvlans) : [],
      } as VlanInput;
      await controller.stage(port, value);
    } catch (e) {
      error = e instanceof Error ? e.message : 'Unable to stage VLAN intent.';
    }
  }
</script>

<div class="breadcrumbs">
  <Link href="/ports">Ports</Link><span>›</span><Link
    href={`/ports/${port.management_id}`}>{port.name}</Link
  ><span>› VLAN</span>
</div>
<header class="page-heading">
  <div>
    <p class="eyebrow">Switching / Port / VLAN intent</p>
    <h1>Edit VLAN</h1>
    <p>
      Prepare a change for {port.name}. Live configuration changes only after validation
      and Safe Apply.
    </p>
  </div>
</header>
<div class="notice">
  <strong>Current native value</strong>
  <p>{vlanText(port.vlan.native)}</p>
</div>
{#if unavailable}<div class="notice warning" role="status">{unavailable}</div>{/if}
<form class="panel form-panel" onsubmit={save}>
  <fieldset
    disabled={!!unavailable || model.busy || !!model.pending || !model.sessionReady}
  >
    <legend>VLAN configuration</legend>
    <label
      >VLAN mode<select bind:value={mode}>
        {#if !vlanModes.some((m) => m === mode)}<option value={mode}
            >{mode || 'Native default (preserved)'}</option
          >{/if}
        {#each standardModes as option}<option
            value={option}
            disabled={!Array.isArray(port.vlan_modes) ||
              !port.vlan_modes.includes(option)}>{option}</option
          >{/each}
        <optgroup label="Advanced"><option value="dot1q-tunnel" disabled={port.qinq_editable !== true}>dot1q-tunnel (QinQ)</option></optgroup>
      </select></label
    >
    {#if mode !== 'trunk'}<label
        >{mode === 'dot1q-tunnel' ? 'Service VLAN tag' : 'VLAN tag'}<input
          inputmode="numeric"
          required
          bind:value={tag}
          placeholder="1–4094"
        /></label
      >{/if}
    {#if mode !== 'access' && mode !== 'dot1q-tunnel'}<label
        >Trunk VLANs<input
          bind:value={trunks}
          placeholder="10, 20, 30"
          aria-describedby="trunks-help"
        /></label
      >
      <p id="trunks-help">
        An empty list permits all VLANs. This native OVS meaning is retained in the Diff.
      </p>{/if}
    {#if mode === 'dot1q-tunnel'}
      <label>Customer VLANs<input bind:value={cvlans} placeholder="10, 20, 30" aria-describedby="cvlans-help" /></label>
      <p id="cvlans-help">An empty list permits all customer VLANs. The service VLAN is the outer tag; customer VLANs are the inner tags.</p>
      <p>Service TPID: {port.qinq_ethertype ?? '802.1ad (native default)'}. This setting is preserved. Review the peer configuration and management path before applying.</p>
    {:else if original.vlan.native?.vlan_mode === 'dot1q-tunnel'}
      <p class="notice warning">Leaving QinQ clears the customer VLAN list. The Diff shows the original list and rollback restores it.</p>
    {/if}
    {#if port.qinq_editable !== true}<p>QinQ requires a supported native schema, a known TPID, verified two-tag parsing and an eligible locally managed single-interface system Port.</p>{/if}
    <div class="actions">
      <button type="submit" class="primary">Stage in Candidate</button><Link
        href={`/ports/${port.management_id}`}>Cancel</Link
      >
    </div>
  </fieldset>
  {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
</form>
