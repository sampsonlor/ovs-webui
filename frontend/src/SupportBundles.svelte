<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { SupportBundlePart } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Status from './Status.svelte';
  import { controller, errorText, type Model } from './model';
  import { has } from './policy';
  import { saveVerifiedArtifact } from './artifact-download';
  import {
    authorizedSupportScopes, readVerifiedBundle, supportBundleExpiry, supportBundleID,
    supportBundlePath, supportBundleRoute, supportScopes, type SupportKind,
  } from './support-bundles';

  let { model, expert }: { model: Model; expert: boolean } = $props();
  let selected = $state<SupportKind[]>([]);
  let expired = $state(false);
  let downloading = $state(false);
  let message = $state('');
  let mounted = true;
  onDestroy(() => (mounted = false));
  const id = $derived(supportBundleID(model.query));
  const mayCreate = $derived(model.sessionReady && has(model.session, 'support_bundle.create'));
  const mayDownload = $derived(mayCreate && has(model.session, 'artifacts.read'));
  const metadata = $derived(model.supportBundle.status === 'ready' && mayDownload ? model.supportBundle.value : null);
  const expiry = $derived(metadata ? supportBundleExpiry(metadata) : 0);
  const validSelection = $derived(authorizedSupportScopes(selected, model.session));
  const label = (kind: SupportKind) => supportScopes.find((scope) => scope.kind === kind)?.label ?? kind;

  $effect(() => {
    const remaining = expiry - Date.now();
    expired = !Number.isFinite(remaining) || remaining <= 0;
    if (!expired) {
      const timer = setTimeout(() => (expired = true), Math.min(remaining, 2_147_483_647));
      return () => clearTimeout(timer);
    }
  });
  $effect(() => {
    const allowed = selected.filter((kind) => authorizedSupportScopes([kind], model.session));
    if (allowed.length !== selected.length) selected = allowed;
  });

  function create(event: SubmitEvent) {
    event.preventDefault();
    if (mayCreate && validSelection) void controller.createSupportBundle(selected);
  }

  async function download() {
    const started = metadata;
    const session = model.session;
    const job = id;
    if (!started || !session || !job || expired || downloading || !mayDownload) return;
    const active = () => {
      const current = controller.state;
      const available = current.supportBundle.status === 'ready' ? current.supportBundle.value : null;
      return mounted && current.sessionReady && current.path === supportBundleRoute && supportBundleID(current.query) === job &&
        current.session?.principal_id === session.principal_id && current.session.csrf_token === session.csrf_token &&
        current.session.request_epochs.management === session.request_epochs.management && has(current.session, 'support_bundle.create') &&
        has(current.session, 'artifacts.read') && available?.id === job && available.sha256 === started.sha256 &&
        available.manifest.expires_at === started.manifest.expires_at &&
        authorizedSupportScopes(started.manifest.sections.map((section) => section.kind), current.session);
    };
    downloading = true;
    message = '';
    try {
      const bytes = await readVerifiedBundle(job, started, (part) =>
        controller.api.read<SupportBundlePart>(`${supportBundlePath}/${job}/parts?part=${part}`), active);
      if (!active() || Date.now() >= supportBundleExpiry(started)) return;
      saveVerifiedArtifact(bytes, `ovs-webui-support-${job}.zip`, 'application/zip');
      message = 'Archive verified. Download requested.';
    } catch (error) {
      if (active()) message = `Download stopped. ${errorText(error)}`;
      void controller.refresh();
    } finally {
      downloading = false;
    }
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Operations</p>
    <h1>Support Bundle</h1>
    <p>{id ? 'Review the selected sections and download the expiring archive.' : 'Choose the observations to include in a redacted support archive.'}</p>
  </div>
  {#if id && mayDownload}<button onclick={() => controller.refresh()}>Refresh manifest</button>{/if}
</header>

{#if !mayCreate}
  <p class="notice warning" role="status">Current permissions or connection do not allow access to Support Bundles.</p>
{:else if !id}
  <section class="panel" aria-labelledby="support-scope-heading">
    <h2 id="support-scope-heading">Bundle scope</h2>
    <p>Redaction is always on. The archive excludes credentials, raw configuration, packet data, and unrestricted logs.</p>
    <form onsubmit={create}>
      <fieldset>
        <legend>Include observations</legend>
        {#each supportScopes as scope (scope.kind)}
          {@const allowed = has(model.session, scope.permission)}
          <div>
            <label><input type="checkbox" bind:group={selected} value={scope.kind} disabled={!allowed} /> {scope.label}</label>
            <small>{allowed ? scope.detail : 'Permission required'}</small>
          </div>
        {/each}
      </fieldset>
      <button class="primary" disabled={!validSelection || model.busy || !!model.pending}>Create Support Bundle</button>
    </form>
  </section>
{:else}
  <nav class="actions" aria-label="Support evidence">
    <Link href={supportBundleRoute}>New Support Bundle</Link>
    {#if has(model.session, 'jobs.read')}<Link href={`/operations/jobs/${id}`}>Review Job</Link>{/if}
  </nav>
  {#if !mayDownload}
    <p class="notice warning" role="status">Artifact read permission is required to download this bundle.</p>
  {:else if metadata}
    <section class="panel" aria-labelledby="support-manifest-heading">
      <h2 id="support-manifest-heading">Bundle manifest</h2>
      <dl>
        <dt>Created</dt><dd><time datetime={metadata.manifest.created_at}>{metadata.manifest.created_at}</time></dd>
        <dt>Expires</dt><dd><time datetime={metadata.manifest.expires_at}>{metadata.manifest.expires_at}</time></dd>
        <dt>Archive size</dt><dd>{metadata.bytes.toLocaleString()} bytes</dd>
        {#if expert}<dt>SHA-256</dt><dd class="mono">{metadata.sha256}</dd>{/if}
      </dl>
      <ul>
        {#each metadata.manifest.sections as section (section.kind)}
          <li>{label(section.kind)} <Status value={section.state} />{#if expert && section.reason}<small>{section.reason}</small>{/if}</li>
        {/each}
      </ul>
      {#if expert}
        <details>
          <summary>Redaction policy</summary>
          <p>{metadata.manifest.redaction.policy}</p>
          <ul>{#each metadata.manifest.redaction.omitted as omitted}<li>{omitted}</li>{/each}</ul>
        </details>
      {/if}
      {#if expired}<p class="notice warning" role="status">This archive has expired.</p>{/if}
      <button class="primary" disabled={expired || downloading} onclick={download}>{downloading ? 'Verifying archive…' : 'Download verified ZIP'}</button>
      {#if message}<p role="status">{message}</p>{/if}
    </section>
  {:else if model.supportBundle.error === 'SUPPORT_BUNDLE_UNAVAILABLE'}
    <p class="notice warning" role="status">The archive is unavailable or expired.</p>
  {:else}<LoadNotice load={model.supportBundle} />{/if}
{/if}
