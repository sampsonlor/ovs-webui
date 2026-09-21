<script lang="ts">
  import type { Model } from './model';
  import { controller, refPath } from './model';
  import { applyReady, decisionReady, has, remaining } from './policy';
  import Diff from './Diff.svelte';
  import Status from './Status.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import Link from './Link.svelte';
  let {
    model,
    expert,
    desktop,
    mobile,
    tick,
  }: { model: Model; expert: boolean; desktop: boolean; mobile: boolean; tick: number } =
    $props();
  const c = $derived(model.workspace.value?.candidate ?? null);
  const v = $derived(model.validation.value);
  const t = $derived(model.transaction.value);
  const blocked = $derived(model.busy || !!model.pending || !model.sessionReady);
  const page = $derived(model.path.split('/')[2]);
  const resourceID = $derived(model.path.split('/')[3]);
  const left = $derived(t ? remaining(t, model.transaction.received, tick) : null);
  const transactionFresh = $derived(model.transaction.status === 'ready' && tick - model.transaction.received <= 5000);
  let reason = $state('');
  let reviewed = $state(false);
  let discard = $state(false);
  let password = $state('');
  let resolutions = $state<Record<string, 'keep-current' | 'keep-mine' | ''>>({});
  async function reauthenticate(event: SubmitEvent) {
    event.preventDefault();
    const value = password;
    password = '';
    await controller.elevate(value);
  }
  function rebase() {
    if (!c) return;
    void controller.candidateCommand({
      operation: 'rebase',
      instance_generation: c.current_instance_generation,
      current_config_revision: c.current_config_revision,
      conflict_snapshot_id: c.conflict_snapshot_id,
      resolutions: c.intents.map((i) => ({
        intent_id: i.intent_id,
        choice: resolutions[i.intent_id],
      })),
    });
  }
</script>

<header class="page-heading">
  <div>
    <p class="eyebrow">Shared Change Control</p>
    <h1>
      {page === 'validations'
        ? 'Diff & Validation'
        : page === 'transactions'
          ? 'Safe Apply'
          : 'Candidate Workspace'}
    </h1>
    <p>Candidate → Diff / Validation → Safe Apply → Event / Audit</p>
  </div>
</header>
{#if page === 'candidates' || page === 'candidate'}
  <LoadNotice load={model.workspace} />
  {#if c && resourceID && c.id !== resourceID}
    <div class="notice warning">
      <strong>This Candidate is no longer current.</strong>
      <p>
        Historical execution remains in its transaction and evidence. Your current
        workspace has a separate identity.
      </p>
      <Link href={`/changes/candidates/${c.id}`}>Open current Candidate</Link>
    </div>
  {:else if c}
    <div class="summary">
      <Status value={c.state} /><span>{c.intents.length} staged intents</span><span
        class="mono">{c.id}</span
      >
    </div>
    {#if expert}<p class="mono">
        Revision {c.revision} · generation {c.instance_generation ?? 'Unknown'}
      </p>{/if}
    {#each c.checks ?? [] as gate}<div class="notice warning">
        <strong>{gate.code}</strong>
        <p>{gate.reason}</p>
      </div>{/each}
    {#if c.diff_truncated}<p class="notice warning">
        Review is incomplete. Applying this Candidate is blocked.
      </p>{/if}
    <Diff fields={c.diff ?? []} />
    {#if c.state === 'conflict'}
      <section class="panel">
        <h2>Resolve against this snapshot</h2>
        <p>
          Choose explicitly for every intent. Keep current removes your intent; keep mine
          rebases it onto the current native value.
        </p>
        {#each c.intents as intent}<label
            >Port {intent.object.management_id.slice(0, 8)}<select
              bind:value={resolutions[intent.intent_id]}
              disabled={!desktop || blocked || !has(model.session, 'workspace.write')}
              ><option value="">Choose resolution</option><option value="keep-current"
                >Keep current</option
              ><option value="keep-mine">Keep mine</option></select
            ></label
          >{/each}
        <button
          onclick={rebase}
          disabled={!desktop ||
            blocked ||
            !has(model.session, 'workspace.write') ||
            !c.conflict_snapshot_id ||
            c.intents.some((i) => !resolutions[i.intent_id])}
          >Rebase reviewed choices</button
        >
      </section>
    {/if}
    {#if !desktop}<p class="notice">
        This device supports review. Prepare and validate new changes on a desktop.
      </p>{/if}
    <div class="actions">
      <button
        class="primary"
        onclick={() => controller.validate()}
        disabled={!desktop ||
          blocked ||
          !has(model.session, 'configuration.validate') ||
          c.state !== 'dirty' ||
          !!c.consumed_by}>Validate Candidate</button
      >
      <button
        onclick={() => {
          discard = !discard;
        }}
        disabled={!desktop ||
          blocked ||
          !has(model.session, 'workspace.write') ||
          !c.intents.length ||
          !!c.consumed_by}>Discard intents…</button
      >
    </div>
    {#if discard}<div class="notice warning">
        <p>
          Discard every staged intent in this revision? Live OVS configuration is
          unchanged.
        </p>
        <button
          onclick={() => {
            discard = false;
            void controller.candidateCommand({ operation: 'discard' });
          }}
          disabled={blocked}>Confirm discard</button
        ><button
          onclick={() => {
            discard = false;
          }}>Cancel discard</button
        >
      </div>{/if}
    {#if model.workspace.value?.latest_validation}<p>
        <Link href={refPath(model.workspace.value.latest_validation)!}
          >Open latest validation</Link
        >
      </p>{/if}
  {/if}
{:else if page === 'validations'}
  <LoadNotice load={model.validation} />
  {#if v}
    <div class="summary">
      <Status value={v.state} /><span
        >{v.usable ? 'Usable for this revision' : 'Not usable for execution'}</span
      ><Link href={`/changes/candidates/${v.candidate_id}`}>Candidate</Link><Link
        href={`/operations/jobs/${v.job_ref.id}`}>Validation Job</Link
      >
    </div>
    <p>
      Expires at {v.expires_at}. Validation success and Job completion do not mean that
      configuration has been applied.
    </p>
    {#each [...v.checks, ...(v.invalidations ?? [])] as gate}<div
        class="notice"
        class:warning={gate.state !== 'allowed'}
      >
        <Status value={gate.state} /> <strong>{gate.code}</strong>
        <p>{gate.reason}</p>
      </div>{/each}
    <Diff fields={v.diff} />
    {#if expert}<details>
        <summary>Validation identity and policy</summary>
        <dl>
          <dt>Validation</dt>
          <dd class="mono">{v.id}</dd>
          <dt>Candidate revision</dt>
          <dd class="mono">{v.candidate_revision}</dd>
          <dt>Policy revision</dt>
          <dd class="mono">{v.policy_revision}</dd>
          <dt>Risk</dt>
          <dd>{v.risk ?? 'Unknown'}</dd>
        </dl>
      </details>{/if}
    <section class="panel">
      <h2>Safe Apply</h2>
      <p>
        The server starts its confirmation window only after Applied and reachability
        evidence. If you do not confirm in time, the server attempts protected recovery.
      </p>
      <form onsubmit={reauthenticate} class="actions">
        <label
          >Verify your password<input
            type="password"
            autocomplete="current-password"
            bind:value={password}
            required
          /></label
        ><button disabled={blocked || !desktop}>Verify identity</button>
      </form>
      <form
        onsubmit={(event) => {
          event.preventDefault();
          if (reviewed && applyReady(c, v, model.session, desktop) && !blocked)
            void controller.apply(reason.trim());
        }}
      >
        <label
          >Change reason<input
            bind:value={reason}
            required
            minlength="3"
            maxlength="500"
          /></label
        >
        <label class="check"
          ><input type="checkbox" bind:checked={reviewed} />I reviewed this Diff,
          including native empty-trunks semantics and recovery constraints.</label
        >
        <button
          type="submit"
          class="primary"
          disabled={blocked ||
            !reviewed ||
            reason.trim().length < 3 ||
            !applyReady(c, v, model.session, desktop)}>Start Safe Apply</button
        >
      </form>
      {#if !applyReady(c, v, model.session, desktop)}<p class="notice warning">
          Execution is blocked by device responsibility, current permissions, Candidate
          revision, validation or server Safe Apply capability. Refresh and review the
          server checks.
        </p>{/if}
    </section>
  {/if}
{:else if page === 'transactions' && resourceID}
  <LoadNotice load={model.transaction} />
  {#if t}
    <div class="summary">
      <Status value={t.safe_apply} /><span class="mono">{t.id}</span><Link
        href={`/operations/jobs/${t.job_ref.id}`}>Execution Job</Link
      >
    </div>
    <section class="panel">
      <h2>Authoritative transaction state</h2>
      {#if !transactionFresh}<p class="notice warning">This observation is stale. Decisions require fresh server evidence.</p>{/if}
      <dl class="state-grid">
        <dt>Knowledge</dt>
        <dd><Status value={t.knowledge} /></dd>
        <dt>OVSDB commit</dt>
        <dd><Status value={t.commit_outcome} /></dd>
        <dt>Applied</dt>
        <dd><Status value={t.applied_outcome} /></dd>
        <dt>Health</dt>
        <dd><Status value={t.health} /></dd>
        <dt>Safe Apply</dt>
        <dd data-testid="safe-state"><Status value={t.safe_apply} /></dd>
        <dt>Server sequence</dt>
        <dd>{t.sequence}</dd>
      </dl>
      {#if t.confirmation_deadline}<div class="notice">
          <strong>Server confirmation deadline</strong>
          <p>
            {t.confirmation_deadline} · {left ?? 'Unknown'} seconds estimated remaining
          </p>
          <p>
            The countdown is informational. Expiry does not imply rollback completion.
          </p>
        </div>{/if}
      {#if ['recovery-required', 'rollback-conflict'].includes(t.safe_apply) || t.knowledge !== 'known'}<div
          class="notice warning"
        >
          <strong>Recovery requires evidence</strong>
          <p>
            Outcome may be unknown or conflict with another writer. Keep the original
            transaction and request identity; do not submit a replacement.
          </p>
        </div>{/if}
      {#if mobile}<p class="notice">
          Mobile incident view: inspect evidence and request rollback of an existing
          transaction. Confirm the configuration from a desktop or tablet.
        </p>{/if}
      <div class="actions">
        <button
          class="primary"
          onclick={() => controller.decide('confirm')}
          disabled={blocked ||
            mobile ||
            left === null ||
            left <= 0 ||
            !transactionFresh ||
            !decisionReady(t, model.session, 'confirm')}>Confirm configuration</button
        ><button
          onclick={() => controller.decide('rollback')}
          disabled={blocked ||
            !transactionFresh ||
            !decisionReady(t, model.session, 'rollback')}>Request rollback</button
        ><button onclick={() => controller.refresh()} disabled={model.busy}
          >Refresh evidence</button
        >
      </div>
    </section>
    <section class="panel">
      <h2>Applied evidence</h2>
      <dl>
        <dt>Source / observation</dt>
        <dd>
          {t.applied_evidence.provider_id} · {t.applied_evidence.observed_at ?? 'Unknown'}
        </dd>
        <dt>Reason</dt>
        <dd>{t.applied_evidence.reason ?? 'No additional reason reported'}</dd>
        {#if expert}<dt>next_cfg target / cur_cfg</dt>
          <dd>
            {t.applied_evidence.next_cfg_target ?? 'Unknown'} / {t.applied_evidence
              .cur_cfg ?? 'Unknown'}
          </dd>
          <dt>Generation</dt>
          <dd class="mono">{t.instance_generation}</dd>{/if}
      </dl>
    </section>
    <p class="actions">
      <Link href="/operations/events">Events</Link><Link href="/operations/audit"
        >Audit</Link
      ><Link href="/changes/candidate">Current Candidate</Link>
    </p>
    <p class="mono">Correlation {t.correlation_id}</p>
  {/if}
{/if}
