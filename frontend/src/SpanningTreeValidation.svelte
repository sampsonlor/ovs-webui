<script lang="ts">
  import type { SpanningTreeParameterValidation } from '../../clients/typescript/public-v1.generated';
  let { validation, expert }: { validation?: SpanningTreeParameterValidation; expert: boolean } = $props();
  const messages: Record<string, string> = {
    STP_RSTP_MUTUALLY_EXCLUSIVE: 'STP and RSTP must not be enabled together.',
    SPANNING_TREE_INTEGER_REQUIRED: 'Use a decimal whole number for this parameter.',
    SPANNING_TREE_PARAMETER_RANGE: 'This parameter is outside its native range.',
    STP_MAX_AGE_HELLO_RELATION: 'STP max age must be at least twice hello time plus two seconds.',
    STP_FORWARD_DELAY_MAX_AGE_RELATION: 'STP max age must not exceed twice forward delay minus two seconds.',
    RSTP_PRIORITY_MULTIPLE_4096: 'RSTP priority must be a multiple of 4096. OVS rounds other values down.',
    RSTP_FORWARD_DELAY_MAX_AGE_RELATION: 'RSTP max age must not exceed twice forward delay minus two seconds.',
    STP_HELLO_TIME_NATIVE_UNIT_CAVEAT: 'Explicit STP hello time needs runtime verification. Tested OVS installs 1 second for configured values from 2 to 10 seconds.',
  };
  const states: Record<string, string> = {
    valid: 'Basic parameter checks passed.',
    invalid: 'Basic parameters need review.',
    withheld: 'Parameter checks are withheld with current permissions.',
    stale: 'Parameter checks are unavailable for stale observations.',
    unsupported: 'Parameter checks are unavailable for this native schema.',
    unknown: 'Parameter checks need complete native configuration.',
    'runtime-unverified': 'Basic values need runtime verification.',
  };
</script>

<section class="panel" aria-label="Spanning tree parameter checks">
  <h2>Basic parameter checks</h2>
  <p class:warning={validation?.state === 'invalid' || validation?.state === 'runtime-unverified'} role="status">{states[validation?.state ?? 'unknown'] ?? 'Parameter checks are unavailable.'}</p>
  {#if validation?.state === 'valid' || validation?.state === 'invalid' || validation?.state === 'runtime-unverified'}
    <p>Checks cover both protocols, including inactive basic settings. Advanced Bridge and Port parameters need separate review. Unset keys use native defaults for these checks; their observed values remain unset.</p>
    {#if validation.checks.length}
      <ul>{#each validation.checks as check}<li>{messages[check.code] ?? 'This parameter needs review.'} <span class="muted">{check.fields.join(', ')}</span>{#if expert}<code>{check.code}</code>{/if}</li>{/each}</ul>
    {/if}
  {/if}
  <p>Passing these checks does not prove installed timers, forwarding or convergence. Configuration editing remains unavailable.</p>
  {#if expert && validation}<p class="muted">Rule set: {validation.version} · Scope: {validation.scope}</p>{/if}
</section>

<style>
  code { display: block; overflow-wrap: anywhere; }
  li { margin-block: .5rem; }
  .warning { color: var(--warn-fg); }
</style>
