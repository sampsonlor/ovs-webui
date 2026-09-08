'use client';

import { useEffect } from 'react';
import {
  ArrowRight,
  Cpu,
  Layers,
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Notice, PageHeader, StatusBadge, type Tone } from './foundation';
import {
  accelerationReviewLabels,
  accelerationStates,
  observationFreshness,
  observationText,
  type AccelerationRecord,
  type AccelerationFamily,
  type AccelerationReviewCase,
  type AccelerationState,
  type Observation,
} from '@/lib/acceleration-model';
import type { AccelerationController } from './acceleration-controller';
import type { Mode, View } from '@/lib/ovs-model';

const tones: Record<AccelerationState, Tone> = {
  Enabled: 'success',
  Available: 'info',
  'Missing prerequisites': 'warning',
  Unsupported: 'neutral',
  Unknown: 'uncertain',
};

function Fact({
  fact,
  controller: acc,
  expert = false,
}: {
  fact: Observation;
  controller: AccelerationController;
  expert?: boolean;
}) {
  const freshness = observationFreshness(fact, acc.now, acc.generation);
  const historical =
    freshness !== 'Fresh' ||
    !!acc.serviceBlock ||
    !!acc.failure ||
    acc.scenario === 'stale';
  return (
    <div className="min-w-0 rounded-lg border bg-background p-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <dt className="text-sm text-muted-foreground">{fact.label}</dt>
        <span
          className={`text-xs ${historical ? 'text-amber-700 dark:text-amber-300' : 'text-muted-foreground'}`}
        >
          {historical
            ? `Historical · ${freshness === 'Fresh' ? 'current evidence unavailable' : freshness}`
            : 'Fresh'}
        </span>
      </div>
      <dd className="mt-2 break-words text-base font-semibold">
        {observationText(fact.value)}
      </dd>
      <dd className="mt-2 break-words text-xs text-muted-foreground">
        {fact.authority} · {fact.source}
      </dd>
      {fact.note && (
        <dd className="mt-2 text-sm text-muted-foreground">{fact.note}</dd>
      )}
      {expert && (
        <dd className="mt-3 break-words border-t pt-2 font-mono text-xs text-muted-foreground">
          {fact.key} · {fact.instance} · generation {fact.generation}
          <br />
          {new Date(fact.observedAt).toISOString()}
        </dd>
      )}
    </div>
  );
}

function CapabilityEvidence({
  record,
  controller: acc,
}: {
  record: AccelerationRecord;
  controller: AccelerationController;
}) {
  const result = acc.assessment(record);
  return (
    <div className="space-y-5 p-5">
      <div className="flex flex-wrap items-center gap-3">
        <ShieldCheck aria-hidden="true" className="size-5 text-primary" />
        <h3 className="font-semibold">Capability evidence · {record.title}</h3>
        <StatusBadge tone={tones[result.state]}>{result.state}</StatusBadge>
      </div>
      <p className="text-sm">{result.reason}</p>
      <dl className="grid gap-4 text-sm md:grid-cols-2">
        <div>
          <dt className="text-muted-foreground">Capability / permission</dt>
          <dd className="mt-1 break-all font-mono">
            acceleration.{record.id}.observe
            <br />
            acceleration.observe
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Scope / ownership</dt>
          <dd className="mt-1">
            {record.scope}
            <br />
            Observe · external platform configuration
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Evidence identity</dt>
          <dd className="mt-1 break-words font-mono">
            {acc.snapshot?.instance} · generation {acc.snapshot?.generation}
            <br />
            {acc.snapshot?.id}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Collection boundary</dt>
          <dd className="mt-1">
            2 capabilities · structured synthetic observations
            <br />
            60-second freshness window · no live provider
          </dd>
        </div>
      </dl>
      <Notice title="Phase 1 · Observe only" tone="info">
        Availability describes the environment. Enablement, tuning, switchdev
        changes and driver provisioning are outside this phase. The global
        Capabilities matrix uses this same captured evidence.
      </Notice>
      <h4 className="text-sm font-semibold">
        Required evidence for this declared profile
      </h4>
      <dl className="grid gap-3 md:grid-cols-2">
        {record.facts
          .filter((item) =>
            ['supported', ...record.prerequisites].includes(item.key),
          )
          .map((fact) => (
            <Fact key={fact.key} fact={fact} controller={acc} expert />
          ))}
      </dl>
    </div>
  );
}

export function P1AccelerationView({
  mode,
  controller: acc,
  openObject,
  openDiagnostics,
  openCapability,
}: {
  mode: Mode;
  controller: AccelerationController;
  openObject: (scope: string) => void;
  openDiagnostics: () => void;
  openCapability: (id: AccelerationFamily) => void;
}) {
  useEffect(() => {
    acc.ensureSnapshot();
  }, [acc]);
  const selected = acc.selected;
  const essential = ['configured', 'operational'];
  const displayFacts =
    selected?.facts.filter((item) =>
      mode === 'expert'
        ? !['supported', ...essential, ...selected.prerequisites].includes(
            item.key,
          )
        : ['vhost', 'hw-flows', 'sw-flows', 'fallback-reason'].includes(
            item.key,
          ),
    ) ?? [];
  const denied = acc.serviceBlock?.startsWith('Permission denied');
  return (
    <div className="space-y-5">
      <PageHeader
        id="P1-10"
        eyebrow="Visibility / Acceleration"
        title="DPDK / Offload"
        scope="Observe"
        description="Review readiness, runtime evidence and prerequisites across the acceleration path."
        actions={
          <>
            <Button
              variant="outline"
              onClick={() => openCapability(acc.selected?.id ?? 'dpdk')}
            >
              Open global capability evidence
            </Button>
            <Button
              variant="outline"
              onClick={acc.read}
              disabled={acc.busy || !!acc.serviceBlock}
            >
              <RefreshCw
                aria-hidden="true"
                className={acc.busy ? 'animate-spin' : ''}
              />
              {acc.busy ? 'Reading…' : 'Refresh observations'}
            </Button>
          </>
        }
      />
      <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
        <output>
          {acc.status}
          {acc.snapshot &&
            ` · captured ${new Date(acc.snapshot.capturedAt).toLocaleTimeString()}`}
        </output>
        <span>Read-only · synthetic data · 60 s freshness</span>
      </div>
      {(acc.serviceBlock || acc.failure) && (
        <Notice
          tone="warning"
          title={
            denied
              ? 'Observation access denied'
              : 'Current observations unavailable'
          }
        >
          {acc.serviceBlock ?? acc.failure}
          {denied
            ? ' Cached observations and capability details are cleared.'
            : ' Retained values are historical and do not establish current readiness.'}
        </Notice>
      )}
      {!denied && (
        <>
          <div className="flex flex-wrap items-end gap-3 rounded-lg border bg-card p-4">
            <div className="min-w-48 flex-1">
              <label
                htmlFor="acc-search"
                className="mb-2 block text-sm font-medium"
              >
                Find capability or object
              </label>
              <div className="relative">
                <Search
                  aria-hidden="true"
                  className="absolute left-3 top-2.5 size-4 text-muted-foreground"
                />
                <Input
                  id="acc-search"
                  className="pl-9"
                  value={acc.search}
                  maxLength={128}
                  placeholder="DPDK, offload, br-offload…"
                  onChange={(event) => acc.setSearch(event.target.value)}
                />
              </div>
            </div>
            <div>
              <label
                htmlFor="acc-state"
                className="mb-2 block text-sm font-medium"
              >
                Availability
              </label>
              <NativeSelect
                id="acc-state"
                value={acc.filter}
                onChange={(event) => acc.setFilter(event.target.value)}
              >
                <NativeSelectOption value="all">All states</NativeSelectOption>
                {accelerationStates.map((state) => (
                  <NativeSelectOption value={state} key={state}>
                    {state}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          </div>
          {acc.busy && !acc.snapshot ? (
            <div
              aria-label="Reading acceleration observations"
              className="grid gap-4 md:grid-cols-2"
            >
              <Skeleton className="h-52" />
              <Skeleton className="h-52" />
            </div>
          ) : !acc.visible.length ? (
            <section className="ovs-surface p-8 text-center">
              <Layers
                aria-hidden="true"
                className="mx-auto size-8 text-muted-foreground"
              />
              <h2 className="mt-4 font-semibold">
                {acc.records.length
                  ? 'No matching capabilities'
                  : acc.snapshot
                    ? 'No capability observations'
                    : 'No observations collected'}
              </h2>
              <p className="mt-2 text-sm text-muted-foreground">
                {acc.records.length
                  ? 'Change the search or availability filter to inspect another capability.'
                  : 'An empty result does not establish unsupported hardware or a disabled runtime.'}
              </p>
              <Button
                variant="outline"
                className="mt-4"
                onClick={acc.records.length ? acc.clearFilters : acc.read}
                disabled={
                  !acc.records.length && (!!acc.serviceBlock || acc.busy)
                }
              >
                {acc.records.length ? 'Clear filters' : 'Read observations'}
              </Button>
            </section>
          ) : (
            <div className="grid gap-4 xl:grid-cols-2">
              {acc.visible.map((record) => {
                const result = acc.assessment(record);
                const active = selected?.id === record.id;
                return (
                  <section
                    key={record.id}
                    aria-label={record.title}
                    className={`ovs-surface flex min-w-0 flex-col p-5 ${active ? 'ring-2 ring-primary/60' : ''}`}
                  >
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="flex items-center gap-2">
                        <Cpu
                          aria-hidden="true"
                          className="size-5 text-primary"
                        />
                        <h2 className="font-semibold">{record.title}</h2>
                      </div>
                      <StatusBadge tone={tones[result.state]}>
                        {result.state}
                      </StatusBadge>
                    </div>
                    <p className="mt-3 text-xs text-muted-foreground">
                      {record.scope} · Provider {record.provider.toLowerCase()}
                    </p>
                    <dl className="my-4 grid grid-cols-2 gap-3">
                      {record.facts
                        .filter((item) => essential.includes(item.key))
                        .map((fact) => (
                          <div key={fact.key}>
                            <dt className="text-xs text-muted-foreground">
                              {fact.label}
                            </dt>
                            <dd className="mt-1 font-semibold">
                              {observationText(fact.value)}
                              <span className="ml-2 text-xs font-normal text-muted-foreground">
                                {observationFreshness(
                                  fact,
                                  acc.now,
                                  acc.generation,
                                ) !== 'Fresh' ||
                                acc.serviceBlock ||
                                acc.failure ||
                                acc.scenario === 'stale'
                                  ? 'Historical'
                                  : ''}
                              </span>
                            </dd>
                          </div>
                        ))}
                    </dl>
                    <p className="flex-1 text-sm text-muted-foreground">
                      {result.reason}
                    </p>
                    <p className="mt-3 text-xs text-muted-foreground">
                      {record.related}
                    </p>
                    <Button
                      variant={active ? 'secondary' : 'outline'}
                      className="mt-4 w-full justify-between"
                      aria-pressed={active}
                      onClick={() => acc.select(record.id)}
                    >
                      Inspect {record.title}
                      <ArrowRight aria-hidden="true" />
                    </Button>
                  </section>
                );
              })}
            </div>
          )}
          {selected ? (
            <section
              className="ovs-surface min-w-0"
              aria-label={`${selected.title} details`}
            >
              <div className="flex flex-wrap items-center justify-between gap-3 border-b p-5">
                <div>
                  <h2 className="font-semibold">
                    {selected.title} · observation detail
                  </h2>
                  <p className="mt-1 text-sm text-muted-foreground">
                    {mode === 'standard'
                      ? 'Readiness and the evidence that supports it.'
                      : 'Field authority, instance generation and provider details.'}
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => acc.setTab('capability')}
                >
                  Inspect capability evidence
                </Button>
              </div>
              <Tabs value={acc.tab} onValueChange={acc.setTab}>
                <div className="overflow-x-auto border-b px-5 py-3">
                  <TabsList aria-label="Acceleration detail">
                    <TabsTrigger value="readiness">Readiness</TabsTrigger>
                    <TabsTrigger value="sources">Sources</TabsTrigger>
                    <TabsTrigger value="capability">
                      Capability evidence
                    </TabsTrigger>
                  </TabsList>
                </div>
                <TabsContent value="readiness" className="space-y-5 p-5">
                  {selected.provider !== 'Available' && (
                    <Notice
                      tone="warning"
                      title={`Provider ${selected.provider.toLowerCase()}`}
                    >
                      {selected.provider === 'Degraded'
                        ? 'Essential readiness signals remain available; selected runtime counters are unknown. Enabled does not mean complete telemetry or healthy forwarding.'
                        : 'The provider cannot establish current runtime or platform support.'}
                    </Notice>
                  )}
                  <dl className="grid gap-3 md:grid-cols-2">
                    {selected.facts
                      .filter((item) => essential.includes(item.key))
                      .map((fact) => (
                        <Fact
                          key={fact.key}
                          fact={fact}
                          controller={acc}
                          expert={mode === 'expert'}
                        />
                      ))}
                  </dl>
                  <div>
                    <h3 className="mb-3 text-sm font-semibold">
                      Prerequisites · declared platform profile
                    </h3>
                    <dl className="grid gap-3 md:grid-cols-2">
                      {selected.facts
                        .filter((item) =>
                          selected.prerequisites.includes(item.key),
                        )
                        .map((fact) => (
                          <Fact
                            key={fact.key}
                            fact={fact}
                            controller={acc}
                            expert={mode === 'expert'}
                          />
                        ))}
                    </dl>
                  </div>
                  <div>
                    <h3 className="mb-3 text-sm font-semibold">
                      {mode === 'expert'
                        ? 'Runtime and topology observations'
                        : 'Coverage and runtime findings'}
                    </h3>
                    <dl className="grid gap-3 md:grid-cols-2">
                      {displayFacts.map((fact) => (
                        <Fact
                          key={fact.key}
                          fact={fact}
                          controller={acc}
                          expert={mode === 'expert'}
                        />
                      ))}
                    </dl>
                  </div>
                  <Notice
                    tone="info"
                    title={
                      selected.id === 'dpdk'
                        ? 'Scope of DPDK evidence'
                        : 'Scope of offload evidence'
                    }
                  >
                    {selected.id === 'dpdk'
                      ? 'DPDK initialization is an instance-level signal. Per-Interface coverage, vhost peers and forwarding health require their own evidence. A netdev Bridge alone does not establish DPDK use.'
                      : 'Configured offload and operational readiness do not prove every flow is in hardware. Software fallback is separate from drops. Representor roles require provider evidence; physical connector and PF/VF mappings remain unknown here.'}
                  </Notice>
                </TabsContent>
                <TabsContent value="sources" className="space-y-4 p-5">
                  <h3 className="font-semibold">
                    Field authority and freshness
                  </h3>
                  <p className="text-sm text-muted-foreground">
                    Each value retains its source and capture time. OVSDB
                    configuration cannot replace runtime evidence. Historical
                    values remain labeled when a provider fails or the
                    generation changes.
                  </p>
                  <dl className="grid gap-3 md:grid-cols-2">
                    {selected.facts.map((fact) => (
                      <Fact
                        key={fact.key}
                        fact={fact}
                        controller={acc}
                        expert
                      />
                    ))}
                  </dl>
                </TabsContent>
                <TabsContent value="capability">
                  <CapabilityEvidence record={selected} controller={acc} />
                </TabsContent>
              </Tabs>
              <div className="flex flex-wrap items-center gap-3 border-t p-5">
                <Button
                  variant="outline"
                  onClick={() => openObject('Bridge/br-offload')}
                >
                  Open related Bridge
                </Button>
                <Button variant="outline" onClick={openDiagnostics}>
                  Inspect diagnostic availability
                </Button>
                <p className="text-xs text-muted-foreground">
                  The acceleration diagnostic provider is unavailable in this
                  prototype.
                </p>
              </div>
            </section>
          ) : (
            !!acc.visible.length && (
              <Notice title="Select a visible capability" tone="info">
                Your previous selection is outside the current filter. Select a
                card to inspect its evidence.
              </Notice>
            )
          )}
        </>
      )}
      <details className="rounded-lg border bg-card p-4">
        <summary className="cursor-pointer text-sm font-medium">
          Acceleration review fixtures
        </summary>
        <div className="mt-4 space-y-2">
          <label htmlFor="acc-review" className="text-sm">
            Synthetic observation state · both capabilities
          </label>
          <NativeSelect
            id="acc-review"
            value={acc.reviewCase}
            disabled={acc.busy || !!acc.serviceBlock}
            onChange={(event) =>
              acc.review(event.target.value as AccelerationReviewCase)
            }
          >
            {Object.entries(accelerationReviewLabels).map(([key, label]) => (
              <NativeSelectOption key={key} value={key}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <p className="text-xs text-muted-foreground">
            Review inputs affect observations only. Shared permission, provider
            and transaction states continue to apply.
          </p>
        </div>
      </details>
    </div>
  );
}

export function AccelerationMobileSummary({
  controller: acc,
  go,
}: {
  controller: AccelerationController;
  go: (view: View) => void;
}) {
  return (
    <section className="ovs-surface space-y-4 p-5">
      <p className="ovs-eyebrow">Incident companion · cached observations</p>
      <h2 className="text-xl font-semibold">DPDK / Offload</h2>
      <p className="break-words text-sm text-muted-foreground">{acc.status}</p>
      {acc.records.map((record) => {
        const result = acc.assessment(record);
        return (
          <article key={record.id} className="space-y-3 rounded-lg border p-4">
            <h3 className="font-semibold">{record.title}</h3>
            <StatusBadge tone={tones[result.state]}>{result.state}</StatusBadge>
            <p className="text-sm">{result.reason}</p>
            <p className="text-xs text-muted-foreground">
              {record.scope} · Provider {record.provider.toLowerCase()}
            </p>
            <details>
              <summary className="cursor-pointer text-sm font-medium">
                Capability evidence · {record.title}
              </summary>
              <dl className="mt-3 space-y-3">
                {record.facts
                  .filter((fact) =>
                    [
                      'supported',
                      'configured',
                      'operational',
                      ...record.prerequisites,
                    ].includes(fact.key),
                  )
                  .map((fact) => (
                    <Fact key={fact.key} fact={fact} controller={acc} />
                  ))}
              </dl>
            </details>
          </article>
        );
      })}
      {!acc.records.length && (
        <p className="text-sm">No readable observations are retained.</p>
      )}
      <p className="text-sm text-muted-foreground">
        Read existing evidence here. Collect new observations on tablet or
        desktop. Active Safe Apply and incident evidence remain shared.
      </p>
      <Button variant="outline" onClick={() => go('evidence')}>
        Open incident evidence
      </Button>
    </section>
  );
}
