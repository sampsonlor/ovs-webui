<script lang="ts">
  import type {
    InterfaceCounterDiagnosticMember,
    InterfaceCounterDiagnosticResult,
    ResourceRef,
  } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { refPath, type Load } from './model';
  import { diagnosticsRoute, terminalDiagnosticJob } from './diagnostics';

  let { state, load, expert }: {
    state?: unknown;
    load: Load<InterfaceCounterDiagnosticResult>;
    expert: boolean;
  } = $props();
  const result = $derived(load.status === 'ready' ? load.value : null);
  const terminal = $derived(state === undefined || terminalDiagnosticJob(state));
  const messages: Record<InterfaceCounterDiagnosticResult['state'], string> = {
    complete: 'The two samples covered the requested Interfaces.',
    partial: 'Some Interface counters could not be compared.',
    truncated: 'The observation reached its Interface limit. Some members may be omitted.',
    'no-data': 'The Job could not produce a comparable pair of samples.',
    'provider-unavailable': 'Current OVSDB statistics were unavailable.',
    'evidence-unavailable': 'The original Port or the samples could not be verified.',
    'operation-failed': 'The Job could not complete the observation.',
    cancelled: 'The observation was cancelled.',
    'timed-out': 'The observation exceeded the selected sample budget.',
  };
  const gaps: Record<string, string> = {
    DIAGNOSTIC_COUNTER_SAMPLE_GAP: 'Comparable samples were missing.',
    DIAGNOSTIC_COUNTER_RESET: 'A counter changed discontinuously.',
    DIAGNOSTIC_COUNTER_FIELDS_UNKNOWN: 'Some counters were not reported in both samples.',
  };
  const columns: Array<[keyof InterfaceCounterDiagnosticMember['deltas'], string]> = [
    ['rx_bytes', 'RX bytes'],
    ['tx_bytes', 'TX bytes'],
    ['rx_packets', 'RX packets'],
    ['tx_packets', 'TX packets'],
    ['rx_errors', 'RX errors'],
    ['tx_errors', 'TX errors'],
    ['rx_dropped', 'RX drops'],
    ['tx_dropped', 'TX drops'],
  ];

  function label(ref: ResourceRef) {
    return `${ref.kind === 'port' ? 'Port' : 'Interface'} · ${expert ? ref.id : ref.id.slice(0, 8)}`;
  }

  function decimal(value: unknown) {
    return typeof value === 'string' && /^[0-9]{1,20}$/.test(value)
      ? value
      : 'Unknown';
  }
</script>

{#snippet reference(ref: ResourceRef)}
  {@const href = refPath(ref)}
  {#if href}<Link {href}>{label(ref)}</Link>{:else}{label(ref)}{/if}
{/snippet}

{#snippet sampledTime(value: string | undefined)}
  {#if value}<time datetime={value}>{value}</time>{:else}Unavailable{/if}
{/snippet}

<section class="panel" aria-labelledby="interface-counter-result-heading">
  <h2 id="interface-counter-result-heading">Interface counter result</h2>
  {#if !terminal}
    <p role="status">The Job has not published its counter observation yet.</p>
  {:else if result}
    <div class="actions"><Status value={result.state} /><span>{messages[result.state] ?? 'The result state is unknown.'}</span></div>
    <dl>
      <dt>Observed Port</dt><dd>{@render reference(result.target_ref)}</dd>
      <dt>First sample</dt><dd>{@render sampledTime(result.first_sampled_at)}</dd>
      <dt>Second sample</dt><dd>{@render sampledTime(result.second_sampled_at)}</dd>
      {#if expert}
        <dt>Source</dt><dd>{result.source}</dd>
        <dt>Freshness</dt><dd><Status value={result.freshness} /></dd>
        {#if result.instance_generation}<dt>Instance generation</dt><dd class="mono">{result.instance_generation}</dd>{/if}
        {#if result.reason_code}<dt>Reason code</dt><dd class="mono">{result.reason_code}</dd>{/if}
      {/if}
    </dl>
    {#if result.members.length}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users can scroll the counter table.) -->
      <div class="table-scroll" role="region" aria-label="Interface counter deltas" tabindex="0">
        <table>
          <thead><tr><th scope="col">Interface</th><th scope="col">Evidence</th>{#each columns as [key, name] (key)}<th scope="col">{name}</th>{/each}</tr></thead>
          <tbody>
            {#each result.members as member (member.interface_ref.id)}
              <tr>
                <th scope="row">{@render reference(member.interface_ref)}</th>
                <td><Status value={member.state} />
                  {#if member.reason_code}
                    <small>{gaps[member.reason_code] ?? 'The counter comparison was limited.'}</small>
                    {#if expert}<small class="mono">{member.reason_code}</small>{/if}
                  {/if}
                </td>
                {#each columns as [key] (key)}<td class="mono">{decimal(member.deltas[key])}</td>{/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {:else if load.error === 'DIAGNOSTIC_RESULT_UNAVAILABLE' || load.error === 'NOT_FOUND'}
    <p class="notice warning" role="status">The result is unavailable or expired.{state !== undefined ? ' The Job record remains available above.' : ''}</p>
  {:else if load.error === 'DIAGNOSTIC_RESULT_TARGET_UNKNOWN' || load.error === 'DIAGNOSTIC_RESULT_IDENTITY_MISMATCH'}
    <p class="notice warning" role="status">The result could not be verified for the original Port.</p>
  {:else if load.error === 'DIAGNOSTIC_RESULT_PENDING'}
    <p role="status">Waiting for the Job observation.</p>
  {:else}
    <LoadNotice {load} />
  {/if}
  <p><Link href={`${diagnosticsRoute}?template=interface-counters`}>Back to Diagnostics</Link></p>
</section>
