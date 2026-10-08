<script lang="ts">
  import type { DiffField, TopologyNode } from '../../clients/typescript/public-v1.generated';
  let { field, expert }: { field: DiffField; expert: boolean } = $props();
  function nodes(value: unknown): TopologyNode[] {
    return Array.isArray(value) ? value as TopologyNode[] : [];
  }
  const original = $derived(nodes(field.before));
  const current = $derived(nodes(field.current));
  const yours = $derived(nodes(field.after));
  const objects = $derived([...new Map([...original, ...current, ...yours].map(n => [n.binding.management_id, n])).values()]);
  function membership(n: TopologyNode, all: TopologyNode[]) {
    return n.links.map(b => all.find(p => p.binding.management_id === b.management_id)?.name ?? `${b.table} ${b.management_id.slice(0, 8)}`).join(', ');
  }
  function value(v: unknown): string {
    if (Array.isArray(v)) return v.length ? v.map(value).join(', ') : 'Native default / empty';
    return v === null || v === undefined ? 'Unknown' : String(v);
  }
</script>

<section class="panel" aria-label="Native topology Diff">
  <h2>Native topology · {field.operation}</h2>
  <p class="notice warning">High-risk Safe Apply. Review the management path and sole uplink. Deletion retires identities; recovery recreates deleted objects with fresh identities. Unknown configuration stays in the server's protected recovery image.</p>
  {#if field.conflict}<p class="notice warning">Configuration or ownership changed. Discard and restage against the current topology; this original cannot be rebased.</p>{/if}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard access to a wide review table.) -->
  <div class="table-scroll" tabindex="0" role="region" aria-label="Topology original current and yours">
    <table>
      <caption>Original → Current → Yours · immutable objects and native settings</caption>
      <thead><tr><th>Object</th><th>Original</th><th>Current</th><th>Yours</th></tr></thead>
      <tbody>{#each objects as object (object.binding.management_id)}
        <tr><th scope="row">{object.name}<br/>{object.binding.table}{#if expert}<small class="mono">{object.binding.management_id}<br/>{object.binding.ovs_uuid}</small>{/if}</th>
          {#each [original, current, yours] as side}
            {@const n = side.find(p => p.binding.management_id === object.binding.management_id)}
            <td>{#if n}<p>{n.binding.table === 'Port' && n.links.length > 1 ? 'Bond Port' : n.binding.table}{n.native_type ? ` · ${n.native_type}` : ''}</p>
              {#if n.links.length}<p>Members: {membership(n, side)}</p>{/if}
              {#if n.configuration_summary}<dl>{#each Object.entries(n.configuration_summary) as [key, v]}<dt>{key}</dt><dd>{value(v)}</dd>{/each}</dl>{/if}
              {#if expert}<small class="mono">Configuration digest: {n.configuration_digest}</small>{/if}
            {:else}<span>Not present</span>{/if}</td>
          {/each}
        </tr>
      {/each}</tbody>
    </table>
  </div>
</section>
