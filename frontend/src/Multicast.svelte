<script lang="ts">
  import type { Model } from './model';
  import type { Bridge } from '../../clients/typescript/public-v1.generated';
  import { controller } from './model';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import { multicastMessage } from './multicast';
  let {model,expert,desktop}:{model:Model;expert:boolean;desktop:boolean}=$props();
  const detail=$derived(model.path.split('/').length===4);
  const item=$derived(detail?model.resource.value as unknown as Bridge|null:null);
  const page=$derived(!detail?model.resource.value:null);
  const items=$derived(Array.isArray(page?.items)?page.items as Bridge[]:[]);
  const source=$derived(observation(item));
  function observation(bridge:Bridge|null){
    const value=bridge?.source;
    const current=value&&typeof value==='object'?value as Record<string,unknown>:{};
    return {freshness:typeof current.freshness==='string'?current.freshness:'unknown',provider:typeof current.provider_id==='string'?current.provider_id:'Unknown provider',time:typeof current.observed_at==='string'?current.observed_at:'Unknown time'};
  }
  const keys=['mcast-snooping-aging-time','mcast-snooping-table-size','mcast-snooping-disable-flood-unregistered'] as const;
  let initialized=$state('');
  let enabled=$state('enabled');
  let values=$state<Record<string,string|number|undefined>>({});
  $effect(()=>{
    if(item&&item.management_id!==initialized){
      initialized=item.management_id;
      enabled=item.multicast_config?.enabled?'enabled':'disabled';
      values=Object.fromEntries(keys.map(key=>[key,item.multicast_config?.parameters[key]??'']));
    }
  });
  const candidate=$derived(model.workspace.value?.candidate);
  const conflict=$derived(!!candidate?.intents.length&&(candidate.intents.length!==1||candidate.intents[0].operation!=='bridge.multicast.configure'||candidate.intents[0].object.management_id!==item?.management_id));
  const unavailable=$derived(!desktop||model.busy||!!model.pending||!model.sessionReady||!candidate||!!candidate.consumed_by||conflict);
  function status(bridge:Bridge){return bridge.multicast_config ? (bridge.multicast_config.enabled?'Enabled':'Disabled') : 'Unavailable or withheld';}
  function stage(event:SubmitEvent){
    event.preventDefault();if(!item?.multicast_execution?.available||unavailable)return;
    const existing=candidate?.intents.find(i=>i.operation==='bridge.multicast.configure'&&i.object.management_id===item.management_id);
    void controller.stageNativeIntent({intent_id:existing?.intent_id??crypto.randomUUID(),operation:'bridge.multicast.configure',object:{management_id:item.management_id,ovs_uuid:item.ovs_uuid,instance_generation:item.instance_generation,table:'Bridge'},multicast:{enabled:enabled==='enabled',parameters:Object.fromEntries(keys.map(key=>[key,String(values[key]??'').trim()||null]))}});
  }
  function next(cursor:string){const query=new URLSearchParams(model.query);query.set('cursor',cursor);return '/switching/multicast?'+query;}
</script>

<section aria-label="Native multicast snooping">
  <p class="eyebrow">Switching</p><h1>Multicast snooping</h1>
  <p>Review native Bridge IGMP / MLD snooping configuration and its connected Ports. Configuration does not prove group membership or packet delivery.</p>
  <LoadNotice load={model.resource}/>
  {#if !detail}
    <div class="table-scroll"><table><caption>Observed Bridges in this page</caption><thead><tr><th>Bridge</th><th>Snooping</th><th>Observation</th><th>Configuration</th></tr></thead><tbody>
      {#each items as bridge(bridge.management_id)}<tr><th scope="row"><Link href={`/switching/multicast/${bridge.management_id}`}>{bridge.name}</Link></th><td>{status(bridge)}</td><td>{observation(bridge).freshness}</td><td>{bridge.multicast_execution?.available?'Review available':multicastMessage(bridge.multicast_execution?.reason)}</td></tr>{/each}
    </tbody></table></div>
    {#if page&&items.length===0}<p>No Bridges in this observation.</p>{/if}
    {#if typeof page?.next_cursor==='string'}<Link href={next(page.next_cursor)}>Next page →</Link>{/if}
  {:else if item}
    <Link href="/switching/multicast">← Multicast snooping</Link><h2>{item.name}</h2>
    <p>Observed configuration: <strong>{status(item)}</strong></p>
    <p>Observation: {source.freshness} · {source.provider} · {source.time}</p>
    {#if source.freshness!=='fresh'}<p class="notice warning" role="status">This observation is not current. Values are last observed; configuration changes are unavailable.</p>{/if}
    <p><Link href={`/bridges/${item.management_id}`}>Review Bridge and Ports</Link> · <Link href={`/operations/events?object_id=${item.management_id}`}>Bridge events</Link> · <Link href={`/operations/audit?object_id=${item.management_id}`}>Bridge audit</Link></p>
    {#if item.multicast_config}<dl><dt>Entry aging time</dt><dd>{item.multicast_config.parameters['mcast-snooping-aging-time']??'Native default (unset)'} seconds</dd><dt>Group table capacity</dt><dd>{item.multicast_config.parameters['mcast-snooping-table-size']??'Native default (unset)'}</dd><dt>Restrict unregistered multicast to router ports</dt><dd>{item.multicast_config.parameters['mcast-snooping-disable-flood-unregistered']??'Native default (unset)'}</dd></dl>{/if}
    <section class="panel" aria-label="Multicast group observations"><h3>Observed groups</h3>
      <p>Group membership is a separate native observation. It does not prove that packets are currently delivered.</p>
      {#if item.multicast_groups?.availability==='known' && item.multicast_groups.items}
        <p>Group observation: {item.multicast_groups.source.freshness} · {item.multicast_groups.source.observed_at??'Unknown time'}</p>
        {#if item.multicast_groups.items.length===0}<p>No groups in this native observation.</p>
        {:else}<div class="table-scroll"><table><caption>Native group and router Port observations</caption><thead><tr><th>Group</th><th>Protocol</th><th>VLAN</th><th>Port</th><th>Age</th></tr></thead><tbody>
          {#each item.multicast_groups.items as group}<tr><td>{group.router?'Router / querier':group.address}</td><td>{group.router?'Native router entry':group.protocol}</td><td>{group.vlan}</td><td><Link href={`/ports/${group.port_ref.id}`}>{group.port_name}</Link>{#if expert}<span class="mono"> · ofport {group.ofport}</span>{/if}</td><td>{group.age_seconds} seconds</td></tr>{/each}
        </tbody></table></div>{/if}
      {:else}<p role="status">{multicastMessage(item.multicast_groups?.reason??'MULTICAST_GROUP_SOURCE_UNAVAILABLE')}</p>{#if expert&&item.multicast_groups?.reason}<code>{item.multicast_groups.reason}</code>{/if}{/if}
    </section>
    {#if !desktop}<p>Review mode. Use a desktop to prepare a change.</p>
    {:else if item.multicast_execution?.available}
      <form onsubmit={stage} aria-label="Prepare multicast change"><fieldset disabled={unavailable}><legend>Native snooping configuration</legend>
        {#if conflict}<p>Review or remove the existing Candidate intent before preparing this change.</p>{/if}
        <label for="mcast-enabled">Desired snooping</label><select id="mcast-enabled" bind:value={enabled}><option value="disabled">Disabled</option><option value="enabled">Enabled</option></select>
        <label for="mcast-aging">Entry aging time (seconds)</label><input id="mcast-aging" type="number" min="15" max="3600" step="1" placeholder="Native default (unset)" bind:value={values[keys[0]]}/>
        <label for="mcast-size">Group table capacity</label><input id="mcast-size" type="number" min="10" max="1000000" step="1" placeholder="Native default (unset)" bind:value={values[keys[1]]}/>
        <label for="mcast-flood">Restrict unregistered multicast to router ports</label><select id="mcast-flood" bind:value={values[keys[2]]}><option value="">Native default (unset)</option><option value="true">Yes</option><option value="false">No · flood to all eligible Ports</option></select>
        <p>Leaving a numeric field empty removes that native setting. Review listener and router connectivity in shared Diff / Validation before Safe Apply.</p>
        <button class="primary">Stage multicast change</button>
      </fieldset></form>
    {:else}<p role="status">Configuration unavailable: {multicastMessage(item.multicast_execution?.reason)}</p>{#if expert&&item.multicast_execution?.reason}<code>{item.multicast_execution.reason}</code>{/if}{/if}
    {#if expert}<dl><dt>Ownership</dt><dd>{item.multicast_execution?.authority??'Unknown'}</dd><dt>Native field</dt><dd>Bridge.mcast_snooping_enable and three basic other_config keys</dd><dt>Management ID</dt><dd class="mono">{item.management_id}</dd></dl>{/if}
  {/if}
</section>
