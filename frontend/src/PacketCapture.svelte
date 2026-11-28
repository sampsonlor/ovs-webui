<script lang="ts">
  import CaptureResult from './CaptureResult.svelte';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import { controller, type Model } from './model';
  import { has } from './policy';
  import {
    captureElevationExpiry, captureInputsValid, captureMaxBytes, captureMaxPackets, captureMinBytes,
    capturePermission, captureProfiles, captureResultID, type CaptureProfile,
  } from './packet-capture';

  let { model, desktop, expert }: { model: Model; desktop: boolean; expert: boolean } = $props();
  let interfaceID = $state('');
  let profile = $state<CaptureProfile>('arp-ethernet-headers');
  let duration = $state(30);
  let bytes = $state(128 * 1024);
  let reviewed = $state(false);
  let elevated = $state(false);
  let password = $state('');
  const resultID = $derived(captureResultID(model.query));
  const permission = $derived(model.sessionReady && capturePermission(model.session));
  const policy = $derived(permission && model.captureTargets.status === 'ready' ? model.captureTargets.value : null);
  const selected = $derived(policy?.policy_state === 'enabled' ? policy.targets.find((target) => target.id === interfaceID) : undefined);
  const valid = $derived(!!selected && captureInputsValid(interfaceID, duration, bytes, profile));
  const blocked = $derived(!model.sessionReady || model.busy || !!model.pending);

  $effect(() => {
    const remaining = captureElevationExpiry(model.session) - Date.now();
    elevated = Number.isFinite(remaining) && remaining > 0;
    if (elevated) {
      const timer = setTimeout(() => { elevated = false; reviewed = false; }, Math.min(remaining, 2_147_483_647));
      return () => clearTimeout(timer);
    }
  });

  async function authorize(event: SubmitEvent) {
    event.preventDefault();
    if (!permission || blocked || !desktop || !valid) return;
    const secret = password;
    password = '';
    await controller.elevate(secret);
  }

  function start(event: SubmitEvent) {
    event.preventDefault();
    if (permission && valid && elevated && reviewed && !blocked) {
      void controller.startCapture(interfaceID, duration, bytes, profile, reviewed, desktop);
      reviewed = false;
    }
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Operations</p>
    <h1>Packet Capture</h1>
    {#if !resultID}<p>Observe fixed Ethernet headers on one approved Interface.</p>{/if}
  </div>
  {#if permission}<button onclick={() => controller.refresh()}>{resultID ? 'Refresh result' : 'Refresh approved Interfaces'}</button>{/if}
</header>

{#if !permission}
  <p class="notice warning" role="status">Current permissions or connection do not allow Packet Capture.</p>
{:else if resultID}
  <CaptureResult id={resultID} {model} {expert} />
{:else if !desktop}
  <p>Use a desktop to review and start a new Packet Capture.</p>
{:else}
  <LoadNotice load={model.captureTargets} />
  {#if policy?.policy_state === 'disabled'}
    <p class="notice warning" role="status">Packet Capture is disabled by management policy.</p>
  {:else if policy?.policy_state === 'unavailable'}
    <p class="notice warning" role="status">Current capture authority is unavailable.</p>
  {:else if policy && !policy.targets.length}
    <p class="notice warning" role="status">No currently approved Interfaces are available.</p>
  {:else if policy?.policy_state === 'enabled'}
    <section class="panel" aria-labelledby="capture-start-heading">
      <h2 id="capture-start-heading">Capture scope</h2>
      <p>Only the 14-byte Ethernet header is retained from each matching frame. Each Job is limited to {captureMaxPackets.toLocaleString()} packets.</p>
      <form onsubmit={start}>
        <label for="capture-target">Approved Interface</label>
        <select id="capture-target" bind:value={interfaceID} onchange={() => (reviewed = false)} required>
          <option value="">Select an Interface</option>
          {#each policy.targets as target (target.id)}<option value={target.id}>{target.name}</option>{/each}
        </select>
        {#if expert && selected}<p class="mono">{selected.id}</p>{/if}
        <label for="capture-profile">Capture profile</label>
        <select id="capture-profile" bind:value={profile} onchange={() => (reviewed = false)}>
          {#each captureProfiles as option}<option value={option.id}>{option.label}</option>{/each}
        </select>
        <label for="capture-duration">Duration in seconds</label>
        <input id="capture-duration" type="number" min="1" max="300" step="1" required bind:value={duration} oninput={() => (reviewed = false)} />
        <label for="capture-bytes">Maximum bytes</label>
        <input id="capture-bytes" type="number" min={captureMinBytes} max={captureMaxBytes} step="1" required bind:value={bytes} oninput={() => (reviewed = false)} />
        <label><input type="checkbox" bind:checked={reviewed} /> I reviewed the selected Interface, profile and limits and accept the capture load.</label>
        <button class="primary" disabled={blocked || !valid || !elevated || !reviewed}>Start Packet Capture</button>
      </form>
      {#if valid && !elevated}
        <form onsubmit={authorize}>
          <label for="capture-password">Confirm your password</label>
          <input id="capture-password" type="password" autocomplete="current-password" required bind:value={password} />
          <button disabled={blocked}>Reauthenticate</button>
        </form>
      {/if}
    </section>
  {/if}
{/if}
{#if permission && has(model.session, 'jobs.read')}
  <p><Link href={resultID ? `/operations/jobs/${resultID}` : '/operations/jobs'}>Review {resultID ? 'Job' : 'Jobs'}</Link></p>
{/if}
