<script lang="ts">
  import type { Model } from './model';
  import type { Port, InventoryField } from '../../clients/typescript/public-v1.generated';
  import { controller } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  let { model, expert, desktop }: { model: Model; expert: boolean; desktop: boolean } = $props();
  const detail = $derived(model.path.split('/').length === 4);
  const item = $derived(detail ? model.resource.value as unknown as Port | null : null);
  const page = $derived(!detail ? model.resource.value : null);
  const items = $derived(Array.isArray(page?.items) ? page.items as Port[] : []);
  let desired = $state('protected');
  const field = $derived((item?.fields as Record<string,InventoryField> | undefined)?.protected);
  function isolationLabel(port: Port) {
    const f=(port.fields as Record<string,InventoryField> | undefined)?.protected;
    if (f?.availability!=='known' || typeof f.value!=='boolean') return f?.availability ?? 'unknown';
    return f.value ? 'Protected' : 'Unprotected';
  }
  function stage(event: SubmitEvent) {
    event.preventDefault();
    if (!item?.isolation_execution?.available || !desktop || model.busy) return;
    const existing=model.workspace.value?.candidate?.intents.find(i=>i.operation==='port.isolation.set'&&i.object.management_id===item.management_id);
    void controller.stageNativeIntent({
      intent_id:existing?.intent_id ?? crypto.randomUUID(),operation:'port.isolation.set',
      object:{management_id:item.management_id,ovs_uuid:item.ovs_uuid,instance_generation:item.instance_generation,table:'Port'},
      protected:desired==='protected',
    });
  }
  function next(cursor: string) {
    const query=new URLSearchParams(model.query);query.set('cursor',cursor);
    return '/switching/isolation?'+query;
  }
</script>

<section aria-label="Native port isolation">
  <p class="eyebrow">Switching</p>
  <h1>Port isolation</h1>
  <p>Protected ports cannot forward to other protected ports on the same Bridge. Unprotected ports can communicate with both groups.</p>
  <p>This controls native OVS switching. It does not provide a firewall or general private VLAN policy.</p>
  <LoadNotice load={model.resource} />
  {#if !detail}
    <table>
      <caption>Observed ports in this page</caption>
      <thead><tr><th>Port</th><th>Isolation</th><th>Configuration</th>{#if expert}<th>Management ID</th>{/if}</tr></thead>
      <tbody>{#each items as port (port.management_id)}
        <tr><th scope="row"><Link href={`/switching/isolation/${port.management_id}`}>{port.name}</Link></th><td>{isolationLabel(port)}</td><td>{port.isolation_execution?.available ? 'Review available' : port.isolation_execution?.reason ?? 'Unavailable'}</td>{#if expert}<td class="mono">{port.management_id}</td>{/if}</tr>
      {/each}</tbody>
    </table>
    {#if page && items.length===0}<p>No ports in this observation.</p>{/if}
    {#if typeof page?.next_cursor==='string'}<Link href={next(page.next_cursor)}>Next page →</Link>{/if}
  {:else if item}
    <Link href="/switching/isolation">← Port isolation</Link>
    <h2>{item.name}</h2>
    <p>Current configuration: <strong>{isolationLabel(item)}</strong></p>
    <p><Link href={`/ports/${item.management_id}`}>Review Port details</Link></p>
    {#if !desktop}<p>Review mode. Use a desktop to prepare a change.</p>
    {:else if item.isolation_execution?.available}
      <form onsubmit={stage} aria-label="Prepare native isolation change">
        <label>Desired isolation<select bind:value={desired}><option value="protected">Protected</option><option value="unprotected">Unprotected</option></select></label>
        <p>The change is staged for shared Diff, Validation and Safe Apply. Review management connectivity before applying.</p>
        <button class="primary" disabled={model.busy || field?.availability!=='known' || !model.workspace.value}>Stage isolation change</button>
      </form>
    {:else}<p role="status">Configuration unavailable: {item.isolation_execution?.reason ?? 'No current execution authority'}.</p>{/if}
    {#if expert}<dl><dt>Ownership</dt><dd>{item.isolation_execution?.authority ?? 'Unknown'}</dd><dt>Native field</dt><dd>Port.protected</dd><dt>Management ID</dt><dd class="mono">{item.management_id}</dd><dt>Source</dt><dd>{field?.source ? JSON.stringify(field.source) : 'Unknown'}</dd></dl>{/if}
  {/if}
</section>
