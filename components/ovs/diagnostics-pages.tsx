'use client';

import { useEffect, useState, type ReactNode } from 'react';
import {
  Activity,
  ArrowLeft,
  ChevronRight,
  Download,
  FileClock,
  Network,
  Play,
  RefreshCw,
  Search,
  Square,
  Terminal,
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
import { Skeleton } from '@/components/ui/skeleton';
import { Progress } from '@/components/ui/progress';
import { Notice, PageHeader, StatusBadge } from './foundation';
import { diagnosticRunBlock } from '@/lib/p1-control';
import {
  diagnostics,
  diagnosticDefinition,
  diagnosticActive,
  diagnosticCancelBlock,
  diagnosticCorrelation,
  diagnosticExport,
  diagnosticJobId,
  diagnosticJobTone,
  diagnosticParameterErrors,
  diagnosticResult,
  diagnosticRetryable,
  diagnosticScopeError,
  diagnosticServiceBlock,
  type DiagnosticDefinition,
} from '@/lib/diagnostics-model';
import type { Scenario } from '@/lib/change-control';
import {
  diagnosticJobLabels,
  type DiagnosticInputState,
  type DiagnosticJobState,
  type DiagnosticParameters,
  type DiagnosticRequest,
  type P1DiagnosticsPage,
  type P1View,
  type PrototypeMode,
} from '@/app/prototype-model';

type Props = {
  view: P1DiagnosticsPage;
  mode: PrototypeMode;
  scenario: Scenario;
  selectedDiagnostic: string;
  setSelectedDiagnostic: (id: string) => void;
  inputState: DiagnosticInputState;
  setInputState: (state: DiagnosticInputState) => void;
  jobState: DiagnosticJobState;
  setJobState: (state: DiagnosticJobState) => void;
  scope: string;
  setScope: (scope: string) => void;
  parameters: DiagnosticParameters;
  setParameters: (parameters: DiagnosticParameters) => void;
  request: DiagnosticRequest | null;
  reviewOnly: boolean;
  busy: boolean;
  origin: string | null;
  clearOrigin: () => void;
  runDiagnostic: () => void;
  cancelDiagnostic: () => void;
  retryDiagnostic: () => void;
  openEvidence: (kind: 'event' | 'audit') => void;
  openObject: () => void;
  go: (view: P1View) => void;
  notify: (message: string) => void;
};

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
        <div className="min-w-0">
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

function Facts({ rows }: { rows: Array<[string, ReactNode]> }) {
  return (
    <dl className="divide-y px-5">
      {rows.map(([label, value]) => (
        <div
          key={label}
          className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-2 py-4 text-sm"
        >
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="min-w-0 break-words font-medium">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Availability({ definition }: { definition: DiagnosticDefinition }) {
  return (
    <StatusBadge
      tone={
        definition.availability === 'Available'
          ? 'info'
          : definition.availability === 'Permission denied'
            ? 'danger'
            : 'uncertain'
      }
    >
      {definition.availability}
    </StatusBadge>
  );
}

function JobBadge({ state }: { state: DiagnosticJobState }) {
  return (
    <StatusBadge tone={diagnosticJobTone(state)}>
      {diagnosticJobLabels[state]}
    </StatusBadge>
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
  error?: string | null;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0">
      <label htmlFor={id} className="mb-2 block text-sm font-medium">
        {label}
      </label>
      {children}
      {error && (
        <p id={`${id}-error`} className="mt-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

function OutputContract({ definition }: { definition: DiagnosticDefinition }) {
  return (
    <>
      {definition.textLineLimit
        ? `Structured summary · text capped at ${definition.textLineLimit} lines`
        : 'Structured output only'}{' '}
      · 64 KiB ceiling
    </>
  );
}

function ResourceState({
  scenario,
  hasJob,
  go,
}: {
  scenario: Scenario;
  hasJob: boolean;
  go: Props['go'];
}) {
  const loading = scenario === 'loading';
  const empty = scenario === 'empty';
  const denied = scenario === 'permission-denied';
  const title = loading
    ? 'Loading diagnostic catalog'
    : empty
      ? 'No diagnostic templates'
      : denied
        ? 'Diagnostic permission required'
        : scenario === 'provider-unavailable'
          ? 'Diagnostic provider unavailable'
          : 'Unable to load diagnostic catalog';
  return (
    <>
      <PageHeader
        id="P1-07"
        eyebrow="Operations / Diagnostics"
        title="Diagnostics"
        description="Choose a bounded operation and review its target before starting a Job."
        actions={
          hasJob && !denied ? (
            <Button variant="outline" onClick={() => go('diagnostic-run')}>
              Open latest job
            </Button>
          ) : undefined
        }
      />
      <Notice tone={loading || empty ? 'neutral' : 'uncertain'} title={title}>
        {loading
          ? 'Waiting for template and capability information.'
          : empty
            ? 'The catalog request succeeded and returned no templates.'
            : denied
              ? 'Catalog and Job details are withheld. Expert mode does not change access.'
              : 'A valid catalog response is unavailable. This is different from an empty catalog.'}
      </Notice>
      {loading && (
        <div
          className="ovs-surface mt-5 space-y-5 p-5"
          aria-label="Loading diagnostic catalog"
        >
          <Skeleton className="h-8 w-2/3" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      )}
    </>
  );
}

function DiagnosticsHub(props: Props & { width: number }) {
  const {
    mode,
    scenario,
    selectedDiagnostic,
    setSelectedDiagnostic,
    inputState,
    setInputState,
    scope,
    setScope,
    parameters,
    setParameters,
    busy,
    request,
    reviewOnly,
    origin,
    clearOrigin,
    runDiagnostic,
    go,
    width,
  } = props;
  const [search, setSearch] = useState('');
  const [category, setCategory] = useState('All');
  const selected = diagnosticDefinition(selectedDiagnostic) ?? diagnostics[0];
  const desktop = width >= 1024;
  const catalog = diagnostics.filter((item) => desktop || item.recommended);
  const shown = catalog.filter(
    (item) =>
      (category === 'All' || category === item.category) &&
      `${item.name} ${item.description} ${item.category}`
        .toLowerCase()
        .includes(search.trim().toLowerCase()),
  );
  const blocked = diagnosticRunBlock(
    selected.id,
    scope,
    inputState,
    busy,
    width,
    parameters,
    scenario,
  );
  const parameterErrors = diagnosticParameterErrors(selected.id, parameters);
  const scopeError =
    diagnosticScopeError(selected.id, scope) ??
    (inputState === 'validation-error'
      ? 'This review case has incompatible object input.'
      : inputState === 'scope-too-broad'
        ? 'This review case exceeds the allowed member scope. Choose one bounded target.'
        : null);
  const serviceBlocked = diagnosticServiceBlock(scenario);
  const locked = busy || !!serviceBlocked || width < 768;
  const selectedOutOfView = !shown.some((item) => item.id === selected.id);
  const clearFilters = () => {
    setSearch('');
    setCategory('All');
  };
  const categories = ['All', ...new Set(catalog.map((item) => item.category))];
  if (!categories.includes(category)) categories.push(category);
  return (
    <>
      <PageHeader
        id="P1-07"
        eyebrow="Operations / Diagnostics"
        title="Diagnostics hub"
        scope="Basic Manage"
        description="Choose a predefined check, review its scope and budget, then inspect the resulting evidence."
        actions={
          <Button
            variant="outline"
            disabled={!request}
            onClick={() => go('diagnostic-run')}
          >
            <Activity />
            Open latest job
          </Button>
        }
      />
      {origin && (
        <Notice
          title="Object scope carried into diagnostics"
          actions={
            <Button variant="outline" onClick={clearOrigin} disabled={busy}>
              Clear source context
            </Button>
          }
        >
          <span className="break-words">
            {origin} · {scope}
          </span>
        </Notice>
      )}
      {busy && (
        <Notice
          title="A diagnostic Job is active"
          actions={
            <Button variant="outline" onClick={() => go('diagnostic-run')}>
              Open active job
            </Button>
          }
        >
          Its submitted target and parameters are locked. Catalog filters remain
          available for review.
        </Notice>
      )}
      {serviceBlocked && (
        <Notice tone="uncertain" title="Diagnostic service unavailable">
          {serviceBlocked}
        </Notice>
      )}
      {['degraded', 'provider-degraded'].includes(scenario) && (
        <Notice tone="warning" title="Partial provider coverage">
          Known templates remain available. New results will identify missing
          provider evidence.
        </Notice>
      )}
      {!desktop && (
        <Notice title="Recommended checks on this device">
          Tablet supports recommended bounded diagnostics. Full catalog review
          remains available on desktop.
        </Notice>
      )}
      <div
        className="ovs-inventory-summary"
        aria-label="Diagnostic catalog summary"
      >
        <span>
          <strong>{catalog.length}</strong> templates
        </span>
        <span>
          <strong>
            {catalog.filter((item) => item.availability === 'Available').length}
          </strong>{' '}
          available
        </span>
        <span>Read-only operations</span>
        <span>Synthetic provider catalog</span>
      </div>
      <div className="mt-5 grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_400px]">
        <div className="min-w-0 space-y-5">
          <Panel
            title="Diagnostic catalog"
            description="Unavailable templates retain their reason and operation limits."
          >
            <div className="flex flex-wrap gap-3 border-b p-5">
              <div className="relative min-w-0 flex-1 basis-56">
                <Search
                  className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
                  aria-hidden="true"
                />
                <Input
                  aria-label="Search diagnostic catalog"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search name, category, or purpose…"
                  className="h-10 pl-9"
                />
              </div>
              <NativeSelect
                aria-label="Filter diagnostic category"
                value={category}
                onChange={(event) => setCategory(event.target.value)}
              >
                {categories.map((value) => (
                  <NativeSelectOption key={value} value={value}>
                    {value}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3 text-sm">
              <output>
                {shown.length} of {catalog.length} templates
              </output>
              {selectedOutOfView && (
                <span className="text-muted-foreground">
                  Selected operation remains in the input panel
                </span>
              )}
            </div>
            {!shown.length ? (
              <div className="p-6">
                <h3 className="font-semibold">No matching diagnostics</h3>
                <p className="mt-2 text-sm text-muted-foreground">
                  No templates match these filters for this device.
                </p>
                <Button
                  className="mt-4"
                  variant="outline"
                  onClick={clearFilters}
                >
                  Clear filters
                </Button>
              </div>
            ) : (
              <>
                <div className="hidden lg:block">
                  <Table className="ovs-data-table">
                    <caption className="sr-only">
                      Diagnostic templates, availability and resource budgets.
                    </caption>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Diagnostic</TableHead>
                        <TableHead>Availability</TableHead>
                        <TableHead>Budget</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {shown.map((item) => (
                        <TableRow
                          key={item.id}
                          data-selected={item.id === selected.id}
                          className="ovs-diagnostic-row"
                        >
                          <TableCell className="whitespace-normal">
                            <Button
                              variant="link"
                              className="h-auto max-w-full justify-start whitespace-normal p-0 text-left text-sm font-semibold"
                              aria-pressed={item.id === selected.id}
                              disabled={busy}
                              onClick={() => setSelectedDiagnostic(item.id)}
                            >
                              {item.name}
                            </Button>
                            <p className="mt-2 text-sm text-muted-foreground">
                              {item.description}
                            </p>
                            <div className="mt-3 flex flex-wrap gap-2">
                              <StatusBadge tone="neutral">
                                {item.category}
                              </StatusBadge>
                              {item.recommended && (
                                <StatusBadge tone="info">
                                  Recommended
                                </StatusBadge>
                              )}
                              {item.id === selected.id && (
                                <span className="text-xs font-semibold text-primary">
                                  Selected
                                </span>
                              )}
                            </div>
                          </TableCell>
                          <TableCell>
                            <Availability definition={item} />
                          </TableCell>
                          <TableCell className="whitespace-normal">
                            <p className="text-sm">{item.timeout}s timeout</p>
                            <p className="mt-2 text-xs text-muted-foreground">
                              {item.impact}
                            </p>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
                <div className="divide-y lg:hidden">
                  {shown.map((item) => (
                    <article
                      key={item.id}
                      data-selected={item.id === selected.id}
                      className="ovs-diagnostic-row p-5"
                    >
                      <Button
                        variant="link"
                        className="h-auto max-w-full justify-start whitespace-normal p-0 text-left text-sm font-semibold"
                        aria-pressed={item.id === selected.id}
                        disabled={busy}
                        onClick={() => setSelectedDiagnostic(item.id)}
                      >
                        {item.name}
                      </Button>
                      <p className="mt-2 text-sm text-muted-foreground">
                        {item.description}
                      </p>
                      <div className="mt-3 flex flex-wrap gap-2">
                        <Availability definition={item} />
                        <StatusBadge tone="neutral">
                          {item.timeout}s timeout
                        </StatusBadge>
                        {item.id === selected.id && (
                          <StatusBadge tone="info">Selected</StatusBadge>
                        )}
                      </div>
                    </article>
                  ))}
                </div>
              </>
            )}
          </Panel>
          <Notice title="Configuration stays in Changes">
            Diagnostics collect evidence. Prepare any resulting Bridge, Bond or
            VLAN configuration through the shared Candidate workspace.
          </Notice>
          <details className="ovs-surface p-5">
            <summary className="cursor-pointer text-sm font-semibold">
              Prototype input review cases
            </summary>
            <p className="mt-3 text-sm text-muted-foreground">
              These controls simulate validation responses. They do not grant
              permission or change the submitted Job.
            </p>
            <NativeSelect
              className="mt-3"
              aria-label="Select input review state"
              value={inputState}
              disabled={busy || !!serviceBlocked}
              onChange={(event) =>
                setInputState(event.target.value as DiagnosticInputState)
              }
            >
              <NativeSelectOption value="valid">Valid input</NativeSelectOption>
              <NativeSelectOption value="validation-error">
                Validation error
              </NativeSelectOption>
              <NativeSelectOption value="scope-too-broad">
                Scope too broad
              </NativeSelectOption>
            </NativeSelect>
          </details>
        </div>
        <div className="min-w-0 space-y-5">
          <form
            className="ovs-surface"
            onSubmit={(event) => {
              event.preventDefault();
              if (!blocked) runDiagnostic();
            }}
          >
            <div className="border-b p-5">
              <p className="ovs-eyebrow">Selected operation</p>
              <h2 className="mt-2 text-lg font-semibold">{selected.name}</h2>
              <div className="mt-3">
                <Availability definition={selected} />
              </div>
            </div>
            <fieldset disabled={locked} className="min-w-0 space-y-5 p-5">
              <legend className="sr-only">Bounded diagnostic input</legend>
              <Field
                id="diagnostic-target"
                label="Target object"
                error={scopeError}
              >
                <NativeSelect
                  id="diagnostic-target"
                  className="w-full"
                  value={scope}
                  aria-invalid={!!scopeError}
                  aria-describedby={
                    scopeError ? 'diagnostic-target-error' : undefined
                  }
                  onChange={(event) => setScope(event.target.value)}
                >
                  {[
                    ...new Set([
                      scope,
                      'Port/bond-storage',
                      'Port/bond-uplink',
                      'Bridge/br-fabric',
                      'Bridge/br-storage',
                      'Bridge/br-mgmt',
                    ]),
                  ].map((target) => (
                    <NativeSelectOption key={target} value={target}>
                      {target}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-1">
                <Field
                  id="diagnostic-budget"
                  label="Sampling budget"
                  error={parameterErrors.sample}
                >
                  <NativeSelect
                    id="diagnostic-budget"
                    className="w-full"
                    value={String(parameters.sampleSeconds)}
                    aria-invalid={!!parameterErrors.sample}
                    aria-describedby={
                      parameterErrors.sample
                        ? 'diagnostic-budget-error'
                        : undefined
                    }
                    onChange={(event) =>
                      setParameters({
                        ...parameters,
                        sampleSeconds: Number(
                          event.target.value,
                        ) as DiagnosticParameters['sampleSeconds'],
                      })
                    }
                  >
                    {[5, 10, 15].map((seconds) => (
                      <NativeSelectOption
                        key={seconds}
                        value={String(seconds)}
                        disabled={
                          !selected.sampleBudgets.includes(
                            seconds as 5 | 10 | 15,
                          )
                        }
                      >
                        {seconds} seconds
                        {!selected.sampleBudgets.includes(
                          seconds as 5 | 10 | 15,
                        )
                          ? ' · unavailable'
                          : ''}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </Field>
                <Field
                  id="diagnostic-detail"
                  label="Output detail"
                  error={parameterErrors.detail}
                >
                  <NativeSelect
                    id="diagnostic-detail"
                    className="w-full"
                    value={parameters.detail}
                    aria-invalid={!!parameterErrors.detail}
                    aria-describedby={
                      parameterErrors.detail
                        ? 'diagnostic-detail-error'
                        : undefined
                    }
                    onChange={(event) =>
                      setParameters({
                        ...parameters,
                        detail: event.target
                          .value as DiagnosticParameters['detail'],
                      })
                    }
                  >
                    <NativeSelectOption value="structured">
                      Structured summary
                    </NativeSelectOption>
                    <NativeSelectOption
                      value="bounded"
                      disabled={!selected.textLineLimit}
                    >
                      Summary + bounded text
                      {!selected.textLineLimit ? ' · unavailable' : ''}
                    </NativeSelectOption>
                  </NativeSelect>
                </Field>
              </div>
            </fieldset>
            <div className="border-t p-5">
              <p className="text-sm text-muted-foreground">
                <OutputContract definition={selected} />
              </p>
              <Button
                type="submit"
                className="mt-4 h-auto min-h-9 w-full whitespace-normal py-2"
                disabled={!!blocked}
              >
                <Play />
                Run bounded diagnostic
              </Button>
              {blocked && (
                <output className="mt-3 block text-sm text-muted-foreground">
                  {blocked}
                </output>
              )}
            </div>
          </form>
          <Panel title="Execution contract">
            <Facts
              rows={[
                ['Required permission', selected.permission],
                ['Resource impact', selected.impact],
                ['Timeout', `${selected.timeout} seconds`],
                [
                  'Safe cancellation',
                  selected.safeCancel
                    ? 'At a provider checkpoint'
                    : 'Unavailable · atomic operation',
                ],
              ]}
            />
          </Panel>
          {mode === 'expert' && (
            <Panel title="Expert operation details">
              <div className="space-y-3 p-5 text-sm">
                <p className="break-words font-mono">{selected.id}</p>
                <p className="text-muted-foreground">
                  Typed object reference, sampling budget and output enum. The
                  template has no free-form command or file input.
                </p>
              </div>
            </Panel>
          )}
          {!request && (
            <details className="ovs-surface p-5">
              <summary className="cursor-pointer text-sm font-semibold">
                Preview a synthetic result
              </summary>
              <p className="mt-3 text-sm text-muted-foreground">
                Uses the selected valid input to demonstrate result states,
                without submitting a diagnostic or creating evidence.
              </p>
              <Button
                className="mt-4"
                variant="outline"
                disabled={!!blocked}
                onClick={() => props.setJobState('complete')}
              >
                Preview result states
                <ChevronRight />
              </Button>
            </details>
          )}
          {request && (
            <p className="text-sm text-muted-foreground">
              {reviewOnly
                ? 'The latest result is a review preview.'
                : 'Open latest job retains the submitted input, even after the next draft changes.'}
            </p>
          )}
        </div>
      </div>
    </>
  );
}

function DiagnosticRun(props: Props & { width: number }) {
  const {
    request,
    mode,
    jobState,
    scenario,
    reviewOnly,
    cancelDiagnostic,
    retryDiagnostic,
    openEvidence,
    openObject,
    setJobState,
    go,
    width,
    notify,
  } = props;
  const [exported, setExported] = useState<{
    request: DiagnosticRequest;
    state: DiagnosticJobState;
    reviewOnly: boolean;
    label: string;
  } | null>(null);
  if (!request)
    return (
      <>
        <PageHeader
          id="P1-08"
          eyebrow="Operations / Diagnostics / Job"
          title="No diagnostic Job yet"
          description="Choose an operation and review its bounded input before starting a Job."
        />
        <Notice
          tone="neutral"
          title="No request has been submitted"
          actions={
            <Button variant="outline" onClick={() => go('diagnostics-hub')}>
              Open diagnostic catalog
            </Button>
          }
        >
          Job state, output and evidence appear after a request is captured.
        </Notice>
      </>
    );
  const definition = diagnosticDefinition(request.id);
  if (!definition)
    return (
      <Notice tone="uncertain" title="Diagnostic template unavailable">
        This request has no template in the current catalog.
      </Notice>
    );
  const active = diagnosticActive(jobState);
  const result = diagnosticResult(request, jobState);
  const serviceBlocked = diagnosticServiceBlock(scenario);
  const cancelBlock = reviewOnly
    ? 'This result preview has no submitted Job to cancel.'
    : diagnosticCancelBlock(request.id, jobState, scenario, width);
  const retryBlock = diagnosticRunBlock(
    request.id,
    request.scope,
    'valid',
    active,
    width,
    request,
    scenario,
  );
  const lifecycle = [
    ['Request', 'Input captured'],
    ['Execution', active ? diagnosticJobLabels[jobState] : 'Ended'],
    ['Result', active ? 'Pending' : diagnosticJobLabels[jobState]],
    [
      'Evidence',
      reviewOnly
        ? 'Preview only'
        : active
          ? 'Audit retained · Event pending'
          : result.evidenceAvailable
            ? 'Correlation retained'
            : 'Event unavailable',
    ],
  ];
  const exportResult = () => {
    const payload = diagnosticExport(request, jobState, reviewOnly);
    if (!payload) return;
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(payload, null, 2)], {
        type: 'application/json',
      }),
    );
    const link = document.createElement('a');
    link.href = url;
    link.download = `${diagnosticJobId}.json`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    setExported({
      request,
      state: jobState,
      reviewOnly,
      label: `${diagnosticJobId}.json · ${request.detail} · ${request.scope}`,
    });
    notify(`Diagnostic export prepared · ${request.scope}`);
  };
  return (
    <>
      <PageHeader
        id="P1-08"
        eyebrow="Operations / Diagnostics / Job"
        title={`Diagnostic Job · ${diagnosticJobId}`}
        scope="Basic Manage"
        description="Review the captured input, execution state, result coverage and associated evidence."
        actions={
          <Button variant="outline" onClick={() => go('diagnostics-hub')}>
            <ArrowLeft />
            Diagnostics
          </Button>
        }
      />
      {reviewOnly && (
        <Notice title="Synthetic result preview">
          The selected state is for review only. It does not submit an operation
          or create Event / Audit evidence.
        </Notice>
      )}
      {serviceBlocked && (
        <Notice tone="uncertain" title="Showing the saved session snapshot">
          {serviceBlocked} The displayed Job is not a fresh provider
          observation.
        </Notice>
      )}
      <div className="mt-5 grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="min-w-0 space-y-5">
          <Panel
            title={definition.name}
            description={`${request.scope} · ${diagnosticCorrelation}`}
            actions={
              <div className="flex flex-wrap gap-2">
                <JobBadge state={jobState} />
                {result.outputAvailable && (
                  <Button variant="outline" onClick={exportResult}>
                    <Download />
                    Export result
                  </Button>
                )}
              </div>
            }
          >
            <div className="p-5">
              <ol
                className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-4"
                aria-label="Diagnostic request, execution, result and evidence"
              >
                {lifecycle.map(([label, value]) => (
                  <li
                    key={label}
                    className="min-w-0 rounded-md border bg-muted/25 p-4"
                  >
                    <p className="text-xs text-muted-foreground">{label}</p>
                    <p className="mt-2 break-words text-sm font-medium">
                      {value}
                    </p>
                  </li>
                ))}
              </ol>
              {active && (
                <div className="mt-5">
                  <p className="mb-3 text-sm text-muted-foreground">
                    {jobState === 'queued'
                      ? 'Waiting for a bounded worker'
                      : jobState === 'cancel-requested'
                        ? 'Cancellation requested · waiting for a safe checkpoint'
                        : `Collecting within the ${request.sampleSeconds}s sampling budget`}
                  </p>
                  <Progress
                    aria-label="Diagnostic collection in progress"
                    value={null}
                    className="ovs-diagnostic-progress"
                  />
                  {definition.safeCancel && jobState !== 'cancel-requested' && (
                    <Button
                      className="mt-4"
                      variant="outline"
                      disabled={!!cancelBlock}
                      onClick={cancelDiagnostic}
                    >
                      <Square />
                      Cancel safely
                    </Button>
                  )}
                  {cancelBlock &&
                    definition.safeCancel &&
                    jobState !== 'cancel-requested' && (
                      <p className="mt-2 text-sm text-muted-foreground">
                        {cancelBlock}
                      </p>
                    )}
                </div>
              )}
            </div>
          </Panel>
          {active ? (
            <Notice title="Result is not authoritative yet">
              A queued, running or cancellation-requested Job has no published
              health verdict. The captured input stays unchanged across pages.
            </Notice>
          ) : (
            <>
              <Notice tone={result.tone} title={result.title}>
                {result.body}
              </Notice>
              <Panel
                title="Result coverage"
                description="Job completion and object health are separate conclusions."
              >
                <dl className="space-y-5 p-5">
                  {[
                    ['Finding', result.finding],
                    ['Coverage', result.coverage],
                    ['Next safe step', result.next],
                  ].map(([label, value]) => (
                    <div key={label}>
                      <dt className="text-xs text-muted-foreground">{label}</dt>
                      <dd className="mt-2 break-words text-sm font-medium">
                        {value}
                      </dd>
                    </div>
                  ))}
                </dl>
              </Panel>
            </>
          )}
          {result.raw && (
            <Panel
              title="Bounded text excerpt"
              description={`${result.textExcerptLines} excerpt lines · ${result.limits.textLines}-line provider limit · 64 KiB ceiling`}
              actions={
                result.truncated ? (
                  <StatusBadge tone="warning">Truncated upstream</StatusBadge>
                ) : (
                  <StatusBadge tone="neutral">Synthetic excerpt</StatusBadge>
                )
              }
            >
              {mode === 'expert' ? (
                <pre className="overflow-x-auto whitespace-pre-wrap break-words p-5 font-mono text-xs leading-6">
                  {result.raw}
                  {result.truncated
                    ? '\n… omitted provider text is not included in this excerpt'
                    : ''}
                </pre>
              ) : (
                <div className="flex items-start gap-3 p-5">
                  <Terminal
                    className="mt-1 size-5 shrink-0 text-primary"
                    aria-hidden="true"
                  />
                  <p className="text-sm text-muted-foreground">
                    Bounded text was requested. Switch to Expert to inspect the
                    excerpt; permissions and output limits remain the same.
                  </p>
                </div>
              )}
            </Panel>
          )}
          {exported?.request === request &&
            exported.state === jobState &&
            exported.reviewOnly === reviewOnly && (
              <output className="block break-words text-sm text-muted-foreground">
                Export prepared · {exported.label}
              </output>
            )}
          <details className="ovs-surface p-5">
            <summary className="cursor-pointer text-sm font-semibold">
              Prototype result review cases
            </summary>
            <p className="mt-3 text-sm text-muted-foreground">
              Preview an alternate state for this captured input. This stops the
              automatic demonstration and does not rewrite shared evidence.
            </p>
            <NativeSelect
              className="mt-3"
              aria-label="Select diagnostic job review case"
              value={jobState}
              disabled={!!serviceBlocked}
              onChange={(event) =>
                setJobState(event.target.value as DiagnosticJobState)
              }
            >
              {Object.entries(diagnosticJobLabels)
                .filter(([state]) => state !== 'not-started')
                .map(([state, label]) => (
                  <NativeSelectOption key={state} value={state}>
                    {label}
                  </NativeSelectOption>
                ))}
            </NativeSelect>
          </details>
        </div>
        <aside className="min-w-0 space-y-5">
          <Panel title="Captured request">
            <Facts
              rows={[
                ['Actor', 'operator.review'],
                ['Target', request.scope],
                ['Sampling budget', `${request.sampleSeconds} seconds`],
                [
                  'Requested output',
                  request.detail === 'bounded'
                    ? 'Summary + bounded text'
                    : 'Structured summary',
                ],
                ['Permission', definition.permission],
                ['Timeout', `${definition.timeout} seconds`],
                [
                  'Output ceiling',
                  <OutputContract key="limits" definition={definition} />,
                ],
              ]}
            />
          </Panel>
          <Notice tone="neutral" title="Cancellation contract">
            {definition.safeCancel
              ? 'A cancellation request takes effect only at an advertised provider checkpoint.'
              : 'This metadata operation is atomic. Safe cancellation is unavailable.'}
          </Notice>
          <Panel
            title="Correlated evidence"
            description="Result, Event and Audit remain distinct records."
          >
            <div className="space-y-3 p-5">
              <p className="break-words font-mono text-xs text-muted-foreground">
                {diagnosticCorrelation}
              </p>
              <Button
                className="w-full"
                variant="outline"
                disabled={reviewOnly || !result.evidenceAvailable}
                onClick={() => openEvidence('event')}
              >
                <FileClock />
                Open linked Event
              </Button>
              <Button
                className="w-full"
                variant="outline"
                disabled={reviewOnly}
                onClick={() => openEvidence('audit')}
              >
                Open Audit record
              </Button>
              {(reviewOnly || !result.evidenceAvailable) && (
                <p className="text-sm text-muted-foreground">
                  {reviewOnly
                    ? 'A preview has no new evidence records.'
                    : active
                      ? 'The completion Event is pending. The request Audit is available.'
                      : 'The Event link is unavailable. The result and Audit remain inspectable.'}
                </p>
              )}
            </div>
          </Panel>
          {diagnosticRetryable(jobState) && (
            <div>
              <Button
                className="h-auto min-h-9 w-full whitespace-normal py-2"
                disabled={!!retryBlock}
                onClick={retryDiagnostic}
              >
                <RefreshCw />
                Retry this bounded case
              </Button>
              <p className="mt-2 text-sm text-muted-foreground">
                {retryBlock ??
                  'Uses this captured target and parameters, independent of the next input draft.'}
              </p>
            </div>
          )}
          {!active && (
            <Button className="w-full" variant="outline" onClick={openObject}>
              <Network />
              Open related object
            </Button>
          )}
          {mode === 'expert' && (
            <Panel title="Expert operation details">
              <div className="space-y-3 p-5 text-sm">
                <p className="break-words font-mono">{request.id}</p>
                <p className="text-muted-foreground">
                  Fixed Job and correlation identifiers denote this
                  session&apos;s review fixture. Reloading resets the synthetic
                  session.
                </p>
              </div>
            </Panel>
          )}
        </aside>
      </div>
    </>
  );
}

export function P1DiagnosticsView(props: Props) {
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const update = () => setWidth(window.innerWidth);
    update();
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, []);
  if (
    props.scenario === 'permission-denied' ||
    (props.view === 'diagnostics-hub' &&
      ['loading', 'empty', 'error', 'provider-unavailable'].includes(
        props.scenario,
      ))
  )
    return (
      <ResourceState
        scenario={props.scenario}
        hasJob={!!props.request}
        go={props.go}
      />
    );
  return (
    <div className="ovs-diagnostics-pages">
      {props.view === 'diagnostic-run' ? (
        <DiagnosticRun {...props} width={width} />
      ) : (
        <DiagnosticsHub {...props} width={width} />
      )}
    </div>
  );
}
