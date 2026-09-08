'use client';

import { Activity, ArrowRight, RefreshCw, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import { Notice, PageHeader, StatusBadge, type Tone } from './foundation';
import type { HealthController } from './health-controller';
import {
  healthDomains,
  healthReviewLabels,
  rollupHealth,
  type HealthAction,
  type HealthComponent,
  type HealthReviewCase,
  type HealthStatus,
} from '@/lib/health-model';
import type { Mode } from '@/lib/ovs-model';

export const healthTones: Record<HealthStatus, Tone> = {
  Healthy: 'success',
  Recovering: 'info',
  Unknown: 'uncertain',
  Degraded: 'warning',
  Critical: 'danger',
  'Recovery Required': 'danger',
};
const timestamp = (time: number) =>
  time
    ? new Date(time)
        .toISOString()
        .replace('T', ' ')
        .replace(/\.\d{3}Z$/, ' UTC')
    : 'Not observed';
type Props = {
  controller: HealthController;
  onAction: (action: HealthAction) => void;
};

function HealthOverview({ controller: health, onAction }: Props) {
  const priority = health.incidents[0];
  const deadline = health.transaction.deadline;
  return (
    <section
      className="ovs-surface min-w-0 p-5"
      aria-label="Shared health summary"
    >
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="ovs-eyebrow">Managed host · sw-edge-01</p>
          <div className="mt-2 flex flex-wrap items-center gap-3">
            <Activity aria-hidden="true" className="size-5 text-primary" />
            <h2 className="text-xl font-semibold">Operational health</h2>
            <StatusBadge tone={healthTones[health.overall]}>
              {health.overall}
            </StatusBadge>
          </div>
        </div>
        <div className="text-sm text-muted-foreground">
          <p>
            {health.components.length} checks · {health.unknown} unknown
          </p>
          <p>Generation {health.generation} · synthetic observations</p>
        </div>
      </div>
      <p className="mt-4 text-sm leading-6">{health.reason}</p>
      <p className="mt-2 text-xs text-muted-foreground">
        Latest change{' '}
        {timestamp(Math.max(0, ...health.components.map((row) => row.since)))}
      </p>
      {priority && (
        <div className="mt-4 flex flex-wrap items-center gap-3 border-t pt-4">
          <span className="text-sm font-semibold">
            {priority.domain} / {priority.label}
          </span>
          <Button
            variant="outline"
            className="h-auto min-h-9 max-w-full whitespace-normal text-left"
            onClick={() => onAction(priority.action)}
          >
            {priority.action.label}
            <ArrowRight aria-hidden="true" />
          </Button>
        </div>
      )}
      {health.transaction.status === 'countdown' && deadline && (
        <p className="mt-4 text-sm font-semibold text-amber-700 dark:text-amber-300">
          Existing Safe Apply ·{' '}
          {Math.max(0, Math.ceil((deadline - health.now) / 1000))} seconds
          remaining · confirmation stays in the transaction workspace.
        </p>
      )}
    </section>
  );
}

function ComponentDetail({
  component: row,
  mode,
  onAction,
}: {
  component: HealthComponent;
  mode: Mode;
  onAction: Props['onAction'];
}) {
  return (
    <section
      className="ovs-surface min-w-0 p-5"
      aria-label="Health component detail"
    >
      <p className="ovs-eyebrow">{row.domain} · responsibility</p>
      <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">{row.label}</h2>
        <StatusBadge tone={healthTones[row.status]}>{row.status}</StatusBadge>
      </div>
      <p className="mt-4 text-sm leading-6">{row.reason}</p>
      <p className="mt-3 text-sm leading-6 text-muted-foreground">
        {row.impact}
      </p>
      <dl className="mt-5 grid gap-4 border-t pt-4 text-sm sm:grid-cols-2">
        <div className="min-w-0 sm:col-span-2">
          <dt className="text-muted-foreground">
            Observed value {row.historicalStatus && '· retained history'}
          </dt>
          <dd className="mt-1 break-words font-medium">{row.value}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-muted-foreground">Source</dt>
          <dd className="mt-1 break-words">{row.source}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-muted-foreground">Related resource</dt>
          <dd className="mt-1 break-words">{row.related}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-muted-foreground">
            Observed at · {row.freshness}
          </dt>
          <dd className="mt-1 break-words text-xs">
            {timestamp(row.observedAt)}
          </dd>
        </div>
        <div className="min-w-0">
          <dt className="text-muted-foreground">Current status since</dt>
          <dd className="mt-1 break-words text-xs">{timestamp(row.since)}</dd>
        </div>
      </dl>
      {mode === 'expert' && (
        <div className="mt-4 space-y-2 rounded border bg-muted/30 p-3 text-xs">
          <p className="break-words font-mono">
            {row.code} · generation{' '}
            {row.generation ?? 'not captured by this collector'}
          </p>
          <p>
            Rule: confirmed recovery requirement → confirmed critical failure →
            degradation → missing or stale evidence → recovering → healthy
            sample. Unknown coverage remains visible alongside confirmed
            incidents.
          </p>
          {row.historicalStatus && (
            <p>
              Previous observation: {row.historicalStatus}. It is not a current
              verdict.
            </p>
          )}
        </div>
      )}
      <Button
        className="mt-5 h-auto min-h-9 max-w-full whitespace-normal text-left"
        variant="outline"
        onClick={() => onAction(row.action)}
      >
        {row.action.label}
        <ArrowRight aria-hidden="true" className="shrink-0" />
      </Button>
      {row.id === 'manager-db' && row.status === 'Recovery Required' && (
        <p className="mt-3 text-sm text-muted-foreground">
          Review journal evidence and the recovery runbook with the operator.
          Restore is not connected in this prototype. Preserve the existing
          database and transaction history.
        </p>
      )}
    </section>
  );
}

function HealthTimeline({ controller: health, onAction }: Props) {
  return (
    <section
      className="ovs-surface min-w-0 p-5"
      aria-label="Health event timeline"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-base font-semibold">Recent health changes</h2>
        <Button
          variant="ghost"
          size="sm"
          className="h-auto min-h-7 max-w-full whitespace-normal"
          onClick={() =>
            onAction({ label: 'Open shared evidence', view: 'evidence' })
          }
        >
          All events / audit
          <ArrowRight aria-hidden="true" />
        </Button>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        Meaningful state changes are retained in shared Events. Recovery
        preserves Audit records and diagnostic results.
      </p>
      {health.timeline.length ? (
        <ol className="mt-4 divide-y">
          {health.timeline.map((entry, index) => (
            <li
              key={`${entry.at}-${entry.correlation}-${index}`}
              className="py-3 text-sm"
            >
              <p className="break-words">{entry.text}</p>
              <p className="mt-1 break-words text-xs text-muted-foreground">
                {timestamp(entry.at)} · {entry.object}
              </p>
            </li>
          ))}
        </ol>
      ) : (
        <p className="mt-4 text-sm text-muted-foreground">
          No health transitions recorded this session.
        </p>
      )}
    </section>
  );
}

export function P1HealthView({
  controller: health,
  onAction,
  mode,
}: Props & { mode: Mode }) {
  const denied = health.serviceBlock?.startsWith('Permission denied');
  return (
    <div className="ovs-health space-y-5">
      <PageHeader
        id="P1-11"
        eyebrow="Operations"
        title="System Health"
        scope="Observe"
        description="Find the responsible component, check its evidence, and choose a controlled next step."
        actions={
          <Button
            variant="outline"
            disabled={health.busy || !!health.serviceBlock}
            onClick={health.read}
          >
            <RefreshCw
              aria-hidden="true"
              className={health.busy ? 'animate-spin' : ''}
            />
            {health.busy ? 'Reading health…' : 'Refresh health'}
          </Button>
        }
      />
      <p className="text-xs text-muted-foreground">
        One shared observation cache · 60-second health freshness window · no
        live host connection
      </p>
      {(health.serviceBlock || health.failure) && (
        <Notice
          tone="uncertain"
          title={
            denied
              ? 'Health access denied'
              : 'Current health evidence unavailable'
          }
        >
          {health.serviceBlock ?? health.failure}
        </Notice>
      )}
      {!denied && (
        <>
          <HealthOverview controller={health} onAction={onAction} />
          <div
            className="grid gap-3 md:grid-cols-2 xl:grid-cols-3"
            aria-label="Health domains"
          >
            {healthDomains.map((domain) => {
              const rows = health.components.filter(
                (row) => row.domain === domain,
              );
              const status = rollupHealth(rows);
              const main = health.incidents.find(
                (row) => row.domain === domain,
              );
              return (
                <button
                  type="button"
                  key={domain}
                  aria-label={`Inspect ${domain} health`}
                  aria-pressed={health.domain === domain}
                  onClick={() => health.setDomain(domain)}
                  className={`min-w-0 rounded-lg border p-4 text-left transition-colors hover:bg-muted/50 ${health.domain === domain ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'bg-card'}`}
                >
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="text-sm font-semibold">{domain}</span>
                    <StatusBadge tone={healthTones[status]}>
                      {status}
                    </StatusBadge>
                  </div>
                  <p className="mt-3 line-clamp-2 text-sm text-muted-foreground">
                    {main?.reason ??
                      (rows.length
                        ? `${rows.length} current checks in this domain.`
                        : 'No observations for this domain.')}
                  </p>
                  <p className="mt-3 text-xs text-muted-foreground">
                    {rows.length} checks ·{' '}
                    {rows.filter((row) => row.status === 'Unknown').length}{' '}
                    unknown
                  </p>
                </button>
              );
            })}
          </div>
          <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
            <section
              className="ovs-surface min-w-0 p-5"
              aria-label="Health components"
            >
              <div className="flex flex-wrap items-center justify-between gap-3">
                <h2 className="font-semibold">
                  {mode === 'expert' ? 'Component tree' : 'Component checks'}
                </h2>
                <Button variant="ghost" size="sm" onClick={health.clearFilters}>
                  Show all checks
                </Button>
              </div>
              <div className="relative mt-4">
                <Search
                  aria-hidden="true"
                  className="absolute left-3 top-2.5 size-4 text-muted-foreground"
                />
                <Input
                  aria-label="Search health components"
                  placeholder="Find a component or object"
                  className="pl-9"
                  value={health.search}
                  onChange={(event) => health.setSearch(event.target.value)}
                />
              </div>
              <p className="my-3 text-xs text-muted-foreground">
                {health.domain === 'all' ? 'All domains' : health.domain} ·{' '}
                {health.visible.length} checks
              </p>
              <ul className="max-h-[32rem] space-y-2 overflow-y-auto p-0.5">
                {health.visible.map((row) => (
                  <li key={row.id}>
                    <button
                      type="button"
                      aria-label={`Inspect ${row.label}`}
                      aria-pressed={health.selected?.id === row.id}
                      onClick={() => health.select(row.id)}
                      className={`w-full min-w-0 rounded border p-3 text-left hover:bg-muted/50 ${health.selected?.id === row.id ? 'border-primary bg-primary/5' : ''}`}
                    >
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-sm font-medium">{row.label}</span>
                        <StatusBadge tone={healthTones[row.status]}>
                          {row.status}
                        </StatusBadge>
                      </div>
                      <p className="mt-2 break-words text-xs text-muted-foreground">
                        {row.domain} · {row.related} · {row.freshness}
                      </p>
                      {mode === 'expert' && (
                        <p className="mt-1 break-words font-mono text-xs text-muted-foreground">
                          {row.code}
                        </p>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
              {!health.visible.length && (
                <p className="py-6 text-sm text-muted-foreground">
                  No matching components. Clear the filters to review all
                  evidence.
                </p>
              )}
            </section>
            {health.selected ? (
              <ComponentDetail
                component={health.selected}
                mode={mode}
                onAction={onAction}
              />
            ) : (
              <Notice title="No component selected" tone="neutral">
                Select a component after clearing the filters.
              </Notice>
            )}
          </div>
          <HealthTimeline controller={health} onAction={onAction} />
        </>
      )}
      <details className="rounded-lg border bg-muted/20 p-4 text-sm">
        <summary className="cursor-pointer font-medium">
          Prototype review cases
        </summary>
        <p className="mt-3 text-xs text-muted-foreground">
          Observation samples exercise health rules. Existing transaction locks,
          provider snapshots and Job results retain authority.
        </p>
        <NativeSelect
          aria-label="Health review case"
          className="mt-3 w-full"
          value={health.reviewCase}
          disabled={health.busy || !!health.serviceBlock}
          onChange={(event) =>
            health.review(event.target.value as HealthReviewCase)
          }
        >
          {Object.entries(healthReviewLabels).map(([value, label]) => (
            <NativeSelectOption key={value} value={value}>
              {label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </details>
    </div>
  );
}

export function HealthMobileSummary({ controller: health, onAction }: Props) {
  const incident = health.incidents[0];
  const mobileAction = (action: HealthAction) =>
    onAction(
      'diagnostic' in action
        ? { label: 'Inspect diagnostic evidence', view: 'evidence' }
        : action,
    );
  return (
    <div className="ovs-health space-y-4">
      <p className="ovs-eyebrow">System Health · incident companion</p>
      {/* Incident priority is independent of desktop filters and selections. */}
      <HealthOverview controller={health} onAction={mobileAction} />
      {health.serviceBlock || health.failure ? (
        <Notice tone="uncertain" title="Current observation unavailable">
          {health.serviceBlock ?? health.failure}
        </Notice>
      ) : null}
      {incident && (
        <ComponentDetail
          component={{
            ...incident,
            action:
              'diagnostic' in incident.action
                ? { label: 'Inspect diagnostic evidence', view: 'evidence' }
                : incident.action,
          }}
          mode="standard"
          onAction={mobileAction}
        />
      )}
      <p className="text-sm text-muted-foreground">
        Review incidents and existing Safe Apply here. Prepare new diagnostics
        on tablet or desktop.
      </p>
      <Button
        variant="outline"
        disabled={health.busy || !!health.serviceBlock}
        onClick={health.read}
      >
        {health.busy ? 'Reading health…' : 'Refresh health'}
      </Button>
      <HealthTimeline controller={health} onAction={mobileAction} />
    </div>
  );
}
