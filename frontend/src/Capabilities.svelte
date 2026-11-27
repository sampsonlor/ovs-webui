<script lang="ts">
  import type { Resource, ResourcePage } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import {
    capabilitiesRoute,
    capabilityPath,
    componentName,
    healthRoute,
    observationLabel,
    observationReason,
  } from './management-observation';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  const detail = $derived(model.path !== capabilitiesRoute);
  const item = $derived(
    detail && model.resource.status === 'ready'
      ? (model.resource.value as Resource | null)
      : null,
  );
  const page = $derived(
    !detail && model.resource.status === 'ready'
      ? (model.resource.value as ResourcePage | null)
      : null,
  );
  const capability = $derived(item?.capability_evidence);
  const entries = $derived(page?.items.filter((entry) => entry.capability_evidence) ?? []);
  const hasCursor = $derived(new URLSearchParams(model.query).has('cursor'));

  function registryPath(cursor: string | null) {
    const params = new URLSearchParams(model.query);
    if (cursor) params.set('cursor', cursor);
    else params.delete('cursor');
    const query = params.toString();
    return query ? capabilitiesRoute + '?' + query : capabilitiesRoute;
  }
</script>

{#if detail}
  <nav class="breadcrumbs" aria-label="Capability navigation">
    <Link href={capabilitiesRoute + model.query}>Capabilities</Link>
  </nav>
{/if}
<header class="page-heading">
  <div>
    <p class="eyebrow">Administration</p>
    <h1>{detail ? capability?.label ?? 'Capability details' : 'Capabilities'}</h1>
    <p>Review readiness, current grants, and recovery requirements.</p>
  </div>
  <button onclick={() => controller.refresh()}>Refresh capabilities</button>
</header>
<LoadNotice load={model.resource} />
{#if !detail && model.resource.error === 'CURSOR_EXPIRED'}
  <Link href={registryPath(null)}>First capability page</Link>
{/if}

{#if detail && item && capability}
  <section class="panel" aria-labelledby="capability-readiness">
    <h2 id="capability-readiness">Readiness</h2>
    <dl>
      <dt>Availability</dt><dd><Status value={capability.availability} /></dd>
      {#if capability.reason}
        <dt>Reason</dt><dd>{observationReason(capability.reason)}</dd>
      {/if}
      <dt>Current grant</dt><dd><Status value={capability.grant_state} /></dd>
      <dt>Authority</dt><dd>{observationLabel(capability.authority)}</dd>
      <dt>Recovery</dt><dd><Status value={capability.recovery} /></dd>
    </dl>
    {#if capability.prerequisites.length}
      <h3>Prerequisites</h3>
      <ul>
        {#each capability.prerequisites as prerequisite}
          <li>{componentName(prerequisite)}</li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if expert}
    <section class="panel" aria-labelledby="capability-evidence">
      <h2 id="capability-evidence">Capability evidence</h2>
      <dl>
        <dt>Capability key</dt><dd class="mono">{capability.key}</dd>
        <dt>Provider</dt><dd>{item.source.provider_id}</dd>
        <dt>Source authority</dt><dd>{item.source.authority}</dd>
        <dt>Freshness</dt><dd><Status value={item.source.freshness} /></dd>
        <dt>Confidence</dt><dd>{item.source.confidence}</dd>
        <dt>Observed</dt>
        <dd>
          {#if item.source.observed_at}
            <time datetime={item.source.observed_at}>{item.source.observed_at}</time>
          {:else}Not sampled{/if}
        </dd>
        {#if capability.reason}
          <dt>Reason code</dt><dd class="mono">{capability.reason}</dd>
        {/if}
      </dl>
      {#if capability.required_capabilities.length}
        <h3>Required permissions</h3>
        <ul>
          {#each capability.required_capabilities as permission}
            <li class="mono">{permission}</li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}
{:else if detail && item}
  <p class="notice">Current capability evidence is unavailable.</p>
{:else if !detail && page}
  <section class="panel">
    <h2 id="capability-list-heading">Capability registry</h2>
    {#if !entries.length}
      <p>No capability evidence is available.</p>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the registry.) -->
      <div class="table-scroll" role="region" aria-labelledby="capability-list-heading" tabindex="0">
        <table>
          <thead><tr><th>Capability</th><th>Availability</th><th>Current grant</th><th>Recovery</th></tr></thead>
          <tbody>
            {#each entries as entry (entry.id)}
              {@const declared = entry.capability_evidence}
              {@const href = capabilityPath(entry.id)}
              {#if declared}
                <tr>
                  <th scope="row">
                    {#if href}<Link href={href + model.query}>{declared.label}</Link>{:else}{declared.label}{/if}
                    {#if expert}<small>{declared.key}</small>{/if}
                  </th>
                  <td>
                    <Status value={declared.availability} />
                    {#if declared.reason}<small>{observationReason(declared.reason)}</small>{/if}
                  </td>
                  <td><Status value={declared.grant_state} /></td>
                  <td><Status value={declared.recovery} /></td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if hasCursor || page.next_cursor}
      <nav class="actions registry-pages" aria-label="Capability pages">
        {#if hasCursor}<Link href={registryPath(null)}>First capability page</Link>{/if}
        {#if page.next_cursor}<Link href={registryPath(page.next_cursor)}>Next capability page</Link>{/if}
      </nav>
    {/if}
    {#if page.truncated}<p class="notice warning">The registry returned partial results.</p>{/if}
  </section>
{/if}

{#if model.sessionReady && has(model.session, 'state.read')}
  <div class="actions">
    <Link href={healthRoute}>Review System Health</Link>
    {#if detail && capability && capability.recovery !== 'not-required' && has(model.session, 'configuration.read')}
      <Link href="/changes/transactions">Review transactions</Link>
    {/if}
  </div>
{/if}

<style>
  .registry-pages {
    margin-top: 0.75rem;
  }
</style>
