<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { CapturePart } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, errorText, refPath, type Model } from './model';
  import { has } from './policy';
  import { saveVerifiedArtifact } from './artifact-download';
  import { terminalDiagnosticJob } from './diagnostics';
  import {
    canCancelCapture, capturePath, capturePermission, captureProfiles, captureResultID,
    captureRoute, readVerifiedCapture,
  } from './packet-capture';

  let { id, model, expert, job }: { id: string; model: Model; expert: boolean; job?: Record<string, unknown> } = $props();
  let expired = $state(false);
  let downloading = $state(false);
  let message = $state('');
  let mounted = true;
  onDestroy(() => (mounted = false));
  const permitted = $derived(model.sessionReady && capturePermission(model.session) && has(model.session, 'artifacts.read'));
  const artifact = $derived(permitted && model.captureArtifact.status === 'ready' && model.captureArtifact.value?.id === id ? model.captureArtifact.value : null);
  const expiry = $derived(artifact ? Date.parse(artifact.expires_at) : 0);

  $effect(() => {
    const remaining = expiry - Date.now();
    expired = !Number.isFinite(remaining) || remaining <= 0;
    if (!expired) {
      const timer = setTimeout(() => (expired = true), Math.min(remaining, 2_147_483_647));
      return () => clearTimeout(timer);
    }
  });

  async function download() {
    const started = artifact;
    const session = model.session;
    const jobID = id;
    if (!started || !session || expired || downloading || !permitted) return;
    const active = () => {
      const current = controller.state;
      const currentArtifact = current.captureArtifact.status === 'ready' ? current.captureArtifact.value : null;
      const relatedRoute = current.path === `/operations/jobs/${jobID}` ||
        (current.path === captureRoute && captureResultID(current.query) === jobID);
      return mounted && relatedRoute && current.sessionReady && current.session?.principal_id === session.principal_id &&
        current.session.csrf_token === session.csrf_token && current.session.request_epochs.management === session.request_epochs.management &&
        capturePermission(current.session) && has(current.session, 'artifacts.read') && currentArtifact?.id === jobID &&
        currentArtifact.sha256 === started.sha256 && currentArtifact.expires_at === started.expires_at;
    };
    downloading = true;
    message = '';
    try {
      const bytes = await readVerifiedCapture(jobID, started, (part) =>
        controller.api.read<CapturePart>(`${capturePath}/${jobID}/parts?part=${part}`), active);
      if (!active() || Date.now() >= Date.parse(started.expires_at)) return;
      saveVerifiedArtifact(bytes, `ovs-webui-capture-${jobID}.pcap`, 'application/vnd.tcpdump.pcap');
      message = 'Capture verified. Download requested.';
    } catch (error) {
      if (active()) message = `Download stopped. ${errorText(error)}`;
      void controller.refresh();
    } finally {
      downloading = false;
    }
  }
</script>

<section class="panel" aria-labelledby="capture-result-heading">
  <h2 id="capture-result-heading">Packet Capture result</h2>
  {#if job && !terminalDiagnosticJob(job.state)}
    <p role="status"><Status value={String(job.state)} /> The Job has not published a capture file yet.</p>
    {#if model.sessionReady && canCancelCapture(job, model.session)}
      <button disabled={model.busy || !!model.pending} onclick={() => controller.cancelCapture(id)}>Cancel capture</button>
    {/if}
  {:else if !permitted}
    <p class="notice warning" role="status">Current permissions do not allow capture downloads.</p>
  {:else if artifact}
    {@const target = has(model.session, 'inventory.read') ? refPath(artifact.target_ref) : null}
    <dl>
      <dt>Capture result</dt><dd><Status value={artifact.state} /></dd>
      <dt>Interface</dt><dd>{#if target}<Link href={target}>Review Interface</Link>{:else}Approved capture target{/if}{#if expert}<small class="mono">{artifact.target_ref.id}</small>{/if}</dd>
      <dt>Profile</dt><dd>{captureProfiles.find(({ id }) => id === artifact?.profile)?.label}</dd>
      <dt>Captured packets</dt><dd>{artifact.packets}</dd>
      <dt>File size</dt><dd>{artifact.bytes.toLocaleString()} bytes</dd>
      <dt>Started</dt><dd><time datetime={artifact.started_at}>{artifact.started_at}</time></dd>
      <dt>Ended</dt><dd><time datetime={artifact.ended_at}>{artifact.ended_at}</time></dd>
      <dt>Expires</dt><dd><time datetime={artifact.expires_at}>{artifact.expires_at}</time></dd>
      {#if expert}
        <dt>SHA-256</dt><dd class="mono">{artifact.sha256}</dd>
        {#if artifact.reason_code}<dt>Reason code</dt><dd class="mono">{artifact.reason_code}</dd>{/if}
      {/if}
    </dl>
    {#if artifact.state === 'no-data'}<p>No matching packets were observed.</p>{/if}
    {#if artifact.state === 'truncated'}<p>The capture reached its packet or byte limit.</p>{/if}
    {#if artifact.state === 'partial'}<p>The capture ended before all requested observation time elapsed.</p>{/if}
    {#if expired}<p class="notice warning" role="status">This capture file has expired.</p>{/if}
    <button class="primary" disabled={expired || downloading} onclick={download}>{downloading ? 'Verifying capture…' : 'Download verified PCAP'}</button>
    {#if message}<p role="status">{message}</p>{/if}
  {:else if job?.state === 'cancelled'}
    <p role="status">The capture was cancelled. No capture file is available.</p>
  {:else if model.captureArtifact.error === 'CAPTURE_ARTIFACT_UNAVAILABLE' || model.captureArtifact.error === 'CAPTURE_INTERFACE_NOT_AUTHORIZED'}
    <p class="notice warning" role="status">The capture file is unavailable or expired.{job ? '' : ' The Job may still be running.'}</p>
  {:else}<LoadNotice load={model.captureArtifact} />{/if}
  <p><Link href={captureRoute}>Back to Packet Capture</Link></p>
</section>
