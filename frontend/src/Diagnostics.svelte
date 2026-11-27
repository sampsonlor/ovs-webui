<script lang="ts">
  import type { LinkLACPDiagnosticCommand } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LinkLACPResult from './LinkLACPResult.svelte';
  import InterfaceCounterResult from './InterfaceCounterResult.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import { controller, navigate, type Model } from './model';
  import { has } from './policy';
  import { healthRoute } from './management-observation';
  import { supportBundleRoute } from './support-bundles';
  import { capturePermission, captureRoute } from './packet-capture';
  import {
    canObserveDiagnostics,
    canSampleInterfaceCounters,
    diagnosticResultID,
    diagnosticsRoute,
    diagnosticTargetQuery,
    diagnosticTargets,
    diagnosticTemplate,
    type DiagnosticTargetKind,
    type DiagnosticTemplate,
  } from './diagnostics';

  let { model, expert, mobile }: { model: Model; expert: boolean; mobile: boolean } = $props();
  let selectedID = $state('');
  let duration = $state<LinkLACPDiagnosticCommand['sample_seconds']>(5);
  let portLimit = $state(10);
  const template = $derived(diagnosticTemplate(model.query, model.session));
  const counters = $derived(template === 'interface-counters');
  const scope = $derived(diagnosticTargetQuery(model.query, template));
  const resultID = $derived(diagnosticResultID(model.query));
  const mayObserve = $derived(model.sessionReady && canObserveDiagnostics(model.session));
  const maySample = $derived(model.sessionReady && canSampleInterfaceCounters(model.session));
  const allowed = $derived(counters ? maySample : mayObserve);
  const targets = $derived(model.resource.status === 'ready' && allowed
    ? diagnosticTargets(model.resource.value, scope.kind)
    : null);
  const selected = $derived(targets?.find(({ ref }) => ref.id === selectedID));
  const next = $derived(typeof model.resource.value?.next_cursor === 'string' ? model.resource.value.next_cursor : '');

  function targetPage(kind: DiagnosticTargetKind, filter: string, cursor = '', selectedTemplate: DiagnosticTemplate = template) {
    const query = new URLSearchParams({ kind });
    if (selectedTemplate === 'interface-counters') query.set('template', selectedTemplate);
    if (filter) query.set('filter', filter);
    if (cursor) query.set('cursor', cursor);
    return `${diagnosticsRoute}?${query}`;
  }

  function filterTargets(event: SubmitEvent) {
    event.preventDefault();
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    navigate(targetPage(counters || data.get('kind') === 'port' ? 'port' : 'bridge', String(data.get('filter') ?? '').trim()));
  }

  function chooseTemplate(event: Event) {
    const choice = (event.currentTarget as HTMLSelectElement).value;
    if (choice === 'interface-counters' && maySample)
      navigate(targetPage('port', scope.kind === 'port' ? scope.filter : '', '', choice));
    else if (choice === 'link-lacp' && mayObserve)
      navigate(targetPage(scope.kind, scope.filter, '', choice));
  }

  function run(event: SubmitEvent) {
    event.preventDefault();
    if (!selected || !allowed || mobile) return;
    if (counters) void controller.runInterfaceCounters(selected.ref, duration);
    else void controller.runLinkLACP(selected.ref, duration, portLimit);
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Operations</p>
    <h1>Diagnostics</h1>
    {#if !resultID}
      <p>{counters ? 'Observe Interface counter changes on a managed Port.' : 'Observe link state and LACP on a managed Bridge or Port.'}</p>
    {/if}
  </div>
  {#if allowed}<button onclick={() => controller.refresh()}>{resultID ? 'Refresh result' : 'Refresh targets'}</button>{/if}
</header>

{#if !resultID && (mayObserve && maySample || !allowed && (mayObserve || maySample))}
  <label for="diagnostic-template">Diagnostic template</label>
  <select id="diagnostic-template" value={template} onchange={chooseTemplate}>
    {#if mayObserve}<option value="link-lacp">Link and LACP</option>{/if}
    {#if maySample}<option value="interface-counters">Interface counters</option>{/if}
  </select>
{/if}

{#if allowed && resultID}
  {#if counters}
    <InterfaceCounterResult load={model.interfaceCounterResult} {expert} />
  {:else}
    <LinkLACPResult load={model.linkLACPResult} {expert} />
  {/if}
{:else if allowed}
  <section class="panel" aria-labelledby="diagnostic-template-heading">
    <h2 id="diagnostic-template-heading">{counters ? 'Interface counters' : 'Link and LACP'}</h2>
    {#if counters}
      <p>This Active Job compares two adjacent shared OVSDB samples for up to four Interfaces on the selected Port.</p>
    {:else}
      <p>This read-only Job samples the current OVSDB observation within the selected time budget.</p>
    {/if}
    {#if mobile}
      <p>Use a tablet or desktop to start a diagnostic.</p>
    {:else}
      <form class="actions" onsubmit={filterTargets} aria-label="Filter diagnostic targets">
        {#if !counters}
          <label>Target type
            <select name="kind" value={scope.kind}>
              <option value="bridge">Bridge</option>
              <option value="port">Port</option>
            </select>
          </label>
        {/if}
        <label>Target name <input name="filter" maxlength="128" value={scope.filter} /></label>
        <button>Find targets</button>
      </form>
      <LoadNotice load={model.resource} />
      {#if model.resource.status === 'ready' && !targets}
        <p class="notice warning" role="status">Current inventory evidence is unavailable. Refresh targets before starting a diagnostic.</p>
      {:else if targets && !targets.length}
        <p role="status">No authorized {scope.kind === 'bridge' ? 'Bridges' : 'Ports'} match this page.</p>
      {/if}
      {#if scope.cursor || next}
        <div class="actions">
          {#if scope.cursor}<Link href={targetPage(scope.kind, scope.filter)}>First target page</Link>{/if}
          {#if targets && next}<Link href={targetPage(scope.kind, scope.filter, next)}>Next target page</Link>{/if}
        </div>
      {/if}
      {#if targets?.length}
        <form onsubmit={run} aria-label={counters ? 'Run Interface counter diagnostic' : 'Run Link and LACP diagnostic'}>
          <label for="diagnostic-target">{scope.kind === 'bridge' ? 'Bridge' : 'Port'}</label>
          <select id="diagnostic-target" bind:value={selectedID} required>
            <option value="">Select a target</option>
            {#each targets as target (target.ref.id)}
              <option value={target.ref.id}>{target.name}</option>
            {/each}
          </select>
          {#if expert && selected}<p class="mono">{selected.ref.id}</p>{/if}
          <label for="diagnostic-duration">{counters ? 'Sample budget' : 'Observation budget'}</label>
          <select id="diagnostic-duration" bind:value={duration}>
            <option value={5}>5 seconds</option>
            <option value={10}>10 seconds</option>
            <option value={15}>15 seconds</option>
          </select>
          {#if !counters}
            <label for="diagnostic-port-limit">Maximum ports</label>
            <select id="diagnostic-port-limit" bind:value={portLimit}>
              {#each [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as limit}<option value={limit}>{limit}</option>{/each}
            </select>
          {/if}
          <button class="primary" disabled={!selected || model.busy || !!model.pending}>Run diagnostic</button>
        </form>
      {/if}
    {/if}
  </section>
  <nav class="actions" aria-label="Diagnostic evidence">
    {#if has(model.session, 'jobs.read')}<Link href="/operations/jobs">Review Jobs</Link>{/if}
    {#if has(model.session, 'state.read')}<Link href={healthRoute}>Review System Health</Link>{/if}
    {#if has(model.session, 'support_bundle.create')}<Link href={supportBundleRoute}>Support Bundle</Link>{/if}
    {#if capturePermission(model.session)}<Link href={captureRoute}>Packet Capture</Link>{/if}
  </nav>
{:else}
  <p class="notice warning" role="status">Current permissions or connection do not allow this diagnostic.{#if resultID} <Link href={diagnosticsRoute}>Back to Diagnostics</Link>{/if}</p>
{/if}
