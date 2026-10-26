<script lang="ts">
  import type { Resource, TopologyNode } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import { controller } from './model';
  import { has } from './policy';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import { tunnelMessage } from './tunnel';
  let {model, expert, desktop}: {model:Model;expert:boolean;desktop:boolean} = $props();
  const creating = $derived(model.path === '/switching/tunnels/new');
  const detail = $derived(!creating && model.path.split('/').length === 4);
  const data = $derived(model.resource.value);
  const item = $derived(detail ? data as unknown as Resource|null : null);
  const native = $derived(item?.native_tunnel);
  const items = $derived(!detail && !creating && Array.isArray(data?.items) ? data.items as Resource[] : []);
  const nodes = $derived(creating && Array.isArray(data?.nodes) ? data.nodes as TopologyNode[] : []);
  function record(value:unknown):Record<string,unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string,unknown> : {}; }
  const authority = $derived(record(data?.tunnel_authority));
  const targets = $derived(record(data?.tunnel_creation_targets));
  const types = $derived(creating && Array.isArray(data?.tunnel_runtime_types) ? data.tunnel_runtime_types.filter((v):v is string => typeof v === 'string' && ['vxlan','geneve','gre'].includes(v)) : ['vxlan','geneve','gre']);
  const source = $derived(record(item?.source));
  const candidate = $derived(model.workspace.value?.candidate);
  const blocked = $derived(model.busy || !!model.pending || !model.sessionReady || !candidate || !!candidate.consumed_by || candidate.intents.length !== 0);
  let initialized = $state('');
  let bridge = $state('');
  let name = $state('');
  let nativeType = $state('');
  let remote = $state('');
  let local = $state('');
  let key = $state('');
  let destination = $state('');
  let reviewed = $state(false);
  let deletionReviewed = $state(false);
  $effect(() => {
    const identity = creating ? 'new' : typeof item?.id === 'string' ? item.id : '';
    if (identity && initialized !== identity) {
      initialized = identity; reviewed = false; deletionReviewed = false;
      const config = native?.configuration;
      nativeType = config?.native_type ?? '';
      remote = config?.options.remote_ip ?? ''; local = config?.options.local_ip ?? '';
      key = config?.options.key ?? ''; destination = config?.options.dst_port ?? '';
      if (creating) { bridge = ''; name = ''; }
    }
  });
  const currentBridge = $derived(nodes.find(n => n.binding.table === 'Bridge' && n.binding.management_id === bridge));
  const names = $derived(Object.entries(targets).filter(([target, allowed]) => allowed === true && target.startsWith(bridge+':')).map(([target]) => target.slice(bridge.length+1)).sort());
  const available = $derived(desktop && model.resource.status === 'ready' && has(model.session,'workspace.write') && has(model.session,'ovs.tunnel.static.write') && (creating ? data?.tunnel_editable === true && !!currentBridge && authority[bridge] === true && targets[bridge+':'+name] === true : native?.execution.available === true));
  function options() { return {remote_ip:remote.trim() || null,local_ip:local.trim() || null,key:key.trim() || null,dst_port:destination.trim() || null}; }
  function stage(event:SubmitEvent, remove = false) {
    event.preventDefault(); if (!available || blocked || (remove ? !deletionReviewed : !reviewed)) return;
    const object = creating ? currentBridge?.binding : item && typeof item.management_id === 'string' && typeof item.ovs_uuid === 'string' && typeof item.instance_generation === 'string' ? {management_id:item.management_id,ovs_uuid:item.ovs_uuid,instance_generation:item.instance_generation,table:'Port'} : null;
    if (!object) return;
    void controller.stageNativeIntent({intent_id:crypto.randomUUID(),operation:remove?'tunnel.static.delete':creating?'tunnel.static.create':'tunnel.static.configure',object,topology:remove?{}:{...(creating?{name}:{}),tunnel:{native_type:nativeType,options:options()}}});
  }
  function next(cursor:string) { const q = new URLSearchParams(model.query);q.set('cursor',cursor);return '/switching/tunnels?'+q; }
  function observed(value:unknown):string {
    if(Array.isArray(value)) return value.length===1 ? observed(value[0]) : value.length===0 ? 'Not reported' : 'Unknown';
    return typeof value==='string' || typeof value==='number' ? String(value) : 'Unknown';
  }
</script>

<section aria-label="Native static tunnels">
  <p class="eyebrow">Switching</p><h1>Tunnels</h1>
  <p>Review native tunnel Ports and their endpoints. Configured endpoints and OVS port status do not prove remote reachability.</p>
  <LoadNotice load={model.resource}/>
  {#if !detail && !creating}
    {#if desktop && has(model.session,'workspace.write') && has(model.session,'ovs.tunnel.static.write')}<p><Link href="/switching/tunnels/new">Create static tunnel →</Link></p>{/if}
    <div class="table-scroll"><table><caption>Observed tunnel Ports in this page</caption><thead><tr><th>Tunnel</th><th>Type</th><th>Remote endpoint</th><th>Observation</th><th>Configuration</th></tr></thead><tbody>
      {#each items as entry(entry.id)}<tr><th scope="row"><Link href={`/switching/tunnels/${entry.id}`}>{typeof entry.name==='string'?entry.name:'Tunnel'}</Link></th><td>{entry.native_tunnel?.native_type??'Unknown'}</td><td>{entry.native_tunnel?.configuration?.options.remote_ip??'Unknown'}</td><td>{entry.state}</td><td>{entry.native_tunnel?.execution.available?'Review available':tunnelMessage(entry.native_tunnel?.execution.reason)}</td></tr>{/each}
    </tbody></table></div>
    {#if model.resource.status==='ready' && items.length===0}<p>No tunnel Ports in this observation.</p>{/if}
    {#if typeof data?.next_cursor==='string'}<Link href={next(data.next_cursor)}>Next page →</Link>{/if}
  {:else if creating || item}
    <Link href="/switching/tunnels">← Tunnels</Link>
    <h2>{creating?'Create static tunnel':typeof item?.name==='string'?item.name:'Tunnel details'}</h2>
    {#if !creating && native}
      <p>Native type: <strong>{native.native_type??'Unknown'}</strong> · Configuration: {native.configuration_availability}</p>
      <p>Observation: {typeof source.freshness==='string'?source.freshness:'unknown'} · {typeof source.observed_at==='string'?source.observed_at:'Unknown time'}</p>
      {#if source.freshness!=='fresh'}<p class="notice warning" role="status">Values are last observed. Configuration changes require a fresh native observation.</p>{/if}
      <dl><dt>Remote IP</dt><dd>{native.configuration?.options.remote_ip??'Unknown'}</dd><dt>Local IP</dt><dd>{native.configuration?.options.local_ip??'Native default (unset)'}</dd><dt>Key / VNI</dt><dd>{native.configuration?.options.key??'Native default (unset)'}</dd><dt>UDP destination port</dt><dd>{native.configuration?.options.dst_port??'Native default (unset)'}</dd></dl>
      <p>{#if native.bridge_ref}<Link href={`/bridges/${native.bridge_ref.id}`}>Review Bridge</Link> · {/if}<Link href={`/ports/${item?.id}`}>Review Port</Link>{#if native.interface_ref} · <Link href={`/interfaces/${native.interface_ref.id}`}>Review Interface</Link>{/if}</p>
      <p><Link href={`/operations/events?object_id=${item?.id}`}>Port events</Link> · <Link href={`/operations/audit?object_id=${item?.id}`}>Port audit</Link></p>
      {#if native.operational}<section class="panel"><h3>Native port status</h3><p>Observed port number: {observed(native.operational.ofport)} · Link: {observed(native.operational.link_state)}</p><p>Native status is a local OVS observation. Review routing and both tunnel endpoints separately.</p></section>{/if}
    {/if}
    {#if !desktop}<p>Review mode. Use a desktop to prepare a new tunnel change.</p>
    {:else if creating || native?.execution.available}
      <form onsubmit={event=>stage(event)} aria-label={creating?'Prepare static tunnel creation':'Prepare static tunnel change'}>
        <fieldset disabled={blocked || (creating ? data?.tunnel_editable!==true : !available)}><legend>Native static endpoints</legend>
          {#if creating}<label for="tunnel-bridge">Bridge</label><select id="tunnel-bridge" required bind:value={bridge} onchange={()=>{name='';reviewed=false;}}><option value="">Select Bridge</option>{#each nodes.filter(n=>n.binding.table==='Bridge') as node(node.binding.management_id)}<option value={node.binding.management_id}>{node.name}</option>{/each}</select><label for="tunnel-name">New immutable tunnel name</label><select id="tunnel-name" required bind:value={name}><option value="">Select available name</option>{#each names as value}<option {value}>{value}</option>{/each}</select>{#if bridge && names.length===0}<p>No new tunnel names are available for local management on this Bridge.</p>{/if}{/if}
          <label for="tunnel-type">Tunnel type</label><select id="tunnel-type" required bind:value={nativeType}><option value="">Select native type</option>{#each types as value}<option {value}>{value==='vxlan'?'VXLAN':value==='geneve'?'Geneve':'GRE'}</option>{/each}</select>
          <label for="tunnel-remote">Remote IP address</label><input id="tunnel-remote" required maxlength="128" placeholder="192.0.2.20" bind:value={remote}/>
          <label for="tunnel-local">Local IP address (optional)</label><input id="tunnel-local" maxlength="128" bind:value={local}/>
          <label for="tunnel-key">Key / VNI (optional)</label><input id="tunnel-key" inputmode="numeric" maxlength="10" pattern="[0-9]*" bind:value={key}/>
          <label for="tunnel-destination">UDP destination port (optional)</label><input id="tunnel-destination" inputmode="numeric" maxlength="5" pattern="[0-9]*" bind:value={destination}/>
          <p>VXLAN and Geneve keys use 24 bits; GRE uses 32 bits and has no UDP destination port. Empty optional fields preserve native unset semantics in review and recovery.</p>
          <p class="notice warning">Review endpoint routing and management connectivity. This change configures the tunnel Port only; underlay addresses and routes require their own workflow.</p>
          {#if blocked && candidate?.intents.length}<p>Review or discard the existing Candidate before preparing another tunnel change.</p>{/if}
          {#if creating && bridge && authority[bridge]!==true}<p>This Bridge has no current local management ownership or uses an unsupported native combination.</p>{/if}
          <label class="check"><input type="checkbox" bind:checked={reviewed}/>I reviewed the endpoints and management connectivity.</label>
          <button type="submit" disabled={!available || !reviewed}>Stage tunnel change</button>
        </fieldset>
      </form>
      {#if !creating}<form onsubmit={event=>stage(event,true)} aria-label="Prepare static tunnel deletion"><fieldset disabled={!available || blocked}><legend>Delete tunnel</legend><p>Deletes this Port and Interface. Recovery recreates deleted objects with fresh identities and the captured native configuration.</p><label class="check"><input type="checkbox" bind:checked={deletionReviewed}/>I reviewed the tunnel deletion and its affected connectivity.</label><button type="submit" disabled={!deletionReviewed}>Stage tunnel deletion</button></fieldset></form>{/if}
    {:else}<p role="status">{tunnelMessage(native?.execution.reason)}</p>{/if}
    {#if expert && !creating && native}<dl><dt>Management ID</dt><dd class="mono">{item?.id}</dd><dt>Native field group</dt><dd>Interface.type and static remote_ip / local_ip / key / dst_port options</dd><dt>Configuration boundary</dt><dd>{native.reason || native.execution.reason || 'Qualified for review'}</dd></dl>{/if}
  {/if}
</section>
