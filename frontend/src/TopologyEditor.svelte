<script lang="ts">
  import { untrack } from 'svelte';
  import type { Model } from './model';
  import { controller } from './model';
  import { has } from './policy';
  import type { ObjectBinding, TopologyNode, TopologyRequest } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  let {model, desktop, expert}:{model:Model;desktop:boolean;expert:boolean}=$props();
  const data=$derived(model.resource.value);
  const nodes=$derived((Array.isArray(data?.nodes)?data.nodes:[]) as TopologyNode[]);
  const authority=$derived((data?.authority??{}) as Record<string,boolean>);
  let selected=$state(untrack(()=>new URLSearchParams(model.query).get('target')??''));
  let operation=$state('port.create');
  let name=$state('');
  let nativeType=$state<'internal'|'system'>('internal');
  let interfaceNames=$state('');
  let destination=$state('');
  let peer=$state('');
  let ofport=$state('');
  let sources=$state<string[]>([]);
  let members=$state<string[]>([]);
  let reviewed=$state(false);
  let error=$state('');
  const current=$derived(nodes.find(n=>n.binding.management_id===selected));
  const creatingBridge=$derived(operation==='bridge.create-isolated');
  const objectTable=$derived(operation.startsWith('interface.')?'Interface':operation==='port.create'||operation==='bond.create'||operation==='bridge.delete-tree'?'Bridge':'Port');
  const reason=$derived(!desktop?'New topology changes require desktop. Tablet and mobile support review and existing Safe Apply recovery.':!model.sessionReady?'Current session is unavailable.':!has(model.session,'workspace.write')?'Workspace write permission is required.':model.resource.status!=='ready'||data?.state!=='fresh'?'Current provider evidence is unavailable or stale.':creatingBridge?(!has(model.session,'ovs.bridge.create')?'Bridge creation permission is required.':''):data?.editable!==true?'Current schema, provider, topology permission or root authority is unavailable.':!current||current.binding.table!==objectTable?'Select an existing immutable object.':!authority[selected]?'This object has no root topology authority, has foreign domain dependencies or uses an unsupported device type.':'');
  const operations=[['bridge.create-isolated','Create isolated Bridge'],['port.create','Create Port + Interface'],['bond.create','Create Bond Port + Interfaces'],['port.delete','Delete Port + member Interfaces'],['bridge.delete-tree','Delete Bridge + its Ports and Interfaces'],['port.move','Move Port to another Bridge'],['bond.members.set','Change Bond members explicitly'],['interface.ofport.set','Request OpenFlow port number'],['interface.ofport.clear','Use automatic OpenFlow allocation'],['interface.patch.connect','Convert two internal Interfaces to reciprocal Patch peers'],['interface.patch.disconnect','Convert reciprocal Patch peers to internal Interfaces']];
  function binding(id:string):ObjectBinding {
    const n=nodes.find(n=>n.binding.management_id===id);if(!n)throw new Error('Selected object is no longer present. Refresh and review the topology.');return n.binding;
  }
  function objectPath(n:TopologyNode) {return `/${({Bridge:'bridges',Port:'ports',Interface:'interfaces'} as Record<string,string>)[n.binding.table]}/${n.binding.management_id}`;}
  async function stage(event:SubmitEvent) {
    event.preventDefault();error='';if(reason||model.busy||model.pending||!reviewed)return;
    try {
      let intent:object;
      if(creatingBridge) intent={intent_id:crypto.randomUUID(),operation,name};
      else {
        const request:TopologyRequest={};
        if(operation==='port.create'||operation==='bond.create') {request.name=name;request.native_type=operation==='bond.create'?'system':nativeType;if(operation==='bond.create')request.interface_names=interfaceNames.split(',').map(v=>v.trim()).filter(Boolean);}
        if(operation==='port.move')request.destination_bridge=binding(destination);
        if(operation==='bond.members.set'){request.source_ports=sources.map(binding);request.member_interfaces=members.map(binding);}
        if(operation==='interface.ofport.set'){if(!/^[1-9][0-9]*$/.test(ofport)||Number(ofport)>65279)throw new Error('Enter an integer from 1 to 65279.');request.ofport_request=Number(ofport);}
        if(operation.startsWith('interface.patch.'))request.peer=binding(peer);
        intent={intent_id:crypto.randomUUID(),operation,object:binding(selected),topology:request};
      }
      await controller.stageNativeIntent(intent);
    }catch(e){error=e instanceof Error?e.message:'Unable to prepare the topology change.';}
  }
</script>

<header class="page-heading"><div><p class="eyebrow">Switching / Core topology</p><h1>Topology and native operations</h1><p>Review Bridge → Port → Interface relationships. A Bond is a Port with multiple Interfaces.</p></div><Link href="/interfaces">Interface inventory →</Link></header>
<LoadNotice load={model.resource}/>
{#if data}
  <p class="notice warning">{String(data.review_policy??'Topology changes require High-risk Safe Apply.')}</p>
  {#if data.truncated}<p class="notice warning" role="alert">This bounded editor shows 64 objects. Configuration is disabled because the selector is incomplete. Use the paginated inventory to review the full switch.</p>{/if}
  <section class="panel"><h2>Current native relationships</h2>
    {#each nodes.filter(n=>n.binding.table==='Bridge') as bridge (bridge.binding.management_id)}
      <details open><summary><Link href={objectPath(bridge)}>{bridge.name}</Link> · Bridge · {bridge.native_type || 'native system default'}</summary><ul>
        {#each bridge.links as portBinding (portBinding.management_id)}
          {@const port=nodes.find(n=>n.binding.management_id===portBinding.management_id)}
          <li>{#if port}<Link href={objectPath(port)}>{port.name}</Link> · {port.links.length>1?'Bond Port':'Port'}<ul>{#each port.links as ifaceBinding (ifaceBinding.management_id)}{@const iface=nodes.find(n=>n.binding.management_id===ifaceBinding.management_id)}<li>{#if iface}<Link href={objectPath(iface)}>{iface.name}</Link> · Interface · {iface.native_type||'native system default'}{#if expert}<small class="mono">{iface.binding.management_id}</small>{/if}{:else}Interface outside this bounded view{/if}</li>{/each}</ul>{:else}Port outside this bounded view{/if}</li>
        {/each}
      </ul></details>
    {/each}
  </section>
  {#if reason}<p class="notice warning" role="status">{reason}</p>{/if}
  <form class="panel form-panel" onsubmit={stage}>
    <h2>Prepare a semantic change</h2>
    <label for="topology-operation">Operation</label><select id="topology-operation" bind:value={operation} onchange={()=>{sources=[];members=[];reviewed=false;}}>{#each operations as [value,label]}<option {value}>{label}</option>{/each}</select>
    {#if !creatingBridge}<label for="topology-object">Existing object</label><select id="topology-object" bind:value={selected} onchange={()=>{sources=[];members=[];reviewed=false;}}><option value="">Select object</option>{#each nodes.filter(n=>n.binding.table===objectTable) as node (node.binding.management_id)}<option value={node.binding.management_id}>{node.binding.table} · {node.name} · {node.binding.management_id.slice(0,8)}</option>{/each}</select>{/if}
    <fieldset disabled={!!reason||model.busy||!!model.pending||!model.sessionReady}>
      <legend>Change parameters</legend>
      {#if creatingBridge||operation==='port.create'||operation==='bond.create'}
        <label for="topology-name">New immutable name</label><input id="topology-name" required maxlength="15" pattern="[a-zA-Z][a-zA-Z0-9_.-]*" bind:value={name}/>
        <p>A new name requires a root creation grant. Renaming an existing identity is unavailable; delete and recreate through reviewed Candidates.</p>
      {/if}
      {#if operation==='port.create'}<label for="topology-type">Interface type</label><select id="topology-type" bind:value={nativeType}><option value="internal">Internal virtual Interface</option><option value="system">Existing Linux system device</option></select>{/if}
      {#if operation==='bond.create'}<label for="topology-interfaces">Existing Linux device names, comma separated</label><input id="topology-interfaces" required bind:value={interfaceNames}/><p>2–8 explicitly granted, unattached devices. Creates active-backup with LACP off. Review Bond/LACP and VLAN changes separately after creation.</p>{/if}
      {#if operation==='port.move'}<label for="topology-destination">Destination Bridge</label><select id="topology-destination" required bind:value={destination}><option value="">Select Bridge</option>{#each nodes.filter(n=>n.binding.table==='Bridge') as node}<option value={node.binding.management_id}>{node.name} · {node.binding.management_id.slice(0,8)}</option>{/each}</select>{/if}
      {#if operation==='bond.members.set'}
        <p>Select the complete desired member set. Adding a member requires its standalone source Port to be explicitly selected. Removing a member creates a separate Port and requires a new name grant; it is never silently deleted.</p>
        <fieldset><legend>Explicit source Ports to consume</legend>{#each nodes.filter(n=>n.binding.table==='Port'&&n.binding.management_id!==selected&&n.links.length===1) as node}<label><input type="checkbox" value={node.binding.management_id} bind:group={sources}/>{node.name}</label>{/each}</fieldset>
        <fieldset><legend>Complete desired Interface membership</legend>{#each nodes.filter(n=>n.binding.table==='Interface'&&(n.native_type===''||n.native_type==='system')) as node}<label><input type="checkbox" value={node.binding.management_id} bind:group={members}/>{node.name}</label>{/each}</fieldset>
      {/if}
      {#if operation==='interface.ofport.set'}<label for="topology-ofport">Requested OpenFlow port number</label><input id="topology-ofport" inputmode="numeric" required bind:value={ofport}/><p>1–65279. Requested and actual allocation remain separate; confirmation needs actual allocation proof.</p>{/if}
      {#if operation.startsWith('interface.patch.')}<label for="topology-peer">Explicit peer Interface</label><select id="topology-peer" required bind:value={peer}><option value="">Select Interface</option>{#each nodes.filter(n=>n.binding.table==='Interface'&&n.binding.management_id!==selected) as node}<option value={node.binding.management_id}>{node.name} · {node.native_type}</option>{/each}</select><p>Only standalone, non-local internal ↔ reciprocal Patch pairs with compatible empty MTU/allocation and disabled policing configuration. Provider-specific, tunnel and DPDK transitions require their own domain workflow.</p>{/if}
      {#if operation.includes('delete')}<p class="notice warning">This deletes native objects. Strong-reference garbage collection is verified. Recovery reserves fresh identities; historical links remain attached to the deleted objects.</p>{/if}
      <label><input type="checkbox" required bind:checked={reviewed}/>I have reviewed the selected identities, source ownership, management path and sole-uplink risk.</label>
      <button class="primary" type="submit" disabled={!reviewed}>Stage in Candidate</button>
    </fieldset>
    {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
    <p>Staging changes the shared Candidate only. Review Diff and Validation before Safe Apply. Unknown configuration is preserved by the server; Standard and Expert use the same permissions.</p>
  </form>
{/if}
