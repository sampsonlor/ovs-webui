<script lang="ts">
  import type { DiffField } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import TopologyDiff from './TopologyDiff.svelte';
  let { fields, expert = false }: { fields: DiffField[]; expert?: boolean } = $props();
  const internalPorts = $derived(fields.filter((field) => field.operation === 'port.create-internal'));
  const portDeletions = $derived(fields.filter((field) => field.operation === 'port.delete-internal'));
  const deletions = $derived(fields.filter((field) => field.operation === 'bridge.delete-isolated'));
  const qinq = $derived(fields.filter((field) => field.field === 'qinq_context'));
  const mtu = $derived(fields.some((field) => field.operation === 'interface.mtu.set' || field.operation === 'interface.mtu.clear'));
  const policing = $derived(fields.some((field) => field.operation === 'interface.policing.set'));
  function display(value: unknown, field: DiffField, side: 'before' | 'current' | 'after') {
    if (field.operation === 'interface.policing.set' && value !== null && value !== undefined) {
      if (field.field.endsWith('_burst') && value === 0) return '0 · OVS-selected default preserved';
      if (field.field === 'ingress_policing_rate') return `${value} kbit/s${value === 0 ? ' · disabled' : ''}`;
      if (field.field === 'ingress_policing_kpkts_rate') return `${value} kpps${value === 0 ? ' · disabled' : ''}`;
    }
    if (field.field === 'mtu_request' && value === null) return field.conflict && side === 'current' ? 'Automatic / unavailable · review conflict' : 'Automatic · empty native request';
    if (field.field === 'qinq_context' && value && typeof value === 'object') {
      const context = value as Record<string, unknown>;
      return `TPID ${context.ethertype ?? '802.1ad (native default)'} · preserved`;
    }
    if (Array.isArray(value) && value.length === 0 && ['trunks', 'cvlans'].includes(field.field)) {
      let mode = fields.find((other) => other.intent_id === field.intent_id && other.field === 'vlan_mode')?.[side];
      if (mode === null) {
        const tag = fields.find((other) => other.intent_id === field.intent_id && other.field === 'tag')?.[side];
        if (tag !== undefined) mode = tag === null ? 'trunk' : 'access';
      }
      if (field.field === 'cvlans') return mode === 'dot1q-tunnel' ? '∅ · all customer VLANs' : '∅ · not used';
      return ['trunk', 'native-tagged', 'native-untagged'].includes(String(mode)) ? '∅ · all VLANs' : '∅ · not used';
    }
    if (field.operation === 'port.delete-internal') {
      if (value === 'absent') return 'Not present';
      if (value && typeof value === 'object') {
        const graph = value as Record<string, unknown>;
        if ('state' in graph) return 'Removed · rollback recreates with new identities';
        const config = graph.configuration as Record<string, unknown>;
        return `${String(config.bridge_name)} → ${String(config.name)} · internal · access VLAN ${String(config.vlan_id)}`;
      }
    }
    if (field.operation === 'port.create-internal' && value === 'absent') return 'Not present';
    if (field.operation === 'port.create-internal' && value === 'name occupied') return 'Name in use';
    if (field.operation === 'port.create-internal' && value && typeof value === 'object') {
      const port = value as Record<string, unknown>;
      return `${String(port.bridge)} → ${String(port.name)} · internal · access VLAN ${String(port.tag)}`;
    }
    if (field.operation === 'bridge.delete-isolated' && value && typeof value === 'object') {
      const graph = value as Record<string, unknown>;
      return 'state' in graph
        ? 'Removed · rollback recreates with new identities'
        : String(graph.name) + ' · Bridge → local Port → internal Interface';
    }
    return text(value, field.field);
  }
  const text = (value: unknown, field: string) =>
    value === null || value === undefined
      ? 'Native default / unknown'
      : Array.isArray(value) && value.length === 0
        ? field === 'trunks'
          ? '∅ · all VLANs'
          : '∅'
        : JSON.stringify(value);
</script>

{#if mtu}<div class="notice warning"><strong>Interface MTU request</strong><p>Values are bytes. Review the peer and management path before Safe Apply. The confirmation window waits for the actual device MTU; automatic mode depends on the current Bridge devices. Rollback restores the original explicit or empty request with captured dependencies and verifies the actual device value.</p></div>{/if}
{#if policing}<div class="notice warning"><strong>Interface ingress policing</strong><p>Bandwidth is kbit/s; packet rate is kpps. Only one rate is enabled. Both burst values remain zero, preserving OVS-selected defaults. Review the management path before Safe Apply. The server separately checks supported installed Linux rules and native configuration before confirmation and after rollback; this does not measure actual traffic effectiveness. Changed attachment or conflicting rules require a new review.</p></div>{/if}
{#if qinq.length}
  <div class="notice warning"><strong>QinQ service and customer VLANs</strong><p>The service VLAN is the outer tag. An empty customer VLAN list permits all customer VLANs. The native TPID is preserved; external dependency changes block reuse of this review and guarded rollback.</p></div>
  {#if expert}{#each qinq as field}<details><summary>QinQ native dependency evidence</summary><pre>{JSON.stringify({ original: field.before, current: field.current }, null, 2)}</pre></details>{/each}{/if}
{/if}
{#if internalPorts.length}
  <div class="notice">
    <strong>New internal access Port and Interface</strong>
    <p>The existing Bridge and local port are preserved. Rollback removes the new pair only while its configuration and parent membership are unchanged. Host IP configuration is a separate operation.</p>
  </div>
  {#if expert}{#each internalPorts as field}<details><summary>Parent binding and new object identities</summary><pre>{JSON.stringify({ port: field.object, target: field.after }, null, 2)}</pre></details>{/each}{/if}
{/if}

{#if portDeletions.length}
  <div class="notice warning">
    <strong>Delete internal access Port and Interface</strong>
    <p>The parent Bridge, local port and other members are preserved. Rollback recreates this pair with new identities and the same access VLAN. Changed configuration, references or host dependencies block deletion or recovery.</p>
  </div>
  {#if expert}{#each portDeletions as field}<details><summary>Original and reserved replacement identities</summary><pre>{JSON.stringify({ original: field.before, proposed: field.after }, null, 2)}</pre></details>{/each}{/if}
{/if}

{#if deletions.length}
  <div class="notice warning">
    <strong>Rollback recreates with new identities</strong>
    <p>Deletion removes the isolated Bridge and its local Port and Interface. Recovery creates new objects; old identities remain retired. Changed configuration or external dependencies block deletion or recovery.</p>
  </div>
  {#if expert}
    {#each deletions as field}
      <details>
        <summary>Original and reserved replacement identities</summary>
        <pre>{JSON.stringify({ original: field.before, proposed: field.after }, null, 2)}</pre>
      </details>
    {/each}
  {/if}
{/if}

{#each fields.filter(field => field.field === 'native_topology') as field}<TopologyDiff {field} {expert}/>{/each}
{#if fields.some(field => field.field !== 'native_topology') || fields.length === 0}
<!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll a wide Diff.) -->
<div class="table-scroll" tabindex="0" role="region" aria-label="Configuration Diff">
  <table>
    <caption>Original → Current → Yours · values supplied by the server</caption><thead
      ><tr><th>Object / field</th><th>Original</th><th>Current</th><th>Yours</th></tr
      ></thead
    ><tbody>
      {#each fields.filter(field => field.field !== 'native_topology') as field}
        <tr class:conflict={field.conflict}
          ><th scope="row"
            >{#if field.object.table === 'Port' && !['port.create-internal', 'port.delete-internal'].includes(field.operation ?? '')}<Link href={`/ports/${field.object.management_id}`}
              >{field.object.management_id.slice(0, 8)}</Link
            >{:else if field.object.table === 'Interface'}<Link href={`/interfaces/${field.object.management_id}`}>Interface · {field.object.management_id.slice(0, 8)}</Link>{:else}<span>{field.object.table} · {field.object.management_id.slice(0, 8)}</span>{/if}<br />{field.operation === 'port.create-internal' ? 'Create internal Port' : field.operation === 'port.delete-internal' ? 'Delete internal Port' : field.field === 'mtu_default_dependency' ? 'Automatic MTU dependency (bytes)' : field.field}{#if field.conflict}<span class="badge">Conflict</span
              >{/if}</th
          ><td>{display(field.before, field, 'before')}</td><td
            >{display(field.current, field, 'current')}</td
          ><td>{display(field.after, field, 'after')}</td></tr
        >
      {:else}<tr><td colspan="4">No field changes to review.</td></tr>{/each}
    </tbody>
  </table>
</div>
{/if}
