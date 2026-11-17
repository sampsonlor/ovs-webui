<script lang="ts">
  import { tick } from 'svelte';
  import type { QoSAttachIntent, QoSObservation, Resource } from '../../clients/typescript/public-v1.generated';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import { qosMessage } from './qos';

  let { item, model, desktop }: { item: (Resource & { native_qos: QoSObservation }) | null; model: Model; desktop: boolean } = $props();
  const status = $derived(item?.qos_attach);
  const candidate = $derived(model.workspace.value?.candidate);
  const authorized = $derived(has(model.session, 'configuration.read') && has(model.session, 'inventory.read') &&
    has(model.session, 'ovs.qos.write') && has(model.session, 'workspace.write') && status?.reason !== 'CAPABILITY_DENIED');
  const available = $derived(desktop && authorized && !!status?.available && !!status.revision && !!status.ports.length &&
    !!candidate && !candidate.intents.length && !candidate.consumed_by && candidate.state !== 'reconciliation-required' &&
    model.sessionReady && !model.busy && !model.pending);
  let reviewed = $state<string | null>(null);
  let portId = $state('');
  let targetSelect = $state<HTMLSelectElement>();
  let reviewButton = $state<HTMLButtonElement>();
  const target = $derived(status?.ports.find(({ port }) => port.management_id === portId));
  const changed = $derived(reviewed !== null && (reviewed !== status?.revision || !status.available));

  async function review() {
    if (!available || !status?.revision) return;
    portId = '';
    reviewed = status.revision;
    await tick();
    targetSelect?.focus();
  }

  async function cancel() {
    reviewed = null;
    await tick();
    reviewButton?.focus();
  }

  async function stage(event: SubmitEvent) {
    event.preventDefault();
    if (!available || !reviewed || changed || !target || !item ||
      typeof item.ovs_uuid !== 'string' || typeof item.instance_generation !== 'string') return;
    const intent: QoSAttachIntent = {
      intent_id: crypto.randomUUID(),
      operation: 'qos.attach',
      object: target.port,
      qos: { table: 'QoS', management_id: item.id, ovs_uuid: item.ovs_uuid, instance_generation: item.instance_generation },
    };
    await controller.stageNativeIntent(intent);
  }
</script>

{#if authorized && desktop && (status?.available || reviewed)}
  <section class="panel" aria-label="QoS attachment">
    <h3>Attach QoS</h3>
    {#if reviewed}
      <form class="form-panel" onsubmit={stage}>
        <p>Attaching applies this QoS's egress shaping, Queue rates and any configured DSCP marking to the selected Port. Review the management path before Safe Apply.</p>
        {#if changed}
          <p class="notice warning" role="status">The Port or QoS configuration changed. Reopen the review to select a current target.</p>
        {/if}
        <label for="qos-attach-port">Target Port</label>
        <select id="qos-attach-port" bind:this={targetSelect} bind:value={portId} required disabled={!available || changed}>
          <option value="">Select a Port</option>
          {#each status?.ports ?? [] as { port, name } (port.management_id)}
            <option value={port.management_id}>{name}</option>
          {/each}
        </select>
        <div class="actions">
          <button type="submit" class="primary" disabled={!available || changed || !target}>Stage QoS attachment</button>
          <button type="button" onclick={cancel}>Cancel</button>
        </div>
      </form>
    {:else}
      <button bind:this={reviewButton} onclick={review} disabled={!available}>Review QoS attachment</button>
      {#if candidate?.intents.length}<p>Review the current Candidate before staging an attachment.</p>{/if}
    {/if}
  </section>
{:else if authorized && status?.available && !desktop}
  <p class="notice">Use a desktop to stage QoS attachment.</p>
{:else if authorized && status && item?.native_qos.binding_state === 'unbound'}
  <p class="notice" role="status">
    {status.reason === 'EXCLUSIVE_QOS_AUTHORITY_REQUIRED'
      ? 'Attachment requires a separate administrator grant for a target Port and this QoS.'
      : qosMessage(status.reason, 'QoS attachment is unavailable for this configuration.')}
  </p>
{/if}
