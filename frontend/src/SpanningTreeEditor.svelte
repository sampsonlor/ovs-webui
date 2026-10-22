<script lang="ts">
  import { untrack } from 'svelte';
  import type { SpanningTree } from '../../clients/typescript/public-v1.generated';
  import type { Model } from './model';
  import { controller } from './model';
  let { model, item, desktop, expert }: {model:Model;item:SpanningTree;desktop:boolean;expert:boolean}=$props();
  let protocol=$state<'disabled'|'stp'|'rstp'|''>('');
  let values=$state<Record<string,string>>({});
  let scope='';
  let error=$state('');
  let expanded=$state(false);
  const names:Record<string,string>={'stp-priority':'STP priority','stp-hello-time':'STP hello time (seconds)','stp-max-age':'STP max age (seconds)','stp-forward-delay':'STP forward delay (seconds)','rstp-priority':'RSTP priority','rstp-max-age':'RSTP max age (seconds)','rstp-forward-delay':'RSTP forward delay (seconds)'};
  const preparation=$derived(item.preparation);
  const executable=$derived(item.execution?.available === true);
  const operation=$derived(executable?'bridge.spanning-tree.configure':'bridge.spanning-tree.prepare');
  const c=$derived(model.workspace.value?.candidate);
  const workspaceConflict=$derived(!!c?.intents.length && (c.intents.length!==1 || c.intents[0].operation!==operation || c.intents[0].object.management_id!==item.management_id));
  const unavailable=$derived(!desktop || !(preparation?.available||executable) || !c || workspaceConflict || c.consumed_by!==null || model.busy || !!model.pending || !model.sessionReady);
  $effect(()=>{
    const id=item.management_id+'/'+item.instance_generation;
    if(scope!==id){
      scope=id;
      expanded=false;
      untrack(()=>{
        const p=item.spanning_tree.protocol;
        protocol=p==='disabled'||p==='stp'||p==='rstp'?p:'';
        values=Object.fromEntries(Object.keys(names).map(k=>[k,String(item.spanning_tree.configuration[k]?.value??'')]));
        error='';
      });
    }
  });
  function reason(code:string|undefined){
    if(code==='CAPABILITY_DENIED')return 'Your permissions allow observation. Preparing a draft requires separate permission.';
    if(code==='CONFIGURATION_WITHHELD')return 'Configuration and draft authorization are withheld with current permissions.';
    if(code==='SPANNING_TREE_OBSERVATION_STALE')return 'Wait for a fresh Bridge observation before preparing a draft.';
    if(code==='SPANNING_TREE_BRIDGE_AUTHORITY_REQUIRED')return 'This Bridge needs an independent authorization before a draft can be prepared.';
    return 'This Bridge configuration or its connected objects need additional verification before draft preparation is available.';
  }
  async function save(event:SubmitEvent){
    event.preventDefault();error='';
    if(unavailable||!protocol)return;
    const parameters:Record<string,string|null>={};
    for(const k of Object.keys(names).filter(k=>k.startsWith(protocol+'-'))){
      const value=values[k]??'';
      if(value!==''&&!/^[0-9]{1,128}$/.test(value)){error='Enter decimal digits, or leave a parameter blank to use the native default.';return;}
      parameters[k]=value===''?null:value;
    }
    await controller.stageSpanningTree(item,{protocol,parameters},executable);
  }
</script>

<section class="panel" aria-label={executable?'Spanning tree change preparation':'Spanning tree draft preparation'}>
  <h2>{executable?'Prepare a spanning tree change':'Prepare a configuration draft'}</h2>
  <p>Spanning tree changes can interrupt traffic and management connectivity. {executable?'Review the captured configuration and Validation before Safe Apply. A separate management reachability check and confirmation window protect each change. Protocol switches can interrupt traffic; installation does not prove network convergence.':'This Bridge supports preparing and reviewing a draft; execution and safe timer recovery need further verification.'}</p>
  {#if !(preparation?.available||executable)}<p class="notice" role="status">{reason(preparation?.reason)}</p>
  {:else if !desktop}<p class="notice" role="status">Review this Bridge here. Use a desktop to prepare a new spanning tree draft.</p>
  {:else}
    <details bind:open={expanded}><summary>{executable?'Prepare spanning tree change':'Prepare spanning tree draft'}</summary>
      {#if workspaceConflict}<p class="notice warning">Review or remove the existing Candidate intent before preparing a draft for this Bridge.</p>{/if}
      <form onsubmit={save}>
        <fieldset disabled={unavailable}>
          <legend>Bridge protocol and basic parameters</legend>
          <label for="tree-protocol">Spanning tree protocol</label><select id="tree-protocol" bind:value={protocol} required><option value="" disabled>Choose protocol</option><option value="disabled">Disabled</option><option value="stp">STP</option><option value="rstp">RSTP</option></select>
          <p>Blank parameters remove an explicit value and retain the native default. Inactive protocol settings are preserved and also checked during Validation.</p>
          {#each Object.keys(names).filter(k=>protocol!==''&&k.startsWith(protocol+'-')) as key}
            <label for={'draft-'+key}>{names[key]}</label><input id={'draft-'+key} inputmode="numeric" maxlength="128" placeholder="Native default (unset)" bind:value={values[key]} />
          {/each}
          {#if protocol==='rstp'}<p>RSTP hello time is fixed by the native protocol and has no editable Bridge parameter. Priority must be a multiple of 4096.</p>{/if}
          {#if protocol==='stp'}<p class="notice warning">Explicit STP hello time values of 2–10 seconds need independent runtime verification. Configured values alone do not prove installed timers.</p>{/if}
          {#if protocol==='disabled'}<p>Disabling preserves all captured basic parameter values. This draft does not disable the live Bridge.</p>{/if}
          <button type="submit" class="primary" disabled={!protocol}>{executable?'Stage spanning tree change':'Stage spanning tree draft'}</button>
        </fieldset>
        {#if error}<p class="notice warning" role="alert">{error}</p>{/if}
      </form>
    </details>
  {/if}
  {#if expert&&preparation}<p class="muted">{executable?'Change authority':'Draft authority'}: {preparation.authority} · {executable?'Safe Apply required':preparation.mode}{preparation.reason&&!executable?' · '+preparation.reason:''}</p>{/if}
</section>
