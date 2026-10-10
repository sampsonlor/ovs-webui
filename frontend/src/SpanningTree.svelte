<script lang="ts">
  import type { Model } from './model';
  import type { InventoryField, SpanningTree, SpanningTreeObservationPage } from '../../clients/typescript/public-v1.generated';
  import { untrack } from 'svelte';
  import { controller, navigate } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import SpanningTreeFields from './SpanningTreeFields.svelte';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  const detail = $derived(model.path.startsWith('/switching/spanning-tree/'));
  const item = $derived(detail && model.resource.value ? model.resource.value as unknown as SpanningTree : null);
  const page = $derived(!detail && model.resource.value ? model.resource.value as unknown as SpanningTreeObservationPage : null);
  let filter = $state(untrack(() => new URLSearchParams(model.query).get('filter') ?? ''));
  let filterScope: string | undefined;
  $effect(() => {
    const scope = model.path + model.query;
    if (scope !== filterScope) {
      filter = new URLSearchParams(model.query).get('filter') ?? '';
      filterScope = scope;
    }
  });
  function filterPage(event: SubmitEvent) {
    event.preventDefault();
    navigate('/switching/spanning-tree?' + new URLSearchParams({filter,limit:'25'}));
  }
  function pagePath(cursor = '') {
    const q = new URLSearchParams(model.query); q.set('limit', q.get('limit') ?? '100');
    if (cursor) q.set('cursor', cursor); else q.delete('cursor');
    return '/switching/spanning-tree?' + q;
  }
  function subset(fields: Record<string, InventoryField>, names: string[]) {
    return Object.fromEntries(Object.entries(fields).filter(([name]) => names.includes(name)));
  }
  function participation(field: InventoryField) {
    return field.availability === 'known' ? String(field.value).replaceAll('-', ' ') : field.availability;
  }
</script>

<header class="page-heading">
  <div><p class="eyebrow">Switching / Spanning tree</p><h1>STP / RSTP</h1><p>Configured protocol and daemon observations have independent evidence.</p></div>
  <button onclick={() => controller.refresh()}>Refresh spanning tree</button>
</header>
<p class="notice">Observe · Basic configuration is pending its authority, native validation and safe recovery gates. Configuration changes will use Candidate → Diff / Validation → Safe Apply → Event / Audit.</p>
<LoadNotice load={model.resource} />
{#if page}
  <form class="filter-bar" onsubmit={filterPage}><label for="tree-filter">Bridge name</label><input id="tree-filter" type="search" bind:value={filter} maxlength="1024" /><button type="submit">Filter Bridges</button></form>
  {#if page.source?.freshness !== 'fresh'}<p class="notice warning" role="status">Stale observation. Refresh before relying on reported state.</p>{/if}
  <section class="tree-list" aria-label="Spanning tree Bridges">
    {#each page.items as bridge}
      <article class="panel"><h2><Link href={`/switching/spanning-tree/${bridge.management_id}`}>{bridge.name}</Link></h2><dl><dt>Configured protocol</dt><dd>{bridge.spanning_tree.protocol}</dd><dt>Configuration availability</dt><dd>{bridge.spanning_tree.availability}</dd><dt>Ports in snapshot</dt><dd>{bridge.port_count}</dd></dl><Status value={bridge.source.freshness} /></article>
    {:else}<p class="notice">No Bridges in this authorized result.</p>{/each}
  </section>
  <div class="actions"><Link href={pagePath()}>First spanning tree page</Link>{#if page.next_cursor}<Link href={pagePath(page.next_cursor)}>Next spanning tree page</Link>{/if}<span>{page.truncated ? 'More Bridges exist in this snapshot.' : 'End of this Bridge snapshot.'}</span></div>
{:else if item}
  <section class="panel" aria-label="Bridge spanning tree intent">
    <h2>{item.name}</h2><div class="actions"><Link href={`/bridges/${item.bridge_ref.id}`}>Review Bridge</Link><Link href={`/interfaces?bridge_id=${item.bridge_ref.id}`}>Review Bridge Interfaces</Link><Link href="/switching/spanning-tree">All spanning tree Bridges</Link></div>
    {#if item.source.freshness !== 'fresh'}<p class="notice warning" role="status">Stale observation. Reported values are retained for review; current participation is Unknown.</p>{/if}
    {#if item.spanning_tree.protocol === 'invalid-both-enabled'}<p class="notice warning" role="alert">STP and RSTP are both configured. This native conflict needs explicit review; no protocol is chosen automatically.</p>{/if}
    <dl><dt>Configured protocol</dt><dd><strong>{item.spanning_tree.protocol}</strong></dd><dt>Availability</dt><dd>{item.spanning_tree.availability}</dd><dt>Configuration ownership</dt><dd>{item.spanning_tree.ownership}</dd><dt>Observed by</dt><dd>{item.source.provider_id} · {item.source.observed_at ?? 'Unknown'} · {item.source.freshness}</dd></dl>
    <p>Enabling, disabling or changing spanning tree can interrupt management connectivity. Bond, internal and mirror output Ports do not participate in native STP/RSTP. Mirror output associations and unverified Interface types remain Unknown here.</p>
    <SpanningTreeFields fields={subset(item.spanning_tree.configuration, ['stp_enable','rstp_enable'])} {expert} />
  </section>
  <div class="columns"><section class="panel"><h2>Bridge basic parameters</h2><SpanningTreeFields fields={expert ? item.spanning_tree.configuration : subset(item.spanning_tree.configuration, ['stp-priority','stp-hello-time','stp-max-age','stp-forward-delay','rstp-priority','rstp-max-age','rstp-forward-delay'])} {expert} /><p class="muted">Unset parameters retain the native default. A configured value is not evidence that it was applied.</p></section><section class="panel"><h2>Bridge runtime observations</h2><SpanningTreeFields fields={expert ? item.spanning_tree.runtime : subset(item.spanning_tree.runtime, ['stp_bridge_id','stp_designated_root','rstp_bridge_id','rstp_root_id'])} {expert} runtime /></section></div>
  <section aria-label="Spanning tree Ports"><h2>Port participation and runtime</h2><p>Participation describes Port configuration and native exclusions. An enabled Port participates only when its Bridge protocol is enabled.</p>
    {#each item.ports as port}
      <article class="panel"><h3><Link href={`/ports/${port.port_ref.id}`}>{port.name}</Link></h3><dl><dt>STP participation</dt><dd>{participation(port.spanning_tree.stp_participation)} · {port.spanning_tree.stp_participation.reason}</dd><dt>RSTP participation</dt><dd>{participation(port.spanning_tree.rstp_participation)} · {port.spanning_tree.rstp_participation.reason}</dd></dl><SpanningTreeFields fields={expert ? port.spanning_tree.runtime : subset(port.spanning_tree.runtime, ['stp_state','stp_role','rstp_port_state','rstp_port_role'])} {expert} runtime />{#if expert}<details><summary>Port native configuration</summary><SpanningTreeFields fields={port.spanning_tree.configuration} {expert} /></details>{/if}</article>
    {:else}<p class="notice">No Port details in this snapshot.</p>{/each}
    {#if item.ports_truncated}<p class="notice warning" role="status">Partial Port detail. The response budget was reached; review the remaining Ports through Bridge inventory.</p>{/if}
  </section>
  {#if expert}<details><summary>Native identity and delivery gates</summary><dl><dt>Management ID</dt><dd class="mono">{item.management_id}</dd><dt>OVS UUID</dt><dd class="mono">{item.ovs_uuid}</dd><dt>Generation</dt><dd class="mono">{item.instance_generation}</dd><dt>Snapshot</dt><dd class="mono">{item.snapshot_id}</dd><dt>Write gate</dt><dd>{item.write_reason}</dd></dl><p>Independent field authority, native validator, Applied evidence and guarded compensation remain pending. Standard and Expert have the same permissions and gates.</p></details>{/if}
  <p class="muted">Native behavior reference: <a href="https://www.openvswitch.org/support/dist-docs/ovs-vswitchd.conf.db.5.html" target="_blank" rel="noreferrer">Open vSwitch database manual</a>. Observation does not establish loop prevention across the network.</p>
{/if}

<style>
  .tree-list { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr)); gap: 1rem; }
  .filter-bar { display: flex; flex-wrap: wrap; align-items: center; gap: .7rem; margin: 1rem 0; }
  article.panel { margin-bottom: 1rem; }
</style>
