<script lang="ts">
  import type { QoSDetachIntent, Resource } from '../../clients/typescript/public-v1.generated';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import { qosMessage } from './qos';

  let { item, model, desktop }: { item: Resource | null; model: Model; desktop: boolean } = $props();
  const status = $derived(item?.qos_detach);
  const candidate = $derived(model.workspace.value?.candidate);
  const authorized = $derived(has(model.session, 'configuration.read') && has(model.session, 'inventory.read') &&
    has(model.session, 'ovs.qos.write') && has(model.session, 'workspace.write') && status?.reason !== 'CAPABILITY_DENIED');
  const available = $derived(desktop && authorized && status?.available && status.revision && status.port &&
    candidate && !candidate.intents.length && !candidate.consumed_by && model.sessionReady && !model.busy && !model.pending);
  let reviewed = $state<string | null>(null);

  async function stage() {
    if (!available || !status?.port || reviewed !== status.revision || !item ||
      typeof item.ovs_uuid !== 'string' || typeof item.instance_generation !== 'string') return;
    const intent: QoSDetachIntent = {
      intent_id: crypto.randomUUID(),
      operation: 'qos.detach',
      object: status.port,
      qos: { table: 'QoS', management_id: item.id, ovs_uuid: item.ovs_uuid, instance_generation: item.instance_generation },
    };
    await controller.stageNativeIntent(intent);
  }
</script>

{#if authorized && desktop && (status?.available || reviewed)}
  <section class="panel" aria-label="QoS detachment">
    <h3>Detach QoS</h3>
    {#if reviewed}
      <p>Detaching removes egress shaping from the Port. The QoS and Queue objects remain in inventory.</p>
      {#if reviewed !== status?.revision}
        <p class="notice warning" role="status">Current binding evidence differs from this review. Review the current Port before staging.</p>
      {/if}
      <div class="actions">
        <button class="primary" onclick={stage} disabled={!available || reviewed !== status?.revision}>Stage QoS detachment</button>
        <button onclick={() => reviewed = null}>Cancel</button>
      </div>
    {:else}
      <button onclick={() => reviewed = status?.revision ?? null} disabled={!available}>Review QoS detachment</button>
      {#if candidate?.intents.length}<p>Review the current Candidate before staging a detachment.</p>{/if}
    {/if}
  </section>
{:else if status?.available && !desktop}
  <p class="notice">Use a desktop to stage QoS detachment.</p>
{:else if authorized && status && status.reason !== 'QOS_PORT_UNBOUND'}
  <p class="notice" role="status">
    {status.reason === 'EXCLUSIVE_QOS_AUTHORITY_REQUIRED'
      ? 'Detachment requires a separate administrator grant for this Port and QoS.'
      : qosMessage(status.reason)}
  </p>
{/if}
