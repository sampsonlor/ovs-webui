<script lang="ts">
  import type { QoSIntent, QoSObservation, Resource } from '../../clients/typescript/public-v1.generated';
  import { controller, type Model } from './model';

  let { item, model }: { item: Resource & { native_qos: QoSObservation }; model: Model } = $props();
  const change = $derived(item.qos_rate_edit);
  const candidate = $derived(model.workspace.value?.candidate);
  const existing = $derived(candidate?.intents.find(intent =>
    intent.operation === 'qos.configure' && intent.object.management_id === change?.port?.management_id));
  const available = $derived(!!change?.available && !!candidate && (!candidate.intents.length || !!existing) &&
    !candidate.consumed_by && candidate.state !== 'reconciliation-required');
  let opened = $state(false);
  let revision = $state<string | null>(null);
  let maximum = $state('');
  let queues = $state<{ id: number; min: string; max: string }[]>([]);
  let error = $state('');
  const limit = 4294967288;

  function edit() {
    const rates = existing?.qos_change?.after;
    maximum = rates ? String(rates.max_rate_bps) : item.native_qos.max_rate.bps ?? '';
    queues = rates ? rates.queues.map(q => ({ id: q.queue_id, min: String(q.min_rate_bps), max: String(q.max_rate_bps) })) :
      (item.native_qos.queues ?? []).map(q => ({ id: q.queue_id, min: q.min_rate.bps ?? '', max: q.max_rate.bps ?? '' }));
    revision = change?.revision ?? null;
    error = '';
    opened = true;
  }
  function readRate(raw: string) {
    const value = Number(raw);
    return /^[1-9][0-9]*$/.test(raw) && Number.isSafeInteger(value) && value % 8 === 0 && value <= limit ? value : 0;
  }
  async function submit(event: SubmitEvent) {
    event.preventDefault();
    const port = change?.port;
    const type = item.native_qos.discipline;
    if (!available || !port || revision !== change?.revision || model.busy || model.pending ||
      !model.sessionReady || type !== 'linux-htb' && type !== 'linux-hfsc') return;
    const max_rate_bps = readRate(maximum);
    const queueRates = queues.map(q => ({ queue_id: q.id, min_rate_bps: readRate(q.min), max_rate_bps: readRate(q.max) }));
    if (!max_rate_bps || queueRates.some(q => !q.min_rate_bps || q.min_rate_bps > q.max_rate_bps || q.max_rate_bps > max_rate_bps)) {
      error = 'Enter positive multiples of 8. Each Queue minimum must be at most its maximum, within the QoS maximum.';
      return;
    }
    error = '';
    const intent: QoSIntent = {
      intent_id: existing?.intent_id ?? crypto.randomUUID(), operation: 'qos.configure',
      object: port, type, max_rate_bps, queues: queueRates,
    };
    await controller.stageNativeIntent(intent);
  }
</script>

<section class="panel" aria-label="QoS rate change">
  <h3>QoS rate change</h3>
  {#if !opened}
    <button onclick={edit} disabled={!available || model.busy || !!model.pending}>Edit QoS rates</button>
    {#if !available}<p>Review the current Candidate before staging a QoS change.</p>{/if}
  {:else}
    <form class="form-panel" onsubmit={submit}>
      <p>Enter rates in bit/s as multiples of 8, up to {limit}. Review the change in Candidate before Safe Apply.</p>
      {#if change?.revision !== revision}<p class="notice warning" role="status">The QoS configuration changed. Reopen the editor to review the current rates.</p>{/if}
      <fieldset disabled={!available || change?.revision !== revision || model.busy || !!model.pending || !model.sessionReady}>
        <legend>{item.native_qos.discipline} rates</legend>
        <label>QoS maximum rate (bit/s) <input inputmode="numeric" maxlength="10" bind:value={maximum} required /></label>
        {#each queues as queue (queue.id)}
          <fieldset>
            <legend>Queue {queue.id}</legend>
            <label>Queue {queue.id} minimum rate (bit/s) <input inputmode="numeric" maxlength="10" bind:value={queue.min} required /></label>
            <label>Queue {queue.id} maximum rate (bit/s) <input inputmode="numeric" maxlength="10" bind:value={queue.max} required /></label>
          </fieldset>
        {/each}
        <button type="submit" class="primary">Stage in Candidate</button>
      </fieldset>
      <button type="button" onclick={() => opened = false}>Close editor</button>
      {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
    </form>
  {/if}
</section>
