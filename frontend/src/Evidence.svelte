<script lang="ts">
  import type { ResourceRef } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import { navigate, refPath } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  let { model, expert }: { model: Model; expert: boolean } = $props();
  const collection = $derived(model.path.split('/')[2] === 'audit' ? 'audit' : 'events');
  const title = $derived(collection === 'audit' ? 'Audit' : 'Events');
  const base = $derived(`/operations/${collection}`);
  const detail = $derived(model.path !== base);
  const query = $derived(new URLSearchParams(model.query));
  const objectID = $derived(query.get('object_id'));
  const resource = $derived(model.resource.value);
  const records = $derived(Array.isArray(resource?.items) ? resource.items as Record<string, unknown>[] : []);
  const refs = $derived.by(() => {
    const values = Array.isArray(resource?.object_refs) ? resource.object_refs : [];
    return values.filter((ref, index) => ref && typeof ref.kind === 'string' && typeof ref.id === 'string' && refPath(ref) && values.findIndex((other) => other?.kind === ref.kind && other?.id === ref.id) === index) as ResourceRef[];
  });
  function listURL(cursor?: string, limit?: string) {
    const next = new URLSearchParams(model.query);
    next.delete('cursor');
    if (cursor) next.set('cursor', cursor);
    if (limit) next.set('limit', limit);
    return base + (next.size ? `?${next}` : '');
  }
  function recordURL(record: Record<string, unknown>) {
    const path = refPath({kind: collection === 'audit' ? 'audit' : 'event', id: String(record.id ?? '')});
    return path ? path + model.query : null;
  }
  function text(value: unknown) { return value === null || value === undefined ? 'Unknown' : String(value); }
</script>

<header class="page-heading"><div><p class="eyebrow">Operations / Shared evidence</p><h1>{title}{detail ? ' record' : ''}</h1><p>Durable records shared by object details and Change Control.</p></div><button onclick={() => navigate(listURL())}>Refresh retained records</button></header>
{#if objectID}<section class="panel" aria-label="Evidence object scope"><h2>Object scope</h2><p class="mono">{objectID}</p><p>Exact management identity. A same-name replacement has separate records.</p><Link href={base}>Show all authorized {title}</Link></section>{/if}
{#if detail}<p><Link href={base + model.query}>Back to {objectID ? 'object-scoped ' : ''}{title}</Link></p>{/if}
<LoadNotice load={model.resource} />
{#if model.resource.error === 'CURSOR_EXPIRED'}<p class="notice warning">This page snapshot expired or its permissions or retention changed. Refresh retained records to restart within the same object scope.</p>{/if}
{#if resource}
  {#if Array.isArray(resource.items)}
    <p class="notice">Only authorized retained records are shown. Older MTU records may lack Interface associations. An empty result does not prove that no change occurred.</p>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (Shared evidence tables must be keyboard scrollable.) -->
    <div class="table-scroll" tabindex="0" role="region" aria-label="Shared evidence records"><table><thead><tr><th>Operation</th><th>Result / reason</th><th>Origin / time</th>{#if expert}<th>Correlation</th>{/if}</tr></thead><tbody>{#each records as record}<tr><th scope="row">{#if recordURL(record)}<Link href={recordURL(record)!}>{text(record.operation)}</Link>{:else}{text(record.operation)}{/if}</th><td><Status value={text(record.result)} /><small>{text(record.reason_code)}</small></td><td>{text(record.origin)}<small>{text(record.occurred_at)}</small></td>{#if expert}<td class="mono">{text(record.correlation_id)}</td>{/if}</tr>{:else}<tr><td colspan={expert ? 4 : 3}>No authorized retained records in this scope.</td></tr>{/each}</tbody></table></div>
    <div class="actions"><label>Records per page<select value={query.get('limit') ?? '100'} onchange={(event) => navigate(listURL(undefined, event.currentTarget.value))}><option value="2">2</option><option value="25">25</option><option value="100">100</option></select></label><button onclick={() => navigate(listURL())}>First page</button><button disabled={typeof resource.next_cursor !== 'string'} onclick={() => navigate(listURL(String(resource.next_cursor)))}>Next page</button><span>{resource.next_cursor ? 'More retained records in this snapshot.' : 'End of this retained snapshot.'}</span></div>
    {#if expert}<details><summary>Coverage and retention</summary><pre>{JSON.stringify({coverage: resource.coverage, retention: resource.retention, snapshot_id: resource.snapshot_id}, null, 2)}</pre></details>{/if}
  {:else if objectID && !refs.some((ref) => ref.id === objectID)}
    <p class="notice warning">This record does not contain the selected object identity. Return to the scoped list to review its retained records.</p>
  {:else}
    <section class="panel"><h2>{text(resource.operation)}</h2><dl><dt>Result</dt><dd><Status value={text(resource.result)} /></dd><dt>Reason</dt><dd>{text(resource.reason_code)}</dd><dt>Origin</dt><dd>{text(resource.origin)}</dd><dt>Actor attribution</dt><dd>{text(resource.actor_state)}</dd><dt>Recorded at</dt><dd>{text(resource.occurred_at)}</dd><dt>Correlation</dt><dd class="mono">{text(resource.correlation_id)}</dd></dl><h3>Linked resources</h3>{#each refs as ref}<p><Link href={refPath(ref)!}>Open {ref.kind} · {ref.id.slice(0, 8)}</Link></p>{:else}<p>No linked identity was recorded.</p>{/each}<p class="muted">A lifecycle record preserves its own result. Follow the transaction for current commit, Applied, confirmation and recovery state.</p>{#if expert}<details><summary>Authorized evidence record</summary><pre>{JSON.stringify(resource, null, 2)}</pre></details>{/if}</section>
  {/if}
{/if}
