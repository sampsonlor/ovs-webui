<script lang="ts">
  import type { ManagementServiceRestartCommand, Resource } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import { canObserveDiagnostics, diagnosticsRoute } from './diagnostics';
  import {
    capabilitiesRoute,
    componentName,
    observationReason,
  } from './management-observation';

  let { model, expert, desktop }: { model: Model; expert: boolean; desktop: boolean } = $props();
  let password = $state('');
  let duration = $state<300 | 600 | 900>(300);
  let service = $state<ManagementServiceRestartCommand['service']>('web');
  let armed = $state(false);
  let elevated = $state(false);
  const health = $derived(
    model.resource.status === 'ready'
      ? (model.resource.value as Resource | null)
      : null,
  );
  const components = $derived(health?.components ?? []);
  const debug = $derived(components.find((item) => item.key === 'management-debug'));
  const restart = $derived(components.find((item) => item.key === 'management-restart'));
  const mayDebug = $derived(desktop && has(model.session, 'management.debug.write'));
  const mayRestart = $derived(desktop && has(model.session, 'management.services.control'));
  $effect(() => {
    const expiresAt = model.session?.elevated_until;
    const remaining = typeof expiresAt === 'string' ? Date.parse(expiresAt) - Date.now() : 0;
    const current = remaining > 0;
    elevated = current;
    if (!current) return;
    const timer = window.setTimeout(() => (elevated = false), remaining);
    return () => window.clearTimeout(timer);
  });
  const blocked = $derived(!model.sessionReady || model.busy || !!model.pending);
  const routes = [
    { label: 'Jobs', href: '/operations/jobs', permission: 'jobs.read' },
    { label: 'Events', href: '/operations/events', permission: 'events.read' },
    { label: 'Audit', href: '/operations/audit', permission: 'audit.read' },
    { label: 'Transactions', href: '/changes/transactions', permission: 'configuration.read' },
    { label: 'Capabilities', href: capabilitiesRoute, permission: 'capabilities.read' },
  ];
  const evidenceLinks = $derived(
    routes.filter((route) => has(model.session, route.permission)),
  );
  async function authorize(event: SubmitEvent) {
    event.preventDefault();
    const secret = password;
    password = '';
    await controller.elevate(secret);
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Operations</p>
    <h1>System Health</h1>
    <p>Inspect the management services and their observation sources.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh health</button>
</header>
<LoadNotice load={model.resource} />

{#if health}
  <section class="panel" aria-labelledby="management-health">
    <h2 id="management-health">Management readiness</h2>
    <dl>
      <dt>Health state</dt><dd><Status value={health.state} /></dd>
      <dt>Observation freshness</dt><dd><Status value={health.source.freshness} /></dd>
      <dt>Authentication readiness</dt>
      <dd>{health.authentication_ready === true ? 'Ready' : 'Readiness not established'}</dd>
      <dt>Configuration readiness</dt>
      <dd>{health.configuration_ready === true ? 'Ready for validation' : 'Readiness not established'}</dd>
      {#if expert}
        <dt>Provider</dt><dd>{health.source.provider_id}</dd>
        <dt>Authority</dt><dd>{health.source.authority}</dd>
        <dt>Confidence</dt><dd>{health.source.confidence}</dd>
        <dt>Observed</dt>
        <dd>
          {#if health.source.observed_at}
            <time datetime={health.source.observed_at}>{health.source.observed_at}</time>
          {:else}Not sampled{/if}
        </dd>
      {/if}
    </dl>
  </section>

  <section class="panel">
    <h2 id="health-components-heading">Management components</h2>
    {#if !components.length}
      <p>Current component observations are unavailable.</p>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll component observations.) -->
      <div class="table-scroll" role="region" aria-labelledby="health-components-heading" tabindex="0">
        <table>
          <thead>
            <tr>
              <th>Component</th><th>State</th><th>Observation</th>
              {#if expert}<th>Source</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each components as component (component.key)}
              <tr>
                <th scope="row">{componentName(component.key)}{#if expert}<small>{component.key}</small>{/if}</th>
                <td><Status value={component.state} /></td>
                <td>
                  {#if component.reason}{observationReason(component.reason)}{/if}
                  {#if component.active !== undefined || component.capacity !== undefined}
                    <div class="activity">
                      {#if component.active !== undefined}<span>{component.active} active</span>{/if}
                      {#if component.capacity !== undefined}<span>{component.capacity} capacity</span>{/if}
                    </div>
                  {/if}
                  {#if expert && component.reason}<small>{component.reason}</small>{/if}
                </td>
                {#if expert}
                  <td>
                    {component.source.provider_id}
                    <small>{component.source.authority}</small>
                    <small>{component.source.freshness} / {component.source.confidence}</small>
                    {#if component.source.observed_at}
                      <small><time datetime={component.source.observed_at}>{component.source.observed_at}</time></small>
                    {:else}<small>Not sampled</small>{/if}
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  {#if mayDebug || mayRestart}
    <section class="panel" aria-labelledby="management-controls-heading">
      <h2 id="management-controls-heading">Management service controls</h2>
      {#if !elevated}
        <form onsubmit={authorize}>
          <label for="management-control-password">Confirm your password</label>
          <input id="management-control-password" type="password" autocomplete="current-password" required bind:value={password} />
          <button disabled={blocked}>Reauthenticate</button>
        </form>
      {/if}
      {#if mayDebug && debug}
        <h3>Temporary manager Debug</h3>
        <p>Debug logs use the normal redacted journal and return to the default level at expiry.</p>
        <label for="management-debug-duration">Duration</label>
        <select id="management-debug-duration" bind:value={duration}>
          <option value={300}>5 minutes</option><option value={600}>10 minutes</option><option value={900}>15 minutes</option>
        </select>
        <button disabled={blocked || !elevated} onclick={() => controller.setManagementDebug(duration)}>Enable Debug</button>
        {#if debug.state === 'ready'}
          <button disabled={blocked || !elevated} onclick={() => controller.setManagementDebug(0)}>End Debug</button>
        {/if}
      {/if}
      {#if mayRestart && restart}
        <h3>Restart a management service</h3>
        {#if restart.state === 'ready'}
          <p>Restarting an OVS WebUI service can interrupt the console connection. Review the Job after reconnecting.</p>
          <label for="management-restart-service">Service</label>
          <select id="management-restart-service" bind:value={service} onchange={() => (armed = false)}>
            <option value="web">OVS WebUI web service</option><option value="manager">OVS WebUI manager</option>
          </select>
          <label><input type="checkbox" bind:checked={armed} /> I reviewed the selected service</label>
          <button disabled={blocked || !elevated || !armed} onclick={() => {
            armed = false;
            void controller.restartManagementService(service);
          }}>Request restart</button>
        {:else}
          <p>{observationReason(restart.reason || 'MANAGEMENT_RESTART_UNAVAILABLE')}</p>
        {/if}
      {/if}
    </section>
  {/if}

  {#if model.sessionReady && (evidenceLinks.length || canObserveDiagnostics(model.session))}
    <nav class="actions" aria-label="Related operational evidence">
      {#if canObserveDiagnostics(model.session)}<Link href={diagnosticsRoute}>Diagnostics</Link>{/if}
      {#each evidenceLinks as route (route.href)}
        <Link href={route.href}>{route.label}</Link>
      {/each}
    </nav>
  {/if}
{/if}

<style>
  .activity {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 0.75rem;
  }
</style>
