<script lang="ts">
  import type { Resource } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import {
    capabilitiesRoute,
    componentName,
    observationReason,
  } from './management-observation';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  const health = $derived(
    model.resource.status === 'ready'
      ? (model.resource.value as Resource | null)
      : null,
  );
  const components = $derived(health?.components ?? []);
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

  {#if model.sessionReady && evidenceLinks.length}
    <nav class="actions" aria-label="Related operational evidence">
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
