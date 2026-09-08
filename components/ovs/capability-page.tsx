'use client';

import {
  ArrowRight,
  CheckCircle2,
  CircleHelp,
  FilterX,
  RefreshCw,
  Search,
  ShieldCheck,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import { Notice, PageHeader, StatusBadge, type Tone } from './foundation';
import {
  capabilityStates,
  capabilityReviewLabels,
  type CapabilityState,
  type CapabilityRow,
  type CapabilityId,
  type CapabilityReviewCase,
} from '@/lib/capability-model';
import { nativeTarget } from '@/lib/native-capability';
import type { CapabilityController } from './capability-controller';
import type { Mode, View } from '@/lib/ovs-model';

export const capabilityTones: Record<CapabilityState, Tone> = {
  Enabled: 'success',
  Available: 'info',
  'Missing prerequisites': 'warning',
  Unsupported: 'neutral',
  Unknown: 'uncertain',
};
const timestamp = (time: number) =>
  Number.isFinite(time) && time > 0
    ? new Date(time)
        .toISOString()
        .replace('T', ' ')
        .replace(/\.\d{3}Z$/, ' UTC')
    : 'Not observed';
type Props = { controller: CapabilityController; go: (view: View) => void };

export function CapabilitySummary({
  controller: cap,
  go,
  context = 'Shared',
}: Props & { context?: string }) {
  return (
    <section
      aria-label={`${context} capability states`}
      className="ovs-surface min-w-0 p-4"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-semibold">Capability availability</h2>
        {!['Matrix', 'Mobile'].includes(context) && (
          <Button variant="ghost" size="sm" onClick={() => go('capabilities')}>
            Inspect capabilities <ArrowRight aria-hidden="true" />
          </Button>
        )}
      </div>
      {cap.denied ? (
        <p className="mt-3 text-sm">
          Capability access denied. Provider details are hidden.
        </p>
      ) : (
        <ul className="mt-3 flex flex-wrap gap-2 text-sm">
          {capabilityStates.map((state) => (
            <li key={state}>
              <StatusBadge tone={capabilityTones[state]}>
                {state} · {cap.counts[state]}
              </StatusBadge>
            </li>
          ))}
        </ul>
      )}
      <p className="mt-3 text-xs text-muted-foreground">
        Shared capability evidence · {cap.rows.length} declared samples.
        Availability does not grant configuration permission.
      </p>
    </section>
  );
}

function DefinitionList({
  row,
  expert,
}: {
  row: CapabilityRow;
  expert: boolean;
}) {
  return (
    <dl className="mt-4 grid gap-x-6 gap-y-4 text-sm sm:grid-cols-2">
      {[
        ['Management level', row.level],
        ['Authority', row.authority],
        ['Provider', row.provider],
        ['Provider coverage', row.providerState],
        ['Observed at', timestamp(row.observedAt)],
        ['Freshness', row.freshness],
        ...(expert
          ? [
              ['Capability code', row.code],
              ['Required permission', row.permission],
              ['Contract version', row.version],
              ['Evidence reference', row.source],
              [
                'Instance generation',
                row.generation === null
                  ? 'Not captured by this provider'
                  : String(row.generation),
              ],
              ['Provider can_enable', String(row.canEnable)],
            ]
          : []),
      ].map(([label, value]) => (
        <div key={label}>
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="mt-1 break-words font-medium">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function NativeReview({ controller: cap, go }: Props) {
  return (
    <section aria-label="Native action gates" className="mt-5 border-t pt-5">
      <h3 className="flex items-center gap-2 text-base font-semibold">
        <ShieldCheck aria-hidden="true" className="size-4" /> Manage if native
      </h3>
      <ol className="mt-4 grid gap-3 sm:grid-cols-2">
        {cap.gates.map((gate, index) => (
          <li key={gate.id} className="min-w-0 rounded border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h4 className="text-sm font-semibold">
                {index + 1}. {gate.label}
              </h4>
              <StatusBadge tone={gate.passed ? 'success' : 'warning'}>
                {gate.passed ? 'Pass' : 'Blocked'}
              </StatusBadge>
            </div>
            <p className="mt-2 text-sm leading-6 text-muted-foreground">
              {gate.reason}
            </p>
          </li>
        ))}
      </ol>
      {cap.stageBlock ? (
        <div className="mt-4">
          <Notice tone="info" title="Configuration unavailable">
            {cap.stageBlock}
          </Notice>
        </div>
      ) : (
        <>
          <p className="mt-4 text-sm text-muted-foreground lg:hidden">
            Review native configuration on desktop. Existing Safe Apply remains
            available from Changes.
          </p>
          <div className="mt-4 hidden lg:block">
            {!cap.preparing ? (
              <Button onClick={cap.prepare}>
                Review native change <ArrowRight aria-hidden="true" />
              </Button>
            ) : (
              <section
                aria-label="Native change review"
                className="rounded border border-primary/40 bg-primary/5 p-4"
              >
                <h4 className="text-base font-semibold">
                  Enable protected Port
                </h4>
                <p className="mt-2 text-sm leading-6">
                  {nativeTarget.target} on Bridge/{nativeTarget.bridge}. Traffic
                  to other protected peers on this local NORMAL path will be
                  blocked; unprotected peers remain reachable.
                </p>
                <dl className="mt-4 grid gap-3 text-sm sm:grid-cols-2">
                  {[
                    ['Current → Candidate', 'protected: false → true'],
                    [
                      'Risk',
                      cap.proof?.highRisk
                        ? 'High · step-up required'
                        : 'Medium',
                    ],
                    ['Management path', cap.proof?.managementPath ?? 'Unknown'],
                    ['Restart / reboot', 'Not required for this operation'],
                    [
                      'Checkpoint / rollback',
                      'Required through shared Safe Apply',
                    ],
                    ['Execution', 'Synthetic transaction; no connected OVS'],
                  ].map(([label, value]) => (
                    <div key={label}>
                      <dt className="text-muted-foreground">{label}</dt>
                      <dd className="mt-1 font-medium">{value}</dd>
                    </div>
                  ))}
                </dl>
                <label className="mt-4 flex items-start gap-3 text-sm leading-6">
                  <input
                    type="checkbox"
                    className="mt-1 size-4 shrink-0 accent-primary"
                    checked={cap.impactAccepted}
                    onChange={(event) => cap.acknowledge(event.target.checked)}
                  />
                  I reviewed the protected-peer traffic impact.
                </label>
                <div className="mt-4 flex flex-wrap gap-2">
                  <Button disabled={!cap.impactAccepted} onClick={cap.stage}>
                    Add native change to Candidate
                  </Button>
                  <Button variant="outline" onClick={cap.cancel}>
                    Cancel review
                  </Button>
                </div>
              </section>
            )}
          </div>
        </>
      )}
      {cap.transaction.status !== 'idle' && (
        <Button
          className="mt-4"
          variant="outline"
          onClick={() => go('safe-apply')}
        >
          Inspect existing Safe Apply
        </Button>
      )}
    </section>
  );
}

function CapabilityDetail({
  controller: cap,
  mode,
  go,
  openRelated,
}: Props & { mode: Mode; openRelated: (id: CapabilityId) => void }) {
  const row = cap.selected;
  if (!row) return null;
  return (
    <section
      aria-label="Capability evidence"
      className="ovs-surface min-w-0 p-5"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="ovs-eyebrow">{row.domain} / Capability evidence</p>
          <h2 className="mt-2 text-xl font-semibold">{row.title}</h2>
        </div>
        <output aria-label="Selected capability availability">
          <StatusBadge tone={capabilityTones[row.state]}>
            {row.state}
          </StatusBadge>
        </output>
      </div>
      <p className="mt-3 text-sm leading-6">{row.reason}</p>
      <p className="mt-3 break-words rounded bg-muted/50 p-3 text-sm leading-6">
        {row.scope}
      </p>
      <DefinitionList row={row} expert={mode === 'expert'} />
      <div className="mt-5">
        <Notice title={`${row.level} boundary`} tone="info">
          {row.boundary}
        </Notice>
      </div>
      {row.prerequisites.length > 0 && (
        <div className="mt-5 border-t pt-5">
          <h3 className="text-base font-semibold">Prerequisites and owners</h3>
          <ul className="mt-3 space-y-3">
            {row.prerequisites.map((fact) => (
              <li key={fact.label} className="rounded border p-3 text-sm">
                <div className="flex flex-wrap justify-between gap-2">
                  <span className="font-semibold">{fact.label}</span>
                  <span>{fact.value}</span>
                </div>
                <p className="mt-2 leading-6">{fact.reason}</p>
                <p className="mt-2 text-muted-foreground">
                  Owner · {fact.owner}
                </p>
              </li>
            ))}
          </ul>
        </div>
      )}
      {row.id === 'protected' ? (
        <NativeReview controller={cap} go={go} />
      ) : (
        <Button
          className="mt-5"
          variant="outline"
          onClick={() => openRelated(row.id)}
        >
          Open related {row.id === 'lifecycle' ? 'health' : 'view'}{' '}
          <ArrowRight aria-hidden="true" />
        </Button>
      )}
    </section>
  );
}

export function P1CapabilityView({
  controller: cap,
  mode,
  go,
  openRelated,
  refreshAcceleration,
}: Props & {
  mode: Mode;
  openRelated: (id: CapabilityId) => void;
  refreshAcceleration: () => void;
}) {
  return (
    <>
      <PageHeader
        id="P1-12"
        eyebrow="Administration"
        title="Capabilities"
        description="Inspect runtime support, ownership and prerequisites before choosing a configuration path."
        scope="Observe"
        actions={
          <Button
            variant="outline"
            onClick={cap.read}
            disabled={cap.busy || !!cap.readBlock}
          >
            <RefreshCw
              aria-hidden="true"
              className={cap.busy ? 'animate-spin' : ''}
            />
            {cap.busy ? 'Reading registry…' : 'Refresh registry'}
          </Button>
        }
      />
      <div className="mt-5">
        <CapabilitySummary controller={cap} go={go} context="Matrix" />
      </div>
      {cap.denied ? (
        <div className="mt-4">
          <Notice tone="warning" title="Capability access denied">
            capabilities.read is required. Cached provider details and native
            configuration controls have been cleared.
          </Notice>
        </div>
      ) : (
        <>
          {cap.failure && (
            <div className="mt-4">
              <Notice tone="warning" title="Registry evidence unavailable">
                {cap.failure} Independent collection snapshots keep their own
                freshness and source.
              </Notice>
            </div>
          )}
          {cap.snapshot?.empty && (
            <div className="mt-4">
              <Notice title="Registry returned no observations">
                The successful registry read is empty. Declared catalog entries
                remain explainable as Unknown; shared collection evidence is
                shown separately.
              </Notice>
            </div>
          )}
          <div className="mt-5 grid grid-cols-[repeat(auto-fit,minmax(min(100%,24rem),1fr))] items-start gap-5">
            <section
              aria-label="Capability matrix"
              className="ovs-surface min-w-0 overflow-hidden"
            >
              <div className="grid gap-3 border-b p-4 sm:grid-cols-2">
                <label
                  htmlFor="capability-search"
                  className="flex min-w-0 items-center gap-2 sm:col-span-2"
                >
                  <Search
                    aria-hidden="true"
                    className="size-4 shrink-0 text-muted-foreground"
                  />
                  <Input
                    id="capability-search"
                    aria-label="Search capabilities"
                    placeholder="Find capability or provider"
                    value={cap.search}
                    onChange={(event) => cap.setSearch(event.target.value)}
                  />
                </label>
                <NativeSelect
                  aria-label="Capability availability filter"
                  value={cap.filter}
                  onChange={(event) => cap.setFilter(event.target.value)}
                >
                  <NativeSelectOption value="all">
                    All availability
                  </NativeSelectOption>
                  {capabilityStates.map((value) => (
                    <NativeSelectOption key={value} value={value}>
                      {value}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <NativeSelect
                  aria-label="Capability domain filter"
                  value={cap.domain}
                  onChange={(event) => cap.setDomain(event.target.value)}
                >
                  <NativeSelectOption value="all">
                    All domains
                  </NativeSelectOption>
                  {[
                    'Switching',
                    'Visibility',
                    'Operations',
                    'Administration',
                  ].map((domain) => (
                    <NativeSelectOption key={domain}>
                      {domain}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3 text-sm">
                <p>
                  {cap.visible.length} of {cap.rows.length} declared
                  capabilities
                </p>
                <Button variant="ghost" size="sm" onClick={cap.clearFilters}>
                  <FilterX aria-hidden="true" />
                  Clear filters
                </Button>
              </div>
              {cap.visible.length ? (
                <ul className="divide-y">
                  {cap.visible.map((row) => (
                    <li key={row.id}>
                      <button
                        type="button"
                        aria-label={`Inspect ${row.title}`}
                        aria-pressed={cap.selected?.id === row.id}
                        onClick={() => cap.select(row.id)}
                        className={`w-full px-4 py-4 text-left hover:bg-muted/45 ${cap.selected?.id === row.id ? 'bg-primary/5 shadow-[inset_3px_0_0_var(--primary)]' : ''}`}
                      >
                        <div className="flex flex-wrap items-start justify-between gap-2">
                          <span className="text-sm font-semibold">
                            {row.title}
                          </span>
                          <StatusBadge tone={capabilityTones[row.state]}>
                            {row.state}
                          </StatusBadge>
                        </div>
                        <p className="mt-2 text-sm text-muted-foreground">
                          {row.level} · {row.domain} · {row.freshness}
                        </p>
                        {mode === 'expert' && (
                          <p className="mt-2 break-all font-mono text-xs text-muted-foreground">
                            {row.code}
                          </p>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              ) : (
                <div className="p-6">
                  <CircleHelp
                    aria-hidden="true"
                    className="size-5 text-muted-foreground"
                  />
                  <h2 className="mt-3 font-semibold">
                    No matching capabilities
                  </h2>
                  <p className="mt-2 text-sm text-muted-foreground">
                    Change the filters to review the declared catalog.
                  </p>
                </div>
              )}
            </section>
            <CapabilityDetail
              controller={cap}
              mode={mode}
              go={go}
              openRelated={openRelated}
            />
          </div>
          <section
            className="ovs-surface mt-5 flex flex-wrap items-center justify-between gap-4 p-4"
            aria-label="Capability observation sources"
          >
            <div>
              <h2 className="text-sm font-semibold">
                Collection sources remain independent
              </h2>
              <p className="mt-2 text-sm text-muted-foreground">
                DPDK / Offload use the shared acceleration snapshot. OpenFlow
                uses its captured query. Refreshing the registry does not renew
                either snapshot.
              </p>
            </div>
            <Button variant="outline" onClick={refreshAcceleration}>
              Refresh acceleration evidence
            </Button>
          </section>
        </>
      )}
      <details className="ovs-surface mt-5 p-4">
        <summary className="cursor-pointer text-sm font-semibold">
          Synthetic capability review samples
        </summary>
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <label htmlFor="capability-review" className="text-sm">
            Registry / native sample
          </label>
          <NativeSelect
            id="capability-review"
            aria-label="Capability review sample"
            value={cap.reviewCase}
            disabled={cap.busy || !!cap.readBlock}
            onChange={(event) =>
              cap.review(event.target.value as CapabilityReviewCase)
            }
          >
            {Object.entries(capabilityReviewLabels).map(([key, label]) => (
              <NativeSelectOption key={key} value={key}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          Review controls change synthetic observations. They never install host
          components or submit configuration. Unknown inputs retain an
          explanation.
        </p>
      </details>
    </>
  );
}

export function CapabilityMobileSummary({ controller: cap, go }: Props) {
  return (
    <div className="space-y-4">
      <CapabilitySummary controller={cap} go={go} context="Mobile" />
      <section className="ovs-surface p-5">
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <CheckCircle2 aria-hidden="true" className="size-4" />
          Capability incident context
        </h2>
        <p className="mt-3 text-sm leading-6">
          {cap.denied
            ? 'Capability access is denied. Provider details are hidden.'
            : 'Use the retained availability and source context to triage an incident. Registry collection and new native changes require a larger review surface.'}
        </p>
        {cap.selected && (
          <div className="mt-4">
            <h3 className="text-sm font-semibold">{cap.selected.title}</h3>
            <p className="mt-2 text-sm">
              {cap.selected.state} · {cap.selected.freshness}
            </p>
            <p className="mt-2 text-sm text-muted-foreground">
              {cap.selected.reason}
            </p>
          </div>
        )}
        {cap.transaction.status !== 'idle' && (
          <Button
            className="mt-4"
            variant="outline"
            onClick={() => go('safe-apply')}
          >
            Review existing Safe Apply
          </Button>
        )}
      </section>
    </div>
  );
}
