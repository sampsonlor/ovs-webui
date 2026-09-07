'use client';

import { useState, type ReactNode } from 'react';
import {
  ArrowLeft,
  ArrowRight,
  Download,
  Network,
  Play,
  RefreshCw,
  Search,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { Skeleton } from '@/components/ui/skeleton';
import { Notice, PageHeader, StatusBadge, type Tone } from './foundation';
import {
  openFlowReviewLabels,
  type OpenFlowReviewState,
  type PrototypeMode,
} from '@/app/prototype-model';
import {
  flowFreshness,
  flowOutputBytes,
  flowPage,
  flowQueryErrors,
  openFlowSources,
  type FlowQuery,
} from '@/lib/openflow-model';
import type { OpenFlowController } from './openflow-controller';

function Panel({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="ovs-surface min-w-0">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b p-5">
        <div>
          <h2 className="font-semibold">{title}</h2>
          {description && (
            <p className="mt-1 text-sm text-muted-foreground">{description}</p>
          )}
        </div>
        {actions}
      </div>
      {children}
    </section>
  );
}

function Field({
  id,
  label,
  error,
  children,
}: {
  id: string;
  label: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0 space-y-2">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children}
      {error && (
        <p id={`${id}-error`} className="text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

export function P1OpenFlowView({
  mode,
  controller: flow,
  openObject,
}: {
  mode: PrototypeMode;
  controller: OpenFlowController;
  openObject: (target: string) => void;
}) {
  const [detailTab, setDetailTab] = useState('parsed');
  const { draft, snapshot, busy, failure, serviceBlock, now } = flow;
  const errors = flowQueryErrors(draft);
  const draftSource = openFlowSources.find(
    (item) => item.bridge === draft.bridge,
  )!;
  const pending =
    !!snapshot && JSON.stringify(draft) !== JSON.stringify(snapshot.query);
  const paging = flowPage(snapshot?.rows ?? [], flow.page, flow.selectedId);
  const selected = paging.selected;
  const stale = snapshot && flowFreshness(snapshot, now) === 'Stale';
  const partial =
    snapshot &&
    (snapshot.truncation.length > 0 ||
      !!serviceBlock ||
      snapshot.providerDegraded ||
      flow.degraded);
  const tone: Tone =
    serviceBlock || failure
      ? 'warning'
      : stale || partial
        ? 'warning'
        : snapshot
          ? 'info'
          : 'neutral';
  const input = (
    key: 'priority' | 'match' | 'action' | 'search',
    label: string,
    placeholder: string,
  ) => (
    <Field id={`of-${key}`} label={label} error={errors[key]}>
      <Input
        id={`of-${key}`}
        value={draft[key]}
        placeholder={placeholder}
        disabled={busy || !!serviceBlock}
        maxLength={key === 'priority' ? 6 : 128}
        inputMode={key === 'priority' ? 'numeric' : undefined}
        aria-invalid={!!errors[key]}
        aria-describedby={errors[key] ? `of-${key}-error` : undefined}
        onChange={(event) => flow.setDraft({ [key]: event.target.value })}
      />
    </Field>
  );
  const queryText = (query: FlowQuery) =>
    `Table ${query.table} · priority ${query.priority ? '≥ ' + query.priority : 'any'} · in_port ${query.inPort || 'any'}${query.match ? ' · match: ' + query.match : ''}${query.action ? ' · action: ' + query.action : ''}${query.search ? ' · text: ' + query.search : ''}`;

  return (
    <div className="space-y-5">
      <PageHeader
        id="P1-OF-01"
        eyebrow="Switching / OpenFlow"
        title="OpenFlow Viewer"
        scope="Observe"
        description="Collect a bounded flow snapshot. Inspect forwarding intent, authority and coverage before drawing conclusions."
      />
      <Notice
        tone="info"
        title="Forwarding authority is separate from OVSDB ownership"
      >
        This viewer does not modify flows. An OVS-managed Bridge can still have
        a pipeline owned by an external controller. Port VLAN settings alone do
        not prove how that pipeline forwards traffic.
      </Notice>
      <Panel
        title="Collection query"
        description="Choose one Bridge and prepare the next collection. Current snapshot details keep their captured scope."
      >
        <form
          className="space-y-5 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            flow.collect();
          }}
        >
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <Field id="of-bridge" label="Bridge" error={errors.bridge}>
              <NativeSelect
                id="of-bridge"
                value={draft.bridge}
                disabled={busy || !!serviceBlock}
                onChange={(event) =>
                  flow.setDraft({ bridge: event.target.value, inPort: '' })
                }
              >
                {openFlowSources.map((source) => (
                  <NativeSelectOption key={source.bridge} value={source.bridge}>
                    {source.bridge} · {source.datapath}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field id="of-table" label="Table">
              <NativeSelect
                id="of-table"
                value={draft.table}
                disabled={busy || !!serviceBlock}
                onChange={(event) =>
                  flow.setDraft({ table: event.target.value })
                }
              >
                {['all', '0', '10', '20', '30', '90'].map((table) => (
                  <NativeSelectOption key={table} value={table}>
                    {table === 'all' ? 'All sampled tables' : `Table ${table}`}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            {input('priority', 'Minimum priority', '0–65535')}
            <Field id="of-inport" label="in_port" error={errors.inPort}>
              <NativeSelect
                id="of-inport"
                value={draft.inPort}
                disabled={busy || !!serviceBlock}
                onChange={(event) =>
                  flow.setDraft({ inPort: event.target.value })
                }
              >
                <NativeSelectOption value="">
                  Any sampled input
                </NativeSelectOption>
                {draftSource.ports.map((port) => (
                  <NativeSelectOption key={port} value={port}>
                    {port}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
          </div>
          <div className="grid gap-4 md:grid-cols-3">
            {input('match', 'Match contains', 'e.g. dl_vlan=120')}
            {input('action', 'Action contains', 'e.g. output:12')}
            {input('search', 'Cookie or text', 'Search captured fields')}
          </div>
          <p className="text-xs text-muted-foreground">
            Viewer filters use literal text and a minimum priority; they are not
            an ovs-ofctl command parser. All values are synthetic.
          </p>
          <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-4">
            <p className="text-sm text-muted-foreground">
              5 s timeout · 500 rows · 256 KiB export · 30 s freshness
            </p>
            <Button
              type="submit"
              disabled={
                busy || !!serviceBlock || Object.keys(errors).length > 0
              }
            >
              <Play />
              {busy ? 'Collecting…' : 'Run bounded query'}
            </Button>
          </div>
        </form>
      </Panel>
      {pending && (
        <Notice tone="neutral" title="Query changes are not collected yet">
          The snapshot below belongs to <strong>{snapshot.query.bridge}</strong>{' '}
          and its captured filters. Run the query to replace it.
        </Notice>
      )}
      {serviceBlock && (
        <Notice tone="warning" title="Collection unavailable">
          {serviceBlock}
          {snapshot &&
            ' The retained snapshot is historical evidence; current coverage is unverified.'}
        </Notice>
      )}
      {failure && !serviceBlock && (
        <Notice
          tone={failure === 'permission-denied' ? 'danger' : 'warning'}
          title={openFlowReviewLabels[failure]}
        >
          {failure === 'permission-denied'
            ? 'No rows, details or export are available with this permission state.'
            : failure === 'provider-unavailable'
              ? 'No snapshot was returned. Provider availability and forwarding authority cannot be inferred.'
              : 'The bounded query did not complete. No partial rows are presented as a successful result.'}{' '}
          Retry keeps the selected review case until you change it.
        </Notice>
      )}
      {snapshot && (stale || partial) && (
        <Notice
          tone="warning"
          title={stale ? 'Stale snapshot' : 'Partial coverage'}
        >
          {stale
            ? 'This capture is outside its 30-second freshness window. '
            : ''}
          {snapshot.truncation.length
            ? `The result hit ${snapshot.truncation.join(' and ')}; ${snapshot.rows.length} of ${snapshot.matchedRows} matching fixture rows were retained. `
            : ''}
          {snapshot.providerDegraded || flow.degraded
            ? 'The provider is degraded; completeness is unverified. '
            : ''}
          Missing rows are not evidence that a flow is absent.
        </Notice>
      )}
      <Panel
        title="Captured snapshot"
        description={
          snapshot
            ? `${snapshot.query.bridge} · ${queryText(snapshot.query)}`
            : 'No completed snapshot is available.'
        }
        actions={
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              disabled={!snapshot || busy || !!serviceBlock}
              onClick={flow.refresh}
            >
              <RefreshCw />
              Refresh captured query
            </Button>
            <Button
              variant="outline"
              disabled={!snapshot || busy || !!failure}
              onClick={flow.exportSnapshot}
            >
              <Download />
              Export JSON
            </Button>
          </div>
        }
      >
        <div className="space-y-4 p-5">
          <StatusBadge tone={tone}>{flow.status}</StatusBadge>
          {snapshot && (
            <dl className="grid gap-4 text-sm md:grid-cols-2 xl:grid-cols-4">
              <div>
                <dt className="text-muted-foreground">Bridge / datapath</dt>
                <dd className="mt-1 font-medium">
                  {snapshot.query.bridge} / {snapshot.source.datapath}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Protocol / authority</dt>
                <dd className="mt-1">
                  {snapshot.source.protocol}
                  <span className="mt-1 block text-muted-foreground">
                    {snapshot.source.authority}
                  </span>
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Observed at</dt>
                <dd className="mt-1 break-words">
                  {new Date(snapshot.observedAt).toISOString()}
                  <span className="mt-1 block text-muted-foreground">
                    {Math.max(
                      0,
                      Math.floor((now - snapshot.observedAt) / 1000),
                    )}{' '}
                    s ago · {snapshot.elapsedMs} ms collection
                  </span>
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Retained output</dt>
                <dd className="mt-1">
                  {snapshot.rows.length} rows ·{' '}
                  {(
                    flowOutputBytes(snapshot, now || snapshot.observedAt) / 1024
                  ).toFixed(1)}{' '}
                  KiB
                  <span className="mt-1 block text-muted-foreground">
                    {partial
                      ? 'Partial coverage'
                      : 'Complete for captured query'}
                  </span>
                </dd>
              </div>
            </dl>
          )}
        </div>
        {busy && (
          <output
            className="space-y-3 border-t p-5"
            aria-label="Collecting flow snapshot"
          >
            <Skeleton className="h-6 w-2/3" />
            <Skeleton className="h-12 w-full" />
            <p className="text-sm text-muted-foreground">
              Collecting one bounded synthetic snapshot. Any retained rows below
              belong to the previous capture.
            </p>
          </output>
        )}
        {!snapshot && !busy && (
          <div className="border-t p-8 text-center">
            <Search className="mx-auto mb-3 size-7 text-muted-foreground" />
            <h3 className="font-medium">
              {failure || serviceBlock
                ? 'No readable flow result'
                : 'Ready to inspect a pipeline'}
            </h3>
            <p className="mt-2 text-sm text-muted-foreground">
              {failure || serviceBlock
                ? 'Resolve the collection state, then run a permitted query.'
                : 'Run a bounded query to capture flow rows, counters and source metadata.'}
            </p>
          </div>
        )}
        {snapshot && !snapshot.rows.length && (
          <div className="border-t p-8 text-center">
            <h3 className="font-medium">
              {snapshot.matchedRows
                ? 'No rows fit the output budget'
                : '0 matching rows'}
            </h3>
            <p className="mt-2 text-sm text-muted-foreground">
              {snapshot.matchedRows
                ? 'Matching source rows exceeded the output budget. Narrow the query.'
                : 'The captured query returned no rows.'}{' '}
              This is not a provider-health or end-to-end forwarding conclusion.
            </p>
            <Button
              className="mt-4"
              variant="outline"
              disabled={
                busy ||
                !!serviceBlock ||
                (draft.bridge === snapshot.query.bridge &&
                  draft.table === 'all' &&
                  !draft.priority &&
                  !draft.inPort &&
                  !draft.match &&
                  !draft.action &&
                  !draft.search)
              }
              onClick={() =>
                flow.setDraft({
                  ...snapshot.query,
                  table: 'all',
                  priority: '',
                  inPort: '',
                  match: '',
                  action: '',
                  search: '',
                })
              }
            >
              Prepare broader filters
            </Button>
          </div>
        )}
        {!!snapshot?.rows.length && (
          <>
            <div className="hidden border-t lg:block">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Flow / priority</TableHead>
                    <TableHead>Match</TableHead>
                    <TableHead>Actions</TableHead>
                    <TableHead className="text-right">Packets</TableHead>
                    <TableHead>Inspect</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {paging.visible.map((row) => (
                    <TableRow
                      key={row.id}
                      data-state={
                        selected?.id === row.id ? 'selected' : undefined
                      }
                    >
                      <TableCell>
                        <div className="font-medium">Table {row.table}</div>
                        <div className="mt-1 text-xs text-muted-foreground">
                          Priority {row.priority}
                        </div>
                        {mode === 'expert' && (
                          <div className="mt-2 font-mono text-xs">
                            {row.cookie}
                          </div>
                        )}
                      </TableCell>
                      <TableCell className="max-w-56 whitespace-normal break-words font-mono text-xs">
                        {row.match || 'any'}
                      </TableCell>
                      <TableCell className="max-w-48 whitespace-normal break-words font-mono text-xs">
                        {row.action}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {row.packets.toLocaleString('en-US')}
                      </TableCell>
                      <TableCell>
                        <Button
                          variant={
                            selected?.id === row.id ? 'secondary' : 'outline'
                          }
                          size="sm"
                          aria-label={`Inspect flow ${row.cookie}`}
                          aria-pressed={selected?.id === row.id}
                          onClick={() => {
                            flow.select(row.id);
                            setDetailTab('parsed');
                          }}
                        >
                          Inspect
                          <ArrowRight />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="space-y-3 border-t p-5 lg:hidden">
              {paging.visible.map((row) => (
                <button
                  key={row.id}
                  type="button"
                  aria-pressed={selected?.id === row.id}
                  onClick={() => {
                    flow.select(row.id);
                    setDetailTab('parsed');
                  }}
                  className={`w-full rounded-xl border p-4 text-left text-sm focus-visible:outline-2 focus-visible:outline-ring ${selected?.id === row.id ? 'border-primary bg-accent' : 'bg-background'}`}
                >
                  <span className="flex justify-between gap-2 font-semibold">
                    <span>
                      Table {row.table} · Priority {row.priority}
                    </span>
                    <ArrowRight className="size-4 shrink-0" />
                  </span>
                  <span className="mt-2 block break-all font-mono text-xs">
                    {row.match || 'any'}
                  </span>
                  <span className="mt-2 block break-all font-mono text-xs">
                    → {row.action}
                  </span>
                  <span className="mt-3 block text-muted-foreground">
                    {row.packets.toLocaleString('en-US')} packets
                  </span>
                </button>
              ))}
            </div>
            <nav
              aria-label="Flow result pagination"
              className="flex flex-wrap items-center justify-between gap-3 border-t p-5"
            >
              <p className="text-sm text-muted-foreground">
                Page {paging.page} of {paging.pageCount} ·{' '}
                {snapshot.rows.length} retained rows
              </p>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  disabled={paging.page === 1}
                  onClick={() => flow.setPage(paging.page - 1)}
                >
                  <ArrowLeft />
                  Previous
                </Button>
                <Button
                  variant="outline"
                  disabled={paging.page === paging.pageCount}
                  onClick={() => flow.setPage(paging.page + 1)}
                >
                  Next
                  <ArrowRight />
                </Button>
              </div>
            </nav>
          </>
        )}
      </Panel>
      {selected && snapshot && (
        <Panel
          title="Flow detail"
          description={`Table ${selected.table} · Priority ${selected.priority} · ${snapshot.query.bridge}`}
          actions={
            <Button
              variant="outline"
              onClick={() => openObject(selected.relatedObject)}
            >
              <Network />
              {selected.relatedObject}
            </Button>
          }
        >
          <div className="space-y-4 p-5">
            <div className="flex flex-wrap gap-2">
              <StatusBadge tone="neutral">Observe</StatusBadge>
              <StatusBadge tone="info">{snapshot.source.authority}</StatusBadge>
              {stale && (
                <StatusBadge tone="warning">Stale evidence</StatusBadge>
              )}
            </div>
            <Tabs
              value={mode === 'expert' ? detailTab : 'parsed'}
              onValueChange={(value) => setDetailTab(String(value))}
            >
              {mode === 'expert' && (
                <TabsList aria-label="Flow representation">
                  <TabsTrigger value="parsed">Parsed</TabsTrigger>
                  <TabsTrigger value="raw">Raw output</TabsTrigger>
                </TabsList>
              )}
              <TabsContent value="parsed">
                <dl className="grid gap-5 text-sm md:grid-cols-2">
                  <div>
                    <dt className="text-muted-foreground">Match</dt>
                    <dd className="mt-2 break-all font-mono">
                      {selected.match || 'any'}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Actions</dt>
                    <dd className="mt-2 break-all font-mono">
                      {selected.action}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">
                      Counters at capture
                    </dt>
                    <dd className="mt-2">
                      {selected.packets.toLocaleString('en-US')} packets ·{' '}
                      {selected.bytes.toLocaleString('en-US')} bytes
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted-foreground">Input port</dt>
                    <dd className="mt-2">
                      {selected.inPort || 'Not constrained by this match'}
                    </dd>
                  </div>
                  {mode === 'expert' && (
                    <>
                      <div>
                        <dt className="text-muted-foreground">
                          Cookie / duration
                        </dt>
                        <dd className="mt-2 break-all font-mono">
                          {selected.cookie} · {selected.duration} s
                        </dd>
                      </div>
                      <div>
                        <dt className="text-muted-foreground">Snapshot ID</dt>
                        <dd className="mt-2 break-all font-mono text-xs">
                          {snapshot.id}
                        </dd>
                      </div>
                    </>
                  )}
                </dl>
              </TabsContent>
              {mode === 'expert' && (
                <TabsContent value="raw">
                  <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-xl border bg-muted p-4 text-xs leading-6">
                    {selected.raw}
                  </pre>
                  <p className="mt-3 text-sm text-muted-foreground">
                    Exact synthetic source row. Unknown provider fields must
                    remain verbatim in a future collector.
                  </p>
                </TabsContent>
              )}
            </Tabs>
          </div>
        </Panel>
      )}
      <details className="ovs-surface p-5">
        <summary className="cursor-pointer text-sm font-medium focus-visible:outline-2 focus-visible:outline-ring">
          Synthetic collection review cases
        </summary>
        <p className="my-3 text-sm text-muted-foreground">
          Preview the draft query with one declared response. A review case does
          not change live permissions, configuration or Event/Audit records.
          Fresh bounded result resets the response fixture; br-offload remains
          unavailable.
        </p>
        <div className="max-w-md">
          <label htmlFor="of-case" className="mb-2 block text-sm">
            Collection response
          </label>
          <NativeSelect
            id="of-case"
            value={flow.reviewCase}
            disabled={busy || !!serviceBlock}
            onChange={(event) =>
              flow.review(event.target.value as OpenFlowReviewState)
            }
          >
            {Object.entries(openFlowReviewLabels).map(([value, label]) => (
              <NativeSelectOption key={value} value={value}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
      </details>
    </div>
  );
}
