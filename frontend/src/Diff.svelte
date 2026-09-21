<script lang="ts">
  import type { DiffField } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  let { fields }: { fields: DiffField[] } = $props();
  const text = (value: unknown, field: string) =>
    value === null || value === undefined
      ? 'Native default / unknown'
      : Array.isArray(value) && value.length === 0
        ? field === 'trunks'
          ? '∅ · all VLANs'
          : '∅'
        : JSON.stringify(value);
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll a wide Diff.) -->
<div class="table-scroll" tabindex="0" role="region" aria-label="Configuration Diff">
  <table>
    <caption>Original → Current → Yours · values supplied by the server</caption><thead
      ><tr><th>Port / field</th><th>Original</th><th>Current</th><th>Yours</th></tr
      ></thead
    ><tbody>
      {#each fields as field}
        <tr class:conflict={field.conflict}
          ><th scope="row"
            ><Link href={`/ports/${field.object.management_id}`}
              >{field.object.management_id.slice(0, 8)}</Link
            ><br />{field.field}{#if field.conflict}<span class="badge">Conflict</span
              >{/if}</th
          ><td>{text(field.before, field.field)}</td><td
            >{text(field.current, field.field)}</td
          ><td>{text(field.after, field.field)}</td></tr
        >
      {:else}<tr><td colspan="4">No field changes to review.</td></tr>{/each}
    </tbody>
  </table>
</div>
