<script lang="ts">
  import type { LinkLACPDiagnosticResult, ResourceRef } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { refPath, type Load } from './model';
  import { diagnosticsRoute, terminalDiagnosticJob } from './diagnostics';

  let { state, load, expert }: { state?: unknown; load: Load<LinkLACPDiagnosticResult>; expert: boolean } = $props();
  const result = $derived(load.status === 'ready' ? load.value : null);
  const terminal = $derived(state === undefined || terminalDiagnosticJob(state));
  const messages: Record<LinkLACPDiagnosticResult['state'], string> = {
    complete: 'The selected scope was observed.',
    partial: 'Some link or LACP evidence was unavailable or withheld.',
    truncated: 'The result reached its output limit. More ports or interfaces may exist.',
    'no-data': 'The observed Bridge had no ports in scope.',
    'provider-unavailable': 'The OVSDB provider could not supply a current observation.',
    'evidence-unavailable': 'The selected identity or its observation could not be verified.',
    'operation-failed': 'The Job could not complete the observation.',
    cancelled: 'The observation was cancelled.',
    'timed-out': 'The observation exceeded its selected time budget.',
  };
  function label(ref: ResourceRef) {
    return `${ref.kind[0]?.toUpperCase() ?? ''}${ref.kind.slice(1)} · ${expert ? ref.id : ref.id.slice(0, 8)}`;
  }
</script>

{#snippet reference(ref: ResourceRef)}
  {@const href = refPath(ref)}
  {#if href}<Link {href}>{label(ref)}</Link>{:else}{label(ref)}{/if}
{/snippet}

<section class="panel" aria-labelledby="link-lacp-result-heading">
  <h2 id="link-lacp-result-heading">Link and LACP result</h2>
  {#if !terminal}
    <p role="status">The Job has not published a final observation yet.</p>
  {:else if result}
    <div class="actions"><Status value={result.state} /><span>{messages[result.state]}</span></div>
    {#if result.truncated && result.partial}<p>Some retained evidence was also incomplete or withheld.</p>{/if}
    <dl>
      <dt>Observed target</dt><dd>{@render reference(result.target_ref)}</dd>
      {#if result.observed_at}<dt>Observed at</dt><dd><time datetime={result.observed_at}>{result.observed_at}</time></dd>{/if}
      {#if expert}
        <dt>Observation authority</dt><dd>{result.authority ?? 'Unknown'}</dd>
        {#if result.reason_code}<dt>Reason code</dt><dd class="mono">{result.reason_code}</dd>{/if}
      {/if}
    </dl>
    {#each result.ports as port (port.port_ref.id)}
      <h3>{@render reference(port.port_ref)}</h3>
      <p>Configured LACP <Status value={port.configured_lacp} /></p>
      {#if port.members.length}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll member observations.) -->
        <div class="table-scroll" role="region" aria-label={`Member observations for ${label(port.port_ref)}`} tabindex="0">
          <table>
            <thead><tr><th scope="col">Interface</th><th scope="col">Link</th><th scope="col">LACP current</th></tr></thead>
            <tbody>
              {#each port.members as member (member.interface_ref.id)}
                <tr><th scope="row">{@render reference(member.interface_ref)}</th><td><Status value={member.link} /></td><td><Status value={member.lacp_current} /></td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <p>No member observations were available for this Port.</p>
      {/if}
    {/each}
  {:else if load.error === 'DIAGNOSTIC_RESULT_UNAVAILABLE' || load.error === 'NOT_FOUND'}
    <p class="notice warning" role="status">The observation is unavailable or expired.{state !== undefined ? ' The Job record remains available above.' : ''}</p>
  {:else if load.error === 'DIAGNOSTIC_RESULT_PENDING'}
    <p role="status">Waiting for the Job observation.</p>
  {:else}
    <LoadNotice {load} />
  {/if}
  <p><Link href={diagnosticsRoute}>Back to Diagnostics</Link></p>
</section>
