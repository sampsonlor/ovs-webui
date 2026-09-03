'use client';

import { useMemo, useState, type ReactNode } from 'react';
import {
  Activity,
  ArrowLeft,
  CheckCircle2,
  ChevronRight,
  Download,
  FileClock,
  Network,
  Play,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
  Square,
  Terminal,
  Timer,
  TriangleAlert,
  XCircle,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
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
import type {
  DiagnosticInputState,
  DiagnosticJobState,
  P1DiagnosticsPage,
  P1View,
  PrototypeMode,
} from './prototype-model';
import { diagnosticJobLabels } from './prototype-model';

type DiagnosticAvailability =
  | 'Available'
  | 'Permission denied'
  | 'Provider unavailable';

type DiagnosticDefinition = {
  id: string;
  name: string;
  category: 'Network' | 'OVS' | 'OpenFlow' | 'Host' | 'Acceleration';
  description: string;
  permission: string;
  duration: string;
  impact: string;
  output: string;
  availability: DiagnosticAvailability;
  recommended: boolean;
  safeCancel: boolean;
};

const diagnostics: DiagnosticDefinition[] = [
  {
    id: 'diag.net.link-lacp',
    name: 'Interface link & LACP snapshot',
    category: 'Network',
    description:
      'Correlate carrier, speed, duplex, and bounded LACP actor/partner state for one Port or member Interface.',
    permission: 'diagnostic.run.network',
    duration: '< 15 sec',
    impact: 'Read-only · low sampling cost',
    output: 'Structured summary + up to 200 text lines',
    availability: 'Available',
    recommended: true,
    safeCancel: true,
  },
  {
    id: 'diag.ovs.datapath-trace',
    name: 'Bounded datapath packet trace',
    category: 'OVS',
    description:
      'Evaluate one structured packet description against one Bridge without exposing a shell or arbitrary command text.',
    permission: 'diagnostic.run.ovs',
    duration: '< 20 sec',
    impact: 'Read-only · one synthetic packet',
    output: 'Parsed trace tree + up to 300 text lines',
    availability: 'Available',
    recommended: true,
    safeCancel: true,
  },
  {
    id: 'diag.openflow.collection',
    name: 'OpenFlow collection health',
    category: 'OpenFlow',
    description:
      'Check collection freshness, controller authority, table coverage, and current truncation limits.',
    permission: 'diagnostic.run.openflow',
    duration: '< 10 sec',
    impact: 'Read-only metadata query',
    output: 'Structured collection report',
    availability: 'Available',
    recommended: true,
    safeCancel: false,
  },
  {
    id: 'diag.host.interface-counters',
    name: 'Host interface counter sample',
    category: 'Host',
    description:
      'Capture a short, bounded host counter sample for explicitly selected Interfaces.',
    permission: 'diagnostic.run.host',
    duration: '15 sec',
    impact: 'Read-only · capped at 4 Interfaces',
    output: 'Counter deltas + provider metadata',
    availability: 'Permission denied',
    recommended: false,
    safeCancel: true,
  },
  {
    id: 'diag.acceleration.provider',
    name: 'Acceleration provider snapshot',
    category: 'Acceleration',
    description:
      'Collect declared DPDK and hardware-offload availability, prerequisites, and provider freshness.',
    permission: 'diagnostic.run.acceleration',
    duration: '< 20 sec',
    impact: 'Read-only provider query',
    output: 'Capability evidence bundle',
    availability: 'Provider unavailable',
    recommended: false,
    safeCancel: false,
  },
];

const reviewCases: DiagnosticJobState[] = [
  'queued',
  'running',
  'cancel-requested',
  'cancelled',
  'complete',
  'partial',
  'truncated',
  'failed',
  'expired',
  'unavailable',
  'no-finding',
  'no-data',
  'provider-unavailable',
  'command-failed',
  'evidence-unavailable',
];

function ScopeBadge() {
  return (
    <Badge
      variant="outline"
      className="rounded-sm border-amber-300 bg-amber-50 text-amber-800"
    >
      Basic Manage
    </Badge>
  );
}

function PageHeader({
  id,
  title,
  description,
  actions,
}: {
  id: string;
  title: string;
  description: string;
  actions?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div>
        <div className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
          <span>{id}</span>
          <span>·</span>
          <span>Operations / Diagnostics</span>
          <ScopeBadge />
        </div>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
          {description}
        </p>
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

function AvailabilityBadge({
  availability,
}: {
  availability: DiagnosticAvailability;
}) {
  const tone =
    availability === 'Available'
      ? 'border-emerald-300 bg-emerald-50 text-emerald-800'
      : availability === 'Permission denied'
        ? 'border-rose-300 bg-rose-50 text-rose-800'
        : 'border-amber-300 bg-amber-50 text-amber-800';
  return (
    <Badge variant="outline" className={`rounded-sm ${tone}`}>
      {availability}
    </Badge>
  );
}

function JobBadge({ state }: { state: DiagnosticJobState }) {
  const good = ['complete', 'no-finding'].includes(state);
  const active = ['queued', 'running', 'cancel-requested'].includes(state);
  const warning = [
    'partial',
    'truncated',
    'no-data',
    'provider-unavailable',
    'evidence-unavailable',
  ].includes(state);
  const neutral = ['not-started', 'cancelled', 'expired'].includes(state);
  const tone = good
    ? 'border-emerald-300 bg-emerald-50 text-emerald-800'
    : active
      ? 'border-sky-300 bg-sky-50 text-sky-800'
      : warning
        ? 'border-amber-300 bg-amber-50 text-amber-800'
        : neutral
          ? 'border-slate-300 bg-slate-50 text-slate-700'
          : 'border-rose-300 bg-rose-50 text-rose-800';
  return (
    <Badge variant="outline" className={`rounded-sm ${tone}`}>
      {diagnosticJobLabels[state]}
    </Badge>
  );
}

function DiagnosticsHub({
  mode,
  selectedDiagnostic,
  onSelectDiagnostic,
  inputState,
  onInputStateChange,
  scope,
  origin,
  onClearOrigin,
  onRun,
  onOpenJob,
  notify,
}: {
  mode: PrototypeMode;
  selectedDiagnostic: string;
  onSelectDiagnostic: (id: string) => void;
  inputState: DiagnosticInputState;
  onInputStateChange: (state: DiagnosticInputState) => void;
  scope: string;
  origin: string | null;
  onClearOrigin: () => void;
  onRun: () => void;
  onOpenJob: (state: DiagnosticJobState) => void;
  notify: (message: string) => void;
}) {
  const [search, setSearch] = useState('');
  const [category, setCategory] = useState('All');
  const selected =
    diagnostics.find((item) => item.id === selectedDiagnostic) ??
    diagnostics[0];
  const shown = useMemo(
    () =>
      diagnostics.filter((item) => {
        const matchesCategory =
          category === 'All' || item.category === category;
        const haystack =
          `${item.name} ${item.category} ${item.description}`.toLowerCase();
        return matchesCategory && haystack.includes(search.toLowerCase());
      }),
    [category, search],
  );
  const inputValid = inputState === 'valid';
  const canRun = selected.availability === 'Available' && inputValid;
  const validationMessage =
    inputState === 'validation-error'
      ? 'The selected object is incompatible with this diagnostic template.'
      : inputState === 'scope-too-broad'
        ? 'Scope exceeds the hard limit of 4 Interfaces. Narrow the object selection.'
        : null;
  return (
    <>
      <PageHeader
        id="P1-07"
        title="Diagnostics hub"
        description="Run predefined, structured, resource-bounded diagnostics. No arbitrary shell, file access, or unbounded log stream is exposed."
        actions={
          <Button variant="outline" onClick={() => onOpenJob('complete')}>
            <Activity /> Open latest job
          </Button>
        }
      />

      {origin && (
        <div className="mt-4 flex flex-wrap items-center gap-3 border border-[#9bb7cf] bg-[#f2f8fc] px-4 py-3">
          <Network className="size-5 text-[#35627f]" />
          <div className="min-w-[240px] flex-1">
            <p className="text-sm font-semibold">
              Scope preselected from {origin}
            </p>
            <p className="mt-0.5 font-mono text-xs text-muted-foreground">
              {scope} · the diagnostic will read Network and OVS operational
              facts only
            </p>
          </div>
          <Button size="sm" variant="outline" onClick={onClearOrigin}>
            Clear deep-link scope
          </Button>
        </div>
      )}

      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
        <div className="space-y-4">
          <section className="border bg-card">
            <div className="flex flex-wrap items-center gap-3 border-b p-3">
              <div className="relative min-w-[240px] flex-1">
                <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  aria-label="Search diagnostic catalog"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search diagnostic catalog"
                  className="pl-8"
                />
              </div>
              <NativeSelect
                aria-label="Filter diagnostic category"
                value={category}
                onChange={(event) => setCategory(event.target.value)}
              >
                {[
                  'All',
                  'Network',
                  'OVS',
                  'OpenFlow',
                  'Host',
                  'Acceleration',
                ].map((value) => (
                  <NativeSelectOption key={value} value={value}>
                    {value}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
            <div className="border-b bg-muted/35 px-4 py-2 text-xs text-muted-foreground lg:hidden">
              Tablet responsibility: recommended diagnostics only. Full catalog
              review remains on desktop.
            </div>
            <Table>
              <TableHeader className="bg-muted/45">
                <TableRow>
                  <TableHead>Diagnostic</TableHead>
                  <TableHead>Category</TableHead>
                  <TableHead>Availability</TableHead>
                  <TableHead>Budget</TableHead>
                  <TableHead className="text-right">Select</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((item) => (
                  <TableRow
                    key={item.id}
                    className={`${item.recommended ? '' : 'hidden lg:table-row'} ${selected.id === item.id ? 'bg-[#f2f8fc]' : ''}`}
                  >
                    <TableCell>
                      <p className="font-semibold">{item.name}</p>
                      <p className="mt-1 max-w-xl text-xs text-muted-foreground">
                        {item.description}
                      </p>
                      {item.recommended && (
                        <Badge
                          variant="outline"
                          className="mt-2 rounded-sm border-sky-200 bg-sky-50 text-sky-700"
                        >
                          recommended
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell>{item.category}</TableCell>
                    <TableCell>
                      <AvailabilityBadge availability={item.availability} />
                    </TableCell>
                    <TableCell>
                      <p className="font-mono text-xs">{item.duration}</p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {item.impact}
                      </p>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant={
                          selected.id === item.id ? 'default' : 'outline'
                        }
                        onClick={() => {
                          onSelectDiagnostic(item.id);
                          notify(`Selected diagnostic · ${item.name}`);
                        }}
                      >
                        {selected.id === item.id ? 'Selected' : 'Select'}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {shown.length === 0 && (
              <div className="p-8 text-center">
                <p className="text-sm font-semibold">No matching diagnostics</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Clear the catalog filter to restore available templates.
                </p>
              </div>
            )}
          </section>

          <section className="border bg-card">
            <div className="flex flex-wrap items-start justify-between gap-3 border-b px-4 py-3">
              <div>
                <h2 className="text-sm font-semibold">Bounded input</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  Only typed fields, enumerations, object selectors, and
                  explicit limits are accepted.
                </p>
              </div>
              <NativeSelect
                aria-label="Select input review state"
                value={inputState}
                onChange={(event) =>
                  onInputStateChange(event.target.value as DiagnosticInputState)
                }
                size="sm"
              >
                <NativeSelectOption value="valid">
                  Valid input
                </NativeSelectOption>
                <NativeSelectOption value="validation-error">
                  Validation error
                </NativeSelectOption>
                <NativeSelectOption value="scope-too-broad">
                  Scope too broad
                </NativeSelectOption>
              </NativeSelect>
            </div>
            <div className="grid gap-4 p-4 md:grid-cols-3">
              <div className="grid gap-2 text-xs font-medium">
                <span>Target object</span>
                <NativeSelect aria-label="Target object" defaultValue={scope}>
                  <NativeSelectOption value={scope}>{scope}</NativeSelectOption>
                  <NativeSelectOption value="Bridge/br-fabric">
                    Bridge/br-fabric
                  </NativeSelectOption>
                  <NativeSelectOption value="Port/bond-uplink">
                    Port/bond-uplink
                  </NativeSelectOption>
                </NativeSelect>
              </div>
              <div className="grid gap-2 text-xs font-medium">
                <span>Sampling budget</span>
                <NativeSelect aria-label="Sampling budget" defaultValue="10">
                  <NativeSelectOption value="5">5 seconds</NativeSelectOption>
                  <NativeSelectOption value="10">
                    10 seconds · recommended
                  </NativeSelectOption>
                  <NativeSelectOption value="15">
                    15 seconds · maximum
                  </NativeSelectOption>
                </NativeSelect>
              </div>
              <div className="grid gap-2 text-xs font-medium">
                <span>Output detail</span>
                <NativeSelect
                  aria-label="Output detail"
                  defaultValue="structured"
                >
                  <NativeSelectOption value="structured">
                    Structured summary
                  </NativeSelectOption>
                  <NativeSelectOption value="bounded">
                    Summary + bounded text
                  </NativeSelectOption>
                </NativeSelect>
              </div>
            </div>
            {mode === 'expert' && (
              <div className="mx-4 mb-4 grid gap-3 border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4 md:grid-cols-3">
                <div>
                  <p className="font-mono text-[10px] text-muted-foreground">
                    OPERATION TEMPLATE
                  </p>
                  <p className="mt-1 font-mono text-xs font-semibold">
                    {selected.id}
                  </p>
                </div>
                <div>
                  <p className="font-mono text-[10px] text-muted-foreground">
                    HARD LIMIT
                  </p>
                  <p className="mt-1 font-mono text-xs font-semibold">
                    timeout 20s · output 64 KiB
                  </p>
                </div>
                <div>
                  <p className="font-mono text-[10px] text-muted-foreground">
                    ACCEPTED INPUT
                  </p>
                  <p className="mt-1 font-mono text-xs font-semibold">
                    object_ref · duration enum · detail enum
                  </p>
                </div>
              </div>
            )}
            {validationMessage && (
              <div
                role="alert"
                className="mx-4 mb-4 flex gap-3 border border-rose-300 bg-rose-50 p-3 text-rose-900"
              >
                <ShieldAlert className="mt-0.5 size-4 shrink-0" />
                <div>
                  <p className="text-sm font-semibold">Input is blocked</p>
                  <p className="mt-1 text-xs">{validationMessage}</p>
                </div>
              </div>
            )}
          </section>
        </div>

        <aside className="space-y-4">
          <section className="border border-[#9bb7cf] bg-[#f2f8fc] p-4">
            <p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-[#35627f]">
              Selected operation
            </p>
            <h2 className="mt-2 text-base font-semibold">{selected.name}</h2>
            <p className="mt-2 text-xs text-muted-foreground">
              {selected.description}
            </p>
            <dl className="mt-4 space-y-3 text-xs">
              {[
                ['Required permission', selected.permission],
                ['Estimated duration', selected.duration],
                ['Resource impact', selected.impact],
                ['Output contract', selected.output],
                [
                  'Safe cancellation',
                  selected.safeCancel
                    ? 'Supported'
                    : 'Not supported · operation is atomic',
                ],
              ].map(([label, value]) => (
                <div
                  key={label}
                  className="border-t border-[#c8dbe9] pt-2 first:border-t-0 first:pt-0"
                >
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="mt-1 font-mono">{value}</dd>
                </div>
              ))}
            </dl>
            <Button
              disabled={!canRun}
              onClick={onRun}
              className="mt-5 w-full bg-[#157a9e] hover:bg-[#11627f]"
            >
              <Play /> Run bounded diagnostic
            </Button>
            {!canRun && (
              <p className="mt-2 text-xs text-rose-700">
                {selected.availability !== 'Available'
                  ? `${selected.availability}: ${selected.permission}`
                  : 'Resolve input validation before creating a Job.'}
              </p>
            )}
          </section>
          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-emerald-700" />
              <h2 className="text-sm font-semibold">Execution boundary</h2>
            </div>
            <ul className="mt-3 space-y-2 text-xs text-muted-foreground">
              <li>No free-form command or shell interpolation</li>
              <li>No arbitrary file path or credential access</li>
              <li>One explicit target scope and hard resource budget</li>
              <li>Result export is filtered and size-bounded</li>
            </ul>
          </section>
        </aside>
      </div>

      <section className="mt-4 border bg-card">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <div>
            <h2 className="text-sm font-semibold">Recent diagnostic Jobs</h2>
            <p className="mt-0.5 text-xs text-muted-foreground">
              Jobs remain globally visible after leaving this page.
            </p>
          </div>
          <Badge variant="outline" className="rounded-sm">
            retention · 7 days
          </Badge>
        </div>
        <Table>
          <TableHeader className="bg-muted/45">
            <TableRow>
              <TableHead>Job</TableHead>
              <TableHead>Diagnostic</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Started</TableHead>
              <TableHead className="text-right">Open</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {[
              [
                'job-3114',
                'Interface link & LACP snapshot',
                scope,
                'operator.review',
                'complete',
                '12:54:12',
              ],
              [
                'job-3108',
                'OpenFlow collection health',
                'Bridge/br-fabric',
                'operator.review',
                'partial',
                '12:38:44',
              ],
              [
                'job-3099',
                'Bounded datapath packet trace',
                'Bridge/br-fabric',
                'network.admin',
                'failed',
                '11:57:03',
              ],
            ].map(([job, name, jobScope, actor, state, started], index) => (
              <TableRow key={job}>
                <TableCell className="font-mono text-xs font-semibold text-[#164f7b]">
                  {job}
                </TableCell>
                <TableCell>{name}</TableCell>
                <TableCell className="font-mono text-xs">{jobScope}</TableCell>
                <TableCell className="font-mono text-xs">{actor}</TableCell>
                <TableCell>
                  <JobBadge state={state as DiagnosticJobState} />
                </TableCell>
                <TableCell className="font-mono text-xs">{started}</TableCell>
                <TableCell className="text-right">
                  {index === 0 ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => onOpenJob(state as DiagnosticJobState)}
                    >
                      Inspect <ChevronRight />
                    </Button>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      retained
                    </span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </section>
    </>
  );
}

type ResultCopy = {
  title: string;
  body: string;
  finding: string;
  next: string;
  tone: string;
};

function resultCopyFor(state: DiagnosticJobState): ResultCopy {
  if (state === 'cancelled')
    return {
      title: 'Cancelled safely',
      body: 'The provider reached a safe checkpoint and stopped without publishing a diagnostic finding.',
      finding: 'No result · cancellation is not interpreted as healthy',
      next: 'Retry this exact bounded case only if current evidence is still required.',
      tone: 'border-slate-300 bg-slate-50 text-slate-800',
    };
  if (state === 'no-finding')
    return {
      title: 'No finding',
      body: 'The bounded checks completed and found no current link or LACP fault.',
      finding: 'No fault detected in sampled scope',
      next: 'Return to the incident and inspect another evidence domain.',
      tone: 'border-emerald-300 bg-emerald-50 text-emerald-900',
    };
  if (state === 'no-data')
    return {
      title: 'No data',
      body: 'The provider responded successfully but returned no samples for the selected interval.',
      finding: 'Zero samples · not interpreted as healthy',
      next: 'Verify provider freshness or retry this same bounded case.',
      tone: 'border-amber-300 bg-amber-50 text-amber-900',
    };
  if (state === 'provider-unavailable' || state === 'unavailable')
    return {
      title: 'Provider unavailable',
      body: 'The operational provider could not supply authoritative facts for this result.',
      finding: 'Result unavailable · health remains Unknown',
      next: 'Open provider evidence; do not broaden the command scope.',
      tone: 'border-amber-300 bg-amber-50 text-amber-900',
    };
  if (state === 'command-failed' || state === 'failed')
    return {
      title: 'Diagnostic operation failed',
      body: 'The predefined operation exited before a valid structured result was produced.',
      finding: 'Execution failed within the existing budget',
      next: 'Inspect the failure Event or retry this exact safe case.',
      tone: 'border-rose-300 bg-rose-50 text-rose-900',
    };
  if (state === 'expired')
    return {
      title: 'Result expired',
      body: 'Result content exceeded its retention period; metadata and Audit evidence remain.',
      finding: 'Output no longer available',
      next: 'Run a new bounded diagnostic if current evidence is required.',
      tone: 'border-slate-300 bg-slate-50 text-slate-800',
    };
  if (state === 'partial')
    return {
      title: 'Partial result',
      body: 'Carrier state was captured, but one LACP partner sample was unavailable.',
      finding: 'Member enp129s0f1 carrier down · partner state unknown',
      next: 'Inspect provider freshness before changing configuration.',
      tone: 'border-amber-300 bg-amber-50 text-amber-900',
    };
  if (state === 'truncated')
    return {
      title: 'Complete with bounded output',
      body: 'The structured result is complete; raw text reached its hard line limit and was truncated.',
      finding: 'Member enp129s0f1 carrier down confirmed',
      next: 'Use the structured finding; narrow scope before another run.',
      tone: 'border-amber-300 bg-amber-50 text-amber-900',
    };
  if (state === 'evidence-unavailable')
    return {
      title: 'Result complete · evidence link unavailable',
      body: 'The diagnostic result is retained, but the Event sink did not acknowledge the correlation link.',
      finding: 'Member enp129s0f1 carrier down confirmed',
      next: 'Preserve the result and retry evidence reconciliation only.',
      tone: 'border-amber-300 bg-amber-50 text-amber-900',
    };
  return {
    title: 'Finding confirmed',
    body: 'The structured result correlates operational link and LACP facts for the selected object.',
    finding: 'Member enp129s0f1 carrier down · Bond remains available',
    next: 'Open the related object or Event before staging any change.',
    tone: 'border-emerald-300 bg-emerald-50 text-emerald-900',
  };
}

function DiagnosticRun({
  mode,
  diagnostic,
  jobState,
  onSetJobState,
  scope,
  onBack,
  onCancel,
  onRetry,
  onOpenEvidence,
  onOpenObject,
  notify,
}: {
  mode: PrototypeMode;
  diagnostic: DiagnosticDefinition;
  jobState: DiagnosticJobState;
  onSetJobState: (state: DiagnosticJobState) => void;
  scope: string;
  onBack: () => void;
  onCancel: () => void;
  onRetry: () => void;
  onOpenEvidence: (kind: 'event' | 'audit') => void;
  onOpenObject: () => void;
  notify: (message: string) => void;
}) {
  const [exported, setExported] = useState(false);
  const active = ['queued', 'running', 'cancel-requested'].includes(jobState);
  const finished = ![
    'not-started',
    'queued',
    'running',
    'cancel-requested',
  ].includes(jobState);
  const retryable = [
    'cancelled',
    'failed',
    'expired',
    'unavailable',
    'no-data',
    'provider-unavailable',
    'command-failed',
  ].includes(jobState);
  const result = resultCopyFor(jobState);
  const progress =
    jobState === 'queued'
      ? 8
      : jobState === 'running'
        ? 62
        : jobState === 'cancel-requested'
          ? 74
          : finished
            ? 100
            : 0;
  const lifecycle = [
    ['Requested', '12:54:12', true],
    ['Queued', '12:54:12', jobState !== 'not-started'],
    ['Running', '12:54:13', !['not-started', 'queued'].includes(jobState)],
    ['Result', finished ? '12:54:28' : 'pending', finished],
  ] as const;
  const rawAvailable =
    finished &&
    ![
      'cancelled',
      'expired',
      'unavailable',
      'provider-unavailable',
      'no-data',
    ].includes(jobState);
  const evidenceMissing = jobState === 'evidence-unavailable';
  return (
    <>
      <PageHeader
        id="P1-08"
        title="Diagnostic Job · job-3114"
        description="Lifecycle, result limits, and Event/Audit links for one controlled diagnostic operation."
        actions={
          <>
            <Button variant="outline" onClick={onBack}>
              <ArrowLeft /> Diagnostics
            </Button>
            <NativeSelect
              aria-label="Select diagnostic job review case"
              value={jobState === 'not-started' ? 'complete' : jobState}
              onChange={(event) => {
                onSetJobState(event.target.value as DiagnosticJobState);
                setExported(false);
              }}
              size="sm"
            >
              {reviewCases.map((state) => (
                <NativeSelectOption key={state} value={state}>
                  {diagnosticJobLabels[state]}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </>
        }
      />

      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(350px,0.6fr)]">
        <div className="space-y-4">
          <section className="border bg-card">
            <div className="flex flex-wrap items-start justify-between gap-3 border-b px-4 py-3">
              <div>
                <div className="flex items-center gap-2">
                  <h2 className="text-sm font-semibold">{diagnostic.name}</h2>
                  <JobBadge state={jobState} />
                </div>
                <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                  job-3114 · corr-DIAG-91C4 · {scope}
                </p>
              </div>
              {active &&
                diagnostic.safeCancel &&
                jobState !== 'cancel-requested' && (
                  <Button variant="outline" size="sm" onClick={onCancel}>
                    <Square /> Cancel safely
                  </Button>
                )}
            </div>
            <div className="p-4">
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">
                  {jobState === 'queued'
                    ? 'Waiting for one diagnostic worker'
                    : jobState === 'cancel-requested'
                      ? 'Cancellation requested · preserving partial evidence'
                      : active
                        ? 'Collecting bounded provider facts'
                        : 'Job lifecycle closed'}
                </span>
                <span className="font-mono font-semibold">{progress}%</span>
              </div>
              <div className="mt-2 h-2 overflow-hidden bg-muted">
                <div
                  className={`h-full transition-[width] ${jobState === 'cancel-requested' ? 'bg-amber-500' : finished ? 'bg-emerald-600' : 'bg-[#2d7899]'}`}
                  style={{ width: `${progress}%` }}
                />
              </div>
              <ol className="mt-4 grid gap-2 sm:grid-cols-4">
                {lifecycle.map(([label, time, reached]) => (
                  <li
                    key={label}
                    className={`border p-3 ${reached ? 'border-[#9bb7cf] bg-[#f2f8fc]' : 'bg-muted/25 text-muted-foreground'}`}
                  >
                    <p className="text-xs font-semibold">{label}</p>
                    <p className="mt-1 font-mono text-[10px]">{time}</p>
                  </li>
                ))}
              </ol>
            </div>
          </section>

          {active ? (
            <section className="border border-sky-300 bg-sky-50 p-5">
              <div className="flex items-start gap-3">
                <Activity className="mt-0.5 size-5 animate-pulse text-sky-700" />
                <div>
                  <h2 className="text-sm font-semibold text-sky-950">
                    Result is not authoritative yet
                  </h2>
                  <p className="mt-1 text-xs text-sky-800">
                    Live updates accelerate display only. The Job resource
                    remains authoritative if this page closes or reconnects.
                  </p>
                </div>
              </div>
            </section>
          ) : (
            <section className={`border p-5 ${result.tone}`}>
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="flex items-start gap-3">
                  {['failed', 'command-failed'].includes(jobState) ? (
                    <XCircle className="mt-0.5 size-6" />
                  ) : [
                      'partial',
                      'truncated',
                      'no-data',
                      'provider-unavailable',
                      'unavailable',
                      'evidence-unavailable',
                      'cancelled',
                    ].includes(jobState) ? (
                    <TriangleAlert className="mt-0.5 size-6" />
                  ) : (
                    <CheckCircle2 className="mt-0.5 size-6" />
                  )}
                  <div>
                    <p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em]">
                      Structured result
                    </p>
                    <h2 className="mt-1 text-lg font-semibold">
                      {result.title}
                    </h2>
                    <p className="mt-1 max-w-3xl text-sm opacity-85">
                      {result.body}
                    </p>
                  </div>
                </div>
                <span className="font-mono text-[10px]">
                  observed_at · 2026-09-03 12:54:28 UTC
                </span>
              </div>
              <dl className="mt-5 grid gap-3 md:grid-cols-3">
                <div className="border border-current/20 bg-white/65 p-3">
                  <dt className="text-[10px] font-semibold uppercase tracking-[0.1em] opacity-70">
                    Finding
                  </dt>
                  <dd className="mt-2 text-sm font-semibold">
                    {result.finding}
                  </dd>
                </div>
                <div className="border border-current/20 bg-white/65 p-3">
                  <dt className="text-[10px] font-semibold uppercase tracking-[0.1em] opacity-70">
                    Coverage
                  </dt>
                  <dd className="mt-2 text-sm font-semibold">
                    1 Port · 2 Interfaces · 10 sec
                  </dd>
                </div>
                <div className="border border-current/20 bg-white/65 p-3">
                  <dt className="text-[10px] font-semibold uppercase tracking-[0.1em] opacity-70">
                    Next safe step
                  </dt>
                  <dd className="mt-2 text-sm font-semibold">{result.next}</dd>
                </div>
              </dl>
            </section>
          )}

          {rawAvailable && (
            <section className="border bg-card">
              <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
                <div>
                  <h2 className="text-sm font-semibold">Bounded raw output</h2>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    Filtered diagnostic content · never an unbounded log stream
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  <Badge variant="outline" className="rounded-sm">
                    {jobState === 'truncated'
                      ? '200 / 200 lines · truncated'
                      : '24 / 200 lines'}
                  </Badge>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      setExported(true);
                      notify(
                        'Bounded diagnostic export prepared · job-3114.json',
                      );
                    }}
                  >
                    <Download /> Export result
                  </Button>
                </div>
              </div>
              {mode === 'expert' ? (
                <pre className="overflow-x-auto whitespace-pre-wrap p-4 font-mono text-[11px] leading-5 text-slate-700">{`source=linux.netlink, ovsdb-server\nobject=Port/bond-storage\nmember=enp129s0f0 carrier=up speed=25000 duplex=full\nmember=enp129s0f1 carrier=down speed=unknown duplex=unknown\nlacp=off bond_mode=active-backup active_member=enp129s0f0\nprovider_observed_at=2026-09-03T12:54:27Z${jobState === 'truncated' ? '\n… output truncated at hard limit; structured finding retained' : ''}`}</pre>
              ) : (
                <div className="p-4">
                  <div className="flex items-start gap-3 border border-dashed p-4">
                    <Terminal className="mt-0.5 size-4 text-[#35627f]" />
                    <div>
                      <p className="text-sm font-semibold">
                        Raw output is available within the reviewed boundary
                      </p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        Switch to Expert Mode to inspect parsed provider lines.
                        Permission, redaction, and size limits do not change.
                      </p>
                    </div>
                  </div>
                </div>
              )}
              {exported && (
                <div className="border-t bg-emerald-50 px-4 py-3 text-xs text-emerald-800">
                  Export prepared · job-3114.json · secrets, tokens,
                  unauthorized configuration, and excess lines excluded
                </div>
              )}
            </section>
          )}
        </div>

        <aside className="space-y-4">
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Job authority</h2>
            <dl className="mt-3 space-y-3 text-xs">
              {[
                ['Actor', 'operator.review'],
                ['Permission', diagnostic.permission],
                ['Scope', scope],
                ['Timeout', '20 seconds'],
                ['Output ceiling', '64 KiB / 200 lines'],
                ['Correlation ID', 'corr-DIAG-91C4'],
              ].map(([label, value]) => (
                <div
                  key={label}
                  className="flex items-start justify-between gap-4 border-t pt-2 first:border-t-0 first:pt-0"
                >
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="font-mono text-right">{value}</dd>
                </div>
              ))}
            </dl>
          </section>
          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <Timer className="size-4 text-[#35627f]" />
              <h2 className="text-sm font-semibold">Cancellation contract</h2>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">
              {diagnostic.safeCancel
                ? 'The provider advertises safe cancellation. A request is recorded first; partial output is retained only after redaction.'
                : 'This atomic diagnostic cannot be cancelled safely. The control is intentionally absent.'}
            </p>
            {jobState === 'cancel-requested' && (
              <div className="mt-3 border border-amber-300 bg-amber-50 p-3 text-xs text-amber-900">
                Cancel request accepted · waiting for the provider checkpoint
              </div>
            )}
            {jobState === 'cancelled' && (
              <div className="mt-3 border border-slate-300 bg-slate-50 p-3 text-xs">
                Cancelled at a safe checkpoint · no result exported
              </div>
            )}
          </section>
          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <FileClock className="size-4 text-[#35627f]" />
              <h2 className="text-sm font-semibold">Evidence links</h2>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">
              The result, Event, and Audit remain distinct records joined by
              correlation ID.
            </p>
            <div className="mt-3 grid gap-2">
              <Button
                variant="outline"
                disabled={evidenceMissing}
                onClick={() => onOpenEvidence('event')}
              >
                Open linked Event
              </Button>
              <Button variant="outline" onClick={() => onOpenEvidence('audit')}>
                Open Audit record
              </Button>
            </div>
            {evidenceMissing && (
              <p className="mt-2 text-xs text-amber-700">
                Event link unavailable; Audit retains actor and operation
                request.
              </p>
            )}
          </section>
          {retryable && (
            <Button
              onClick={onRetry}
              className="w-full bg-[#173b63] hover:bg-[#234f80]"
            >
              <RefreshCw /> Retry this bounded case
            </Button>
          )}
          {finished && (
            <Button
              variant="outline"
              onClick={() => {
                notify('Opened related object · Port/bond-storage');
                onOpenObject();
              }}
              className="w-full"
            >
              <Network /> Open related object
            </Button>
          )}
        </aside>
      </div>
    </>
  );
}

export function P1DiagnosticsView({
  view,
  mode,
  selectedDiagnostic,
  setSelectedDiagnostic,
  inputState,
  setInputState,
  jobState,
  setJobState,
  scope,
  origin,
  clearOrigin,
  runDiagnostic,
  cancelDiagnostic,
  retryDiagnostic,
  openEvidence,
  go,
  notify,
}: {
  view: P1DiagnosticsPage;
  mode: PrototypeMode;
  selectedDiagnostic: string;
  setSelectedDiagnostic: (id: string) => void;
  inputState: DiagnosticInputState;
  setInputState: (state: DiagnosticInputState) => void;
  jobState: DiagnosticJobState;
  setJobState: (state: DiagnosticJobState) => void;
  scope: string;
  origin: string | null;
  clearOrigin: () => void;
  runDiagnostic: () => void;
  cancelDiagnostic: () => void;
  retryDiagnostic: () => void;
  openEvidence: (kind: 'event' | 'audit') => void;
  go: (view: P1View) => void;
  notify: (message: string) => void;
}) {
  const diagnostic =
    diagnostics.find((item) => item.id === selectedDiagnostic) ??
    diagnostics[0];
  if (view === 'diagnostic-run')
    return (
      <DiagnosticRun
        mode={mode}
        diagnostic={diagnostic}
        jobState={jobState}
        onSetJobState={setJobState}
        scope={scope}
        onBack={() => go('diagnostics-hub')}
        onCancel={cancelDiagnostic}
        onRetry={retryDiagnostic}
        onOpenEvidence={openEvidence}
        onOpenObject={() => go('bond-detail')}
        notify={notify}
      />
    );
  return (
    <DiagnosticsHub
      mode={mode}
      selectedDiagnostic={selectedDiagnostic}
      onSelectDiagnostic={setSelectedDiagnostic}
      inputState={inputState}
      onInputStateChange={setInputState}
      scope={scope}
      origin={origin}
      onClearOrigin={clearOrigin}
      onRun={runDiagnostic}
      onOpenJob={(state) => {
        setJobState(state);
        go('diagnostic-run');
      }}
      notify={notify}
    />
  );
}
