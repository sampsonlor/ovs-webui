<script lang="ts">
  import { tick } from 'svelte';
  import type { QoSCreateIntent, QoSCreateStatus } from '../../clients/typescript/public-v1.generated';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import { parseQoSRate, qosMessage, qosRateLimit } from './qos';

  let { status, model, desktop }: { status: QoSCreateStatus | undefined; model: Model; desktop: boolean } = $props();
  const candidate = $derived(model.workspace.value?.candidate);
  const authorized = $derived(has(model.session, 'inventory.read') && has(model.session, 'configuration.read') &&
    has(model.session, 'workspace.write') && has(model.session, 'ovs.qos.write') && status?.reason !== 'CAPABILITY_DENIED');
  const available = $derived(model.resource.status === 'ready' && status?.available === true &&
    !!status.revision && !!status.instance_generation && status.aliases.length > 0);
  const canStage = $derived(desktop && authorized && available && model.sessionReady && !model.busy && !model.pending &&
    !!candidate && !candidate.intents.length && !candidate.consumed_by && candidate.state !== 'reconciliation-required');

  type QueueDraft = { key: number; number: string; min: string; max: string };
  const maxQueueNumber = 61439;
  const maxQueues = 32;
  let nextKey = 0;
  let reviewed = $state<string | null>(null);
  let profile = $state('');
  let maximum = $state('');
  let queues = $state<QueueDraft[]>([]);
  let error = $state('');
  let openButton = $state<HTMLButtonElement>();
  let profileSelect = $state<HTMLSelectElement>();
  let addButton = $state<HTMLButtonElement>();
  const changed = $derived(reviewed !== null && (!available || status?.revision !== reviewed));

  function newQueue(): QueueDraft {
    return { key: nextKey++, number: '', min: '', max: '' };
  }

  async function open() {
    if (!canStage || !status?.revision) return;
    reviewed = status.revision;
    profile = '';
    maximum = '';
    queues = [newQueue()];
    error = '';
    await tick();
    profileSelect?.focus();
  }

  async function cancel() {
    reviewed = null;
    await tick();
    openButton?.focus();
  }

  async function addQueue() {
    if (queues.length >= maxQueues) return;
    const queue = newQueue();
    queues = [...queues, queue];
    error = '';
    await tick();
    document.getElementById(`qos-create-queue-${queue.key}`)?.focus();
  }

  async function removeQueue(key: number) {
    if (queues.length <= 1) return;
    queues = queues.filter(queue => queue.key !== key);
    error = '';
    await tick();
    addButton?.focus();
  }

  async function stage(event: SubmitEvent) {
    event.preventDefault();
    if (!canStage || !reviewed || changed || !status?.instance_generation || !status.aliases.includes(profile)) return;
    const max_rate_bps = parseQoSRate(maximum);
    const queueRates = queues.map(q => ({
      queue_id: /^(0|[1-9][0-9]*)$/.test(q.number) ? Number(q.number) : -1,
      min_rate_bps: parseQoSRate(q.min),
      max_rate_bps: parseQoSRate(q.max),
    }));
    if (queueRates.some(q => q.queue_id < 0 || q.queue_id > maxQueueNumber) ||
      new Set(queueRates.map(q => q.queue_id)).size !== queueRates.length) {
      error = 'Enter a different Queue number from 0 to 61439 for each Queue.';
      return;
    }
    if (queueRates.length === maxQueues && !queueRates.some(queue => queue.queue_id === 0)) {
      error = 'Include Queue 0 when configuring 32 Queues, or configure at most 31 Queues.';
      return;
    }
    if (max_rate_bps < 12000 || queueRates.some(q => !q.min_rate_bps || q.min_rate_bps > q.max_rate_bps || q.max_rate_bps > max_rate_bps)) {
      error = 'Use a QoS maximum of at least 12000 bit/s. Rates must be positive multiples of 8. Each Queue minimum must be at most its maximum, within the QoS maximum.';
      return;
    }
    error = '';
    const intent: QoSCreateIntent = {
      intent_id: crypto.randomUUID(), operation: 'qos.create', instance_generation: status.instance_generation, name: profile, type: 'linux-hfsc',
      max_rate_bps, queues: queueRates,
    };
    await controller.stageNativeIntent(intent);
  }
</script>

{#if authorized && desktop && (available || reviewed)}
  <section class="panel" aria-label="QoS creation">
    <h2>Create an HFSC QoS profile</h2>
    {#if reviewed}
      <form class="form-panel" onsubmit={stage} oninput={() => error = ''}>
        <p>Save an unbound Linux HFSC profile with dedicated Queues. Egress shaping begins after a separate Port binding.</p>
        {#if changed}
          <p class="notice warning" role="status">The available profile names changed or could not be rechecked. Reopen the form to review the latest options.</p>
        {/if}
        <fieldset disabled={!canStage || changed}>
          <label for="qos-create-name">Profile name</label>
          <select id="qos-create-name" required bind:this={profileSelect} bind:value={profile}>
            <option value="">Select an authorized name</option>
            {#each status?.aliases ?? [] as alias (alias)}<option value={alias}>{alias}</option>{/each}
          </select>
          <label>QoS maximum rate (bit/s) <input inputmode="numeric" maxlength="10" bind:value={maximum} required /></label>
          <p>Enter rates in multiples of 8, up to {qosRateLimit.toLocaleString('en-US')} bit/s. The profile maximum must be at least 12,000 bit/s.</p>
          {#each queues as queue, i (queue.key)}
            <fieldset>
              <legend>Queue {i + 1}</legend>
              <label for={`qos-create-queue-${queue.key}`}>Queue number</label>
              <input id={`qos-create-queue-${queue.key}`} inputmode="numeric" maxlength="5" bind:value={queue.number} required />
              <label>Minimum rate (bit/s) <input inputmode="numeric" maxlength="10" bind:value={queue.min} required /></label>
              <label>Maximum rate (bit/s) <input inputmode="numeric" maxlength="10" bind:value={queue.max} required /></label>
              {#if queues.length > 1}<button type="button" onclick={() => removeQueue(queue.key)}>Remove Queue {i + 1}</button>{/if}
            </fieldset>
          {/each}
          <div class="actions">
            <button type="button" bind:this={addButton} onclick={addQueue} disabled={queues.length >= maxQueues}>Add Queue</button>
            <button type="submit" class="primary">Stage QoS creation</button>
          </div>
        </fieldset>
        <button type="button" onclick={cancel}>Cancel</button>
        {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
      </form>
    {:else}
      <button bind:this={openButton} onclick={open} disabled={!canStage}>Create QoS profile</button>
      {#if candidate?.intents.length}<p>Review the current Candidate before preparing a new profile.</p>{/if}
    {/if}
  </section>
{:else if authorized && available && !desktop}
  <p class="notice">Use a desktop to stage QoS creation.</p>
{:else if authorized && status && !status.available}
  <p class="notice" role="status">{qosMessage(status.reason, 'QoS creation is unavailable for this observation.')}</p>
{/if}
