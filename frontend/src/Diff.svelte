<script lang="ts">
  import type { DiffField } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  let { fields, expert = false }: { fields: DiffField[]; expert?: boolean } = $props();
  const deletions = $derived(fields.filter((field) => field.operation === 'bridge.delete-isolated'));
  function display(value: unknown, field: DiffField) {
    if (field.operation === 'bridge.delete-isolated' && value && typeof value === 'object') {
      const graph = value as Record<string, unknown>;
      return 'state' in graph
        ? 'Removed · rollback recreates with new identities'
        : String(graph.name) + ' · Bridge → local Port → internal Interface';
    }
    return text(value, field.field);
  }
  const text = (value: unknown, field: string) =>
    value === null || value === undefined
      ? 'Native default / unknown'
      : Array.isArray(value) && value.length === 0
        ? field === 'trunks'
          ? '∅ · all VLANs'
          : '∅'
        : JSON.stringify(value);
</script>

{#if deletions.length}
  <div class="notice warning">
    <strong>Rollback recreates with new identities</strong>
    <p>Deletion removes the isolated Bridge and its local Port and Interface. Recovery creates new objects; old identities remain retired. Changed configuration or external dependencies block deletion or recovery.</p>
  </div>
  {#if expert}
    {#each deletions as field}
      <details>
        <summary>Original and reserved replacement identities</summary>
        <pre>{JSON.stringify({ original: field.before, proposed: field.after }, null, 2)}</pre>
      </details>
    {/each}
  {/if}
{/if}

<!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll a wide Diff.) -->
<div class="table-scroll" tabindex="0" role="region" aria-label="Configuration Diff">
  <table>
    <caption>Original → Current → Yours · values supplied by the server</caption><thead
      ><tr><th>Object / field</th><th>Original</th><th>Current</th><th>Yours</th></tr
      ></thead
    ><tbody>
      {#each fields as field}
        <tr class:conflict={field.conflict}
          ><th scope="row"
            >{#if field.object.table === 'Port'}<Link href={`/ports/${field.object.management_id}`}
              >{field.object.management_id.slice(0, 8)}</Link
            >{:else}<span>{field.object.table} · {field.object.management_id.slice(0, 8)}</span>{/if}<br />{field.field}{#if field.conflict}<span class="badge">Conflict</span
              >{/if}</th
          ><td>{display(field.before, field)}</td><td
            >{display(field.current, field)}</td
          ><td>{display(field.after, field)}</td></tr
        >
      {:else}<tr><td colspan="4">No field changes to review.</td></tr>{/each}
    </tbody>
  </table>
</div>
