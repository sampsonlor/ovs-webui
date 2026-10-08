<script lang="ts">
  import { untrack } from 'svelte';
  import type { Model } from './model';
  import { controller, refPath } from './model';
  import { observation } from './interface-observation';
  import { nativeTypes } from './interface-query';
  import { has } from './policy';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  let { model, expert }: { model: Model; expert: boolean } = $props();
  let filter = $state(untrack(() => model.interfaceFilter));
  let limit = $state(String(untrack(() => model.interfaceLimit)));
  let typeChoice = $state('all');
  let customType = $state('');
  let linkState = $state('');
  const page = $derived(model.interfaces.value);
  const canReadType = $derived(has(model.session, 'configuration.read'));
  const bridgePath = $derived(refPath({kind: 'bridge', id: model.interfaceBridgeID}));
  $effect(() => {
    // Only a route change replaces draft controls. Background polling does not.
    const query = model.query;
    untrack(() => {
      const q = new URLSearchParams(query);
      filter = q.get('filter') ?? '';
      limit = q.get('limit') ?? '25';
      const type = q.get('native_type');
      typeChoice = type === null ? 'all' : type === '' ? 'empty' : nativeTypes.includes(type) ? `native:${type}` : 'custom';
      customType = type ?? '';
      linkState = q.get('link_state') ?? '';
    });
  });
  function search(event: SubmitEvent) {
    event.preventDefault();
    const nativeType = typeChoice === 'all' ? null : typeChoice === 'empty' ? '' : typeChoice === 'custom' ? customType : typeChoice.slice(7);
    controller.interfacePage(filter.trim(), Number(limit), '', {bridgeID: model.interfaceBridgeID, nativeType, linkState});
  }
  function clear() {
    filter = '';
    limit = '25';
    typeChoice = 'all';
    customType = '';
    linkState = '';
    controller.interfacePage('', 25, '', {bridgeID: '', nativeType: null, linkState: ''});
  }
</script>

<header class="page-heading">
  <div><p class="eyebrow">Switching / Interfaces</p><h1>Interfaces</h1><p>Inspect native Interface values and their Bridge → Port relationships.</p></div>
  <button onclick={() => controller.interfacePage()}>Refresh inventory</button>
</header>
<div class="summary" aria-label="Interface Bridge scope">
  {#if model.interfaceBridgeID}
    <span>Bridge scope: {#if bridgePath}<Link href={bridgePath}>{typeof model.resource.value?.name === 'string' ? model.resource.value.name : 'Selected Bridge'}</Link>{:else}Unresolved Bridge{/if}</span>
    <button onclick={() => controller.interfacePage(undefined, undefined, '', {bridgeID: '', nativeType: model.interfaceNativeType, linkState: model.interfaceLinkState})}>All Bridges</button>
  {:else}<span>All Bridges</span>{/if}
  <Link href="/bridges">Choose a Bridge →</Link>
</div>
{#if model.interfaces.error === 'BRIDGE_SCOPE_NOT_FOUND'}<p class="notice warning" role="status">The selected Bridge identity is no longer available. A same-name replacement is a different object. Choose a Bridge or clear this scope.</p>{/if}
<form class="inventory-filters" onsubmit={search}>
  <div class="filter-control"><label for="interface-filter">Interface name</label><input id="interface-filter" type="search" bind:value={filter} maxlength="1024" placeholder="Filter all observed Interfaces" /></div>
  <div class="filter-control"><label for="interface-type-filter">Native type</label><select id="interface-type-filter" bind:value={typeChoice} disabled={!canReadType}><option value="all">All native types</option><option value="empty">System default (empty type)</option>{#each nativeTypes as type}<option value={`native:${type}`}>{type}</option>{/each}<option value="custom">Other exact native type</option></select>{#if typeChoice === 'custom'}<label for="interface-custom-type">Exact native type</label><input id="interface-custom-type" bind:value={customType} maxlength="64" required disabled={!canReadType} />{/if}</div>
  <div class="filter-control"><label for="interface-link-filter">OVS link state</label><select id="interface-link-filter" bind:value={linkState}><option value="">All OVS link states</option><option value="up">Up</option><option value="down">Down</option><option value="unknown">Unknown observation</option></select></div>
  <div class="filter-control"><label for="interface-limit">Page size</label><select id="interface-limit" bind:value={limit}>{#each [10, 25, 50, 100] as size}<option value={String(size)}>{size}</option>{/each}</select></div>
  <button type="submit">Apply filter</button><button type="button" onclick={clear}>Clear filters</button>
</form>
{#if !canReadType}<p class="notice">Configuration read permission is required for native type filtering. Clear an existing type filter to review the permitted inventory.</p>{/if}
<LoadNotice load={model.interfaces} />
{#if model.interfaces.error === 'CURSOR_EXPIRED'}<p class="notice warning">The page snapshot changed or expired. Restart from the first page.</p>{/if}
{#if page}
  <div class="summary"><strong>{page.items.length} Interfaces on this page</strong><Status value={page.source?.freshness} /><Status value={page.availability} /><span>Observed {page.source?.observed_at ?? 'Unknown'}</span></div>
  {#if page.source?.freshness !== 'fresh'}<p class="notice warning" role="status">Stale observation. Refresh before relying on these values.</p>{/if}
  <p class="muted query-summary">{model.interfaceFilter ? `Server name filter: ${model.interfaceFilter}. ` : ''}{model.interfaceNativeType !== null ? `Exact native type: ${model.interfaceNativeType || 'empty (system default)'}. ` : ''}{model.interfaceLinkState ? `OVS link state: ${model.interfaceLinkState}. ` : ''}Names use natural numeric ordering. Filters cover the server snapshot, including later pages. Coverage: selected OVSDB fields; Linux observations are available in detail only.</p>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the inventory.) -->
  <div class="table-scroll" tabindex="0" role="region" aria-label="Interfaces inventory">
    <table><caption>Native OVS Interface rows · page from one server snapshot</caption><thead><tr><th>Interface</th><th>Native type</th><th>Bridge / Port</th><th>OVS state</th><th>MTU</th>{#if expert}<th>Identity / source</th>{/if}</tr></thead><tbody>
      {#each page.items as item (item.management_id)}
        <tr><th scope="row"><Link href={`/interfaces/${item.management_id}${model.query}`}>{item.name}</Link><small>{item.local_interface === true ? 'Local internal Interface' : item.port_kind === 'bond' ? 'Bond member Interface' : 'Port member Interface'}</small></th>
          <td>{observation(item, 'type')}</td><td>{#if item.bridge_ref && refPath(item.bridge_ref)}<Link href={refPath(item.bridge_ref)!}>Bridge · {item.bridge_ref.id.slice(0, 8)}</Link>{:else}<span>Bridge unknown</span>{/if}<small><Link href={`/ports/${item.port_ref.id}`}>Port · {item.port_ref.id.slice(0, 8)}</Link></small></td>
          <td>Admin: {observation(item, 'admin_state')}<small>Link: {observation(item, 'link_state')}</small><small>OpenFlow port: {observation(item, 'ofport')}</small></td>
          <td>{observation(item, 'mtu')}<small>Requested: {observation(item, 'mtu_request')}</small></td>
          {#if expert}<td class="mono">{item.management_id}<small>{item.source.provider_id} · {item.source.confidence}</small><small>Ownership: {typeof item.ownership === 'string' ? item.ownership : 'unknown'}</small></td>{/if}
        </tr>
      {:else}<tr><td colspan={expert ? 6 : 5}>No Interfaces match this filter. This is an observed empty result.</td></tr>{/each}
    </tbody></table>
  </div>
  {#if expert}<details><summary>Inventory coverage and snapshot</summary><p class="mono">Snapshot {page.snapshot_id} · generation {page.instance_generation ?? 'Unknown'}</p><pre>{JSON.stringify(page.coverage, null, 2)}</pre></details>{/if}
{/if}
<div class="actions"><button onclick={() => controller.interfacePage()}>First page</button><button disabled={!page?.next_cursor} onclick={() => controller.interfacePage(model.interfaceFilter, model.interfaceLimit, page!.next_cursor!)}>Next page</button>{#if page}<span>{page.truncated ? 'More Interfaces exist in this snapshot.' : 'End of this snapshot.'}</span>{/if}</div>
