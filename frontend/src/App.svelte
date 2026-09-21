<script lang="ts">
  import { onMount } from 'svelte';
  import { start, refPath } from './model';
  import { editReason, has, vlanText } from './policy';
  import type { ResourceRef } from '../../clients/typescript/public-v1.generated';
  import Link from './Link.svelte';
  import Status from './Status.svelte';
  import LoadNotice from './LoadNotice.svelte';
  import VlanEditor from './VlanEditor.svelte';
  import Changes from './Changes.svelte';

  const controller = start();
  const model = controller.store;
  let expert = $state(false);
  let dark = $state(false);
  let width = $state(window.innerWidth);
  let tick = $state(performance.now());
  let username = $state('');
  let password = $state('');
  let provider = $state<'local' | 'tacacs'>('local');
  let search = $state('');
  const desktop = $derived(width >= 1100);
  const mobile = $derived(width < 700);
  const path = $derived($model.path);
  const port = $derived($model.port.value);
  const candidate = $derived($model.workspace.value?.candidate);
  const transaction = $derived($model.transaction.value);
  const records = $derived(
    Array.isArray($model.resource.value?.items)
      ? ($model.resource.value.items as Record<string, unknown>[])
      : [],
  );
  const domains = [
    { name: 'Overview', href: '/overview', icon: '◈' },
    { name: 'Switching', href: '/ports', icon: '⑂' },
    { name: 'Visibility', href: '/visibility', icon: '◎' },
    { name: 'Operations', href: '/operations/jobs', icon: '↗' },
    { name: 'Administration', href: '/administration', icon: '⚙' },
  ];
  function text(value: unknown) {
    return value === null || value === undefined
      ? 'Unknown'
      : typeof value === 'object'
        ? JSON.stringify(value)
        : String(value);
  }
  function relationship(ref: ResourceRef) {
    return typeof ref.name === 'string' ? ref.name : ref.id;
  }
  function itemPath(record: Record<string, unknown>): string | null {
    const id = String(record.id ?? '');
    if (!/^[0-9a-f-]{36}$/.test(id)) return null;
    return `${path}/${id}`;
  }
  async function login(event: SubmitEvent) {
    event.preventDefault();
    const secret = password;
    password = '';
    await controller.login({ username, password: secret, provider });
  }
  onMount(() => {
    dark = localStorage.getItem('ovs.theme') === 'dark';
    expert = localStorage.getItem('ovs.depth') === 'expert';
    void controller.refresh();
    const timer = window.setInterval(() => {
      tick = performance.now();
      if (document.visibilityState === 'visible' && !$model.busy)
        void controller.refresh();
    }, 2000);
    return () => clearInterval(timer);
  });
  $effect(() => {
    document.documentElement.classList.toggle('dark', dark);
    localStorage.setItem('ovs.theme', dark ? 'dark' : 'light');
    localStorage.setItem('ovs.depth', expert ? 'expert' : 'standard');
  });
</script>

<svelte:window
  bind:innerWidth={width}
  onpopstate={() => controller.go(location.pathname)}
  ononline={() => controller.refresh()}
  onstorage={(event) => { if (event.key?.startsWith('ovs.pending.v1.')) void controller.refresh(); }}
/>
<a class="skip-link" href="#main">Skip to content</a>
{#if !$model.session}
  <main id="main" class="login-layout">
    <section class="login-intro">
      <div class="brand-mark">OVS</div>
      <p class="eyebrow">Single-node management</p>
      <h1>OVS WebUI</h1>
      <p>Inspect the switch.<br />Review the intent.<br />Apply with evidence.</p>
      <div class="login-flow">Bridge → Port → Interface</div>
    </section>
    <section class="panel login-panel">
      <h2>Sign in</h2>
      <p>Use your management account to resume its Candidate and transaction history.</p>
      <form onsubmit={login}>
        <label
          >Authentication provider<select bind:value={provider}
            ><option value="local">Local</option><option value="tacacs">TACACS+</option
            ></select
          ></label
        >
        <label
          >Username<input
            autocomplete="username"
            bind:value={username}
            required
            maxlength="128"
          /></label
        >
        <label
          >Password<input
            type="password"
            autocomplete="current-password"
            bind:value={password}
            required
          /></label
        >
        <button class="primary" disabled={$model.busy || $model.booting}
          >{$model.busy ? 'Signing in…' : 'Sign in'}</button
        >
      </form>
      {#if $model.message}<p class="notice" role="status">{$model.message}</p>{/if}
      <button
        class="text-button"
        onclick={() => controller.refresh()}
        disabled={$model.busy}>Check existing session</button
      >
      <p class="muted">
        Safe Apply recovery continues on the server when this browser is closed.
      </p>
    </section>
  </main>
{:else}
  <div class="shell">
    <aside class="sidebar">
      <Link href="/overview"
        ><span class="brand"
          ><span class="brand-mark">OVS</span><span
            >WebUI<small>Single-node management</small></span
          ></span
        ></Link
      >
      <nav aria-label="Product domains">
        {#each domains as domain}<Link
            href={domain.href}
            current={path === domain.href ||
              (domain.name === 'Switching' &&
                (path.startsWith('/ports/') ||
                  path.startsWith('/bridges/') ||
                  path.startsWith('/interfaces/')))}
            ><span aria-hidden="true">{domain.icon}</span> {domain.name}</Link
          >{/each}
      </nav>
      <div class="nav-section">
        <p class="eyebrow">Switching</p>
        <Link href="/ports" current={path.startsWith('/ports')}>Ports</Link>
      </div>
      <nav aria-label="Change Control" class="nav-section">
        <p class="eyebrow">Shared Change Control</p>
        <Link href="/changes/candidate" current={path.includes('/candidate')}
          >Candidate Workspace</Link
        ><Link href="/changes/transactions" current={path.includes('/transactions')}
          >Transactions</Link
        ><Link href="/operations/jobs" current={path.includes('/jobs')}>Jobs</Link><Link
          href="/operations/events"
          current={path.includes('/events')}>Events</Link
        ><Link href="/operations/audit" current={path.includes('/audit')}>Audit</Link>
      </nav>
      <p class="sidebar-footer">
        {mobile
          ? 'Incident companion'
          : desktop
            ? 'Full review & configuration'
            : 'Review & existing Safe Apply'}
      </p>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <span class="service-label"
          ><span class="dot" class:offline={!$model.sessionReady}></span>
          {$model.sessionReady
            ? 'Authenticated management'
            : 'Connection / authorization unavailable'}</span
        >
        <div class="actions">
          <button
            aria-pressed={expert}
            onclick={() => {
              expert = !expert;
            }}>{expert ? 'Expert' : 'Standard'}</button
          ><button
            aria-label="Toggle color theme"
            onclick={() => {
              dark = !dark;
            }}>{dark ? 'Light theme' : 'Dark theme'}</button
          ><span>{$model.session.display_name}</span><button
            onclick={() => controller.logout()}
            disabled={$model.busy}>Sign out</button
          >
        </div>
      </header>
      <div class="change-strip">
        <Link
          href={candidate ? `/changes/candidates/${candidate.id}` : '/changes/candidate'}
          >Candidate {candidate
            ? `· ${candidate.intents.length} intents`
            : '· unavailable'}</Link
        >
        {#if candidate}<Status value={candidate.state} />{/if}
        {#if transaction}<Link href={`/changes/transactions/${transaction.id}`}
            >Safe Apply · {transaction.safe_apply}</Link
          >{/if}
        <span class="muted"
          >{expert
            ? 'Expert exposes native detail; permissions are unchanged.'
            : 'Standard · task-focused review'}</span
        >
      </div>
      <main id="main" tabindex="-1">
        {#if $model.message}<div class="notice" role="status">{$model.message}</div>{/if}
        {#if !$model.sessionReady}<div class="notice warning" role="alert">
            Current authorization or connection could not be verified. Configuration
            actions are disabled.<button onclick={() => controller.refresh()}
              >Refresh session and state</button
            >
          </div>{/if}
        {#if $model.pending}<section class="notice warning" role="alert">
            <strong>Request outcome needs recovery</strong>
            <p>
              The original request is preserved. New commands are blocked until its
              receipt is recovered.
            </p>
            <p class="mono">
              {$model.pending.request_id} · {$model.pending.request_domain}
            </p>
            <button
              onclick={() => controller.recover()}
              disabled={$model.busy || !$model.sessionReady}
              >Recover original request</button
            >
          </section>{/if}
        {#if path === '/ports'}
          <header class="page-heading">
            <div>
              <p class="eyebrow">Switching / Ports</p>
              <h1>Ports</h1>
              <p>Inspect links, ownership and VLAN intent before preparing a change.</p>
            </div>
            <button
              onclick={() => {
                search = '';
                controller.page();
              }}>Refresh inventory</button
            >
          </header>
          <LoadNotice load={$model.ports} />
          {#if $model.ports.value}
            {@const page = $model.ports.value}
            <div class="summary">
              <strong>{page.items.length} ports on this page</strong><Status
                value={page.source.freshness}
              /><Status value={page.availability} /><span
                >Observed {page.source.observed_at ?? 'Unknown'}</span
              >
            </div>
            {#if page.source.freshness !== 'fresh'}<p class="notice warning">
                Stale observation. No configuration action can rely on this snapshot.
              </p>{/if}
            <label class="search"
              >Find on this page<input
                type="search"
                bind:value={search}
                placeholder="Port name or management ID"
              /></label
            >
            <!-- svelte-ignore a11y_no_noninteractive_tabindex (Scrollable inventory must be keyboard accessible.) -->
            <div
              class="table-scroll"
              tabindex="0"
              role="region"
              aria-label="Ports inventory"
            >
              <table>
                <thead
                  ><tr
                    ><th>Port</th><th>Bridge / Interfaces</th><th>OVS link</th><th
                      >Native VLAN</th
                    ><th>VLAN authority</th>{#if expert}<th>Identity / source</th
                      >{/if}</tr
                  ></thead
                ><tbody>
                  {#each page.items.filter((p) => `${p.name} ${p.management_id}`
                      .toLowerCase()
                      .includes(search.toLowerCase())) as p (p.management_id)}
                    <tr
                      ><th scope="row"
                        ><Link href={`/ports/${p.management_id}`}>{p.name}</Link><small
                          >{p.kind === 'bond'
                            ? `Bond · ${p.interface_refs.length} member Interfaces`
                            : 'Port'}</small
                        ></th
                      ><td
                        ><Link href={`/bridges/${p.bridge_ref.id}`}
                          >{relationship(p.bridge_ref)}</Link
                        ><small
                          >{p.interface_refs.length} Interface{p.interface_refs.length ===
                          1
                            ? ''
                            : 's'}</small
                        ></td
                      ><td
                        ><Status value={p.ovs_link_state.value} /><small
                          >Linux carrier: {p.linux_carrier.value ?? 'Unknown'}</small
                        ></td
                      ><td>{vlanText(p.vlan.native)}</td><td
                        >{text(p.vlan_ownership ?? 'unknown')}</td
                      >{#if expert}<td class="mono"
                          >{p.management_id}<small
                            >{p.vlan.source.provider_id} · {p.vlan.availability}</small
                          ></td
                        >{/if}</tr
                    >
                  {:else}<tr
                      ><td colspan={expert ? 6 : 5}
                        >No Ports match this page. Empty is distinct from an unavailable
                        provider.</td
                      ></tr
                    >{/each}
                </tbody>
              </table>
            </div>
            <div class="actions">
              <button onclick={() => controller.page()}>First page</button><button
                disabled={!page.next_cursor}
                onclick={() => controller.page(page.next_cursor!)}>Next page</button
              ><span
                >{page.truncated
                  ? 'More objects exist. Pages are bound to a server snapshot.'
                  : 'End of this snapshot.'}</span
              >
            </div>
          {/if}
        {:else if path.startsWith('/ports/')}
          <LoadNotice load={$model.port} />
          {#if port}
            {#if path.endsWith('/vlan')}{#key port.management_id}<VlanEditor
                  {port}
                  model={$model}
                  {desktop}
                />{/key}
            {:else}
              <div class="breadcrumbs">
                <Link href="/ports">Ports</Link><span>› {port.name}</span>
              </div>
              <header class="page-heading">
                <div>
                  <p class="eyebrow">Switching / Port detail</p>
                  <h1>{port.name}</h1>
                  <p>
                    {port.kind === 'bond'
                      ? 'A Bond is a Port with member Interfaces.'
                      : 'Native Port identity and current observations.'}
                  </p>
                </div>
                <Status value={port.vlan.source.freshness} />
              </header>
              <section class="panel">
                <h2>Bridge → Port → Interface</h2>
                <div class="topology">
                  <Link href={`/bridges/${port.bridge_ref.id}`}
                    >Bridge · {relationship(port.bridge_ref)}</Link
                  ><span>→</span><strong>Port · {port.name}</strong><span>→</span>
                  <div>
                    {#each port.interface_refs as ref}<p>
                        <Link href={`/interfaces/${ref.id}`}
                          >Interface · {relationship(ref)}</Link
                        >
                      </p>{/each}
                  </div>
                </div>
              </section>
              <div class="columns">
                <section class="panel">
                  <h2>VLAN</h2>
                  <p>{vlanText(port.vlan.native)}</p>
                  <dl>
                    <dt>Availability</dt>
                    <dd>{port.vlan.availability}</dd>
                    <dt>VLAN ownership</dt>
                    <dd>{text(port.vlan_ownership ?? 'unknown')}</dd>
                    <dt>Source</dt>
                    <dd>
                      {port.vlan.source.provider_id} · {port.vlan.source.observed_at ??
                        'Unknown'}
                    </dd>
                  </dl>
                  {#if editReason(port, $model.session, desktop)}<p class="notice">
                      {editReason(port, $model.session, desktop)}
                    </p>{:else if $model.sessionReady && !$model.pending}<Link
                      href={`/ports/${port.management_id}/vlan`}>Edit VLAN intent →</Link
                    >{/if}
                </section>
                <section class="panel">
                  <h2>Link observations</h2>
                  <dl>
                    <dt>OVS link state</dt>
                    <dd><Status value={port.ovs_link_state.value} /></dd>
                    <dt>Linux carrier</dt>
                    <dd><Status value={port.linux_carrier.value} /></dd>
                    <dt>Port kind</dt>
                    <dd>{port.kind}</dd>
                  </dl>
                  <p class="muted">
                    OVS link state and Linux carrier are independent provider
                    observations.
                  </p>
                </section>
              </div>
              {#if expert}<details open>
                  <summary>Native identity & field permissions</summary>
                  <dl>
                    <dt>Management ID</dt>
                    <dd class="mono">{port.management_id}</dd>
                    <dt>OVS UUID</dt>
                    <dd class="mono">{port.ovs_uuid}</dd>
                    <dt>Generation</dt>
                    <dd class="mono">{port.instance_generation}</dd>
                  </dl>
                  <pre>{JSON.stringify(port.fields, null, 2)}</pre>
                </details>{/if}
            {/if}
          {/if}
        {:else if path.startsWith('/changes/') && path !== '/changes/transactions'}
          {#key path}<Changes model={$model} {expert} {desktop} {mobile} {tick} />{/key}
        {:else if path.startsWith('/operations/') || path === '/changes/transactions' || path.startsWith('/bridges/') || path.startsWith('/interfaces/')}
          <header class="page-heading">
            <div>
              <p class="eyebrow">Shared resources / authoritative evidence</p>
              <h1>
                {path.split('/')[2] === 'transactions'
                  ? 'Transactions'
                  : path.startsWith('/operations/')
                    ? (
                        { jobs: 'Jobs', events: 'Events', audit: 'Audit' } as Record<
                          string,
                          string
                        >
                      )[path.split('/')[2]]
                    : path.startsWith('/bridges/')
                      ? 'Bridge'
                      : 'Interface'}
              </h1>
            </div>
            <button onclick={() => controller.refresh()}>Refresh resource</button>
          </header>
          <LoadNotice load={$model.resource} />
          {#if $model.resource.value}
            {@const resource = $model.resource.value}
            {#if Array.isArray(resource.items)}
              <!-- svelte-ignore a11y_no_noninteractive_tabindex (Scrollable evidence must be keyboard accessible.) -->
              <div
                class="table-scroll"
                tabindex="0"
                role="region"
                aria-label="Shared resource records"
              >
                <table>
                  <thead
                    ><tr><th>Resource</th><th>State / code</th><th>Evidence</th></tr
                    ></thead
                  ><tbody
                    >{#each records as record}<tr
                        ><th scope="row"
                          >{#if itemPath(record)}<Link href={itemPath(record)!}
                              >{text(record.operation ?? record.kind ?? record.id)}</Link
                            >{:else}{text(record.id)}{/if}</th
                        ><td
                          ><Status
                            value={String(
                              record.safe_apply ??
                                record.state ??
                                record.code ??
                                'Unknown',
                            )}
                          /></td
                        ><td
                          >{text(
                            record.summary ?? record.occurred_at ?? record.correlation_id,
                          )}</td
                        ></tr
                      >{:else}<tr
                        ><td colspan="3">No authorized records in this result.</td></tr
                      >{/each}</tbody
                  >
                </table>
              </div>
              {#if resource.truncated || resource.next_cursor}<p class="notice warning">
                  This result is truncated. Additional records are available through the
                  public API; this page does not claim complete history.
                </p>{/if}
            {:else}
              <section class="panel">
                <h2>
                  {text(
                    resource.name ??
                      resource.operation ??
                      resource.code ??
                      resource.resource_kind,
                  )}
                </h2>
                <dl>
                  {#each Object.entries(resource).filter(([key, value]) => (expert || !['fields', 'config_revision', 'instance_generation', 'source'].includes(key)) && (typeof value !== 'object' || value === null)) as [key, value]}<dt
                    >
                      {key.replaceAll('_', ' ')}
                    </dt>
                    <dd class:mono={key.endsWith('id')}>{text(value)}</dd>{/each}
                </dl>
                {#each ['resource_ref', 'job_ref', 'port_ref'] as key}{#if resource[key] && refPath(resource[key] as ResourceRef)}<p
                    >
                      <Link href={refPath(resource[key] as ResourceRef)!}
                        >Open {key.replaceAll('_', ' ')}</Link
                      >
                    </p>{/if}{/each}
                {#each ['port_refs', 'interface_refs', 'object_refs'] as key}{#if Array.isArray(resource[key])}{#each resource[key] as ref}{#if refPath(ref)}<p
                        >
                          <Link href={refPath(ref)!}>{relationship(ref)}</Link>
                        </p>{/if}{/each}{/if}{/each}
                {#if typeof resource.transaction_id === 'string'}<p>
                    <Link href={`/changes/transactions/${resource.transaction_id}`}
                      >Transaction</Link
                    >
                  </p>{/if}
                {#if expert}<details>
                    <summary>Authorized native resource</summary>
                    <pre>{JSON.stringify(resource, null, 2)}</pre>
                  </details>{/if}
              </section>
            {/if}
          {/if}
        {:else if path === '/overview'}
          <header class="page-heading">
            <div>
              <p class="eyebrow">Overview</p>
              <h1>Management overview</h1>
              <p>Readiness and shared workflow entry points.</p>
            </div>
          </header>
          <LoadNotice load={$model.resource} />
          {#if $model.resource.value}<section class="panel">
              <h2>Service readiness</h2>
              <dl>
                <dt>Runtime</dt>
                <dd><Status value={String($model.resource.value.state)} /></dd>
                <dt>Configuration capability</dt>
                <dd>
                  {$model.resource.value.configuration_ready === true
                    ? 'Available, subject to validation'
                    : 'Unavailable'}
                </dd>
              </dl>
            </section>{/if}
          <div class="columns">
            <section class="panel">
              <h2>Switching</h2>
              <p>Review real Ports and their Bridge / Interface relationships.</p>
              <Link href="/ports">Open Ports →</Link>
            </section>
            <section class="panel">
              <h2>Change Control</h2>
              <p>
                Continue your durable workspace and inspect server-owned transaction
                evidence.
              </p>
              <Link href="/changes/candidate">Open Candidate →</Link>
            </section>
          </div>
        {:else if path === '/administration'}
          <header class="page-heading">
            <div>
              <p class="eyebrow">Administration / Account</p>
              <h1>Account & access</h1>
              <p>Current session claims come from the management service.</p>
            </div>
          </header>
          <section class="panel">
            <h2>{$model.session.display_name}</h2>
            <dl>
              <dt>Session expires</dt>
              <dd>{$model.session.expires_at}</dd>
              <dt>Principal</dt>
              <dd class="mono">{$model.session.principal_id}</dd>
              <dt>Depth</dt>
              <dd>{expert ? 'Expert' : 'Standard'} · permissions are unchanged</dd>
            </dl>
            <h3>Effective capabilities</h3>
            <ul class="capabilities">
              {#each $model.session.effective_capabilities as cap}<li>{cap}</li>{/each}
            </ul>
          </section>
          <p class="notice">
            User and role administration is tracked in #20. Its full management screen
            remains outside this Ports/VLAN slice.
          </p>
        {:else if path === '/visibility'}
          <header class="page-heading">
            <div>
              <p class="eyebrow">Visibility</p>
              <h1>Visibility</h1>
            </div>
          </header>
          <p class="notice">
            Observation pages are scheduled for later frontend migration. OpenFlow, DPDK
            and Offload retain their approved Observe boundaries.
          </p>
          <Link href="/operations/events">Inspect current Events →</Link>
        {:else}<h1>Page unavailable</h1>
          <Link href="/ports">Return to Ports</Link>{/if}
      </main>
      <footer>
        OVS WebUI · {has($model.session, 'configuration.apply')
          ? 'Changes require Candidate, Validation and server admission'
          : 'Current permissions do not allow applying configuration'}
      </footer>
    </div>
  </div>
{/if}
