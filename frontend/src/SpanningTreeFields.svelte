<script lang="ts">
  import type { InventoryField } from '../../clients/typescript/public-v1.generated';
  let { fields, expert, runtime = false }: { fields: Record<string, InventoryField>; expert: boolean; runtime?: boolean } = $props();
  function value(field: InventoryField) {
    if (field.availability === 'unset') return 'Native default (unset)';
    if (field.availability !== 'known') return field.availability === 'withheld' ? 'Withheld' : field.availability === 'unsupported' ? 'Unsupported' : 'Unknown';
    return field.value === null ? 'Unknown' : String(field.value);
  }
</script>

<dl class="tree-fields">
  {#each Object.entries(fields) as [name, field]}
    <dt>{name.replaceAll('_', ' ').replaceAll('-', ' ')}</dt>
    <dd><strong>{value(field)}</strong>{#if expert}<small>{field.availability} · {field.source.authority} · {field.source.freshness}{#if field.reason} · {field.reason}{/if}</small>{/if}</dd>
  {/each}
</dl>
{#if runtime}<p class="muted">A reported state or role is an OVS observation. Missing values remain Unknown; this does not prove packet forwarding or convergence.</p>{/if}

<style>
  .tree-fields dd { overflow-wrap: anywhere; }
  small { display: block; font-size: .78rem; font-weight: normal; color: var(--muted); margin-top: .2rem; }
</style>
