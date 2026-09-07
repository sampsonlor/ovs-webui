'use client';

import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  Boxes,
  Cable,
  ChevronRight,
  Clock3,
  Code2,
  GitCompareArrows,
  Info,
  Layers3,
  Menu,
  ShieldCheck,
  X,
} from 'lucide-react';

import { VlanEdit } from '@/components/ovs/vlan-editor';
import { PrototypeNavigation } from '@/components/ovs/prototype-navigation';
import {
  Workspace,
  DiffPage,
  SafeApply,
  Evidence,
  ScenarioBanner,
} from '@/components/ovs/change-pages';
import {
  TransactionBanner,
  outcomeLabels,
} from '@/components/ovs/transaction-states';
import {
  initialControlState,
  transition,
  transactionLocked,
  topologyStageBlock,
  scenarioLabels,
  candidateName,
  type ControlAction,
  type ControlState,
  type Scenario,
} from '@/lib/change-control';
import {
  p1Steps,
  diagnosticJobLabels,
  openFlowReviewLabels,
  type DiagnosticJobState,
  type OpenFlowReviewState,
} from './prototype-model';
import {
  P1Surface,
  P1MobileSummary,
  isP1View,
  useP1Controller,
} from '@/components/ovs/p1-surface';
import {
  representativeBondIntent,
  runnableDiagnostics,
} from '@/lib/p1-control';
import { useCoreLab } from '@/hooks/use-core-lab';
import { CoreLabTransactionBanner } from '@/components/ovs/core-lab-safe-apply';
import {
  CoreLabSession,
  CoreLabSurface,
  coreLabViews,
} from '@/components/ovs/core-lab-surface';

declare const __OVS_CORE_LAB__: boolean;

import { PortsPage } from '@/components/ovs/ports-page';
import {
  PageHeader,
  ScopeBadge,
  StateDot,
  StatusBadge,
  Notice,
} from '@/components/ovs/foundation';
import {
  ports,
  vlanLabel,
  nativeVlanFields,
  type Port,
  type View,
  type Mode,
  type VlanMode,
} from '@/lib/ovs-model';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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

type WebMcpTool = {
  name: string;
  title: string;
  description: string;
  inputSchema: Record<string, unknown>;
  annotations: { readOnlyHint: boolean; untrustedContentHint: boolean };
  execute(input: unknown): unknown;
};

type WebMcpDocument = Document & {
  modelContext?: {
    registerTool(
      tool: WebMcpTool,
      options?: { signal?: AbortSignal },
    ): void | Promise<void>;
  };
};

const steps: Array<{ id: string; label: string; view: View }> = [
  { id: 'P0-01', label: 'Dashboard', view: 'dashboard' },
  { id: 'P0-02', label: 'Ports', view: 'ports' },
  { id: 'P0-03', label: 'Port detail', view: 'port-detail' },
  { id: 'P0-04', label: 'VLAN edit', view: 'vlan-edit' },
  { id: 'P0-05', label: 'Workspace', view: 'workspace' },
  { id: 'P0-06', label: 'Diff', view: 'diff' },
  { id: 'P0-07', label: 'Safe Apply', view: 'safe-apply' },
  { id: 'P0-08', label: 'Evidence', view: 'evidence' },
  { id: 'P0-09', label: 'Responsive', view: 'responsive' },
];

function Dashboard({
  state,
  go,
  openPort,
  onDiagnose,
}: {
  state: ControlState;
  go: (view: View) => void;
  openPort: (name: string) => void;
  onDiagnose: () => void;
}) {
  const staged = Boolean(state.candidate);
  const scenario = state.scenario;
  const driftFree = scenario !== 'drift' && scenario !== 'outcome-unknown';
  return (
    <>
      <PageHeader
        id="P0-01"
        eyebrow="Overview"
        title="Switch overview"
        description="Operational posture, pending intent, and evidence entry points—without hiding uncertainty."
        scope="Observe"
        actions={
          <>
            <Button variant="outline" onClick={onDiagnose}>
              Diagnose health
            </Button>
            <Button
              onClick={() => go('ports')}
              className="gap-2 bg-[#157a9e] hover:bg-[#11627f]"
            >
              Open ports <ChevronRight />
            </Button>
          </>
        }
      />
      <div className="mt-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[
          {
            label: 'Ports online',
            value: '4 / 6',
            meta: '1 down · 1 unknown',
            icon: Cable,
            tone: 'text-amber-700',
          },
          {
            label: 'Bridges',
            value: '4',
            meta: '6 representative ports',
            icon: Boxes,
            tone: 'text-slate-600',
          },
          {
            label: 'Active alerts',
            value: scenario === 'normal' ? '2' : '3',
            meta: '0 critical · warnings',
            icon: AlertTriangle,
            tone: 'text-amber-700',
          },
          {
            label: 'Configuration',
            value: driftFree ? 'Drift-free' : 'Drift',
            meta: driftFree
              ? 'generation 1842'
              : '1841 desired / 1842 observed',
            icon: ShieldCheck,
            tone: driftFree ? 'text-emerald-700' : 'text-rose-700',
          },
        ].map(({ label, value, meta, icon: Icon, tone }) => (
          <article
            key={label}
            className="border bg-card p-4 shadow-[0_1px_0_rgb(15_23_42/4%)]"
          >
            <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
              <span>{label}</span>
              <Icon className={`size-4 ${tone}`} />
            </div>
            <p className="mt-3 text-2xl font-semibold tracking-tight">
              {value}
            </p>
            <p className={`mt-1 text-xs ${tone}`}>{meta}</p>
          </article>
        ))}
      </div>
      <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.75fr)]">
        <section className="min-w-0 border bg-card">
          <div className="flex items-center justify-between border-b px-4 py-3">
            <div>
              <h2 className="text-sm font-semibold">Port posture</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">
                Last confirmed fixture · representative links
              </p>
            </div>
            <Button variant="ghost" size="sm" onClick={() => go('ports')}>
              View all 6
            </Button>
          </div>
          <Table>
            <TableHeader className="bg-muted/45">
              <TableRow>
                <TableHead>Port</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Speed</TableHead>
                <TableHead>VLAN</TableHead>
                <TableHead>Bridge</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ports
                .slice(0, 4)
                .map((item) =>
                  state.live[item.name]
                    ? { ...item, vlan: vlanLabel(state.live[item.name]) }
                    : item,
                )
                .map((port) => (
                  <TableRow
                    key={port.name}
                    className="cursor-pointer"
                    onClick={() => openPort(port.name)}
                  >
                    <TableCell className="font-mono text-xs font-semibold text-[#164f7b]">
                      {port.name}
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex items-center gap-2">
                        <StateDot state={port.state} />
                        {port.state}
                      </span>
                    </TableCell>
                    <TableCell>{port.speed}</TableCell>
                    <TableCell>{port.vlan}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {port.bridge}
                    </TableCell>
                  </TableRow>
                ))}
            </TableBody>
          </Table>
        </section>
        <div className="grid gap-4">
          <section className="border border-[#9bb7cf] bg-[#f2f8fc] p-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <p className="font-mono text-xs font-semibold uppercase tracking-[0.12em] text-[#35627f]">
                  Candidate workspace
                </p>
                <h2 className="mt-1 text-base font-semibold">
                  {staged ? '1 staged change' : 'Workspace is clean'}
                </h2>
              </div>
              <Layers3 className="size-5 text-[#35627f]" />
            </div>
            <div className="mt-4 border border-[#c8dbe9] bg-white p-3">
              <p className="text-sm font-medium">
                {staged
                  ? `${candidateName(state.candidate)} · ${state.candidate!.kind.toUpperCase()}`
                  : 'No pending intent'}
              </p>
              <p className="mt-1 font-mono text-xs text-muted-foreground">
                {state.candidate
                  ? state.candidate.kind === 'vlan'
                    ? `${vlanLabel(state.candidate.base)} → ${vlanLabel(state.candidate.mine)}`
                    : state.candidate.intent.summary
                  : 'Start from a managed object.'}
              </p>
            </div>
            <Button
              onClick={() => go(staged ? 'workspace' : 'ports')}
              className="mt-3 w-full bg-[#173b63] hover:bg-[#234f80]"
            >
              {staged ? 'Review workspace' : 'Browse managed objects'}
            </Button>
          </section>
          <section className="border bg-card p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">Recent evidence</h2>
              <Button variant="ghost" size="xs" onClick={() => go('evidence')}>
                Timeline
              </Button>
            </div>
            <ol className="mt-3 space-y-3">
              {state.evidence
                .slice(-3)
                .reverse()
                .map((entry, index) => (
                  <li key={index} className="border-t pt-2 text-xs">
                    <span className="font-semibold text-primary">
                      {entry.kind}
                    </span>
                    <p className="mt-1 text-muted-foreground">{entry.text}</p>
                  </li>
                ))}
            </ol>
            {!state.evidence.length && (
              <p className="mt-3 text-sm text-muted-foreground">
                No operations recorded this session.
              </p>
            )}
          </section>
        </div>
      </div>
    </>
  );
}

function PortDetail({
  port,
  generation,
  mode,
  go,
  onDiagnose,
}: {
  port: Port;
  generation: number;
  mode: Mode;
  go: (view: View) => void;
  onDiagnose: () => void;
}) {
  const editable = port.scope !== 'Observe' && port.authority === 'OVS';
  return (
    <>
      <PageHeader
        id="P0-03"
        eyebrow="Switching / Port detail"
        title={port.name}
        description="Operational state, native object relationship, ownership, and safe entry points for configuration."
        scope={port.scope}
        actions={
          <>
            <Button variant="outline" onClick={() => go('ports')}>
              <ArrowLeft /> Ports
            </Button>
            <Button variant="outline" onClick={onDiagnose}>
              Diagnose
            </Button>
            {editable && (
              <Button
                onClick={() => go('vlan-edit')}
                className="bg-[#157a9e] hover:bg-[#11627f]"
              >
                Edit VLAN
              </Button>
            )}
          </>
        }
      />
      {!editable && (
        <div className="mt-4 flex items-start gap-3 border border-slate-300 bg-slate-50 p-4">
          <Info className="mt-0.5 size-5 text-slate-600" />
          <div>
            <p className="text-sm font-semibold">Observe-only object</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Authority: {port.provider}. OVS WebUI will not show an Edit action
              until the provider advertises a native capability.
            </p>
          </div>
        </div>
      )}
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(320px,0.6fr)]">
        <div className="space-y-4">
          <section className="border bg-card">
            <div className="border-b px-4 py-3">
              <h2 className="text-sm font-semibold">Operational summary</h2>
            </div>
            <dl className="grid sm:grid-cols-2 xl:grid-cols-4">
              {[
                ['Link state', port.state],
                ['Speed', port.speed],
                ['VLAN', port.vlan],
                ['Bridge', port.bridge],
              ].map(([label, value]) => (
                <div
                  key={label}
                  className="border-b p-4 sm:border-r xl:border-b-0"
                >
                  <dt className="text-xs text-muted-foreground">{label}</dt>
                  <dd className="mt-1 font-mono text-sm font-semibold">
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
          <section className="border bg-card p-4">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">OVS object relationship</h2>
              <Badge variant="outline" className="rounded-sm">
                invariant
              </Badge>
            </div>
            <div className="mt-4 grid items-center gap-2 sm:grid-cols-[1fr_auto_1fr_auto_1fr]">
              <div className="border bg-muted/35 p-3">
                <p className="font-mono text-xs text-muted-foreground">
                  Bridge
                </p>
                <p className="mt-1 font-mono text-sm font-semibold">
                  {port.bridge}
                </p>
              </div>
              <ChevronRight className="mx-auto size-4 text-muted-foreground" />
              <div className="border border-[#8bb7d1] bg-[#f2f8fc] p-3">
                <p className="font-mono text-xs text-muted-foreground">Port</p>
                <p className="mt-1 font-mono text-sm font-semibold">
                  {port.name}
                </p>
              </div>
              <ChevronRight className="mx-auto size-4 text-muted-foreground" />
              <div className="border bg-muted/35 p-3">
                <p className="font-mono text-xs text-muted-foreground">
                  {port.members ? 'Member Interfaces' : 'Interface'}
                </p>
                <p className="mt-1 break-words font-mono text-sm font-semibold">
                  {port.interfaceName}
                </p>
              </div>
            </div>
          </section>
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Configuration surfaces</h2>
            <div className="mt-3 grid gap-3 sm:grid-cols-3">
              {[
                ['VLAN', editable ? 'Basic Manage' : 'Observe', port.vlan],
                [
                  'Bond / LACP',
                  'Observe',
                  port.members
                    ? `Bond Port · ${port.members.length} member Interfaces`
                    : 'Not a bond member',
                ],
                ['STP / RSTP', 'Observe', 'Forwarding'],
              ].map(([name, scope, value]) => (
                <button
                  key={name}
                  type="button"
                  disabled={name !== 'VLAN' || !editable}
                  onClick={() => go('vlan-edit')}
                  className="border bg-white p-3 text-left disabled:cursor-default disabled:opacity-70"
                >
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-semibold">{name}</span>
                    <ScopeBadge scope={scope} />
                  </div>
                  <p className="mt-3 text-xs text-muted-foreground">{value}</p>
                </button>
              ))}
            </div>
          </section>
        </div>
        <aside className="space-y-4">
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Identity & authority</h2>
            <dl className="mt-3 space-y-3 text-xs">
              {[
                ['Authority', port.authority],
                ['Provider', port.provider],
                ['Generation', String(generation)],
                ['Source', 'Last confirmed fixture'],
                [
                  'Permission',
                  editable
                    ? 'change.stage: allowed'
                    : 'change.stage: unavailable',
                ],
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
          {mode === 'expert' && (
            <section className="border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4">
              <div className="flex items-center gap-2">
                <Code2 className="size-4 text-[#35627f]" />
                <h2 className="text-sm font-semibold">Expert native fields</h2>
              </div>
              <pre className="mt-3 overflow-x-auto border bg-white p-3 font-mono text-xs leading-5 text-slate-700">
                {JSON.stringify(
                  {
                    _uuid: port.uuid,
                    name: port.name,
                    ...(port.scope === 'Observe'
                      ? { vlan: 'Provider-owned · unavailable' }
                      : nativeVlanFields(port.config)),
                  },
                  null,
                  2,
                )}
              </pre>
            </section>
          )}
        </aside>
      </div>
    </>
  );
}

function ResponsivePage({
  state,
  go,
}: {
  state: ControlState;
  go: (view: View) => void;
}) {
  return (
    <>
      <PageHeader
        id="P0-09"
        eyebrow="Responsive responsibility"
        title="Device responsibility"
        description="Desktop prepares configuration. Tablet reviews. Mobile handles incidents and existing transactions."
      />
      <div className="mt-5 grid gap-5 xl:grid-cols-[1fr_360px]">
        <section className="ovs-surface">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Responsibility</TableHead>
                <TableHead>Desktop</TableHead>
                <TableHead>Tablet</TableHead>
                <TableHead>Mobile</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {[
                [
                  'Observe health & evidence',
                  'Full',
                  'Full',
                  'Incident summary',
                ],
                ['Prepare VLAN intent', 'Full', 'Review only', 'No'],
                ['Start Safe Apply', 'Yes', 'No', 'No'],
                ['Handle active Safe Apply', 'Yes', 'Yes', 'Yes'],
                ['Native identity', 'Expert', 'Read-only', 'Summary'],
              ].map((row) => (
                <TableRow key={row[0]}>
                  {row.map((value, index) => (
                    <TableCell key={index}>{value}</TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <p className="border-t p-4 text-sm text-muted-foreground">
            Mode never changes permissions. Production confirmation requires an
            authorized, reauthenticated responder.
          </p>
        </section>
        <MobilePanel state={state} go={go} framed />
      </div>
    </>
  );
}

function MobilePanel({
  state,
  go,
  framed = false,
}: {
  state: ControlState;
  go: (view: View) => void;
  framed?: boolean;
}) {
  const active = transactionLocked(state.transaction.status);
  return (
    <section
      className={`ovs-surface mx-auto w-full max-w-md p-5 ${framed ? 'border-4' : ''}`}
      aria-label="Mobile incident companion"
    >
      <p className="ovs-eyebrow">sw-edge-01 / incident companion</p>
      <h2 className="mt-2 text-xl font-semibold">
        {active ? 'Safe Apply needs attention' : 'Node posture'}
      </h2>
      {active ? (
        <div className="mt-4">
          <StatusBadge
            tone={
              state.transaction.status === 'countdown' ? 'info' : 'uncertain'
            }
          >
            {outcomeLabels[state.transaction.status]}
          </StatusBadge>
          <p className="mt-3 text-sm">
            {state.transaction.id} · {candidateName(state.transaction.snapshot)}
          </p>
          <Button className="mt-4 w-full" onClick={() => go('safe-apply')}>
            Handle active transaction
          </Button>
        </div>
      ) : (
        <div className="mt-4">
          <p className="text-sm">Last inventory: 4 up · 1 down · 1 unknown.</p>
          <Notice
            tone={state.scenario === 'normal' ? 'warning' : 'uncertain'}
            title={
              state.scenario === 'normal'
                ? 'server-08 · link down'
                : scenarioLabels[state.scenario]
            }
          >
            Synthetic inventory. No live network is connected.
          </Notice>
        </div>
      )}
      <Button
        variant="outline"
        className="mt-3 w-full"
        onClick={() => go('evidence')}
      >
        Open evidence
      </Button>
      <p className="mt-4 text-sm text-muted-foreground">
        New high-risk configuration requires desktop.
      </p>
    </section>
  );
}

function P0Stepper({ view, go }: { view: View; go: (view: View) => void }) {
  return (
    <nav
      aria-label="P0 and P1 prototype steps"
      className="mt-5 overflow-x-auto border-t pt-3"
    >
      <ol className="flex min-w-max items-center gap-1">
        {[...steps, ...p1Steps].map((step, index) => (
          <li key={step.id} className="flex items-center">
            <button
              type="button"
              onClick={() => go(step.view)}
              aria-current={view === step.view ? 'step' : undefined}
              className={`px-2 py-1 text-left ${view === step.view ? 'bg-[#dce9f5] text-[#12385e]' : 'text-muted-foreground hover:bg-muted'}`}
            >
              <span className="block font-mono text-xs">{step.id}</span>
              <span className="block text-xs font-medium">{step.label}</span>
            </button>
            {index < steps.length + p1Steps.length - 1 && (
              <ChevronRight className="size-3 text-slate-300" />
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}

export default function Home() {
  const labEnabled = __OVS_CORE_LAB__;
  const [view, setView] = useState<View>('ports');
  const lab = useCoreLab(labEnabled, view !== 'vlan-edit');
  const [mode, setMode] = useState<Mode>('standard');
  const [selectedPort, setSelectedPort] = useState('server-07');
  const [search, setSearch] = useState('');
  const [vlanMode, setVlanMode] = useState('access');
  const [allowedVlans, setAllowedVlans] = useState('');
  const [nativeTag, setNativeTag] = useState('120');
  const [control, setControl] = useState<ControlState>(initialControlState);
  const [currentTime, setCurrentTime] = useState(0);
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [toast, setToast] = useState(
    'Design System v0.1 · Ports and shared transaction states',
  );
  const controlRef = useRef(control);
  const selectedRef = useRef(selectedPort);
  const presentationRef = useRef({ view, mode });
  useEffect(() => {
    presentationRef.current = { view, mode };
  }, [view, mode]);

  const act = (action: ControlAction) => {
    if (labEnabled && !['note', 'record-evidence'].includes(action.type)) {
      const error =
        'Local integration uses the saved server Candidate. Open Ports or Changes; other configuration APIs are not connected yet.';
      setToast(error);
      return { ...controlRef.current, error };
    }
    if ('now' in action) setCurrentTime(action.now);
    const next = transition(controlRef.current, action);
    if (next !== controlRef.current) {
      controlRef.current = next;
      setControl(next);
      if (action.type !== 'tick' && action.type !== 'note')
        setToast(next.message);
    }
    return next;
  };

  const selectPort = (name: string) => {
    selectedRef.current = name;
    setSelectedPort(name);
  };
  const go = (next: View) => {
    if (next === 'vlan-edit') {
      const original =
        ports.find((item) => item.name === selectedRef.current) ?? ports[1];
      const current = controlRef.current;
      const value =
        current.candidate?.kind === 'vlan' &&
        current.candidate.port.name === original.name
          ? current.candidate.mine
          : (current.live[original.name] ?? original.config);
      setVlanMode(value.mode);
      setAllowedVlans(value.trunks);
      setNativeTag(value.tag === null ? '' : String(value.tag));
    }
    setView(next);
    setNavigationOpen(false);
    window.scrollTo({ top: 0, behavior: 'auto' });
  };
  const portFixture =
    ports.find((item) => item.name === selectedPort) ?? ports[1];
  const config = control.live[selectedPort] ?? portFixture.config;
  const port = {
    ...portFixture,
    config,
    vlan:
      portFixture.scope === 'Observe' ? portFixture.vlan : vlanLabel(config),
  };
  const locked = transactionLocked(control.transaction.status);
  const stageChange = () => {
    const next = act({
      type: 'stage',
      port,
      mine: {
        mode: vlanMode as VlanMode,
        tag: vlanMode === 'trunk' ? null : Number(nativeTag),
        trunks: vlanMode === 'access' ? '' : allowedVlans,
      },
      desktop: window.matchMedia('(min-width: 1024px)').matches,
      now: Date.now(),
    });
    if (!next.error) go('workspace');
  };

  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      setCurrentTime(now);
      const next = transition(controlRef.current, { type: 'tick', now });
      if (next !== controlRef.current) {
        controlRef.current = next;
        setControl(next);
        setToast(next.message);
      }
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  const p1 = useP1Controller({
    getScenario: () => controlRef.current.scenario,
    act,
    go,
    notify: setToast,
    scenario: control.scenario,
    openPort: (name) => {
      if (!ports.some((item) => item.name === name)) {
        setToast(`Port/${name} is outside the representative inventory.`);
        return;
      }
      selectPort(name);
      go('port-detail');
    },
  });
  const interactionRef = useRef({ act, go, p1 });
  useEffect(() => {
    interactionRef.current = { act, go, p1 };
  });
  useEffect(() => {
    if (labEnabled) return;
    const context = (document as WebMcpDocument).modelContext;
    if (!context?.registerTool) return;
    const lifecycle = new AbortController();
    const register = (tool: WebMcpTool) => {
      try {
        void Promise.resolve(
          context.registerTool(tool, { signal: lifecycle.signal }),
        ).catch(() => undefined);
      } catch {
        /* Registration is optional. */
      }
    };
    const inputObject = (input: unknown): Record<string, unknown> => {
      if (!input || typeof input !== 'object' || Array.isArray(input))
        throw new Error('Expected an object');
      return input as Record<string, unknown>;
    };
    const run = (action: ControlAction) => {
      const next = interactionRef.current.act(action);
      if (next.error) throw new Error(next.error);
      return next;
    };
    const emptySchema = {
      type: 'object',
      properties: {},
      additionalProperties: false,
    };
    register({
      name: 'read_prototype_state',
      title: 'Read prototype state',
      description:
        'Read synthetic presentation, candidate and transaction resources.',
      inputSchema: emptySchema,
      annotations: { readOnlyHint: true, untrustedContentHint: false },
      execute: () => ({
        ...presentationRef.current,
        ...controlRef.current,
        diagnostic: {
          job: interactionRef.current.p1.request ? 'job-3114' : null,
          reviewOnly: interactionRef.current.p1.reviewOnly,
          state: interactionRef.current.p1.jobState,
          scope:
            interactionRef.current.p1.request?.scope ??
            interactionRef.current.p1.scope,
          request: interactionRef.current.p1.request,
        },
        openFlowReviewState: interactionRef.current.p1.openFlowState,
        openFlowCollection: {
          status: interactionRef.current.p1.openFlow.status,
          capturedQuery:
            interactionRef.current.p1.openFlow.snapshot?.query ?? null,
          retainedRows:
            interactionRef.current.p1.openFlow.snapshot?.rows.length ?? 0,
        },
        prototype: true,
      }),
    });
    register({
      name: 'navigate_p0_view',
      title: 'Navigate P0 view',
      description: 'Open an existing P0 view without changing configuration.',
      inputSchema: {
        type: 'object',
        properties: {
          view: { type: 'string', enum: steps.map((step) => step.view) },
        },
        required: ['view'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true, untrustedContentHint: false },
      execute: (input) => {
        const next = inputObject(input).view;
        if (
          typeof next !== 'string' ||
          !steps.some((step) => step.view === next)
        )
          throw new Error('Unknown P0 view');
        interactionRef.current.go(next as View);
        return { view: next };
      },
    });
    register({
      name: 'stage_vlan_change',
      title: 'Stage VLAN change',
      description:
        'Stage one synthetic VLAN intent. Desktop, authority and VLAN validation gates match the UI.',
      inputSchema: {
        type: 'object',
        properties: {
          port: { type: 'string' },
          vlanMode: {
            type: 'string',
            enum: ['access', 'trunk', 'native-tagged'],
          },
          allowedVlans: { type: 'string' },
          nativeTag: { type: 'integer', minimum: 1, maximum: 4094 },
        },
        required: ['port', 'vlanMode', 'allowedVlans'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const data = inputObject(input);
        const target = ports.find((item) => item.name === data.port);
        if (
          !target ||
          typeof data.allowedVlans !== 'string' ||
          !['access', 'trunk', 'native-tagged'].includes(String(data.vlanMode))
        )
          throw new Error('Invalid port or VLAN intent');
        const tag =
          data.vlanMode === 'trunk'
            ? null
            : typeof data.nativeTag === 'number'
              ? data.nativeTag
              : (controlRef.current.live[target.name] ?? target.config).tag;
        const next = run({
          type: 'stage',
          port: target,
          mine: {
            mode: data.vlanMode as VlanMode,
            tag,
            trunks: data.vlanMode === 'access' ? '' : data.allowedVlans,
          },
          desktop: window.matchMedia('(min-width: 1024px)').matches,
          now: Date.now(),
        });
        selectPort(target.name);
        interactionRef.current.go('workspace');
        return { candidate: next.candidate };
      },
    });
    register({
      name: 'validate_workspace',
      title: 'Validate workspace',
      description:
        'Validate the current candidate revision; never bypass a blocking scenario.',
      inputSchema: emptySchema,
      annotations: { readOnlyHint: true, untrustedContentHint: false },
      execute: () => {
        const next = run({ type: 'validate', now: Date.now() });
        interactionRef.current.go('diff');
        return { revision: next.validatedRevision, result: 'pass' };
      },
    });
    register({
      name: 'start_safe_apply',
      title: 'Start Safe Apply',
      description:
        'Start a validated synthetic transaction with a required Audit reason, on desktop only.',
      inputSchema: {
        type: 'object',
        properties: { reason: { type: 'string' } },
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const data = inputObject(input);
        if (data.reason !== undefined) {
          if (typeof data.reason !== 'string')
            throw new Error('reason must be text');
          run({ type: 'note', note: data.reason });
        }
        const next = run({
          type: 'start',
          desktop: window.matchMedia('(min-width: 1024px)').matches,
          now: Date.now(),
        });
        interactionRef.current.go('safe-apply');
        return next.transaction;
      },
    });
    register({
      name: 'complete_safe_apply',
      title: 'Complete Safe Apply',
      description:
        'Confirm or request a protected rollback of the active transaction only.',
      inputSchema: {
        type: 'object',
        properties: {
          decision: { type: 'string', enum: ['confirm', 'rollback'] },
        },
        required: ['decision'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const decision = inputObject(input).decision;
        if (decision !== 'confirm' && decision !== 'rollback')
          throw new Error('Invalid decision');
        const next = run({ type: decision, now: Date.now() });
        interactionRef.current.go('safe-apply');
        return next.transaction;
      },
    });
    register({
      name: 'set_review_scenario',
      title: 'Set review scenario',
      description:
        'Inject synthetic observations for reviewing exception states. Active transaction locks remain in force.',
      inputSchema: {
        type: 'object',
        properties: {
          scenario: { type: 'string', enum: Object.keys(scenarioLabels) },
        },
        required: ['scenario'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const next = inputObject(input).scenario;
        if (typeof next !== 'string' || !Object.hasOwn(scenarioLabels, next))
          throw new Error('Unknown scenario');
        return run({
          type: 'scenario',
          scenario: next as Scenario,
          now: Date.now(),
        }).scenario;
      },
    });
    register({
      name: 'navigate_prototype_view',
      title: 'Navigate prototype view',
      description: 'Open an existing P0 or P1 view without changing intent.',
      inputSchema: {
        type: 'object',
        properties: {
          view: {
            type: 'string',
            enum: [...steps, ...p1Steps].map((step) => step.view),
          },
        },
        required: ['view'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true, untrustedContentHint: false },
      execute: (input) => {
        const next = inputObject(input).view;
        if (
          typeof next !== 'string' ||
          ![...steps, ...p1Steps].some((step) => step.view === next)
        )
          throw new Error('Unknown prototype view');
        interactionRef.current.go(next as View);
        return { view: next };
      },
    });
    register({
      name: 'stage_bond_change',
      title: 'Stage Bond change',
      description:
        'Stage a representative Bond Port intent through the shared Candidate safety gates.',
      inputSchema: {
        type: 'object',
        properties: {
          name: { type: 'string' },
          bridge: { type: 'string' },
          mode: {
            type: 'string',
            enum: ['balance-tcp', 'active-backup', 'balance-slb'],
          },
          lacp: { type: 'string', enum: ['active', 'passive', 'off'] },
        },
        required: ['name', 'bridge', 'mode', 'lacp'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const next = interactionRef.current.p1.stageIntent(
          representativeBondIntent(inputObject(input)),
        );
        if (next.error) throw new Error(next.error);
        return {
          workspace: 'ws-183',
          candidate: next.candidate,
          prototype: true,
        };
      },
    });
    register({
      name: 'run_bounded_diagnostic',
      title: 'Run bounded diagnostic',
      description: 'Start a predefined, bounded synthetic diagnostic Job.',
      inputSchema: {
        type: 'object',
        properties: {
          diagnostic: { type: 'string', enum: runnableDiagnostics },
          scope: { type: 'string' },
        },
        required: ['diagnostic', 'scope'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const data = inputObject(input);
        if (
          typeof data.diagnostic !== 'string' ||
          typeof data.scope !== 'string'
        )
          throw new Error('Diagnostic and one object scope are required.');
        const blocked = interactionRef.current.p1.runDiagnostic(
          data.diagnostic,
          data.scope,
        );
        if (blocked) throw new Error(blocked);
        return {
          job: 'job-3114',
          state: 'queued',
          correlationId: 'corr-DIAG-91C4',
          prototype: true,
        };
      },
    });
    register({
      name: 'cancel_diagnostic_job',
      title: 'Cancel diagnostic Job',
      description: 'Request cancellation at an advertised safe checkpoint.',
      inputSchema: emptySchema,
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: () => {
        const blocked = interactionRef.current.p1.cancelDiagnostic();
        if (blocked) throw new Error(blocked);
        return { job: 'job-3114', state: 'cancel-requested' };
      },
    });
    register({
      name: 'set_diagnostic_review_state',
      title: 'Set diagnostic review state',
      description: 'Select a synthetic Job or result state for review.',
      inputSchema: {
        type: 'object',
        properties: {
          state: { type: 'string', enum: Object.keys(diagnosticJobLabels) },
        },
        required: ['state'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: false },
      execute: (input) => {
        const next = inputObject(input).state;
        if (
          typeof next !== 'string' ||
          !Object.hasOwn(diagnosticJobLabels, next)
        )
          throw new Error('Unknown diagnostic review state');
        const blocked = interactionRef.current.p1.reviewDiagnostic(
          next as DiagnosticJobState,
        );
        if (blocked) throw new Error(blocked);
        return {
          job: next === 'not-started' ? null : 'job-3114',
          state: next,
          reviewOnly: next !== 'not-started',
        };
      },
    });
    register({
      name: 'set_openflow_review_state',
      title: 'Set OpenFlow review state',
      description: 'Open the read-only viewer in a synthetic collection state.',
      inputSchema: {
        type: 'object',
        properties: {
          state: { type: 'string', enum: Object.keys(openFlowReviewLabels) },
        },
        required: ['state'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true, untrustedContentHint: false },
      execute: (input) => {
        const next = inputObject(input).state;
        if (
          typeof next !== 'string' ||
          !Object.hasOwn(openFlowReviewLabels, next)
        )
          throw new Error('Unknown OpenFlow review state');
        const blocked = interactionRef.current.p1.reviewOpenFlow(
          next as OpenFlowReviewState,
        );
        return {
          view: 'openflow-viewer',
          requestedCase: next,
          blocked,
          capability: 'Observe',
        };
      },
    });
    return () => lifecycle.abort();
  }, [labEnabled]);

  const common = { state: control, mode, act, go };
  const refreshInventory = () => {
    if (
      [
        'loading',
        'empty',
        'error',
        'provider-unavailable',
        'degraded',
      ].includes(controlRef.current.scenario)
    )
      act({ type: 'scenario', scenario: 'normal', now: Date.now() });
    setToast('Synthetic inventory refreshed. Transaction state retained.');
  };
  let content: ReactNode;
  if (labEnabled && coreLabViews.includes(view))
    content = (
      <CoreLabSurface
        connection={lab}
        view={view}
        mode={mode}
        selected={selectedPort}
        setSelected={selectPort}
        go={go}
      />
    );
  else if (isP1View(view))
    content = (
      <P1Surface
        view={view}
        mode={mode}
        scenario={control.scenario}
        stageBlock={
          labEnabled
            ? 'Bridge and Bond configuration is not connected to the local lab. Review the saved Candidate in Changes.'
            : topologyStageBlock(control, true)
        }
        controller={p1}
        go={go}
        notify={setToast}
      />
    );
  else if (view === 'dashboard')
    content = (
      <Dashboard
        state={control}
        go={go}
        onDiagnose={() =>
          p1.openDiagnostics(
            'Port/bond-storage',
            'System Health / member degradation',
          )
        }
        openPort={(name) => {
          selectPort(name);
          go('port-detail');
        }}
      />
    );
  else if (view === 'ports')
    content = (
      <PortsPage
        mode={mode}
        scenario={control.scenario}
        onRefresh={refreshInventory}
        search={search}
        setSearch={setSearch}
        selected={selectedPort}
        setSelected={selectPort}
        go={go}
        live={control.live}
      />
    );
  else if (view === 'port-detail')
    content = (
      <PortDetail
        port={port}
        generation={control.generation}
        mode={mode}
        go={go}
        onDiagnose={() => p1.openDiagnostics(`Port/${port.name}`)}
      />
    );
  else if (view === 'vlan-edit')
    content = (
      <VlanEdit
        port={port}
        mode={mode}
        vlanMode={vlanMode}
        setVlanMode={setVlanMode}
        allowedVlans={allowedVlans}
        setAllowedVlans={setAllowedVlans}
        nativeTag={nativeTag}
        setNativeTag={setNativeTag}
        locked={locked}
        onStage={stageChange}
        go={go}
      />
    );
  else if (view === 'workspace') content = <Workspace {...common} />;
  else if (view === 'diff') content = <DiffPage {...common} />;
  else if (view === 'safe-apply')
    content = <SafeApply {...common} currentTime={currentTime} />;
  else if (view === 'evidence') content = <Evidence state={control} go={go} />;
  else content = <ResponsivePage state={control} go={go} />;
  if (
    control.scenario === 'permission-denied' &&
    ['port-detail', 'vlan-edit'].includes(view)
  )
    content = (
      <Notice tone="danger" title="Permission required">
        switching.read is unavailable. Object details are withheld.
      </Notice>
    );

  return (
    <main className="min-h-screen bg-background text-foreground">
      <a
        className="sr-only focus:not-sr-only focus:block focus:p-3"
        href="#page-content"
      >
        Skip to page content
      </a>
      <header className="sticky top-0 z-30 border-b bg-card">
        <div className="flex min-h-16 flex-wrap items-center gap-3 px-4 py-2 lg:px-6">
          <Button
            className="lg:hidden"
            variant="ghost"
            size="icon"
            aria-label="Toggle navigation"
            aria-expanded={navigationOpen}
            onClick={() => setNavigationOpen(!navigationOpen)}
          >
            <Menu />
          </Button>
          <button
            onClick={() => go('dashboard')}
            className="grid size-9 place-items-center rounded bg-primary font-mono text-xs font-bold text-primary-foreground"
            aria-label="OVS dashboard"
          >
            OVS
          </button>
          <div className="mr-auto">
            <p className="text-sm font-semibold tracking-tight">OVS WebUI</p>
            <p className="font-mono text-xs text-muted-foreground">
              sw-edge-01
            </p>
          </div>
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="sm" onClick={() => go('dashboard')}>
              <Activity />
              <span className="hidden sm:inline">Health</span>
              <span className="sr-only sm:hidden">Health</span>
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                go(
                  labEnabled
                    ? 'evidence'
                    : locked
                      ? 'safe-apply'
                      : p1.jobState !== 'not-started'
                        ? 'diagnostic-run'
                        : 'diagnostics-hub',
                )
              }
            >
              <Clock3 />
              <span className="hidden sm:inline">Jobs</span>
              <span className="sr-only sm:hidden">Jobs</span>
              {labEnabled
                ? lab.state.transaction?.locksCandidate
                  ? ' · 1'
                  : ''
                : locked || p1.busy
                  ? ` · ${Number(locked) + Number(p1.busy)}`
                  : ''}
            </Button>
            <Button size="sm" onClick={() => go('workspace')}>
              <GitCompareArrows />
              Changes ·{' '}
              {labEnabled
                ? (lab.state.snapshot?.candidate.intents.length ?? '—')
                : control.candidate
                  ? 1
                  : 0}
            </Button>
          </div>
          <div
            className="hidden items-center rounded border p-0.5 md:flex"
            aria-label="Information depth"
          >
            {(['standard', 'expert'] as const).map((value) => (
              <button
                key={value}
                aria-pressed={mode === value}
                onClick={() => setMode(value)}
                className={`rounded-sm px-2.5 py-1.5 text-sm capitalize ${mode === value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}
              >
                {value}
              </button>
            ))}
          </div>
        </div>
        {labEnabled ? (
          <CoreLabTransactionBanner
            connection={lab}
            onOpen={() => go('safe-apply')}
          />
        ) : (
          <TransactionBanner
            state={control}
            now={currentTime}
            onOpen={() => go('safe-apply')}
          />
        )}
      </header>
      {labEnabled && <CoreLabSession connection={lab} />}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b bg-muted/60 px-4 py-2 lg:px-6">
        <p className="text-xs text-muted-foreground">
          {labEnabled
            ? coreLabViews.includes(view)
              ? 'Ports / Candidate integration · synthetic inventory · durable local storage'
              : 'P1 review fixtures · configuration uses the connected Ports / Candidate workflow'
            : 'Prototype · synthetic data · session resets on reload'}
        </p>
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          Review state
          <NativeSelect
            aria-label="Review state"
            disabled={labEnabled}
            size="sm"
            value={control.scenario}
            onChange={(event) =>
              act({
                type: 'scenario',
                scenario: event.target.value as Scenario,
                now: Date.now(),
              })
            }
          >
            {Object.entries(scenarioLabels).map(([value, label]) => (
              <NativeSelectOption key={value} value={value}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </label>
      </div>
      {navigationOpen && (
        <PrototypeNavigation view={view} mode={mode} go={go} compact />
      )}
      <div className="min-h-[calc(100vh-7rem)] lg:grid lg:grid-cols-[224px_minmax(0,1fr)]">
        <aside className="hidden border-r bg-sidebar p-3 lg:block">
          <PrototypeNavigation view={view} mode={mode} go={go} />
          <div className="mt-8 border-t px-3 pt-4">
            <p className="text-xs font-medium text-muted-foreground">
              Object model
            </p>
            <p className="mt-2 font-mono text-xs leading-6 text-muted-foreground">
              Bridge → Port → Interface
            </p>
            <button
              className="mt-4 text-left text-sm text-muted-foreground hover:text-primary"
              onClick={() => go('responsive')}
            >
              Device responsibility
            </button>
          </div>
        </aside>
        <section
          id="page-content"
          tabIndex={-1}
          className="min-w-0 p-4 pb-20 lg:px-8 lg:py-7"
        >
          <div className="mx-auto max-w-[1480px]">
            <ScenarioBanner state={control} act={act} go={go} />
            {!labEnabled && control.error && (
              <Notice tone="danger" title="Action unavailable" urgent>
                {control.error}
              </Notice>
            )}
            <div
              className={
                ['safe-apply', 'evidence'].includes(view) ||
                (labEnabled && coreLabViews.includes(view))
                  ? ''
                  : 'hidden md:block'
              }
            >
              {content}
            </div>
            {!['safe-apply', 'evidence'].includes(view) &&
              !(labEnabled && coreLabViews.includes(view)) && (
                <div className="md:hidden">
                  {isP1View(view) &&
                  control.scenario !== 'permission-denied' ? (
                    <P1MobileSummary view={view} controller={p1} go={go} />
                  ) : (
                    <MobilePanel state={control} go={go} />
                  )}
                </div>
              )}
            <details className="mt-8 border-t pt-3">
              <summary className="cursor-pointer text-xs text-muted-foreground">
                P0 + P1 review paths · Design System v0.1
              </summary>
              <P0Stepper view={view} go={go} />
            </details>
            <footer className="mt-5 flex flex-wrap justify-between gap-2 border-t pt-3 text-xs text-muted-foreground">
              <span>
                OVS 3.4.1 · schema 8.3.1 · fixture generation{' '}
                {control.generation}
              </span>
              <span>Integrated prototype · DS v0.1</span>
            </footer>
          </div>
        </section>
      </div>
      <div
        className={
          toast
            ? 'fixed bottom-3 right-3 z-40 flex max-w-[min(28rem,calc(100vw-1.5rem))] items-start gap-2 rounded border bg-card px-4 py-3 text-sm shadow-sm'
            : 'sr-only'
        }
      >
        <output aria-live="polite" aria-atomic="true">
          {toast}
        </output>
        {toast && (
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Dismiss notification"
            onClick={() => setToast('')}
          >
            <X aria-hidden="true" />
          </Button>
        )}
      </div>
    </main>
  );
}
