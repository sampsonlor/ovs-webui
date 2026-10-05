<script lang="ts">
  import type { Interface } from '../../clients/typescript/public-v1.generated';
  import { nativeTypeContext, patchPeerReason, currentPatchPeer } from './interface-native-types';
  import { refPath } from './model';
  import Link from './Link.svelte';
  import Status from './Status.svelte';
  let { item, expert }: { item: Interface; expert: boolean } = $props();
  const context = $derived(nativeTypeContext(item));
  const peer = $derived(item.patch_peer);
  const links = $derived(currentPatchPeer(item) && peer && refPath(peer.peer_ref) && refPath(peer.peer_port_ref) && refPath(peer.peer_bridge_ref));
</script>

<section class="panel" aria-label="Native type and associations">
  <h2>Native type and associations</h2>
  <p><strong>{context.label}</strong></p><p>{context.detail}</p>
  {#if context.patch}
    <h3>Configured patch peer</h3><Status value={peer?.availability ?? 'unknown'} /><p role="status">{patchPeerReason(item)}</p>
    {#if links && peer}
      <div class="topology"><Link href={refPath(peer.peer_ref)!}>Peer Interface →</Link><Link href={refPath(peer.peer_port_ref)!}>Peer Port →</Link><Link href={refPath(peer.peer_bridge_ref)!}>Peer Bridge →</Link></div>
    {/if}
    {#if expert && peer}<p class="muted">{peer.reason} · {peer.source.authority}<br />{peer.source.freshness} · {peer.source.confidence} · {peer.source.observed_at ?? 'Not observed'}</p>{/if}
  {/if}
  <p class="muted">Type and attachment changes require their own reviewed Candidate operation. Native names and references are preserved.</p>
</section>
