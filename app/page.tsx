'use client';

import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  Activity, AlertTriangle, ArrowLeft, Boxes, Cable, CheckCircle2, ChevronRight,
  CircleDot, ClipboardCheck, Clock3, Code2, FileClock, Gauge, GitCompareArrows,
  Info, Layers3, ListFilter, LockKeyhole, Menu, Network, PanelTop,
  RotateCcw, Search, Settings, ShieldAlert, ShieldCheck, SlidersHorizontal,
  Smartphone, TerminalSquare, TriangleAlert, Undo2, WifiOff, XCircle, Zap,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select';
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table';
import { P1SwitchingView } from './p1-switching';
import {
  p1Steps,
  p1Views,
  type ChangeIntent,
  type P1View,
  type PrototypeMode,
  type ReviewScenario,
} from './prototype-model';

type View = 'dashboard' | 'ports' | 'port-detail' | 'vlan-edit' | 'workspace' | 'diff' | 'safe-apply' | 'evidence' | 'responsive' | P1View;
type Mode = PrototypeMode;
type Scenario = ReviewScenario;
type ApplyState = 'idle' | 'countdown' | 'confirmed' | 'rolled-back' | 'outcome-unknown';

type Port = {
  name: string;
  state: 'Up' | 'Down' | 'Unknown';
  speed: string;
  vlan: string;
  bridge: string;
  interfaceName: string;
  provider: string;
  authority: 'OVS' | 'External';
  scope: 'Manage' | 'Basic Manage' | 'Observe';
  uuid: string;
};

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
    registerTool(tool: WebMcpTool, options?: { signal?: AbortSignal }): void | Promise<void>;
  };
};

const ports: Port[] = [
  { name: 'uplink-01', state: 'Up', speed: '100 Gbps', vlan: 'Trunk · 10, 20, 120', bridge: 'br-fabric', interfaceName: 'enp65s0f0', provider: 'system', authority: 'OVS', scope: 'Manage', uuid: '84a9…1fc2' },
  { name: 'server-07', state: 'Up', speed: '25 Gbps', vlan: 'Access · 120', bridge: 'br-fabric', interfaceName: 'enp129s0f1', provider: 'system', authority: 'OVS', scope: 'Manage', uuid: '29fd…8a71' },
  { name: 'server-08', state: 'Down', speed: '—', vlan: 'Access · 120', bridge: 'br-fabric', interfaceName: 'enp129s0f2', provider: 'system', authority: 'OVS', scope: 'Basic Manage', uuid: '742e…53b1' },
  { name: 'bond-storage', state: 'Up', speed: '50 Gbps', vlan: 'Trunk · 300–319', bridge: 'br-storage', interfaceName: 'bond-storage', provider: 'bond', authority: 'OVS', scope: 'Basic Manage', uuid: '83cb…41c9' },
  { name: 'rep0', state: 'Unknown', speed: 'Unknown', vlan: 'Provider-owned', bridge: 'br-offload', interfaceName: 'pf0hpf', provider: 'SmartNIC', authority: 'External', scope: 'Observe', uuid: '2e13…c0d7' },
  { name: 'mgmt0', state: 'Up', speed: '1 Gbps', vlan: 'Native · 4094', bridge: 'br-mgmt', interfaceName: 'eno1', provider: 'system', authority: 'OVS', scope: 'Basic Manage', uuid: 'b12c…832a' },
];

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

const scenarioLabels: Record<Scenario, string> = {
  normal: 'Normal path',
  drift: 'Drift detected',
  'member-down': 'Bond member down',
  'lacp-mismatch': 'LACP mismatch',
  'validation-blocked': 'Validation blocked',
  'provider-degraded': 'Provider degraded',
  'advanced-config': 'Advanced config detected',
  'outcome-unknown': 'Outcome unknown',
  'network-loss': 'Network loss',
};

function ScopeBadge({ scope }: { scope: string }) {
  const classes = scope === 'Manage'
    ? 'border-sky-300 bg-sky-50 text-sky-800'
    : scope === 'Observe'
      ? 'border-slate-300 bg-slate-50 text-slate-700'
      : 'border-amber-300 bg-amber-50 text-amber-800';
  return <Badge variant="outline" className={`rounded-sm ${classes}`}>{scope}</Badge>;
}

function StateDot({ state }: { state: Port['state'] }) {
  const tone = state === 'Up' ? 'bg-emerald-500' : state === 'Down' ? 'bg-rose-500' : 'bg-slate-400';
  return <span aria-hidden="true" className={`inline-block size-2 rounded-full ${tone}`} />;
}

function PageHeader({ id, eyebrow, title, description, scope, actions }: {
  id: string; eyebrow: string; title: string; description: string; scope: string; actions?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div>
        <div className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground"><span>{id}</span><span>·</span><span>{eyebrow}</span><ScopeBadge scope={scope} /></div>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p>
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

function ScenarioBanner({ scenario, onResolve }: { scenario: Scenario; onResolve: () => void }) {
  if (scenario === 'normal') return null;
  const details: Record<Exclude<Scenario, 'normal'>, { title: string; body: string; icon: typeof AlertTriangle; tone: string }> = {
    drift: { title: 'Observed state differs from desired generation', body: 'Desired generation 1841 · observed generation 1842. Resolve or rebase before Apply.', icon: GitCompareArrows, tone: 'border-amber-300 bg-amber-50 text-amber-950' },
    'member-down': { title: 'A Bond member is operationally down', body: 'bond-storage remains available with reduced redundancy. Inspect member evidence before changing intent.', icon: Cable, tone: 'border-amber-300 bg-amber-50 text-amber-950' },
    'lacp-mismatch': { title: 'LACP negotiation does not match the partner', body: 'bond-uplink is configured active, but its partner reports a passive/static mismatch. Validation blocks a new Apply.', icon: Network, tone: 'border-rose-300 bg-rose-50 text-rose-950' },
    'validation-blocked': { title: 'Candidate is blocked by policy validation', body: 'A management-path or object-membership rule failed. Diff & Validation identifies the exact gate; Apply remains unavailable.', icon: ShieldAlert, tone: 'border-rose-300 bg-rose-50 text-rose-950' },
    'provider-degraded': { title: 'External switching provider is degraded', body: 'Provider-owned objects may be stale or unknown. Native OVS objects remain available within advertised capability.', icon: WifiOff, tone: 'border-amber-300 bg-amber-50 text-amber-950' },
    'advanced-config': { title: 'Advanced native configuration detected', body: 'Standard fields cannot represent every current option. Existing native fields will be preserved and made explicit in Expert Mode.', icon: Code2, tone: 'border-amber-300 bg-amber-50 text-amber-950' },
    'outcome-unknown': { title: 'Last configuration outcome is unknown', body: 'The controller lost authoritative completion evidence. Reconcile before starting another change.', icon: TriangleAlert, tone: 'border-rose-300 bg-rose-50 text-rose-950' },
    'network-loss': { title: 'Management connection is unavailable', body: 'The server-side Safe Apply timer continues. Local confirmation is unavailable until reconnect.', icon: WifiOff, tone: 'border-amber-300 bg-amber-50 text-amber-950' },
  };
  const item = details[scenario];
  const Icon = item.icon;
  return (
    <div role="alert" className={`mt-4 flex flex-wrap items-center gap-3 border px-4 py-3 ${item.tone}`}>
      <Icon className="size-5 shrink-0" />
      <div className="min-w-[240px] flex-1"><p className="text-sm font-semibold">{item.title}</p><p className="mt-0.5 text-xs opacity-80">{item.body}</p></div>
      {(scenario === 'drift' || scenario === 'network-loss') && <Button size="sm" variant="outline" onClick={onResolve}>{scenario === 'drift' ? 'Rebase candidate' : 'Attempt reconnect'}</Button>}
    </div>
  );
}

function Dashboard({ intent, staged, scenario, go }: { intent: ChangeIntent | null; staged: boolean; scenario: Scenario; go: (view: View) => void }) {
  const driftFree = scenario !== 'drift' && scenario !== 'outcome-unknown';
  return (
    <>
      <PageHeader id="P0-01" eyebrow="Overview" title="Fabric dashboard" description="Operational posture, pending intent, and evidence entry points—without hiding uncertainty." scope="Observe" actions={<Button onClick={() => go('ports')} className="gap-2 bg-[#157a9e] hover:bg-[#11627f]">Open ports <ChevronRight /></Button>} />
      <div className="mt-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {[
          { label: 'Ports online', value: '47 / 48', meta: '1 down · 1 unknown', icon: Cable, tone: 'text-amber-700' },
          { label: 'Bridges', value: '4', meta: '18 attached ports', icon: Boxes, tone: 'text-slate-600' },
          { label: 'Active alerts', value: scenario === 'normal' ? '2' : '3', meta: '0 critical · warnings', icon: AlertTriangle, tone: 'text-amber-700' },
          { label: 'Configuration', value: driftFree ? 'Drift-free' : 'Drift', meta: driftFree ? 'generation 1842' : '1841 desired / 1842 observed', icon: ShieldCheck, tone: driftFree ? 'text-emerald-700' : 'text-rose-700' },
        ].map(({ label, value, meta, icon: Icon, tone }) => (
          <article key={label} className="border bg-card p-4 shadow-[0_1px_0_rgb(15_23_42/4%)]"><div className="flex items-center justify-between text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground"><span>{label}</span><Icon className={`size-4 ${tone}`} /></div><p className="mt-3 text-2xl font-semibold tracking-tight">{value}</p><p className={`mt-1 text-xs ${tone}`}>{meta}</p></article>
        ))}
      </div>
      <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1.55fr)_minmax(320px,0.75fr)]">
        <section className="min-w-0 border bg-card">
          <div className="flex items-center justify-between border-b px-4 py-3"><div><h2 className="text-sm font-semibold">Port posture</h2><p className="mt-0.5 text-xs text-muted-foreground">Representative links · refreshed 12s ago</p></div><Button variant="ghost" size="sm" onClick={() => go('ports')}>View all 48</Button></div>
          <Table><TableHeader className="bg-muted/45"><TableRow><TableHead>Port</TableHead><TableHead>State</TableHead><TableHead>Speed</TableHead><TableHead>VLAN</TableHead><TableHead>Bridge</TableHead></TableRow></TableHeader><TableBody>{ports.slice(0, 4).map((port) => <TableRow key={port.name} className="cursor-pointer" onClick={() => go('port-detail')}><TableCell className="font-mono text-xs font-semibold text-[#164f7b]">{port.name}</TableCell><TableCell><span className="inline-flex items-center gap-2"><StateDot state={port.state} />{port.state}</span></TableCell><TableCell>{port.speed}</TableCell><TableCell>{port.vlan}</TableCell><TableCell className="font-mono text-xs">{port.bridge}</TableCell></TableRow>)}</TableBody></Table>
        </section>
        <div className="grid gap-4">
          <section className="border border-[#9bb7cf] bg-[#f2f8fc] p-4"><div className="flex items-start justify-between gap-3"><div><p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-[#35627f]">Candidate workspace</p><h2 className="mt-1 text-base font-semibold">{staged ? '1 staged change' : 'Workspace is clean'}</h2></div><Layers3 className="size-5 text-[#35627f]" /></div><div className="mt-4 border border-[#c8dbe9] bg-white p-3"><p className="text-sm font-medium">{intent ? `${intent.objectName} · ${intent.kind}` : 'No pending intent'}</p><p className="mt-1 font-mono text-xs text-muted-foreground">{intent?.summary ?? 'Start from a managed object.'}</p></div><Button onClick={() => go(staged ? 'workspace' : 'switching-overview')} className="mt-3 w-full bg-[#173b63] hover:bg-[#234f80]">{staged ? 'Review workspace' : 'Browse managed objects'}</Button></section>
          <section className="border bg-card p-4"><div className="flex items-center justify-between"><h2 className="text-sm font-semibold">Recent evidence</h2><Button variant="ghost" size="xs" onClick={() => go('evidence')}>Timeline</Button></div><ol className="mt-3 space-y-3">{[['12:42:08', 'Health', 'Port server-08 link down'], ['12:40:31', 'Audit', staged ? 'Workspace ws-183 updated' : 'User session authenticated'], ['12:36:10', 'Job', 'Config reconcile succeeded']].map(([time, type, text]) => <li key={time} className="grid grid-cols-[58px_52px_1fr] gap-2 border-t pt-2 text-xs first:border-t-0 first:pt-0"><span className="font-mono text-muted-foreground">{time}</span><span className="font-semibold text-[#35627f]">{type}</span><span>{text}</span></li>)}</ol></section>
        </div>
      </div>
    </>
  );
}

function PortsPage({ mode, search, setSearch, selected, setSelected, go }: { mode: Mode; search: string; setSearch: (value: string) => void; selected: string; setSelected: (value: string) => void; go: (view: View) => void }) {
  const shown = useMemo(() => ports.filter((port) => `${port.name} ${port.bridge} ${port.interfaceName}`.toLowerCase().includes(search.toLowerCase())), [search]);
  return (
    <>
      <PageHeader id="P0-02" eyebrow="Switching / Ports" title="Ports" description="Find an operational port, distinguish native identity from presentation, and enter object-scoped change workflows." scope="Manage" actions={<Button variant="outline" disabled title="Saved-view persistence is outside this prototype batch"><ListFilter /> Saved view</Button>} />
      <div className="mt-5 flex flex-wrap items-center gap-3 border bg-card p-3"><div className="relative min-w-[260px] flex-1"><Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input aria-label="Search ports" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search port, bridge, or interface" className="pl-8" /></div><NativeSelect aria-label="Filter state" defaultValue="all"><NativeSelectOption value="all">All states</NativeSelectOption><NativeSelectOption value="up">Up</NativeSelectOption><NativeSelectOption value="down">Down</NativeSelectOption><NativeSelectOption value="unknown">Unknown</NativeSelectOption></NativeSelect><Button variant="outline" disabled title="Column persistence is outside this prototype batch"><SlidersHorizontal /> Columns</Button></div>
      <div className="mt-3 border bg-card"><div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-2.5"><p className="text-xs text-muted-foreground">Showing {shown.length} representative rows · production list may be virtualized and truncated</p><Badge variant="outline" className="rounded-sm">fresh · 8s</Badge></div><Table><TableHeader className="bg-muted/45"><TableRow><TableHead>Port</TableHead><TableHead>State</TableHead><TableHead>Scope</TableHead><TableHead>VLAN</TableHead><TableHead>Bridge</TableHead>{mode === 'expert' && <><TableHead>Interface</TableHead><TableHead>Authority</TableHead><TableHead>UUID</TableHead></>}</TableRow></TableHeader><TableBody>{shown.map((port) => <TableRow key={port.name} data-state={selected === port.name ? 'selected' : undefined} className="cursor-pointer" onClick={() => { setSelected(port.name); go('port-detail'); }}><TableCell className="font-mono text-xs font-semibold text-[#164f7b]">{port.name}</TableCell><TableCell><span className="inline-flex items-center gap-2"><StateDot state={port.state} />{port.state}</span></TableCell><TableCell><ScopeBadge scope={port.scope} /></TableCell><TableCell>{port.vlan}</TableCell><TableCell className="font-mono text-xs">{port.bridge}</TableCell>{mode === 'expert' && <><TableCell className="font-mono text-xs">{port.interfaceName}</TableCell><TableCell>{port.authority}</TableCell><TableCell className="font-mono text-xs">{port.uuid}</TableCell></>}</TableRow>)}</TableBody></Table></div>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground"><span>Unknown is not treated as Down. Provider-owned rows remain inspectable without a false Edit action.</span><span className="font-mono">dataset · 2,384 objects</span></div>
    </>
  );
}

function PortDetail({ port, mode, go }: { port: Port; mode: Mode; go: (view: View) => void }) {
  const editable = port.scope !== 'Observe';
  return (
    <>
      <PageHeader id="P0-03" eyebrow="Switching / Port detail" title={port.name} description="Operational state, native object relationship, ownership, and safe entry points for configuration." scope={port.scope} actions={<><Button variant="outline" onClick={() => go('ports')}><ArrowLeft /> Ports</Button>{editable && <Button onClick={() => go('vlan-edit')} className="bg-[#157a9e] hover:bg-[#11627f]">Edit VLAN</Button>}</>} />
      {!editable && <div className="mt-4 flex items-start gap-3 border border-slate-300 bg-slate-50 p-4"><Info className="mt-0.5 size-5 text-slate-600" /><div><p className="text-sm font-semibold">Observe-only object</p><p className="mt-1 text-xs text-muted-foreground">Authority: {port.provider}. OVS WebUI will not show an Edit action until the provider advertises a native capability.</p></div></div>}
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(320px,0.6fr)]">
        <div className="space-y-4">
          <section className="border bg-card"><div className="border-b px-4 py-3"><h2 className="text-sm font-semibold">Operational summary</h2></div><dl className="grid sm:grid-cols-2 xl:grid-cols-4">{[['Link state', port.state], ['Speed', port.speed], ['VLAN', port.vlan], ['Bridge', port.bridge]].map(([label, value]) => <div key={label} className="border-b p-4 sm:border-r xl:border-b-0"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="mt-1 font-mono text-sm font-semibold">{value}</dd></div>)}</dl></section>
          <section className="border bg-card p-4"><div className="flex items-center justify-between"><h2 className="text-sm font-semibold">OVS object relationship</h2><Badge variant="outline" className="rounded-sm">invariant</Badge></div><div className="mt-4 grid items-center gap-2 sm:grid-cols-[1fr_auto_1fr_auto_1fr]"><div className="border bg-muted/35 p-3"><p className="font-mono text-[10px] text-muted-foreground">Bridge</p><p className="mt-1 font-mono text-sm font-semibold">{port.bridge}</p></div><ChevronRight className="mx-auto size-4 text-muted-foreground" /><div className="border border-[#8bb7d1] bg-[#f2f8fc] p-3"><p className="font-mono text-[10px] text-muted-foreground">Port</p><p className="mt-1 font-mono text-sm font-semibold">{port.name}</p></div><ChevronRight className="mx-auto size-4 text-muted-foreground" /><div className="border bg-muted/35 p-3"><p className="font-mono text-[10px] text-muted-foreground">Interface</p><p className="mt-1 font-mono text-sm font-semibold">{port.interfaceName}</p></div></div></section>
          <section className="border bg-card p-4"><h2 className="text-sm font-semibold">Configuration surfaces</h2><div className="mt-3 grid gap-3 sm:grid-cols-3">{[['VLAN', editable ? 'Basic Manage' : 'Observe', port.vlan], ['Bond / LACP', 'Observe', 'Not a bond member'], ['STP / RSTP', 'Observe', 'Forwarding']].map(([name, scope, value]) => <button key={name} type="button" disabled={name !== 'VLAN' || !editable} onClick={() => go('vlan-edit')} className="border bg-white p-3 text-left disabled:cursor-default disabled:opacity-70"><div className="flex items-center justify-between"><span className="text-sm font-semibold">{name}</span><ScopeBadge scope={scope} /></div><p className="mt-3 text-xs text-muted-foreground">{value}</p></button>)}</div></section>
        </div>
        <aside className="space-y-4">
          <section className="border bg-card p-4"><h2 className="text-sm font-semibold">Identity & authority</h2><dl className="mt-3 space-y-3 text-xs">{[['Authority', port.authority], ['Provider', port.provider], ['Generation', '1842'], ['Freshness', '8 seconds'], ['Permission', editable ? 'change.stage: allowed' : 'change.stage: unavailable']].map(([label, value]) => <div key={label} className="flex items-start justify-between gap-4 border-t pt-2 first:border-t-0 first:pt-0"><dt className="text-muted-foreground">{label}</dt><dd className="font-mono text-right">{value}</dd></div>)}</dl></section>
          {mode === 'expert' && <section className="border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4"><div className="flex items-center gap-2"><Code2 className="size-4 text-[#35627f]" /><h2 className="text-sm font-semibold">Expert native fields</h2></div><pre className="mt-3 overflow-x-auto border bg-white p-3 font-mono text-[11px] leading-5 text-slate-700">{`_uuid: ${port.uuid}\nname: ${port.name}\ntag: 120\ntrunks: []\nvlan_mode: access`}</pre></section>}
        </aside>
      </div>
    </>
  );
}

function VlanEdit({ port, mode, vlanMode, setVlanMode, allowedVlans, setAllowedVlans, onStage, go }: { port: Port; mode: Mode; vlanMode: string; setVlanMode: (value: string) => void; allowedVlans: string; setAllowedVlans: (value: string) => void; onStage: () => void; go: (view: View) => void }) {
  const managementRisk = port.name === 'mgmt0';
  return (
    <>
      <PageHeader id="P0-04" eyebrow="Port / VLAN edit" title={`Edit VLAN · ${port.name}`} description="A bounded editor that creates candidate intent; it never writes live configuration from this page." scope="Basic Manage" actions={<Button variant="outline" onClick={() => go('port-detail')}><ArrowLeft /> Cancel</Button>} />
      <div className={`mt-4 flex items-start gap-3 border p-4 ${managementRisk ? 'border-rose-300 bg-rose-50' : 'border-amber-300 bg-amber-50'}`}><ShieldAlert className={`mt-0.5 size-5 ${managementRisk ? 'text-rose-700' : 'text-amber-700'}`} /><div><p className="text-sm font-semibold">{managementRisk ? 'Management path change' : 'Medium-risk connectivity change'}</p><p className="mt-1 text-xs text-muted-foreground">The change will be staged, validated, diffed, and applied through Safe Apply. Current traffic is unaffected until Apply.</p></div></div>
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1fr)_400px]">
        <form className="border bg-card p-5" onSubmit={(event) => { event.preventDefault(); onStage(); }}><div className="flex items-center justify-between border-b pb-3"><h2 className="text-sm font-semibold">VLAN intent</h2><Badge variant="outline" className="rounded-sm">dirty · local only</Badge></div><div className="mt-5 grid gap-5 sm:grid-cols-2"><label htmlFor="vlan-mode" className="grid gap-1.5 text-xs font-medium">VLAN mode<NativeSelect id="vlan-mode" className="w-full" value={vlanMode} onChange={(event) => setVlanMode(event.target.value)}><NativeSelectOption value="access">Access</NativeSelectOption><NativeSelectOption value="trunk">Trunk</NativeSelectOption><NativeSelectOption value="native-tagged">Native tagged</NativeSelectOption></NativeSelect></label><label htmlFor="native-vlan" className="grid gap-1.5 text-xs font-medium">Native / access VLAN<Input id="native-vlan" value="120" readOnly /></label><label htmlFor="allowed-vlans" className="grid gap-1.5 text-xs font-medium sm:col-span-2">Allowed VLANs<Input id="allowed-vlans" value={allowedVlans} onChange={(event) => setAllowedVlans(event.target.value)} placeholder="120, 240" /><span className="font-normal text-muted-foreground">Comma-separated IDs or ranges. Reserved VLAN 4094 requires elevated review.</span></label></div>{mode === 'expert' && <div className="mt-5 border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4"><p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-[#35627f]">Native mapping preview</p><pre className="mt-2 font-mono text-xs leading-5">{`Port.vlan_mode = "${vlanMode}"\nPort.tag = 120\nPort.trunks = [${allowedVlans}]`}</pre></div>}<div className="mt-6 flex flex-wrap items-center justify-between gap-3 border-t pt-4"><div className="flex items-center gap-2 text-xs text-muted-foreground"><LockKeyhole className="size-4" /> Network.Operator · change.stage</div><div className="flex gap-2"><Button type="button" variant="outline" onClick={() => go('port-detail')}>Discard</Button><Button type="submit" className="bg-[#173b63] hover:bg-[#234f80]">Add to workspace <ChevronRight /></Button></div></div></form>
        <aside className="space-y-4"><section className="border bg-card p-4"><h2 className="text-sm font-semibold">Intent preview</h2><div className="mt-4 grid grid-cols-[1fr_auto_1fr] items-center gap-3"><div className="border bg-muted/40 p-3"><p className="text-xs text-muted-foreground">Current</p><p className="mt-1 font-mono text-sm">access · 120</p></div><ChevronRight className="size-4 text-muted-foreground" /><div className="border border-[#8bb7d1] bg-[#f2f8fc] p-3"><p className="text-xs text-muted-foreground">Candidate</p><p className="mt-1 font-mono text-sm">{vlanMode} · {allowedVlans}</p></div></div></section><section className="border bg-card p-4"><h2 className="text-sm font-semibold">Risk & policy</h2><ul className="mt-3 space-y-2 text-xs"><li className="flex items-center gap-2"><CheckCircle2 className="size-4 text-emerald-600" /> Object authority is OVS</li><li className="flex items-center gap-2"><CheckCircle2 className="size-4 text-emerald-600" /> Capability vlan.basic_manage available</li><li className="flex items-center gap-2"><Clock3 className="size-4 text-amber-600" /> Safe Apply confirmation required</li></ul></section></aside>
      </div>
    </>
  );
}

function Workspace({ intent, staged, scenario, onRemove, go }: { intent: ChangeIntent | null; staged: boolean; scenario: Scenario; onRemove: () => void; go: (view: View) => void }) {
  const origin = intent?.kind === 'bond' ? 'bonds' : intent?.kind === 'bridge' ? 'bridges' : 'ports';
  return (
    <>
      <PageHeader id="GLOBAL-01" eyebrow="Global / Candidate workspace" title="Candidate workspace" description="A shared place to review pending intent, authorship, conflicts, and readiness before validation." scope="Manage" actions={<Button variant="outline" onClick={() => go(origin)}>Add another change</Button>} />
      {!intent || !staged ? <section className="mt-8 border border-dashed bg-card px-6 py-16 text-center"><Layers3 className="mx-auto size-8 text-muted-foreground" /><h2 className="mt-3 text-base font-semibold">Workspace is clean</h2><p className="mt-1 text-sm text-muted-foreground">Start from a managed object. There is no live-save shortcut.</p><Button className="mt-5" onClick={() => go('switching-overview')}>Browse switching objects</Button></section> : <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
        <section className="border bg-card">
          <div className="flex items-center justify-between border-b px-4 py-3"><div><h2 className="text-sm font-semibold">Workspace ws-183</h2><p className="mt-0.5 font-mono text-[11px] text-muted-foreground">base generation 1841 · author operator.review</p></div><Badge variant="outline" className={`rounded-sm ${scenario === 'drift' ? 'border-amber-300 bg-amber-50 text-amber-800' : 'border-sky-300 bg-sky-50 text-sky-800'}`}>{scenario === 'drift' ? 'conflict' : 'dirty'}</Badge></div>
          <div className="p-4"><article className="border"><div className="flex flex-wrap items-start justify-between gap-3 border-b bg-muted/35 px-4 py-3"><div><p className="font-mono text-xs font-semibold text-[#164f7b]">{intent.objectType} / {intent.objectName}</p><p className="mt-1 text-sm">{intent.summary}</p></div><div className="flex gap-2"><ScopeBadge scope={intent.kind === 'vlan' ? 'Basic Manage' : 'Manage'} /><Button variant="ghost" size="xs" onClick={onRemove}>Remove</Button></div></div><dl className="grid gap-3 p-4 text-xs sm:grid-cols-4"><div><dt className="text-muted-foreground">Change type</dt><dd className="mt-1 font-semibold capitalize">{intent.kind}</dd></div><div><dt className="text-muted-foreground">Risk</dt><dd className="mt-1 font-semibold">{intent.risk}</dd></div><div><dt className="text-muted-foreground">Apply strategy</dt><dd className="mt-1 font-semibold">{intent.risk === 'Low' ? 'Apply' : 'Safe Apply'}</dd></div><div><dt className="text-muted-foreground">Capability</dt><dd className="mt-1 font-mono font-semibold">{intent.capability}</dd></div></dl></article></div>
        </section>
        <aside className="border bg-card p-4"><h2 className="text-sm font-semibold">Readiness</h2><ul className="mt-3 space-y-3 text-xs"><li className="flex items-start gap-2"><CheckCircle2 className="mt-0.5 size-4 text-emerald-600" /> Intent has complete native object identity</li><li className="flex items-start gap-2"><CheckCircle2 className="mt-0.5 size-4 text-emerald-600" /> Permission snapshot valid</li><li className="flex items-start gap-2">{scenario === 'drift' ? <XCircle className="mt-0.5 size-4 text-rose-600" /> : <CheckCircle2 className="mt-0.5 size-4 text-emerald-600" />} Base generation {scenario === 'drift' ? 'changed' : 'matches observed'}</li><li className="flex items-start gap-2">{scenario === 'advanced-config' ? <Info className="mt-0.5 size-4 text-amber-600" /> : <CheckCircle2 className="mt-0.5 size-4 text-emerald-600" />} {scenario === 'advanced-config' ? 'Advanced native fields explicitly preserved' : 'No hidden native field loss'}</li></ul><Button className="mt-5 w-full bg-[#173b63] hover:bg-[#234f80]" onClick={() => go('diff')}>Review diff & validation <ChevronRight /></Button></aside>
      </div>}
    </>
  );
}

function DiffPage({ intent, scenario, note, setNote, onApply, go, onResolve }: { intent: ChangeIntent | null; scenario: Scenario; note: string; setNote: (value: string) => void; onApply: () => void; go: (view: View) => void; onResolve: () => void }) {
  const blocked = !intent || scenario === 'validation-blocked' || scenario === 'drift' || scenario === 'lacp-mismatch' || scenario === 'outcome-unknown';
  const checks = [
    ['Schema', 'Pass', 'Candidate matches OVS schema 8.3.1'],
    ['Capability', 'Pass', `${intent?.capability ?? 'No candidate'} available`],
    ['Management path', scenario === 'validation-blocked' ? 'Blocked' : 'Pass', scenario === 'validation-blocked' ? (intent?.kind === 'bond' ? 'Selected Interface is already assigned to another Port' : 'VLAN 4094 is reserved') : 'Out-of-band path remains reachable'],
    ['Generation', scenario === 'drift' ? 'Blocked' : 'Pass', scenario === 'drift' ? 'Observed generation advanced to 1842' : 'Base generation is current'],
    ...(intent?.kind === 'bond' ? [['LACP partner', scenario === 'lacp-mismatch' ? 'Blocked' : 'Pass', scenario === 'lacp-mismatch' ? 'Partner aggregation is incompatible' : 'Negotiation contract is compatible']] : []),
  ];
  return (
    <>
      <PageHeader id="GLOBAL-02" eyebrow="Workspace / Diff & validation" title="Review candidate" description="Make the exact native mutation, validation evidence, risk, and confirmation strategy explicit before Apply." scope="Manage" actions={<Button variant="outline" onClick={() => go('workspace')}><ArrowLeft /> Workspace</Button>} />
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.65fr)]"><div className="space-y-4"><section className="border bg-card"><div className="flex items-center justify-between border-b px-4 py-3"><div><h2 className="text-sm font-semibold">Semantic diff</h2><p className="mt-0.5 font-mono text-[11px] text-muted-foreground">{intent ? `${intent.evidenceObject} · mutation 1 of 1` : 'workspace is empty'}</p></div><Badge variant="outline" className={`rounded-sm ${intent?.risk === 'High' ? 'border-rose-300 bg-rose-50 text-rose-800' : 'border-amber-300 bg-amber-50 text-amber-800'}`}>{intent?.risk?.toLowerCase() ?? 'unknown'} risk</Badge></div><div className="grid md:grid-cols-2"><div className="border-b p-4 md:border-b-0 md:border-r"><p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">Current · generation 1841</p><pre className="mt-3 min-h-32 overflow-x-auto whitespace-pre-wrap border bg-slate-50 p-4 font-mono text-xs leading-6">{intent?.current ?? 'No current object selected'}</pre></div><div className="p-4"><p className="font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-[#35627f]">Candidate · workspace ws-183</p><pre className="mt-3 min-h-32 overflow-x-auto whitespace-pre-wrap border border-[#9bb7cf] bg-[#f2f8fc] p-4 font-mono text-xs leading-6">{intent?.candidate ?? 'No candidate intent'}</pre></div></div></section><section className="border bg-card"><div className="border-b px-4 py-3"><h2 className="text-sm font-semibold">Validation results</h2></div><Table><TableHeader className="bg-muted/45"><TableRow><TableHead>Gate</TableHead><TableHead>Result</TableHead><TableHead>Evidence</TableHead></TableRow></TableHeader><TableBody>{checks.map(([gate, result, evidence]) => <TableRow key={gate}><TableCell className="font-semibold">{gate}</TableCell><TableCell><span className={`inline-flex items-center gap-2 font-semibold ${result === 'Pass' ? 'text-emerald-700' : 'text-rose-700'}`}>{result === 'Pass' ? <CheckCircle2 className="size-4" /> : <XCircle className="size-4" />}{result}</span></TableCell><TableCell className="text-muted-foreground">{evidence}</TableCell></TableRow>)}</TableBody></Table></section></div><aside className="space-y-4"><section className="border bg-card p-4"><h2 className="text-sm font-semibold">Apply contract</h2><dl className="mt-3 space-y-3 text-xs">{[['Strategy', intent?.risk === 'Low' ? 'Apply' : 'Safe Apply'], ['Confirmation window', intent?.risk === 'Low' ? 'Not required' : '90 seconds'], ['Automatic fallback', intent?.risk === 'Low' ? 'Transaction abort' : 'Rollback'], ['Audit correlation', 'corr-7F3A-91D2']].map(([label, value]) => <div key={label} className="flex items-start justify-between gap-4 border-t pt-2 first:border-t-0 first:pt-0"><dt className="text-muted-foreground">{label}</dt><dd className="font-mono text-right">{value}</dd></div>)}</dl></section><section className="border bg-card p-4"><label htmlFor="change-reason" className="grid gap-2 text-xs font-medium">Change reason<Input id="change-reason" value={note} onChange={(event) => setNote(event.target.value)} placeholder="Required for Audit" /></label><p className="mt-2 text-[11px] text-muted-foreground">Recorded with actor, diff, policy result, and correlation ID.</p></section>{blocked && <div className="border border-rose-300 bg-rose-50 p-4"><div className="flex gap-2"><ShieldAlert className="size-4 text-rose-700" /><p className="text-sm font-semibold text-rose-900">Apply is blocked</p></div><p className="mt-2 text-xs text-rose-800">Resolve every blocking gate; Expert Mode cannot bypass policy.</p>{scenario === 'drift' && <Button className="mt-3" variant="outline" size="sm" onClick={onResolve}>Rebase candidate</Button>}</div>}<Button disabled={blocked || !note.trim()} onClick={onApply} className="hidden w-full bg-[#b45721] hover:bg-[#91451b] lg:inline-flex"><Zap /> Apply with Safe Apply</Button><div className="border border-dashed p-3 text-xs text-muted-foreground lg:hidden">Tablet can review validation, but this Apply action requires desktop.</div></aside></div>
    </>
  );
}

function SafeApply({ intent, scenario, applyState, seconds, onConfirm, onRollback, onReconnect, go }: { intent: ChangeIntent | null; scenario: Scenario; applyState: ApplyState; seconds: number; onConfirm: () => void; onRollback: () => void; onReconnect: () => void; go: (view: View) => void }) {
  const unknown = applyState === 'outcome-unknown' || scenario === 'outcome-unknown';
  const disconnected = scenario === 'network-loss';
  const progress = Math.max(0, Math.min(100, (seconds / 90) * 100));
  return (
    <>
      <PageHeader id="GLOBAL-03" eyebrow="Apply / Safe Apply" title={unknown ? 'Outcome requires reconciliation' : 'Confirm connectivity'} description={`The ${intent?.evidenceObject ?? 'candidate'} change is active provisionally. Confirm success before the server-side timer expires, or rollback explicitly.`} scope="Manage" actions={<Button variant="outline" onClick={() => go('evidence')}><FileClock /> Open evidence</Button>} />
      <div className={`mt-5 border-2 p-5 ${unknown ? 'border-rose-400 bg-rose-50' : disconnected ? 'border-amber-400 bg-amber-50' : 'border-[#2d7899] bg-[#eff8fb]'}`}><div className="flex flex-wrap items-start justify-between gap-4"><div className="flex items-start gap-3">{unknown ? <TriangleAlert className="mt-0.5 size-7 text-rose-700" /> : disconnected ? <WifiOff className="mt-0.5 size-7 text-amber-700" /> : <Clock3 className="mt-0.5 size-7 text-[#1e6786]" />}<div><p className="font-mono text-[10px] font-semibold uppercase tracking-[0.14em] text-muted-foreground">safe-apply / job-2047</p><h2 className="mt-1 text-xl font-semibold">{unknown ? 'Outcome unknown' : disconnected ? 'Connection lost · timer continues' : 'Candidate is live provisionally'}</h2><p className="mt-1 text-sm text-muted-foreground">{unknown ? 'Do not assume success or failure. Use evidence and reconcile observed state.' : 'Verify that management reachability and expected traffic remain healthy.'}</p></div></div>{!unknown && <div className="min-w-[128px] border bg-white p-3 text-center"><p className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">Time remaining</p><p className="mt-1 font-mono text-3xl font-semibold tabular-nums">{String(Math.floor(seconds / 60)).padStart(2, '0')}:{String(seconds % 60).padStart(2, '0')}</p></div>}</div>{!unknown && <div className="mt-5 h-2 overflow-hidden bg-white"><div className={`h-full transition-[width] duration-1000 ${seconds < 30 ? 'bg-rose-500' : 'bg-[#2d7899]'}`} style={{ width: `${progress}%` }} /></div>}</div>
      <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]"><section className="border bg-card"><div className="border-b px-4 py-3"><h2 className="text-sm font-semibold">Verification checklist</h2></div><div className="grid gap-3 p-4 sm:grid-cols-2">{[['Controller session', disconnected ? 'Unavailable' : unknown ? 'Unknown' : 'Healthy'], ['Management route', disconnected ? 'Unknown' : 'Reachable'], [intent ? intent.evidenceObject : 'Target object', unknown ? 'Unknown' : 'Healthy'], ['Observed generation', unknown ? 'Unconfirmed' : '1842']].map(([label, value]) => { const good = value === 'Healthy' || value === 'Reachable' || value === 'Up' || value === '1842'; return <div key={label} className="flex items-center justify-between gap-3 border p-3"><span className="text-xs text-muted-foreground">{label}</span><span className={`inline-flex items-center gap-2 text-xs font-semibold ${good ? 'text-emerald-700' : 'text-amber-700'}`}>{good ? <CheckCircle2 className="size-4" /> : <TriangleAlert className="size-4" />}{value}</span></div>; })}</div></section><aside className="border bg-card p-4"><h2 className="text-sm font-semibold">Operator decision</h2><p className="mt-2 text-xs text-muted-foreground">Confirmation records actor, method, and correlation ID. Rollback is always available while the timer is authoritative.</p>{unknown ? <Button className="mt-5 w-full" variant="destructive" onClick={() => go('evidence')}>Reconcile from evidence</Button> : <div className="mt-5 grid gap-2"><Button disabled={disconnected} onClick={onConfirm} className="bg-emerald-700 hover:bg-emerald-800"><CheckCircle2 /> Confirm success</Button><Button onClick={onRollback} variant="destructive"><Undo2 /> Roll back now</Button>{disconnected && <Button onClick={onReconnect} variant="outline"><RotateCcw /> Attempt reconnect</Button>}</div>}<div className="mt-4 border-t pt-3 text-[11px] text-muted-foreground"><p className="font-mono">corr-7F3A-91D2</p><p className="mt-1">Reauthentication may be required on mobile confirmation.</p></div></aside></div>
    </>
  );
}

function Evidence({ applyState, intent, go }: { applyState: ApplyState; intent: ChangeIntent | null; go: (view: View) => void }) {
  const [exported, setExported] = useState(false);
  const outcome = applyState === 'confirmed' ? 'Confirmed' : applyState === 'rolled-back' ? 'Rolled back' : applyState === 'outcome-unknown' ? 'Outcome unknown' : 'No completed apply';
  const tone = applyState === 'confirmed' ? 'border-emerald-300 bg-emerald-50 text-emerald-900' : applyState === 'rolled-back' ? 'border-amber-300 bg-amber-50 text-amber-900' : 'border-slate-300 bg-slate-50 text-slate-800';
  const timeline = applyState === 'rolled-back'
    ? [['12:49:02', 'Audit', 'Rollback requested by operator.review'], ['12:49:03', 'Job', 'Rollback completed · generation 1843'], ['12:49:04', 'Health', 'Management reachability restored']]
    : applyState === 'outcome-unknown'
      ? [['12:49:02', 'Event', 'Controller connection lost'], ['12:49:05', 'Job', 'Completion acknowledgement missing'], ['12:49:08', 'Audit', 'Outcome marked unknown · reconciliation required']]
      : [['12:48:12', 'Job', 'Candidate activated provisionally'], ['12:48:36', 'Health', 'Management reachability verified'], ['12:48:43', 'Audit', 'Safe Apply confirmed by operator.review']];
  return (
    <>
      <PageHeader id="GLOBAL-04" eyebrow="Evidence / Correlated timeline" title="Change evidence" description="Health, Event, Job, and Audit remain distinct evidence types, joined by object, time, and correlation ID." scope="Observe" actions={<Button onClick={() => go('dashboard')}>Return to dashboard</Button>} />
      <div className={`mt-5 flex flex-wrap items-center gap-3 border p-4 ${tone}`}><ClipboardCheck className="size-6" /><div className="flex-1"><p className="text-sm font-semibold">{outcome}</p><p className="mt-0.5 font-mono text-xs opacity-80">corr-7F3A-91D2 · workspace ws-183 · job-2047 · {intent?.evidenceObject ?? 'object unavailable'}</p></div><ScopeBadge scope="Observe" /></div>
      <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(330px,0.65fr)]"><section className="border bg-card"><div className="flex items-center justify-between border-b px-4 py-3"><div><h2 className="text-sm font-semibold">Correlated timeline</h2>{exported && <p className="mt-1 text-xs text-emerald-700">Evidence package prepared · corr-7F3A-91D2.json</p>}</div><Button variant="outline" size="sm" onClick={() => setExported(true)}>Export evidence</Button></div><ol>{timeline.map(([time, type, text], index) => <li key={time} className="grid grid-cols-[72px_90px_1fr] gap-3 border-b px-4 py-4 text-sm last:border-b-0"><span className="font-mono text-xs text-muted-foreground">{time}</span><span className="font-semibold text-[#35627f]">{type}</span><div><p>{text}</p><p className="mt-1 font-mono text-[10px] text-muted-foreground">seq {index + 1} · object {intent?.evidenceObject ?? 'unknown'}</p></div></li>)}</ol></section><aside className="space-y-4"><section className="border bg-card p-4"><h2 className="text-sm font-semibold">Evidence boundaries</h2><dl className="mt-3 space-y-3 text-xs">{[['Health', 'Current posture, not intent'], ['Event', 'A fact observed at a point in time'], ['Job', 'Execution progress and result'], ['Audit', 'Actor, decision, policy, and change record']].map(([label, value]) => <div key={label} className="border-t pt-2 first:border-t-0 first:pt-0"><dt className="font-semibold text-[#35627f]">{label}</dt><dd className="mt-1 text-muted-foreground">{value}</dd></div>)}</dl></section><section className="border bg-card p-4"><h2 className="text-sm font-semibold">Deep links</h2><div className="mt-3 grid gap-2"><Button variant="outline" onClick={() => go(intent?.kind === 'bond' ? 'bond-detail' : intent?.kind === 'bridge' ? 'bridge-detail' : 'port-detail')}>Open {intent?.evidenceObject ?? 'source object'}</Button><Button variant="outline" onClick={() => go('workspace')}>Open workspace ws-183</Button><Button variant="outline" onClick={() => go('safe-apply')}>Open Apply job</Button></div></section></aside></div>
    </>
  );
}

function ResponsivePage({ applyState, go }: { applyState: ApplyState; go: (view: View) => void }) {
  return (
    <>
      <PageHeader id="P0-09" eyebrow="Responsive responsibility" title="Device responsibility boundary" description="Responsive design changes the job, not merely the width: desktop manages, tablet reviews and performs bounded tasks, mobile supports incidents." scope="Basic Manage" actions={<Button variant="outline" onClick={() => go('dashboard')}><PanelTop /> Desktop view</Button>} />
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1fr)_380px]"><section className="border bg-card"><Table><TableHeader className="bg-muted/45"><TableRow><TableHead>Responsibility</TableHead><TableHead>Desktop</TableHead><TableHead>Tablet</TableHead><TableHead>Mobile</TableHead></TableRow></TableHeader><TableBody>{[
        ['Observe health & events', 'Full', 'Full', 'Incident summary'], ['Edit VLAN / port intent', 'Full', 'Bounded Standard', 'No'], ['Diff & validation review', 'Full', 'Review', 'Summary only'], ['Start Safe Apply', 'Yes', 'No', 'No'], ['Confirm / rollback active Apply', 'Yes', 'When trusted', 'Emergency + reauth'], ['Expert/native fields', 'Full', 'Read-only', 'No'],
      ].map((row) => <TableRow key={row[0]}>{row.map((value, index) => <TableCell key={value} className={index === 0 ? 'font-semibold' : ''}>{value}</TableCell>)}</TableRow>)}</TableBody></Table><div className="border-t p-4 text-xs text-muted-foreground">P0 review rule: do not compress a desktop configuration form into a phone. Preserve incident evidence, countdown, and safe decisions only.</div></section><MobilePanel applyState={applyState} onConfirm={() => go('evidence')} onRollback={() => go('evidence')} onOpenEvidence={() => go('evidence')} framed /></div>
    </>
  );
}

function MobilePanel({ applyState, onConfirm, onRollback, onOpenEvidence, framed = false }: { applyState: ApplyState; onConfirm: () => void; onRollback: () => void; onOpenEvidence: () => void; framed?: boolean }) {
  const active = applyState === 'countdown' || applyState === 'outcome-unknown';
  return (
    <section className={`${framed ? 'border-8 border-slate-800 shadow-xl' : ''} mx-auto w-full max-w-[380px] self-start overflow-hidden bg-[#f5f7fa]`} aria-label="Mobile incident companion">
      <div className="flex items-center justify-between bg-[#122f4e] px-4 py-3 text-white"><div className="flex items-center gap-2"><div className="grid size-7 place-items-center border border-white text-[9px] font-black">OVS</div><div><p className="text-xs font-semibold">sw-edge-01</p><p className="font-mono text-[9px] text-sky-100">incident companion</p></div></div><Badge variant="outline" className="border-emerald-300 bg-emerald-50 text-emerald-800">Online</Badge></div>
      <div className="p-4"><div className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground"><Smartphone className="size-3.5" /> Mobile responsibility</div><h2 className="mt-2 text-lg font-semibold">{active ? 'Safe Apply needs attention' : 'Fabric posture'}</h2>{active ? <div className="mt-4 border border-amber-300 bg-amber-50 p-4"><p className="text-xs font-semibold text-amber-900">job-2047 · 00:42 remaining</p><p className="mt-2 text-xs text-amber-800">Confirm only after reauthentication and management reachability verification.</p><div className="mt-4 grid gap-2"><Button onClick={onConfirm} className="bg-emerald-700 hover:bg-emerald-800"><CheckCircle2 /> Confirm</Button><Button onClick={onRollback} variant="destructive"><Undo2 /> Roll back</Button></div></div> : <><div className="mt-4 grid grid-cols-2 gap-3"><div className="border bg-white p-3"><p className="text-[10px] text-muted-foreground">Ports online</p><p className="mt-1 text-xl font-semibold">47 / 48</p></div><div className="border bg-white p-3"><p className="text-[10px] text-muted-foreground">Alerts</p><p className="mt-1 text-xl font-semibold text-amber-700">2</p></div></div><div className="mt-3 border bg-white p-3"><p className="text-xs font-semibold">Latest incident</p><p className="mt-2 text-xs text-muted-foreground">server-08 link down · 34s ago</p><Button className="mt-3 w-full" variant="outline" onClick={onOpenEvidence}>Open evidence</Button></div></>}</div>
    </section>
  );
}

function P0Stepper({ view, go }: { view: View; go: (view: View) => void }) {
  return <nav aria-label="P0 prototype steps" className="mt-5 hidden overflow-x-auto border-t pt-3 xl:block"><ol className="flex min-w-max items-center gap-1">{steps.map((step, index) => <li key={step.id} className="flex items-center"><button type="button" onClick={() => go(step.view)} aria-current={view === step.view ? 'step' : undefined} className={`px-2 py-1 text-left ${view === step.view ? 'bg-[#dce9f5] text-[#12385e]' : 'text-muted-foreground hover:bg-muted'}`}><span className="block font-mono text-[9px]">{step.id}</span><span className="block text-[11px] font-medium">{step.label}</span></button>{index < steps.length - 1 && <ChevronRight className="size-3 text-slate-300" />}</li>)}</ol></nav>;
}

function P1Stepper({ view, go }: { view: P1View; go: (view: P1View) => void }) {
  return <nav aria-label="P1 Batch 01 prototype steps" className="mt-5 hidden overflow-x-auto border-t pt-3 xl:block"><ol className="flex min-w-max items-center gap-1">{p1Steps.map((step, index) => <li key={step.id} className="flex items-center"><button type="button" onClick={() => go(step.view)} aria-current={view === step.view ? 'step' : undefined} className={`px-3 py-1 text-left ${view === step.view ? 'bg-[#dce9f5] text-[#12385e]' : 'text-muted-foreground hover:bg-muted'}`}><span className="block font-mono text-[9px]">{step.id}</span><span className="block text-[11px] font-medium">{step.label}</span></button>{index < p1Steps.length - 1 && <ChevronRight className="size-3 text-slate-300" />}</li>)}</ol></nav>;
}

export default function Home() {
  const [view, setView] = useState<View>('switching-overview');
  const [mode, setMode] = useState<Mode>('standard');
  const [scenario, setScenario] = useState<Scenario>('normal');
  const [staged, setStaged] = useState(false);
  const [intent, setIntent] = useState<ChangeIntent | null>(null);
  const [selectedPort, setSelectedPort] = useState('server-07');
  const [selectedBridge, setSelectedBridge] = useState('br-fabric');
  const [selectedBond, setSelectedBond] = useState('bond-uplink');
  const [search, setSearch] = useState('');
  const [bridgeSearch, setBridgeSearch] = useState('');
  const [vlanMode, setVlanMode] = useState('trunk');
  const [allowedVlans, setAllowedVlans] = useState('120, 240');
  const [note, setNote] = useState('P0 low-fidelity review');
  const [applyState, setApplyState] = useState<ApplyState>('idle');
  const [seconds, setSeconds] = useState(90);
  const [navOpen, setNavOpen] = useState(false);
  const [toast, setToast] = useState('P1 Batch 01 · Bridge + Bond/LACP · Draft for review');
  const stateRef = useRef({ view, mode, scenario, staged, applyState, intent });

  const port = ports.find((item) => item.name === selectedPort) ?? ports[1];

  useEffect(() => { stateRef.current = { view, mode, scenario, staged, applyState, intent }; }, [view, mode, scenario, staged, applyState, intent]);

  useEffect(() => {
    if (applyState !== 'countdown') return;
    const timer = window.setInterval(() => setSeconds((current) => {
      if (current <= 1) {
        window.clearInterval(timer);
        setApplyState('rolled-back');
        setStaged(false);
        setView('evidence');
        setToast('Safe Apply expired · automatic rollback completed');
        return 0;
      }
      return current - 1;
    }), 1000);
    return () => window.clearInterval(timer);
  }, [applyState]);

  const go = (next: View) => {
    const step = [...steps, ...p1Steps].find((item) => item.view === next);
    setView(next);
    setNavOpen(false);
    setToast(`${step?.id ?? 'GLOBAL'} · ${step?.label ?? next}`);
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };
  const stageChange = () => {
    setIntent({
      kind: 'vlan',
      objectType: 'Port',
      objectName: selectedPort,
      title: `Update Port / ${selectedPort}`,
      summary: `VLAN access 120 → ${vlanMode} ${allowedVlans}`,
      current: 'vlan_mode: access\ntag: 120\ntrunks: []',
      candidate: `vlan_mode: ${vlanMode}\ntag: 120\ntrunks: [${allowedVlans}]`,
      risk: selectedPort === 'mgmt0' ? 'High' : 'Medium',
      capability: 'vlan.basic_manage',
      evidenceObject: `Port/${selectedPort}`,
    });
    setStaged(true);
    setToast(`Staged VLAN intent for ${selectedPort}`);
    go('workspace');
  };
  const stageIntent = (nextIntent: ChangeIntent) => {
    setIntent(nextIntent);
    setStaged(true);
    setToast(`Staged ${nextIntent.kind} intent for ${nextIntent.objectName}`);
    go('workspace');
  };
  const removeChange = () => { setStaged(false); setIntent(null); setToast('Workspace is clean'); };
  const startApply = () => {
    if (!staged || !intent || scenario === 'validation-blocked' || scenario === 'drift' || scenario === 'lacp-mismatch') { setToast('Apply is blocked by current review state'); return; }
    setSeconds(90);
    setApplyState(scenario === 'outcome-unknown' ? 'outcome-unknown' : 'countdown');
    setToast(scenario === 'outcome-unknown' ? 'Outcome unknown · reconciliation required' : 'Safe Apply started · confirmation timer active');
    go('safe-apply');
  };
  const confirmApply = () => { if (scenario === 'network-loss') { setToast('Confirmation unavailable while disconnected'); return; } setApplyState('confirmed'); setStaged(false); setToast('Safe Apply confirmed'); go('evidence'); };
  const rollbackApply = () => { setApplyState('rolled-back'); setStaged(false); setToast('Rollback requested and completed'); go('evidence'); };
  const resolveScenario = () => { setScenario('normal'); setToast('Review scenario resolved · normal path restored'); };

  useEffect(() => {
    const context = (document as WebMcpDocument).modelContext;
    if (!context?.registerTool) return;
    const lifecycle = new AbortController();
    const register = (tool: WebMcpTool) => {
      try { void Promise.resolve(context.registerTool(tool, { signal: lifecycle.signal })).catch(() => undefined); } catch { /* Unsupported registration must not disrupt the UI. */ }
    };
    const flush = () => new Promise<void>((resolve) => window.setTimeout(resolve, 0));
    register({ name: 'read_prototype_state', title: 'Read prototype state', description: 'Read the current P0/P1 view, mode, scenario, candidate intent, and Safe Apply state.', inputSchema: { type: 'object', properties: {}, additionalProperties: false }, annotations: { readOnlyHint: true, untrustedContentHint: false }, execute: () => ({ ...stateRef.current }) });
    register({ name: 'navigate_prototype_view', title: 'Navigate prototype view', description: 'Navigate to one named P0 or P1 prototype view without changing configuration intent.', inputSchema: { type: 'object', properties: { view: { type: 'string', enum: [...steps.map((step) => step.view), ...p1Steps.map((step) => step.view)] } }, required: ['view'], additionalProperties: false }, annotations: { readOnlyHint: true, untrustedContentHint: false }, execute: async (input) => { const next = (input as { view?: View }).view; if (!next || ![...steps, ...p1Steps].some((step) => step.view === next)) throw new Error('Unknown prototype view'); setView(next); await flush(); return { view: next }; } });
    register({ name: 'stage_vlan_change', title: 'Stage VLAN change', description: 'Stage a VLAN intent for an OVS-managed port and open the Candidate Workspace.', inputSchema: { type: 'object', properties: { port: { type: 'string' }, vlanMode: { type: 'string', enum: ['access', 'trunk', 'native-tagged'] }, allowedVlans: { type: 'string' } }, required: ['port', 'vlanMode', 'allowedVlans'], additionalProperties: false }, annotations: { readOnlyHint: false, untrustedContentHint: false }, execute: async (input) => { const data = input as { port?: string; vlanMode?: string; allowedVlans?: string }; const target = ports.find((item) => item.name === data.port); if (!target || target.scope === 'Observe') throw new Error('Port is unavailable for managed VLAN intent'); if (!data.allowedVlans?.trim()) throw new Error('allowedVlans is required'); const nextMode = data.vlanMode ?? 'trunk'; const nextIntent: ChangeIntent = { kind: 'vlan', objectType: 'Port', objectName: target.name, title: `Update Port / ${target.name}`, summary: `VLAN access 120 → ${nextMode} ${data.allowedVlans}`, current: 'vlan_mode: access\ntag: 120\ntrunks: []', candidate: `vlan_mode: ${nextMode}\ntag: 120\ntrunks: [${data.allowedVlans}]`, risk: target.name === 'mgmt0' ? 'High' : 'Medium', capability: 'vlan.basic_manage', evidenceObject: `Port/${target.name}` }; setSelectedPort(target.name); setVlanMode(nextMode); setAllowedVlans(data.allowedVlans); setIntent(nextIntent); setStaged(true); setView('workspace'); await flush(); return { workspace: 'ws-183', staged: true, port: target.name }; } });
    register({ name: 'stage_bond_change', title: 'Stage Bond change', description: 'Stage a representative Bond Port intent with two Interface members and open the Candidate Workspace.', inputSchema: { type: 'object', properties: { name: { type: 'string' }, bridge: { type: 'string' }, mode: { type: 'string', enum: ['balance-tcp', 'active-backup', 'balance-slb'] }, lacp: { type: 'string', enum: ['active', 'passive', 'off'] } }, required: ['name', 'bridge', 'mode', 'lacp'], additionalProperties: false }, annotations: { readOnlyHint: false, untrustedContentHint: false }, execute: async (input) => { const data = input as { name?: string; bridge?: string; mode?: string; lacp?: string }; if (!data.name?.trim() || !data.bridge?.trim()) throw new Error('Bond name and bridge are required'); const nextIntent: ChangeIntent = { kind: 'bond', objectType: 'Port', objectName: data.name, title: `Create Bond Port / ${data.name}`, summary: `Attach 2 Interface members on ${data.bridge} · ${data.mode} · LACP ${data.lacp}`, current: 'object: absent', candidate: `bridge: ${data.bridge}\nbond_mode: ${data.mode}\nlacp: ${data.lacp}\ninterfaces: [enp65s0f2, enp65s0f3]`, risk: data.bridge === 'br-mgmt' ? 'High' : 'Medium', capability: 'bond.manage', evidenceObject: `Port/${data.name}` }; setSelectedBond(data.name); setSelectedBridge(data.bridge); setIntent(nextIntent); setStaged(true); setView('workspace'); await flush(); return { workspace: 'ws-183', staged: true, bond: data.name }; } });
    register({ name: 'validate_workspace', title: 'Validate workspace', description: 'Run the prototype validation contract for the staged workspace and open Diff & Validation.', inputSchema: { type: 'object', properties: {}, additionalProperties: false }, annotations: { readOnlyHint: true, untrustedContentHint: false }, execute: async () => { if (!stateRef.current.intent) throw new Error('Workspace is empty'); const blocked = ['drift', 'validation-blocked', 'lacp-mismatch', 'outcome-unknown'].includes(stateRef.current.scenario); setView('diff'); await flush(); return { workspace: 'ws-183', result: blocked ? 'blocked' : 'pass', scenario: stateRef.current.scenario }; } });
    register({ name: 'start_safe_apply', title: 'Start Safe Apply', description: 'Start the provisional Safe Apply countdown for a validated staged workspace.', inputSchema: { type: 'object', properties: {}, additionalProperties: false }, annotations: { readOnlyHint: false, untrustedContentHint: false }, execute: async () => { const current = stateRef.current; if (!current.intent) throw new Error('Workspace is empty'); if (current.scenario === 'validation-blocked' || current.scenario === 'drift' || current.scenario === 'lacp-mismatch') throw new Error('Validation blocks Apply'); setSeconds(90); setApplyState(current.scenario === 'outcome-unknown' ? 'outcome-unknown' : 'countdown'); setView('safe-apply'); await flush(); return { job: 'job-2047', state: current.scenario === 'outcome-unknown' ? 'outcome-unknown' : 'provisional', confirmationWindowSeconds: 90 }; } });
    register({ name: 'complete_safe_apply', title: 'Complete Safe Apply', description: 'Confirm or roll back the active Safe Apply operation.', inputSchema: { type: 'object', properties: { decision: { type: 'string', enum: ['confirm', 'rollback'] } }, required: ['decision'], additionalProperties: false }, annotations: { readOnlyHint: false, untrustedContentHint: false }, execute: async (input) => { const decision = (input as { decision?: string }).decision; if (stateRef.current.applyState !== 'countdown') throw new Error('No authoritative countdown is active'); if (decision === 'confirm' && stateRef.current.scenario === 'network-loss') throw new Error('Confirmation unavailable while disconnected'); if (decision !== 'confirm' && decision !== 'rollback') throw new Error('Decision must be confirm or rollback'); setApplyState(decision === 'confirm' ? 'confirmed' : 'rolled-back'); setStaged(false); setView('evidence'); await flush(); return { job: 'job-2047', outcome: decision === 'confirm' ? 'confirmed' : 'rolled-back' }; } });
    register({ name: 'set_review_scenario', title: 'Set review scenario', description: 'Select a predefined normal or abnormal state for prototype review.', inputSchema: { type: 'object', properties: { scenario: { type: 'string', enum: Object.keys(scenarioLabels) } }, required: ['scenario'], additionalProperties: false }, annotations: { readOnlyHint: false, untrustedContentHint: false }, execute: async (input) => { const next = (input as { scenario?: Scenario }).scenario; if (!next || !(next in scenarioLabels)) throw new Error('Unknown scenario'); setScenario(next); if (next === 'member-down') { setSelectedBond('bond-storage'); setSelectedBridge('br-storage'); } else if (next === 'provider-degraded') { setSelectedBond('bond-provider'); setSelectedBridge('br-offload'); } else if (next === 'lacp-mismatch' || next === 'advanced-config') { setSelectedBond('bond-uplink'); setSelectedBridge('br-fabric'); } await flush(); return { scenario: next, label: scenarioLabels[next] }; } });
    return () => lifecycle.abort();
  }, []);

  const status = scenario === 'normal' ? ['Healthy', 'border-emerald-300 bg-emerald-50 text-emerald-800'] : scenario === 'network-loss' ? ['Disconnected', 'border-rose-300 bg-rose-50 text-rose-800'] : scenario === 'drift' ? ['Drift', 'border-amber-300 bg-amber-50 text-amber-800'] : ['Attention', 'border-rose-300 bg-rose-50 text-rose-800'];

  const nav = [
    { label: 'Overview', icon: Gauge, action: () => go('dashboard'), active: view === 'dashboard' },
    { label: 'Switching', icon: Network, action: () => go('switching-overview'), active: ['ports', 'port-detail', 'vlan-edit', ...p1Views].includes(view as P1View) },
    { label: 'Operations', icon: Activity, action: () => go('safe-apply'), active: view === 'safe-apply' },
    { label: 'Evidence', icon: FileClock, action: () => go('evidence'), active: view === 'evidence' },
    { label: 'System', icon: Settings, action: () => go('responsive'), active: view === 'responsive' },
  ];

  let content: ReactNode;
  if (p1Views.includes(view as P1View)) content = <P1SwitchingView view={view as P1View} mode={mode} scenario={scenario} search={bridgeSearch} setSearch={setBridgeSearch} selectedBridge={selectedBridge} setSelectedBridge={setSelectedBridge} selectedBond={selectedBond} setSelectedBond={setSelectedBond} go={(next) => go(next)} onStageIntent={stageIntent} />;
  else if (view === 'dashboard') content = <Dashboard intent={intent} staged={staged} scenario={scenario} go={go} />;
  else if (view === 'ports') content = <PortsPage mode={mode} search={search} setSearch={setSearch} selected={selectedPort} setSelected={setSelectedPort} go={go} />;
  else if (view === 'port-detail') content = <PortDetail port={port} mode={mode} go={go} />;
  else if (view === 'vlan-edit') content = <VlanEdit port={port} mode={mode} vlanMode={vlanMode} setVlanMode={setVlanMode} allowedVlans={allowedVlans} setAllowedVlans={setAllowedVlans} onStage={stageChange} go={go} />;
  else if (view === 'workspace') content = <Workspace intent={intent} staged={staged} scenario={scenario} onRemove={removeChange} go={go} />;
  else if (view === 'diff') content = <DiffPage intent={intent} scenario={scenario} note={note} setNote={setNote} onApply={startApply} go={go} onResolve={resolveScenario} />;
  else if (view === 'safe-apply') content = <SafeApply intent={intent} scenario={scenario} applyState={applyState} seconds={seconds} onConfirm={confirmApply} onRollback={rollbackApply} onReconnect={resolveScenario} go={go} />;
  else if (view === 'evidence') content = <Evidence applyState={applyState} intent={intent} go={go} />;
  else content = <ResponsivePage applyState={applyState} go={go} />;

  return (
    <main className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-30 flex h-14 items-center border-b bg-white px-3 lg:px-6">
        <div className="flex min-w-0 items-center gap-3"><Button className="hidden md:inline-flex lg:hidden" variant="ghost" size="icon" aria-label="Open navigation" aria-expanded={navOpen} onClick={() => setNavOpen((current) => !current)}><Menu /></Button><button type="button" onClick={() => go('dashboard')} className="grid size-8 place-items-center border-2 border-primary bg-primary text-xs font-black text-primary-foreground">OVS</button><div className="hidden min-w-0 sm:block"><p className="truncate text-sm font-semibold tracking-tight">Open Switch Management Plane</p><p className="truncate font-mono text-[10px] text-muted-foreground">node / sw-edge-01</p></div></div>
        <div className="ml-auto flex items-center gap-2"><div className="hidden items-center gap-2 xl:flex"><span className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground">Review state</span><NativeSelect value={scenario} onChange={(event) => { const next = event.target.value as Scenario; setScenario(next); if (next === 'member-down') { setSelectedBond('bond-storage'); setSelectedBridge('br-storage'); } else if (next === 'provider-degraded') { setSelectedBond('bond-provider'); setSelectedBridge('br-offload'); } else if (next === 'lacp-mismatch' || next === 'advanced-config') { setSelectedBond('bond-uplink'); setSelectedBridge('br-fabric'); } setToast(`Scenario · ${scenarioLabels[next]}`); }} size="sm">{Object.entries(scenarioLabels).map(([value, label]) => <NativeSelectOption key={value} value={value}>{label}</NativeSelectOption>)}</NativeSelect></div><Badge variant="outline" className={`h-7 rounded-sm ${status[1]}`}><CircleDot data-icon="inline-start" />{status[0]}</Badge><div className="hidden items-center border p-0.5 lg:flex"><button type="button" aria-pressed={mode === 'standard'} onClick={() => setMode('standard')} className={`px-2 py-1 text-xs font-medium ${mode === 'standard' ? 'bg-[#173b63] text-white' : 'text-muted-foreground'}`}>Standard</button><button type="button" aria-pressed={mode === 'expert'} onClick={() => setMode('expert')} className={`px-2 py-1 text-xs font-medium ${mode === 'expert' ? 'bg-[#173b63] text-white' : 'text-muted-foreground'}`}>Expert</button></div><Button size="sm" onClick={() => go('workspace')} className="gap-2 bg-[#173b63] hover:bg-[#234f80]"><GitCompareArrows /> <span className="hidden sm:inline">Workspace</span> · {staged ? 1 : 0}</Button></div>
      </header>

      {navOpen && <div className="fixed inset-x-0 top-14 z-40 hidden border-b bg-white p-4 shadow-lg md:block lg:hidden"><nav aria-label="Tablet navigation" className="mx-auto grid max-w-3xl grid-cols-2 gap-2 sm:grid-cols-3">{[
        ['Switching overview', 'switching-overview'], ['Bridges', 'bridges'], ['Bond / LACP', 'bonds'], ['Ports', 'ports'], ['Candidate workspace', 'workspace'], ['Evidence', 'evidence'],
      ].map(([label, target]) => <button key={label} type="button" onClick={() => go(target as View)} className={`border px-3 py-3 text-left text-sm ${view === target ? 'border-[#8bb7d1] bg-[#f2f8fc] font-semibold text-[#12385e]' : 'bg-white text-slate-700'}`}>{label}</button>)}</nav><p className="mx-auto mt-3 max-w-3xl text-xs text-muted-foreground">Tablet responsibility: Standard-mode review and bounded operations. Starting Apply remains desktop-only.</p></div>}

      <div className="md:hidden"><MobilePanel applyState={applyState} onConfirm={confirmApply} onRollback={rollbackApply} onOpenEvidence={() => go('evidence')} /></div>

      <div className="hidden min-h-[calc(100vh-3.5rem)] md:grid lg:grid-cols-[224px_minmax(0,1fr)]">
        <aside className="hidden border-r bg-[#f4f7fa] p-3 lg:block"><nav aria-label="Primary navigation" className="space-y-1">{nav.map(({ label, icon: Icon, action, active }) => <button key={label} type="button" onClick={action} className={`flex w-full items-center gap-3 rounded-sm px-3 py-2 text-left text-sm font-medium transition-colors ${active ? 'bg-[#dce9f5] text-[#12385e]' : 'text-slate-600 hover:bg-white hover:text-slate-950'}`}><Icon className="size-4" />{label}</button>)}</nav><div className="mt-6 border-t pt-4"><p className="px-3 pb-2 font-mono text-[10px] font-semibold uppercase tracking-[0.12em] text-slate-500">Switching</p>{[['Overview', 'switching-overview'], ['Bridges', 'bridges'], ['Ports', 'ports'], ['VLAN', 'ports'], ['Bond / LACP', 'bonds'], ['STP / RSTP', 'switching-overview']].map(([label, target]) => <button key={label} type="button" onClick={() => go(target as View)} className={`flex w-full items-center justify-between rounded-sm px-3 py-1.5 text-left text-sm ${view === target ? 'bg-white font-semibold text-[#12385e]' : 'text-slate-600 hover:bg-white'}`}><span>{label}</span><ChevronRight className="size-3.5 text-slate-400" /></button>)}</div><div className="mt-6 border border-dashed border-slate-300 bg-white p-3"><div className="flex items-center gap-2 text-xs font-semibold text-slate-700"><TerminalSquare className="size-4" /> Architecture invariant</div><p className="mt-2 font-mono text-[10px] leading-5 text-slate-500">Bridge → Port → Interface</p></div><button type="button" onClick={() => go('responsive')} className="mt-3 flex w-full items-center gap-2 border border-dashed border-slate-300 bg-white p-3 text-left text-xs text-slate-600"><Smartphone className="size-4" /><span>Responsive responsibility</span></button></aside>
        <section className="min-w-0 px-4 py-5 sm:px-6 lg:px-8 lg:py-7"><div className="mx-auto max-w-[1480px]"><div className="mb-3 flex items-center justify-between gap-3 lg:hidden"><Badge variant="outline" className="rounded-sm">Tablet · Standard responsibility</Badge><span className="text-xs text-muted-foreground">Apply commit remains desktop-only</span></div><ScenarioBanner scenario={scenario} onResolve={resolveScenario} />{content}{p1Views.includes(view as P1View) ? <P1Stepper view={view as P1View} go={(next) => go(next)} /> : <P0Stepper view={view} go={go} />}<footer className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t pt-3 font-mono text-[10px] text-muted-foreground"><span>OVS 3.4.1 · datapath system · schema 8.3.1</span><span>{p1Views.includes(view as P1View) ? 'P1 Batch 01 · Draft for review' : 'P0 Low-Fidelity UX Baseline v0.1 · Frozen'}</span></footer></div></section>
      </div>
      <output aria-live="polite" aria-atomic="true" className="fixed bottom-4 right-4 z-40 hidden max-w-sm border border-slate-300 bg-white px-3 py-2 text-xs shadow-lg md:block"><span className="font-mono text-[10px] text-muted-foreground">STATUS</span><span className="ml-2">{toast}</span></output>
    </main>
  );
}
