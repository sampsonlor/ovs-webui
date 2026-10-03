<script lang="ts">
  import { untrack } from 'svelte';
  import type { Interface } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import { controller } from './model';
  import { mtuEditReason, mtuNumber } from './policy';
  import { observation } from './interface-observation';
  import Link from './Link.svelte';
  let { item, model, desktop }: { item: Interface; model: Model; desktop: boolean } = $props();
  const original = untrack(() => item);
  let requested = $state(String(mtuNumber(original.fields?.mtu_request?.value) ?? ''));
  let error = $state('');
  const unavailable = $derived(mtuEditReason(item, model.session, desktop) ||
    (item.management_id !== original.management_id || item.ovs_uuid !== original.ovs_uuid || item.instance_generation !== original.instance_generation || item.config_revision !== original.config_revision || item.port_ref.id !== original.port_ref.id || item.bridge_ref?.id !== original.bridge_ref?.id
      ? 'Live configuration or attachment changed. Return to the Interface and review the current value.' : ''));
  async function save(event: SubmitEvent) {
    event.preventDefault();
    error = '';
    if (unavailable || model.busy || model.pending || !model.sessionReady) return;
    if (!/^[0-9]+$/.test(requested) || !Number.isSafeInteger(Number(requested)) || Number(requested) < 576 || Number(requested) > 65535) {
      error = 'Enter one MTU request from 576 to 65535 bytes.';
      return;
    }
    await controller.stageMTU(item, Number(requested));
  }
</script>

<div class="breadcrumbs"><Link href="/interfaces">Interfaces</Link><span>›</span><Link href={`/interfaces/${item.management_id}`}>{item.name}</Link><span>› MTU</span></div>
<header class="page-heading"><div><p class="eyebrow">Switching / Interface / MTU intent</p><h1>Edit MTU request</h1><p>Prepare a change for {item.name}. Review its Diff, then use Safe Apply.</p></div></header>
<div class="notice"><strong>Current native value</strong><p>Requested {observation(item, 'mtu_request')} bytes · observed {observation(item, 'mtu')} bytes.</p><p>The requested value is configuration; the observed value proves what the device has applied.</p></div>
{#if unavailable}<p class="notice warning" role="status">{unavailable}</p>{/if}
<form class="panel form-panel" onsubmit={save}>
  <fieldset disabled={!!unavailable || model.busy || !!model.pending || !model.sessionReady}>
    <legend>Explicit internal Interface MTU</legend>
    <label for="mtu-request">MTU request (bytes)</label><input id="mtu-request" inputmode="numeric" required bind:value={requested} aria-describedby="mtu-help" />
    <p id="mtu-help">576–65535 bytes. Device support is confirmed by the server after execution. Clearing the request is unavailable in this workflow.</p>
    <p>Changing MTU can disrupt traffic. Review the peer and management path. Rollback restores the captured explicit request and waits for the original device MTU.</p>
    <div class="actions"><button type="submit" class="primary">Stage in Candidate</button><Link href={`/interfaces/${item.management_id}`}>Cancel</Link></div>
  </fieldset>
  {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
</form>
