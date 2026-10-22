<script lang="ts">
  import { treeParameterMessages as messages } from './spanning-tree';
  import type { SpanningTreeParameterValidation } from '../../clients/typescript/public-v1.generated';
  let { validation, expert, draftAvailable = false, executionAvailable = false }: { validation?: SpanningTreeParameterValidation; expert: boolean; draftAvailable?: boolean; executionAvailable?:boolean } = $props();
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
  <p>Passing these checks does not prove installed timers, forwarding or convergence. {executionAvailable?'Prepare a separate change below. Execution requires verified native state and Safe Apply.':draftAvailable ? 'Prepare a separate draft below. Execution remains unavailable.' : 'Configuration editing remains unavailable.'}</p>
  {#if expert && validation}<p class="muted">Rule set: {validation.version} · Scope: {validation.scope}</p>{/if}
</section>

<style>
  code { display: block; overflow-wrap: anywhere; }
  li { margin-block: .5rem; }
  .warning { color: var(--warn-fg); }
</style>
