<script lang="ts">
  import type { Interface } from '../../clients/typescript/public-v1.generated';
  import { configurationFields, configurationObservation } from './interface-configuration';
  import { field, availability } from './interface-observation';
  import Status from './Status.svelte';
  let { item, expert }: { item: Interface; expert: boolean } = $props();
</script>

<section class="panel">
  <h2>Native configuration requests</h2>
  <p>Requested port numbers can differ from the actual allocation above. Policing values describe configured ingress limits, not measured traffic or proven enforcement.</p>
  <p class="muted">Bandwidth uses kbit/s and kbit. Packet limits use kpps and kpackets (1,000 packets). Zero disables a rate; zero burst retains the native default.</p>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll configuration observations.) -->
  <div class="table-scroll" tabindex="0" role="region" aria-label="Native configuration requests">
    <table><thead><tr><th>Field</th><th>Configured value</th><th>Availability</th>{#if expert}<th>Source / observation</th>{/if}</tr></thead>
      <tbody>{#each configurationFields as [name, label]}{@const f = field(item, name)}
        <tr><th scope="row">{label}{#if expert}<small>{name}</small>{/if}</th><td>{configurationObservation(item, name)}</td><td><Status value={availability(item, name)} /></td>{#if expert}<td>{f?.source?.provider_id ?? 'Unavailable'} · {f?.source?.authority ?? 'Unknown'}<small>{f?.source?.freshness ?? 'Unavailable'} · {f?.source?.confidence ?? 'Unknown'}</small><small>{f?.source?.observed_at ?? 'Not observed'}</small>{#if f?.reason}<small>{f.reason}</small>{/if}</td>{/if}</tr>
      {/each}</tbody>
    </table>
  </div>
</section>
