<script lang="ts">
  import type { MirrorConfiguration, MirrorPort, Resource, TopologyNode } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import { controller } from './model';
  import { has } from './policy';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import { mirrorMessage } from './mirror';
  let {model,expert,desktop}:{model:Model;expert:boolean;desktop:boolean}=$props();
  const creating=$derived(model.path==='/switching/mirrors/new');
  const detail=$derived(!creating && model.path.split('/').length===4);
  const data=$derived(model.resource.value);
  const item=$derived(detail?data as unknown as Resource|null:null);
  const native=$derived(item?.native_mirror);
  const counters=$derived(record(record(item?.fields).statistics));
  const items=$derived(!detail && !creating && Array.isArray(data?.items)?data.items as Resource[]:[]);
  const nodes=$derived(creating && Array.isArray(data?.nodes)?data.nodes as TopologyNode[]:[]);
  function record(value:unknown):Record<string,unknown>{return value && typeof value==='object' && !Array.isArray(value)?value as Record<string,unknown>:{};}
  const authority=$derived(record(data?.mirror_authority));
  const targets=$derived(record(data?.mirror_creation_targets));
  const candidate=$derived(model.workspace.value?.candidate);
  const blocked=$derived(model.busy || !!model.pending || !model.sessionReady || !candidate || !!candidate.consumed_by || candidate.intents.length!==0);
  let initialized=$state(''),bridge=$state(''),name=$state('');
  let selectAll=$state(false),sources=$state<string[]>([]),destinations=$state<string[]>([]),vlans=$state('');
  let outputKind=$state('port'),outputPort=$state(''),outputVLAN=$state(''),snaplen=$state('');
  let reviewed=$state(false),deletionReviewed=$state(false);
  const currentBridge=$derived(nodes.find(n=>n.binding.table==='Bridge' && n.binding.management_id===bridge));
  const names=$derived(Object.entries(targets).filter(([target,allowed])=>allowed===true && target.startsWith(bridge+':')).map(([target])=>target.slice(bridge.length+1)).sort());
  const ports=$derived.by(()=>{
    if(!creating)return native?.ports??[];
    return (currentBridge?.links??[]).flatMap(binding=>{
      const node=nodes.find(n=>n.binding.table==='Port' && n.binding.management_id===binding.management_id);
      if(!node || node.binding.table!=='Port')return [];
      const editable=authority[binding.management_id]===true && node.links.length===1 && authority[node.links[0].management_id]===true;
      return [{binding:{...binding,table:'Port' as const},name:node.name,editable,output_eligible:editable && node.name!==currentBridge?.name} satisfies MirrorPort];
    });
  });
  $effect(()=>{
    const identity=creating?'new':item?`${item.id}:${String(item.config_revision)}`:'';
    if(identity && identity!==initialized){
      initialized=identity;reviewed=false;deletionReviewed=false;
      const config=native?.configuration;
      selectAll=config?.select_all??false;sources=[...config?.source_port_ids??[]];destinations=[...config?.destination_port_ids??[]];
      vlans=config?.select_vlans.join(', ')??'';outputKind=config?.output_vlan!==null && config?.output_vlan!==undefined?'vlan':'port';
      outputPort=config?.output_port_id??'';outputVLAN=config?.output_vlan??'';snaplen=config?.snaplen??'';
      if(creating){bridge='';name='';}
    }
  });
  $effect(()=>{void [bridge,name,selectAll,sources.join(','),destinations.join(','),vlans,outputKind,outputPort,outputVLAN,snaplen];reviewed=false;});
  const available=$derived(desktop && model.resource.status==='ready' && has(model.session,'workspace.write') && has(model.session,'ovs.mirror.write') && (creating?data?.mirror_editable===true && !!currentBridge && authority[bridge]===true && targets[bridge+':'+name]===true:native?.execution.available===true));
  function config():MirrorConfiguration{return {select_all:selectAll,source_port_ids:selectAll?[]:[...sources],destination_port_ids:selectAll?[]:[...destinations],select_vlans:vlans.trim()?vlans.split(',').map(v=>v.trim()):[],output_port_id:outputKind==='port'?outputPort||null:null,output_vlan:outputKind==='vlan'?outputVLAN.trim()||null:null,snaplen:snaplen.trim()||null};}
  function stage(event:SubmitEvent,remove=false){
    event.preventDefault();if(!available || blocked || (remove?!deletionReviewed:!reviewed))return;
    const object=creating?currentBridge?.binding:item && typeof item.ovs_uuid==='string' && typeof item.instance_generation==='string'?{management_id:item.id,ovs_uuid:item.ovs_uuid,instance_generation:item.instance_generation,table:'Mirror'}:null;
    if(!object)return;
    void controller.stageNativeIntent({intent_id:crypto.randomUUID(),operation:remove?'mirror.session.delete':creating?'mirror.session.create':'mirror.session.configure',object,topology:remove?{}:{...(creating?{name}:{}),mirror:config()}});
  }
  function portName(id:string){return ports.find(p=>p.binding.management_id===id)?.name??'Unavailable Port';}
  function selectors(ids:string[]){return ids.length?ids.map(portName).join(', '):'None';}
  function next(cursor:string){const q=new URLSearchParams(model.query);q.set('cursor',cursor);return '/switching/mirrors?'+q;}
</script>

<section aria-label="Native Mirror sessions">
  <p class="eyebrow">Switching</p><h1>Mirrors</h1>
  <p>Review native Bridge Mirror sessions, traffic selectors and copy destinations. Configuration and counters do not prove packet delivery.</p>
  <LoadNotice load={model.resource}/>
  {#if !creating && !detail}
    {#if desktop && has(model.session,'workspace.write') && has(model.session,'ovs.mirror.write')}<p><Link href="/switching/mirrors/new">Create Mirror session →</Link></p>{/if}
    <div class="table-scroll"><table><caption>Observed native Mirror sessions in this page</caption><thead><tr><th>Session</th><th>Observation</th><th>Configuration</th></tr></thead><tbody>
      {#each items as entry(entry.id)}<tr><th scope="row"><Link href={`/switching/mirrors/${entry.id}`}>{typeof entry.name==='string'?entry.name:'Mirror session'}</Link></th><td>{entry.state}</td><td>{entry.native_mirror?.execution.available?'Review available':mirrorMessage(entry.native_mirror?.execution.reason)}</td></tr>{/each}
    </tbody></table></div>
    {#if model.resource.status==='ready' && items.length===0}<p>No Mirror sessions in this observation.</p>{/if}
    {#if typeof data?.next_cursor==='string'}<Link href={next(data.next_cursor)}>Next page →</Link>{/if}
  {:else if creating || item}
    <Link href="/switching/mirrors">← Mirrors</Link><h2>{creating?'Create Mirror session':typeof item?.name==='string'?item.name:'Mirror details'}</h2>
    {#if creating}<p>This selector lists locally provisioned Mirror Bridges and their observed Ports. Validation checks the complete native dependency graph.</p>{/if}
    {#if creating && model.resource.status==='ready' && data?.mirror_editable!==true}<p role="status">Creation is unavailable. Review a fresh, complete Bridge configuration and local management ownership.</p>{/if}
    {#if !creating && native}
      <p>Configuration: {native.configuration_availability} · Observation: {item?.state}</p>
      {#if native.configuration}{@const c=native.configuration}<dl><dt>Source traffic entering Ports</dt><dd>{c.select_all?'All eligible Bridge Ports':selectors(c.source_port_ids)}</dd><dt>Destination traffic leaving Ports</dt><dd>{c.select_all?'All eligible Bridge Ports':selectors(c.destination_port_ids)}</dd><dt>VLAN filter</dt><dd>{c.select_vlans.length?c.select_vlans.join(', '):'All VLANs'}</dd><dt>Copy destination</dt><dd>{c.output_port_id?portName(c.output_port_id):c.output_vlan?`RSPAN VLAN ${c.output_vlan}`:'Unknown'}</dd><dt>Snap length</dt><dd>{c.snaplen?`${c.snaplen} bytes`:'Native default (unset)'}</dd></dl>{/if}
      {#if native.bridge_ref}<p><Link href={`/bridges/${native.bridge_ref.id}`}>Review Bridge and Ports</Link></p>{/if}
      <p><Link href={`/operations/events?object_id=${item?.id}`}>Mirror events</Link> · <Link href={`/operations/audit?object_id=${item?.id}`}>Mirror audit</Link></p>
      <section class="panel"><h3>Native copy counters</h3><p>Daemon observations are cumulative counters and do not establish delivery at the receiver.</p>{#if counters.availability==='known'}<dl>{#each Object.entries(record(counters.value)) as [key,value]}<dt>{key}</dt><dd>{typeof value==='string' || typeof value==='number'?String(value):'Unknown'}</dd>{/each}</dl>{#if Object.keys(record(counters.value)).length===0}<p>No counters reported by this native observation.</p>{/if}{:else}<p>Copy counters are unavailable in this observation.</p>{/if}</section>
      {#if item?.state!=='fresh'}<p class="notice warning">Values are last observed. Configuration changes require a fresh observation.</p>{/if}
    {/if}
    {#if !desktop}<p>Review mode. Use a desktop to prepare a new Mirror change.</p>
    {:else if creating || native?.execution.available}
      <form onsubmit={event=>stage(event)} aria-label={creating?'Prepare Mirror creation':'Prepare Mirror change'}><fieldset disabled={blocked || (creating?data?.mirror_editable!==true:!available)}><legend>Native Mirror selectors and output</legend>
        {#if creating}<label for="mirror-bridge">Bridge</label><select id="mirror-bridge" required bind:value={bridge} onchange={()=>{name='';sources=[];destinations=[];outputPort='';}}><option value="">Select Bridge</option>{#each nodes.filter(n=>n.binding.table==='Bridge') as node(node.binding.management_id)}<option value={node.binding.management_id}>{node.name}</option>{/each}</select><label for="mirror-name">New immutable session name</label><select id="mirror-name" required bind:value={name}><option value="">Select available name</option>{#each names as value}<option {value}>{value}</option>{/each}</select>{#if bridge && names.length===0}<p>No session names are available for local management on this Bridge.</p>{/if}{/if}
        <label class="check"><input type="checkbox" bind:checked={selectAll}/>Select all Bridge Ports in both directions</label>
        {#if !selectAll}<fieldset><legend>Traffic entering source Ports</legend>{#each ports as port(port.binding.management_id)}<label class="check"><input type="checkbox" value={port.binding.management_id} bind:group={sources} disabled={!port.editable}/>{port.name}</label>{/each}</fieldset><fieldset><legend>Traffic leaving destination Ports</legend>{#each ports as port(port.binding.management_id)}<label class="check"><input type="checkbox" value={port.binding.management_id} bind:group={destinations} disabled={!port.editable}/>{port.name}</label>{/each}</fieldset>{/if}
        <label for="mirror-vlans">VLAN filter (optional, comma separated)</label><input id="mirror-vlans" maxlength="1535" bind:value={vlans}/><p>Empty selects all VLANs; 0 selects untagged traffic. The VLAN filter also applies when all Ports are selected.</p>
        <label for="mirror-output-kind">Copy destination type</label><select id="mirror-output-kind" bind:value={outputKind}><option value="port">Reserved output Port</option><option value="vlan">RSPAN VLAN</option></select>
        {#if outputKind==='port'}<label for="mirror-output-port">Output Port</label><select id="mirror-output-port" required bind:value={outputPort}><option value="">Select output Port</option>{#each ports.filter(p=>p.output_eligible) as port(port.binding.management_id)}<option value={port.binding.management_id}>{port.name}</option>{/each}</select><p class="notice warning">OVS reserves this Port for mirrored traffic and discards frames received on it. Review management connectivity before choosing an output.</p>{:else}<label for="mirror-output-vlan">RSPAN VLAN</label><input id="mirror-output-vlan" required inputmode="numeric" pattern="[0-9]+" maxlength="4" bind:value={outputVLAN}/><p class="notice warning">Copies can leave multiple eligible Bridge Ports. Review VLAN membership, loop risk and every connected device before using RSPAN.</p>{/if}
        <label for="mirror-snaplen">Snap length in bytes (optional)</label><input id="mirror-snaplen" inputmode="numeric" pattern="[0-9]*" maxlength="5" bind:value={snaplen}/><p>Use 14–65535 bytes, or leave empty for native unset. Broad Port/VLAN selection can increase load.</p>
        {#if blocked && candidate?.intents.length}<p>Review or discard the existing Candidate before preparing another Mirror change.</p>{/if}
        <label class="check"><input type="checkbox" bind:checked={reviewed}/>I reviewed the selectors, copy destination and management connectivity.</label><button type="submit" disabled={!available || !reviewed}>Stage Mirror change</button>
      </fieldset></form>
      {#if !creating}<form onsubmit={event=>stage(event,true)} aria-label="Prepare Mirror deletion"><fieldset disabled={!available || blocked}><legend>Delete Mirror session</legend><p>Removes this session from its Bridge. Recovery recreates the captured session with a fresh identity.</p><label class="check"><input type="checkbox" bind:checked={deletionReviewed}/>I reviewed the Mirror deletion and affected monitoring.</label><button type="submit" disabled={!deletionReviewed}>Stage Mirror deletion</button></fieldset></form>{/if}
    {:else}<p role="status">{mirrorMessage(native?.execution.reason || native?.reason)}</p>{/if}
    {#if expert && !creating && native}<dl><dt>Management ID</dt><dd class="mono">{item?.id}</dd><dt>Native fields</dt><dd>Mirror selectors, output_port / output_vlan and snaplen; Bridge.mirrors membership</dd><dt>Configuration boundary</dt><dd>{native.reason || native.execution.reason || 'Qualified for review'}</dd></dl>{/if}
  {/if}
</section>
