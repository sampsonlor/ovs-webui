<script lang="ts">
  import { tick } from 'svelte';
  import type {
    TelemetryBridgeBinding,
    TelemetryExporterClearIntent,
    TelemetryExporterSetIntent,
    TelemetryExporterWriteStatus,
  } from '../../clients/typescript/public-v1.generated';
  import { controller, type Model } from './model';
  import {
    collectorTargetKey,
    telemetryLabels,
    telemetryMessage,
    telemetrySettingLabels,
    telemetrySettings,
    type TelemetryProtocol,
  } from './telemetry';

  type Setting = keyof typeof telemetrySettingLabels;
  type Draft = { mode: 'configure' | 'disable'; revision: string };
  type Review = { targets: string[]; settings: Partial<Record<Setting, number | null>> };

  let {
    protocol,
    object,
    status,
    model,
  }: { protocol: TelemetryProtocol; object: TelemetryBridgeBinding; status: TelemetryExporterWriteStatus; model: Model } = $props();

  const candidate = $derived(model.workspace.value?.candidate);
  const available = $derived(status.available && !!status.revision && model.resource.status === 'ready' &&
    model.sessionReady && !model.busy && !model.pending && !!candidate && !candidate.intents.length &&
    !candidate.consumed_by && candidate.state !== 'reconciliation-required');

  let draft = $state<Draft | null>(null);
  let review = $state<Review | null>(null);
  let targets = $state('');
  let values = $state<Partial<Record<Setting, string>>>({});
  let error = $state('');
  let configureButton = $state<HTMLButtonElement>();
  let disableButton = $state<HTMLButtonElement>();
  let targetsField = $state<HTMLTextAreaElement>();
  let stageButton = $state<HTMLButtonElement>();
  const changed = $derived(!!draft && (!status.available || status.revision !== draft.revision));

  async function open(mode: Draft['mode']) {
    if (!available || !status.revision || (mode === 'disable' && !status.configured)) return;
    targets = status.targets?.join('\n') ?? '';
    values = Object.fromEntries(telemetrySettings[protocol].map(({ key }) => [key, String(status.settings[key] ?? '')]));
    review = null;
    error = '';
    draft = { mode, revision: status.revision };
    await tick();
    if (mode === 'configure') targetsField?.focus();
    else stageButton?.focus();
  }

  async function cancel() {
    const mode = draft?.mode;
    draft = null;
    review = null;
    await tick();
    if (mode === 'disable') disableButton?.focus();
    else configureButton?.focus();
  }

  async function prepare(event: SubmitEvent) {
    event.preventDefault();
    if (!available || changed || draft?.mode !== 'configure') return;
    error = '';
    const lines = targets.split('\n').map(target => target.trim()).filter(Boolean);
    const normalized = lines.map(collectorTargetKey);
    if (lines.length < 1 || lines.length > 16 || normalized.includes(null)) {
      error = 'Enter 1 to 16 numeric unicast IP addresses with UDP ports, one per line.';
      targetsField?.focus();
      return;
    }
    if (new Set(normalized).size !== normalized.length) {
      error = 'Each collector target must be unique.';
      targetsField?.focus();
      return;
    }
    const settings: Review['settings'] = {};
    for (const { key, min, max, optional } of telemetrySettings[protocol]) {
      const next = values[key]?.trim() ?? '';
      const previous = status.settings[key] ?? null;
      if (next === '') {
        if (previous !== null && optional) settings[key] = null;
        continue;
      }
      const value = Number(next);
      if (value === previous) continue;
      if (!/^-?\d+$/.test(next) || !Number.isInteger(value) || value < min || value > max) {
        error = `${telemetrySettingLabels[key]} must be a whole number from ${min} to ${max}.`;
        return;
      }
      settings[key] = value;
    }
    const original = new Set((status.targets ?? []).map(collectorTargetKey));
    if (status.configured && !Object.keys(settings).length && original.size === normalized.length &&
      normalized.every(target => original.has(target))) {
      error = 'Change a collector target or setting to continue.';
      return;
    }
    review = { targets: lines, settings };
    await tick();
    stageButton?.focus();
  }

  async function edit() {
    review = null;
    await tick();
    targetsField?.focus();
  }

  function setIntent(planned: Review): TelemetryExporterSetIntent {
    const base = { intent_id: crypto.randomUUID(), operation: 'telemetry.exporter.set' as const, object, targets: planned.targets };
    const { sampling, polling, active_timeout, obs_domain_id, obs_point_id } = planned.settings;
    switch (protocol) {
      case 'sflow':
        return { ...base, protocol, settings: { sampling, polling } };
      case 'netflow':
        return { ...base, protocol, settings: { active_timeout: active_timeout ?? undefined } };
      case 'ipfix':
        return { ...base, protocol, settings: { sampling, obs_domain_id, obs_point_id } };
    }
  }

  async function stage(event: SubmitEvent) {
    event.preventDefault();
    if (!available || changed || !draft) return;
    if (draft.mode === 'disable' && status.configured) {
      const intent: TelemetryExporterClearIntent = {
        intent_id: crypto.randomUUID(), operation: 'telemetry.exporter.clear', object, protocol,
      };
      await controller.stageNativeIntent(intent);
    } else if (draft.mode === 'configure' && review) {
      await controller.stageNativeIntent(setIntent(review));
    }
  }
</script>

{#if draft}
  {#if changed}
    <p class="notice warning" role="status">The exporter configuration or authority changed. Reopen the editor to review the current configuration.</p>
    <button onclick={cancel}>Cancel</button>
  {:else if draft.mode === 'disable'}
    <form class="form-panel" onsubmit={stage}>
      <p>Disabling {telemetryLabels[protocol]} stops this Bridge from sending to its current collector destinations. Safe Apply can restore the exporter configuration.</p>
      <div class="actions">
        <button class="primary" type="submit" bind:this={stageButton} disabled={!available}>Stage {telemetryLabels[protocol]} disablement</button>
        <button type="button" onclick={cancel}>Cancel</button>
      </div>
    </form>
  {:else if review}
    <form class="form-panel" onsubmit={stage}>
      <h4>Review {telemetryLabels[protocol]} configuration</h4>
      <p>Flow records or samples go to these UDP destinations. Export traffic and switch load may change.</p>
      <ul class="review-targets">
        {#each review.targets as target (target)}<li class="mono">{target}</li>{/each}
      </ul>
      {#if Object.keys(review.settings).length}
        <dl>
          {#each telemetrySettings[protocol] as { key } (key)}
            {#if key in review.settings}
              <dt>{telemetrySettingLabels[key]}</dt>
              <dd>{review.settings[key] ?? 'Unset native value'}</dd>
            {/if}
          {/each}
        </dl>
      {/if}
      <div class="actions">
        <button class="primary" type="submit" bind:this={stageButton} disabled={!available}>Stage {telemetryLabels[protocol]} configuration</button>
        <button type="button" onclick={edit}>Back to edit</button>
        <button type="button" onclick={cancel}>Cancel</button>
      </div>
    </form>
  {:else}
    <form class="form-panel" onsubmit={prepare}>
      <label for={`${protocol}-collector-targets`}>Collector targets</label>
      <textarea
        id={`${protocol}-collector-targets`}
        aria-describedby={`${protocol}-collector-hint`}
        bind:this={targetsField}
        bind:value={targets}
        maxlength="4096"
        rows="3"
        spellcheck="false"
      ></textarea>
      <p id={`${protocol}-collector-hint`} class="muted">One IPv4:port or [IPv6]:port per line.</p>
      {#each telemetrySettings[protocol] as { key, optional } (key)}
        <label for={`${protocol}-${key}`}>{telemetrySettingLabels[key]}</label>
        <input id={`${protocol}-${key}`} inputmode="numeric" maxlength="16" bind:value={values[key]} />
        <p class="muted">{optional ? 'Clear to unset the native value.' : 'Leave blank to keep the native value.'}</p>
      {/each}
      {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
      <div class="actions">
        <button class="primary" type="submit" disabled={!available}>Review changes</button>
        <button type="button" onclick={cancel}>Cancel</button>
      </div>
    </form>
  {/if}
{:else if status.available}
  <div class="actions editor-actions">
    <button bind:this={configureButton} onclick={() => open('configure')} disabled={!available}>Configure {telemetryLabels[protocol]}</button>
    {#if status.configured}
      <button bind:this={disableButton} onclick={() => open('disable')} disabled={!available}>Disable {telemetryLabels[protocol]}</button>
    {/if}
  </div>
{:else}
  <p class="muted">{telemetryMessage(status.reason, 'Protected exporter configuration is unavailable.')}</p>
{/if}

<style>
  .form-panel, .editor-actions { margin-top: .8rem; }
  .form-panel { padding-top: .65rem; }
  .form-panel h4 { margin: 0; }
  .form-panel label { display: block; }
  .form-panel input, .form-panel textarea { max-width: 100%; width: 100%; }
  .form-panel textarea, .review-targets { overflow-wrap: anywhere; }
  .form-panel .muted { margin-top: .25rem; }
  .review-targets { padding-inline-start: 1.25rem; }
</style>
