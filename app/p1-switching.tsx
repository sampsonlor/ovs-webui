'use client';

import { useMemo, useState } from 'react';
import {
  Activity,
  AlertTriangle,
  ArrowLeft,
  Boxes,
  Cable,
  CheckCircle2,
  ChevronRight,
  Clock3,
  Code2,
  GitCompareArrows,
  Info,
  Layers3,
  LockKeyhole,
  Network,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
  SlidersHorizontal,
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
  ChangeIntent,
  P1View,
  PrototypeMode,
  ReviewScenario,
} from './prototype-model';

type Bridge = {
  name: string;
  state: 'Up' | 'Degraded' | 'Unknown';
  datapath: string;
  ports: number;
  interfaces: number;
  bonds: number;
  vlans: string;
  rstp: string;
  authority: 'OVS' | 'External';
  scope: 'Manage' | 'Basic Manage' | 'Observe';
  uuid: string;
  provider: string;
};

type Bond = {
  name: string;
  bridge: string;
  state: 'Up' | 'Degraded' | 'Unknown';
  mode: 'balance-tcp' | 'active-backup' | 'balance-slb';
  lacp: 'active' | 'passive' | 'off';
  members: string[];
  memberSpeeds: string[];
  hashPolicy: string;
  minLinks: number;
  authority: 'OVS' | 'External';
  scope: 'Manage' | 'Observe';
  uuid: string;
  provider: string;
};

const bridges: Bridge[] = [
  {
    name: 'br-fabric',
    state: 'Up',
    datapath: 'system',
    ports: 8,
    interfaces: 10,
    bonds: 1,
    vlans: '10, 20, 120, 240',
    rstp: 'Enabled',
    authority: 'OVS',
    scope: 'Manage',
    uuid: 'c44d…7b92',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-storage',
    state: 'Up',
    datapath: 'system',
    ports: 3,
    interfaces: 4,
    bonds: 1,
    vlans: '300–319',
    rstp: 'Disabled',
    authority: 'OVS',
    scope: 'Manage',
    uuid: '2a1e…94f0',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-mgmt',
    state: 'Up',
    datapath: 'system',
    ports: 2,
    interfaces: 2,
    bonds: 0,
    vlans: '4094 native',
    rstp: 'Enabled',
    authority: 'OVS',
    scope: 'Manage',
    uuid: '8b9a…a110',
    provider: 'ovsdb-server',
  },
  {
    name: 'br-offload',
    state: 'Unknown',
    datapath: 'netdev',
    ports: 5,
    interfaces: 5,
    bonds: 1,
    vlans: 'Provider-owned',
    rstp: 'Unknown',
    authority: 'External',
    scope: 'Observe',
    uuid: '6fce…004d',
    provider: 'SmartNIC provider',
  },
];

const bonds: Bond[] = [
  {
    name: 'bond-uplink',
    bridge: 'br-fabric',
    state: 'Up',
    mode: 'balance-tcp',
    lacp: 'active',
    members: ['enp65s0f0', 'enp65s0f1'],
    memberSpeeds: ['100 Gbps', '100 Gbps'],
    hashPolicy: 'L2/L3/L4',
    minLinks: 1,
    authority: 'OVS',
    scope: 'Manage',
    uuid: '9f84…a3d1',
    provider: 'ovsdb-server',
  },
  {
    name: 'bond-storage',
    bridge: 'br-storage',
    state: 'Up',
    mode: 'active-backup',
    lacp: 'off',
    members: ['enp129s0f0', 'enp129s0f1'],
    memberSpeeds: ['25 Gbps', '25 Gbps'],
    hashPolicy: 'active member',
    minLinks: 1,
    authority: 'OVS',
    scope: 'Manage',
    uuid: '83cb…41c9',
    provider: 'ovsdb-server',
  },
  {
    name: 'bond-provider',
    bridge: 'br-offload',
    state: 'Unknown',
    mode: 'balance-slb',
    lacp: 'off',
    members: ['pf0hpf', 'pf1hpf'],
    memberSpeeds: ['Unknown', 'Unknown'],
    hashPolicy: 'Provider-defined',
    minLinks: 0,
    authority: 'External',
    scope: 'Observe',
    uuid: 'b8d1…7e44',
    provider: 'SmartNIC provider',
  },
];

function ScopeBadge({
  scope,
}: {
  scope: Bridge['scope'] | 'Manage + Observe';
}) {
  const tone =
    scope === 'Manage'
      ? 'border-sky-300 bg-sky-50 text-sky-800'
      : scope === 'Observe'
        ? 'border-slate-300 bg-slate-50 text-slate-700'
        : 'border-amber-300 bg-amber-50 text-amber-800';
  return (
    <Badge variant="outline" className={`rounded-sm ${tone}`}>
      {scope}
    </Badge>
  );
}

function PageHeader({
  id,
  eyebrow,
  title,
  description,
  scope,
  route,
  actions,
}: {
  id: string;
  eyebrow: string;
  title: string;
  description: string;
  scope: Bridge['scope'] | 'Manage + Observe';
  route: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-4">
      <div>
        <div className="flex flex-wrap items-center gap-2 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
          <span>{id}</span>
          <span>·</span>
          <span>{eyebrow}</span>
          <ScopeBadge scope={scope} />
        </div>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
          {description}
        </p>
        <p className="mt-2 font-mono text-[10px] text-slate-400">
          route · {route}
        </p>
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

function StateLabel({ state }: { state: Bridge['state'] }) {
  const tone =
    state === 'Up'
      ? 'text-emerald-700'
      : state === 'Degraded'
        ? 'text-amber-700'
        : 'text-slate-600';
  const dot =
    state === 'Up'
      ? 'bg-emerald-500'
      : state === 'Degraded'
        ? 'bg-amber-500'
        : 'bg-slate-400';
  return (
    <span className={`inline-flex items-center gap-2 font-medium ${tone}`}>
      <span className={`size-2 rounded-full ${dot}`} />
      {state}
    </span>
  );
}

function Metric({
  label,
  value,
  note,
  icon: Icon,
  tone = 'text-slate-600',
}: {
  label: string;
  value: string;
  note: string;
  icon: typeof Boxes;
  tone?: string;
}) {
  return (
    <article className="border bg-card p-4 shadow-[0_1px_0_rgb(15_23_42/4%)]">
      <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
        <span>{label}</span>
        <Icon className={`size-4 ${tone}`} />
      </div>
      <p className="mt-3 text-2xl font-semibold tracking-tight">{value}</p>
      <p className={`mt-1 text-xs ${tone}`}>{note}</p>
    </article>
  );
}

function SwitchingOverview({
  scenario,
  go,
  setSelectedBridge,
}: {
  scenario: ReviewScenario;
  go: (view: P1View) => void;
  setSelectedBridge: (value: string) => void;
}) {
  const degraded =
    scenario === 'member-down' ||
    scenario === 'lacp-mismatch' ||
    scenario === 'provider-degraded';
  return (
    <>
      <PageHeader
        id="P1-01"
        eyebrow="Switching / Overview"
        title="Switching overview"
        description="One operational entry point for Bridge, Port, Interface, VLAN, and Bond/LACP posture—while preserving their native OVS hierarchy."
        scope="Observe"
        route="/switching"
        actions={
          <Button
            onClick={() => go('bridges')}
            className="bg-[#157a9e] hover:bg-[#11627f]"
          >
            Open bridges <ChevronRight />
          </Button>
        }
      />
      <div className="mt-6 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Metric
          label="Bridges"
          value="4"
          note="3 managed · 1 observed"
          icon={Boxes}
        />
        <Metric
          label="Ports / interfaces"
          value="18 / 21"
          note="native objects kept distinct"
          icon={Layers3}
        />
        <Metric
          label="Bonds"
          value="3"
          note={degraded ? '1 requires attention' : '2 managed · 1 observed'}
          icon={Network}
          tone={degraded ? 'text-amber-700' : 'text-slate-600'}
        />
        <Metric
          label="Switching health"
          value={degraded ? 'Degraded' : 'Healthy'}
          note={
            degraded
              ? 'exception scenario active'
              : 'generation 1842 · fresh 8s'
          }
          icon={degraded ? AlertTriangle : ShieldCheck}
          tone={degraded ? 'text-amber-700' : 'text-emerald-700'}
        />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1.45fr)_minmax(330px,0.55fr)]">
        <section className="border bg-card">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
            <div>
              <h2 className="text-sm font-semibold">Bridge posture</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">
                Bridge → Port → Interface remains the authoritative object path
              </p>
            </div>
            <Button variant="outline" size="sm" onClick={() => go('bridges')}>
              View inventory
            </Button>
          </div>
          <Table>
            <TableHeader className="bg-muted/45">
              <TableRow>
                <TableHead>Bridge</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Ports</TableHead>
                <TableHead>Interfaces</TableHead>
                <TableHead>Bonds</TableHead>
                <TableHead>Scope</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {bridges.map((bridge) => (
                <TableRow
                  key={bridge.name}
                  className="cursor-pointer"
                  onClick={() => {
                    setSelectedBridge(bridge.name);
                    go('bridge-detail');
                  }}
                >
                  <TableCell className="font-mono text-xs font-semibold text-[#164f7b]">
                    {bridge.name}
                  </TableCell>
                  <TableCell>
                    <StateLabel state={bridge.state} />
                  </TableCell>
                  <TableCell>{bridge.ports}</TableCell>
                  <TableCell>{bridge.interfaces}</TableCell>
                  <TableCell>{bridge.bonds}</TableCell>
                  <TableCell>
                    <ScopeBadge scope={bridge.scope} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>

        <aside className="space-y-4">
          <section className="border border-[#9bb7cf] bg-[#f2f8fc] p-4">
            <div className="flex items-center gap-2">
              <GitCompareArrows className="size-4 text-[#35627f]" />
              <h2 className="text-sm font-semibold">Change contract</h2>
            </div>
            <ol className="mt-4 space-y-3 text-xs">
              {[
                'Start from managed object',
                'Stage candidate intent',
                'Review semantic diff',
                'Safe Apply when risky',
                'Link Event and Audit evidence',
              ].map((label, index) => (
                <li key={label} className="flex items-center gap-3">
                  <span className="grid size-5 shrink-0 place-items-center border border-[#8bb7d1] bg-white font-mono text-[10px]">
                    {index + 1}
                  </span>
                  <span>{label}</span>
                </li>
              ))}
            </ol>
          </section>
          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <Activity className="size-4 text-amber-700" />
              <h2 className="text-sm font-semibold">Current attention</h2>
            </div>
            <ul className="mt-3 space-y-3 text-xs">
              <li className="border-t pt-2 first:border-t-0 first:pt-0">
                <span className="font-semibold">server-08</span>
                <p className="mt-1 text-muted-foreground">
                  Interface down · Port still modeled separately
                </p>
              </li>
              <li className="border-t pt-2">
                <span className="font-semibold">br-offload</span>
                <p className="mt-1 text-muted-foreground">
                  Provider-owned health is unknown, not Down
                </p>
              </li>
            </ul>
          </section>
        </aside>
      </div>
    </>
  );
}

function BridgeList({
  mode,
  search,
  setSearch,
  setSelectedBridge,
  go,
  onStageBridge,
}: {
  mode: PrototypeMode;
  search: string;
  setSearch: (value: string) => void;
  setSelectedBridge: (value: string) => void;
  go: (view: P1View) => void;
  onStageBridge: (intent: ChangeIntent) => void;
}) {
  const [authority, setAuthority] = useState('all');
  const shown = useMemo(
    () =>
      bridges.filter(
        (bridge) =>
          (authority === 'all' ||
            bridge.authority.toLowerCase() === authority) &&
          `${bridge.name} ${bridge.datapath} ${bridge.vlans}`
            .toLowerCase()
            .includes(search.toLowerCase()),
      ),
    [authority, search],
  );
  const stageNewBridge = () =>
    onStageBridge({
      kind: 'bridge',
      objectType: 'Bridge',
      objectName: 'br-tenant-240',
      title: 'Create Bridge / br-tenant-240',
      summary: 'Create system datapath bridge with RSTP enabled',
      current: 'object: absent',
      candidate:
        'name: br-tenant-240\ndatapath_type: system\nrstp_enable: true',
      risk: 'Medium',
      capability: 'bridge.manage',
      evidenceObject: 'Bridge/br-tenant-240',
      bridgeName: 'br-tenant-240',
    });
  return (
    <>
      <PageHeader
        id="P1-02"
        eyebrow="Switching / Bridges"
        title="Bridges"
        description="Inventory and manage logical switching domains without flattening their Port and Interface children."
        scope="Manage"
        route="/switching/bridges"
        actions={
          <Button
            onClick={stageNewBridge}
            className="bg-[#157a9e] hover:bg-[#11627f]"
          >
            <Plus /> New bridge intent
          </Button>
        }
      />
      <div className="mt-5 flex flex-wrap items-center gap-3 border bg-card p-3">
        <div className="relative min-w-[260px] flex-1">
          <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            className="pl-8"
            placeholder="Search bridge, datapath, or VLAN"
            aria-label="Search bridges"
          />
        </div>
        <NativeSelect
          value={authority}
          onChange={(event) => setAuthority(event.target.value)}
          aria-label="Filter bridge authority"
        >
          <NativeSelectOption value="all">All authorities</NativeSelectOption>
          <NativeSelectOption value="ovs">OVS managed</NativeSelectOption>
          <NativeSelectOption value="external">External</NativeSelectOption>
        </NativeSelect>
        <Badge variant="outline" className="rounded-sm">
          fresh · 8s
        </Badge>
        <output className="text-sm text-muted-foreground">
          {shown.length} of {bridges.length} bridges
        </output>
      </div>
      <div className="mt-3 overflow-x-auto border bg-card">
        <Table>
          <TableHeader className="bg-muted/45">
            <TableRow>
              <TableHead>Bridge</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Datapath</TableHead>
              <TableHead>Ports</TableHead>
              <TableHead>Interfaces</TableHead>
              <TableHead>Bonds</TableHead>
              <TableHead>VLANs</TableHead>
              {mode === 'expert' && (
                <>
                  <TableHead>Authority</TableHead>
                  <TableHead>UUID</TableHead>
                </>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((bridge) => (
              <TableRow
                key={bridge.name}
                className="cursor-pointer"
                onClick={() => {
                  setSelectedBridge(bridge.name);
                  go('bridge-detail');
                }}
              >
                <TableCell className="font-mono text-xs font-semibold text-[#164f7b]">
                  <button
                    className="ovs-object-link"
                    onClick={(event) => {
                      event.stopPropagation();
                      setSelectedBridge(bridge.name);
                      go('bridge-detail');
                    }}
                  >
                    {bridge.name}
                  </button>
                </TableCell>
                <TableCell>
                  <StateLabel state={bridge.state} />
                </TableCell>
                <TableCell>
                  <ScopeBadge scope={bridge.scope} />
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {bridge.datapath}
                </TableCell>
                <TableCell>{bridge.ports}</TableCell>
                <TableCell>{bridge.interfaces}</TableCell>
                <TableCell>{bridge.bonds}</TableCell>
                <TableCell>{bridge.vlans}</TableCell>
                {mode === 'expert' && (
                  <>
                    <TableCell>{bridge.authority}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {bridge.uuid}
                    </TableCell>
                  </>
                )}
              </TableRow>
            ))}
            {shown.length === 0 && (
              <TableRow>
                <TableCell
                  colSpan={mode === 'expert' ? 10 : 8}
                  className="p-6 text-center"
                >
                  <p className="text-sm">No bridges match these filters.</p>
                  <Button
                    variant="link"
                    onClick={() => {
                      setAuthority('all');
                      setSearch('');
                    }}
                  >
                    Clear bridge filters
                  </Button>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        Observe-only bridges stay inspectable. External authority never receives
        a misleading configuration action.
      </p>
    </>
  );
}

function BridgeDetail({
  bridge,
  mode,
  go,
  onStageBridge,
  onCreateBond,
}: {
  bridge: Bridge;
  mode: PrototypeMode;
  go: (view: P1View) => void;
  onStageBridge: (intent: ChangeIntent) => void;
  onCreateBond: (bridge: string) => void;
}) {
  const editable = bridge.scope === 'Manage';
  const childRows =
    bridge.name === 'br-storage'
      ? [
          [
            'bond-storage',
            'Bond Port',
            'enp129s0f0, enp129s0f1',
            'Up',
            'trunk 300–319',
          ],
          ['storage-node-01', 'System Port', 'enp129s0f2', 'Up', 'access 310'],
        ]
      : bridge.name === 'br-mgmt'
        ? [
            ['mgmt0', 'System Port', 'eno1', 'Up', 'native 4094'],
            ['mgmt-backup', 'System Port', 'eno2', 'Up', 'native 4094'],
          ]
        : bridge.name === 'br-offload'
          ? [
              [
                'bond-provider',
                'Bond Port',
                'pf0hpf, pf1hpf',
                'Unknown',
                'provider-owned',
              ],
              ['rep0', 'Provider Port', 'pf2hpf', 'Unknown', 'provider-owned'],
            ]
          : [
              [
                'bond-uplink',
                'Bond Port',
                'enp65s0f0, enp65s0f1',
                'Up',
                'trunk 10, 20, 120',
              ],
              ['server-07', 'System Port', 'enp129s0f1', 'Up', 'access 120'],
              ['server-08', 'System Port', 'enp129s0f2', 'Down', 'access 120'],
            ];
  const stageRstp = () =>
    onStageBridge({
      kind: 'bridge',
      objectType: 'Bridge',
      objectName: bridge.name,
      title: `Update Bridge / ${bridge.name}`,
      summary: `${bridge.rstp === 'Enabled' ? 'Disable' : 'Enable'} RSTP`,
      current: `rstp_enable: ${bridge.rstp === 'Enabled'}`,
      candidate: `rstp_enable: ${bridge.rstp !== 'Enabled'}`,
      risk: bridge.name === 'br-mgmt' ? 'High' : 'Medium',
      capability: 'bridge.manage',
      evidenceObject: `Bridge/${bridge.name}`,
      bridgeName: bridge.name,
    });
  return (
    <>
      <PageHeader
        id="P1-03"
        eyebrow="Switching / Bridge detail"
        title={bridge.name}
        description="Bridge posture, child objects, ownership, and candidate-only configuration entry points."
        scope={bridge.scope}
        route={`/switching/bridges/${bridge.name}`}
        actions={
          <>
            <Button variant="outline" onClick={() => go('bridges')}>
              <ArrowLeft /> Bridges
            </Button>
            {editable && (
              <Button variant="outline" onClick={stageRstp}>
                Stage RSTP change
              </Button>
            )}
            {editable && (
              <Button
                onClick={() => onCreateBond(bridge.name)}
                className="bg-[#157a9e] hover:bg-[#11627f]"
              >
                <Plus /> Add bond
              </Button>
            )}
          </>
        }
      />
      {!editable && (
        <div className="mt-4 flex items-start gap-3 border border-slate-300 bg-slate-50 p-4">
          <ShieldAlert className="mt-0.5 size-5 text-slate-600" />
          <div>
            <p className="text-sm font-semibold">Provider-controlled bridge</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Authority: {bridge.provider}. This page remains Observe until the
              provider advertises native management capability.
            </p>
          </div>
        </div>
      )}
      {bridge.name === 'br-mgmt' && (
        <div className="mt-4 flex items-start gap-3 border border-rose-300 bg-rose-50 p-4">
          <ShieldAlert className="mt-0.5 size-5 text-rose-700" />
          <div>
            <p className="text-sm font-semibold">Management-plane bridge</p>
            <p className="mt-1 text-xs text-rose-800">
              Every connectivity-affecting change is high risk and must use Safe
              Apply. Delete is unavailable while the active management route
              depends on this bridge.
            </p>
          </div>
        </div>
      )}
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(330px,0.6fr)]">
        <div className="space-y-4">
          <section className="border bg-card">
            <div className="border-b px-4 py-3">
              <h2 className="text-sm font-semibold">Operational summary</h2>
            </div>
            <dl className="grid sm:grid-cols-2 xl:grid-cols-4">
              {[
                ['State', bridge.state],
                ['Datapath', bridge.datapath],
                ['VLAN domain', bridge.vlans],
                ['RSTP', bridge.rstp],
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
              <h2 className="text-sm font-semibold">Native object hierarchy</h2>
              <Badge variant="outline" className="rounded-sm">
                architecture invariant
              </Badge>
            </div>
            <div className="mt-4 grid items-stretch gap-2 md:grid-cols-[1fr_auto_1fr_auto_1fr]">
              <div className="border border-[#8bb7d1] bg-[#f2f8fc] p-4">
                <p className="font-mono text-[10px] text-muted-foreground">
                  Bridge
                </p>
                <p className="mt-1 font-mono text-sm font-semibold">
                  {bridge.name}
                </p>
                <p className="mt-3 text-xs text-muted-foreground">
                  logical switching domain
                </p>
              </div>
              <ChevronRight className="m-auto size-4 text-muted-foreground" />
              <button
                type="button"
                onClick={() => go('bonds')}
                className="border bg-white p-4 text-left hover:border-[#8bb7d1]"
              >
                <p className="font-mono text-[10px] text-muted-foreground">
                  Port children
                </p>
                <p className="mt-1 text-sm font-semibold">
                  {bridge.ports} ports · {bridge.bonds} bond
                </p>
                <p className="mt-3 text-xs text-[#164f7b]">
                  Open Bond/LACP inventory
                </p>
              </button>
              <ChevronRight className="m-auto size-4 text-muted-foreground" />
              <div className="border bg-muted/35 p-4">
                <p className="font-mono text-[10px] text-muted-foreground">
                  Interface children
                </p>
                <p className="mt-1 text-sm font-semibold">
                  {bridge.interfaces} interfaces
                </p>
                <p className="mt-3 text-xs text-muted-foreground">
                  operational attachment points
                </p>
              </div>
            </div>
          </section>
          <section className="border bg-card">
            <div className="flex items-center justify-between border-b px-4 py-3">
              <div>
                <h2 className="text-sm font-semibold">
                  Representative Port children
                </h2>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  A Bond is represented as one Port with multiple Interface
                  members
                </p>
              </div>
              <Button variant="ghost" size="sm" onClick={() => go('bonds')}>
                Bond/LACP
              </Button>
            </div>
            <Table>
              <TableHeader className="bg-muted/45">
                <TableRow>
                  <TableHead>Port</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Interfaces</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>VLAN</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {childRows.map((row) => (
                  <TableRow key={row[0]}>
                    <TableCell className="font-mono text-xs font-semibold text-[#164f7b]">
                      {row[0]}
                    </TableCell>
                    <TableCell>{row[1]}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {row[2]}
                    </TableCell>
                    <TableCell>{row[3]}</TableCell>
                    <TableCell>{row[4]}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </section>
        </div>
        <aside className="space-y-4">
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Identity & authority</h2>
            <dl className="mt-3 space-y-3 text-xs">
              {[
                ['Authority', bridge.authority],
                ['Provider', bridge.provider],
                ['Generation', '1842'],
                ['Freshness', '8 seconds'],
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
          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <CheckCircle2 className="size-4 text-emerald-600" />
              <h2 className="text-sm font-semibold">Lifecycle guardrails</h2>
            </div>
            <ul className="mt-3 space-y-2 text-xs text-muted-foreground">
              <li>Changes create candidate intent only</li>
              <li>Child dependencies are validated before delete</li>
              <li>Management risk selects Safe Apply</li>
            </ul>
          </section>
          {mode === 'expert' && (
            <section className="border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4">
              <div className="flex items-center gap-2">
                <Code2 className="size-4 text-[#35627f]" />
                <h2 className="text-sm font-semibold">Expert native fields</h2>
              </div>
              <pre className="mt-3 overflow-x-auto border bg-white p-3 font-mono text-[11px] leading-5 text-slate-700">{`_uuid: ${bridge.uuid}\nname: ${bridge.name}\ndatapath_type: ${bridge.datapath}\nrstp_enable: ${bridge.rstp === 'Enabled'}`}</pre>
            </section>
          )}
        </aside>
      </div>
    </>
  );
}

function bondState(bond: Bond, scenario: ReviewScenario): Bond['state'] {
  if (bond.name === 'bond-storage' && scenario === 'member-down')
    return 'Degraded';
  if (bond.name === 'bond-uplink' && scenario === 'lacp-mismatch')
    return 'Degraded';
  if (bond.authority === 'External' && scenario === 'provider-degraded')
    return 'Unknown';
  return bond.state;
}

function lacpState(bond: Bond, scenario: ReviewScenario) {
  if (bond.authority === 'External' && scenario === 'provider-degraded')
    return 'Stale';
  if (bond.name === 'bond-uplink' && scenario === 'lacp-mismatch')
    return 'Partner mismatch';
  return bond.lacp === 'off' ? 'Not negotiated' : 'Negotiated';
}

function BondList({
  mode,
  scenario,
  selectedBridge,
  setSelectedBridge,
  setSelectedBond,
  onCreateBond,
  go,
}: {
  mode: PrototypeMode;
  scenario: ReviewScenario;
  selectedBridge: string;
  setSelectedBridge: (value: string) => void;
  setSelectedBond: (value: string) => void;
  onCreateBond: (bridge: string) => void;
  go: (view: P1View) => void;
}) {
  const [bridgeFilter, setBridgeFilter] = useState(
    selectedBridge === 'br-offload' ? 'all' : selectedBridge,
  );
  const shown =
    bridgeFilter === 'all'
      ? bonds
      : bonds.filter((bond) => bond.bridge === bridgeFilter);
  return (
    <>
      <PageHeader
        id="P1-04"
        eyebrow="Switching / Bond & LACP"
        title="Bond / LACP"
        description="Manage a Bond as one OVS Port with two or more Interface members, while exposing negotiation and member health as observed evidence."
        scope="Manage + Observe"
        route="/switching/bonds"
        actions={
          <Button
            onClick={() =>
              onCreateBond(
                selectedBridge === 'br-offload' ? 'br-fabric' : selectedBridge,
              )
            }
            className="bg-[#157a9e] hover:bg-[#11627f]"
          >
            <Plus /> New bond intent
          </Button>
        }
      />
      <div className="mt-5 flex flex-wrap items-center gap-3 border bg-card p-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <SlidersHorizontal className="size-4" /> Bridge
        </div>
        <NativeSelect
          value={bridgeFilter}
          onChange={(event) => setBridgeFilter(event.target.value)}
          aria-label="Filter bonds by bridge"
        >
          <NativeSelectOption value="all">All bridges</NativeSelectOption>
          {bridges.map((bridge) => (
            <NativeSelectOption key={bridge.name} value={bridge.name}>
              {bridge.name}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
          <RefreshCw className="size-3.5" /> observed 8s ago
        </div>
      </div>
      <div className="mt-3 overflow-x-auto border bg-card">
        <Table>
          <TableHeader className="bg-muted/45">
            <TableRow>
              <TableHead>Bond Port</TableHead>
              <TableHead>Bridge</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead>Mode</TableHead>
              <TableHead>LACP</TableHead>
              <TableHead>Members</TableHead>
              <TableHead>Capacity</TableHead>
              {mode === 'expert' && (
                <>
                  <TableHead>min-links</TableHead>
                  <TableHead>UUID</TableHead>
                </>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((bond) => {
              const state = bondState(bond, scenario);
              const negotiation = lacpState(bond, scenario);
              return (
                <TableRow
                  key={bond.name}
                  className="cursor-pointer"
                  onClick={() => {
                    setSelectedBridge(bond.bridge);
                    setSelectedBond(bond.name);
                    go('bond-detail');
                  }}
                >
                  <TableCell>
                    <div className="font-mono text-xs font-semibold text-[#164f7b]">
                      {bond.name}
                    </div>
                    <div className="mt-1 text-[10px] text-muted-foreground">
                      Port · {bond.members.length} Interface members
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {bond.bridge}
                  </TableCell>
                  <TableCell>
                    <StateLabel state={state} />
                  </TableCell>
                  <TableCell>
                    <ScopeBadge scope={bond.scope} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {bond.mode}
                  </TableCell>
                  <TableCell>
                    <span
                      className={`text-xs font-medium ${negotiation === 'Negotiated' ? 'text-emerald-700' : negotiation === 'Partner mismatch' ? 'text-rose-700' : 'text-slate-600'}`}
                    >
                      {bond.lacp} · {negotiation}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {bond.members.length}
                  </TableCell>
                  <TableCell>
                    {bond.name === 'bond-provider'
                      ? 'Unknown'
                      : bond.memberSpeeds.join(' + ')}
                  </TableCell>
                  {mode === 'expert' && (
                    <>
                      <TableCell>{bond.minLinks}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {bond.uuid}
                      </TableCell>
                    </>
                  )}
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>
          Operational degradation does not rewrite desired Bond intent.
          Provider-owned rows remain Observe-only.
        </span>
        <span className="font-mono">Port rows · 3</span>
      </div>
    </>
  );
}

function BondEditor({
  bond,
  initialBridge,
  mode,
  scenario,
  go,
  onStageIntent,
}: {
  bond?: Bond;
  initialBridge: string;
  mode: PrototypeMode;
  scenario: ReviewScenario;
  go: (view: P1View) => void;
  onStageIntent: (intent: ChangeIntent) => void;
}) {
  const creating = !bond;
  const [name, setName] = useState(bond?.name ?? 'bond-edge-02');
  const [bridge, setBridge] = useState(bond?.bridge ?? initialBridge);
  const [bondMode, setBondMode] = useState<Bond['mode']>(
    bond?.mode ?? 'balance-tcp',
  );
  const [lacp, setLacp] = useState<Bond['lacp']>(bond?.lacp ?? 'active');
  const [minLinks, setMinLinks] = useState(String(bond?.minLinks ?? 1));
  const [members, setMembers] = useState<string[]>(
    bond?.members ?? ['enp65s0f2', 'enp65s0f3'],
  );
  const availableMembers =
    bridge === 'br-storage'
      ? ['enp129s0f0', 'enp129s0f1', 'enp129s0f2']
      : bridge === 'br-mgmt'
        ? ['eno1', 'eno2', 'enp4s0f0']
        : ['enp65s0f0', 'enp65s0f1', 'enp65s0f2', 'enp65s0f3'];
  const advancedLocked =
    scenario === 'advanced-config' && mode === 'standard' && !creating;
  const structurallyBlocked =
    !/^[a-zA-Z0-9_.-]{1,63}$/.test(name) ||
    members.length < 2 ||
    !Number.isInteger(Number(minLinks)) ||
    !minLinks.trim() ||
    (bondMode === 'balance-tcp' && lacp === 'off') ||
    members.some((member) => !availableMembers.includes(member)) ||
    Number(minLinks) < 0 ||
    Number(minLinks) > members.length;
  const toggleMember = (member: string) =>
    setMembers((current) =>
      current.includes(member)
        ? current.filter((item) => item !== member)
        : [...current, member],
    );
  const stage = () => {
    if (structurallyBlocked || advancedLocked) return;
    onStageIntent({
      kind: 'bond',
      objectType: 'Port',
      objectName: name,
      title: `${creating ? 'Create' : 'Update'} Bond Port / ${name}`,
      summary: `${creating ? 'Attach' : 'Update'} ${members.length} Interface members on ${bridge} · ${bondMode} · LACP ${lacp}`,
      current: creating
        ? 'object: absent'
        : `bridge: ${bond?.bridge}\nbond_mode: ${bond?.mode}\nlacp: ${bond?.lacp}\ninterfaces: [${bond?.members.join(', ')}]`,
      candidate: `bridge: ${bridge}\nbond_mode: ${bondMode}\nlacp: ${lacp}\nother_config:min-links: ${minLinks}\ninterfaces: [${members.join(', ')}]${scenario === 'advanced-config' ? '\nother_config:bond-rebalance-interval: 10000 # preserved' : ''}`,
      risk:
        bridge === 'br-mgmt' || scenario === 'member-down' ? 'High' : 'Medium',
      capability: 'bond.manage',
      evidenceObject: `Port/${name}`,
      bridgeName: bridge,
    });
  };
  return (
    <>
      <PageHeader
        id="P1-05"
        eyebrow="Bond / Candidate editor"
        title={`${creating ? 'Create' : 'Edit'} Bond / LACP`}
        description="Compose a Bond Port and its Interface membership as candidate intent. This editor cannot write directly to live OVSDB."
        scope="Manage"
        route={
          creating
            ? '/switching/bonds/new'
            : `/switching/bonds/${bond.name}/edit`
        }
        actions={
          <Button
            variant="outline"
            onClick={() => go(creating ? 'bonds' : 'bond-detail')}
          >
            <ArrowLeft /> Cancel
          </Button>
        }
      />
      {scenario === 'advanced-config' && (
        <div className="mt-4 flex items-start gap-3 border border-amber-300 bg-amber-50 p-4">
          <Code2 className="mt-0.5 size-5 text-amber-700" />
          <div className="flex-1">
            <p className="text-sm font-semibold">
              Advanced native field detected
            </p>
            <p className="mt-1 text-xs text-amber-900">
              other_config:bond-rebalance-interval is outside the Standard
              editor. It will never be silently removed.
              {mode === 'standard'
                ? ' Switch to Expert Mode to review and preserve it before staging.'
                : ' The retained value is shown below and will be preserved.'}
            </p>
          </div>
        </div>
      )}
      {scenario === 'lacp-mismatch' && (
        <div className="mt-4 flex items-start gap-3 border border-rose-300 bg-rose-50 p-4">
          <AlertTriangle className="mt-0.5 size-5 text-rose-700" />
          <div>
            <p className="text-sm font-semibold">
              Observed LACP partner mismatch
            </p>
            <p className="mt-1 text-xs text-rose-900">
              You may stage corrective intent, but Diff & Validation will block
              Apply until the partner state is compatible or the exception is
              explicitly resolved.
            </p>
          </div>
        </div>
      )}
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.25fr)_minmax(340px,0.75fr)]">
        <form
          className="border bg-card"
          onSubmit={(event) => {
            event.preventDefault();
            stage();
          }}
        >
          <div className="flex flex-wrap items-center justify-between gap-2 border-b px-5 py-3">
            <div>
              <h2 className="text-sm font-semibold">Bond intent</h2>
              <p className="mt-0.5 text-xs text-muted-foreground">
                Port + Interface membership
              </p>
            </div>
            <Badge
              variant="outline"
              className="rounded-sm border-sky-300 bg-sky-50 text-sky-800"
            >
              candidate only
            </Badge>
          </div>
          <div className="grid gap-5 p-5 sm:grid-cols-2">
            <label
              htmlFor="bond-name"
              className="grid gap-1.5 text-xs font-medium"
            >
              Bond Port name
              <Input
                id="bond-name"
                value={name}
                disabled={!creating}
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <label
              htmlFor="bond-bridge"
              className="grid gap-1.5 text-xs font-medium"
            >
              Parent Bridge
              <NativeSelect
                id="bond-bridge"
                className="w-full"
                value={bridge}
                onChange={(event) => {
                  setBridge(event.target.value);
                  setMembers([]);
                }}
                disabled={!creating}
              >
                {bridges
                  .filter((item) => item.scope === 'Manage')
                  .map((item) => (
                    <NativeSelectOption key={item.name} value={item.name}>
                      {item.name}
                    </NativeSelectOption>
                  ))}
              </NativeSelect>
            </label>
            <label
              htmlFor="bond-mode"
              className="grid gap-1.5 text-xs font-medium"
            >
              Bond mode
              <NativeSelect
                id="bond-mode"
                className="w-full"
                value={bondMode}
                onChange={(event) =>
                  setBondMode(event.target.value as Bond['mode'])
                }
              >
                <NativeSelectOption value="balance-tcp">
                  balance-tcp
                </NativeSelectOption>
                <NativeSelectOption value="active-backup">
                  active-backup
                </NativeSelectOption>
                <NativeSelectOption value="balance-slb">
                  balance-slb
                </NativeSelectOption>
              </NativeSelect>
            </label>
            <label
              htmlFor="bond-lacp"
              className="grid gap-1.5 text-xs font-medium"
            >
              LACP
              <NativeSelect
                id="bond-lacp"
                className="w-full"
                value={lacp}
                onChange={(event) =>
                  setLacp(event.target.value as Bond['lacp'])
                }
              >
                <NativeSelectOption value="active">active</NativeSelectOption>
                <NativeSelectOption value="passive">passive</NativeSelectOption>
                <NativeSelectOption value="off">off</NativeSelectOption>
              </NativeSelect>
            </label>
            <label
              htmlFor="bond-min-links"
              className="grid gap-1.5 text-xs font-medium"
            >
              Minimum active links
              <Input
                id="bond-min-links"
                type="number"
                min="0"
                max={Math.max(1, members.length)}
                value={minLinks}
                onChange={(event) => setMinLinks(event.target.value)}
              />
            </label>
            <label
              htmlFor="bond-hash-policy"
              className="grid gap-1.5 text-xs font-medium"
            >
              Hash policy
              <Input
                id="bond-hash-policy"
                value={
                  bondMode === 'balance-tcp'
                    ? 'L2 / L3 / L4'
                    : bondMode === 'active-backup'
                      ? 'Active member only'
                      : 'Source MAC + VLAN'
                }
                readOnly
              />
            </label>
          </div>
          <div className="border-t px-5 py-4">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="text-sm font-semibold">Interface members</h3>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  Select at least two unassigned interfaces
                </p>
              </div>
              <Badge
                variant="outline"
                className={`rounded-sm ${members.length >= 2 ? 'border-emerald-300 bg-emerald-50 text-emerald-800' : 'border-rose-300 bg-rose-50 text-rose-800'}`}
              >
                {members.length} selected
              </Badge>
            </div>
            <div className="mt-3 grid gap-2 sm:grid-cols-2">
              {availableMembers.map((member) => {
                const checked = members.includes(member);
                const unavailable =
                  !checked &&
                  (member === 'enp65s0f0' || member === 'enp65s0f1') &&
                  creating;
                const controlId = `bond-member-${member}`;
                return (
                  <label
                    key={member}
                    htmlFor={controlId}
                    className={`flex items-center gap-3 border p-3 text-xs ${checked ? 'border-[#8bb7d1] bg-[#f2f8fc]' : 'bg-white'} ${unavailable ? 'opacity-55' : ''}`}
                  >
                    <input
                      id={controlId}
                      aria-label={`Select Interface ${member}`}
                      type="checkbox"
                      checked={checked}
                      disabled={unavailable}
                      onChange={() => toggleMember(member)}
                      className="size-4 accent-[#173b63]"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block font-mono font-semibold">
                        {member}
                      </span>
                      <span className="mt-1 block text-muted-foreground">
                        {unavailable
                          ? 'assigned to bond-uplink'
                          : 'system · 100 Gbps · available'}
                      </span>
                    </span>
                  </label>
                );
              })}
            </div>
          </div>
          {mode === 'expert' && (
            <div className="mx-5 mb-5 border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4">
              <div className="flex items-center gap-2">
                <Code2 className="size-4 text-[#35627f]" />
                <h3 className="text-sm font-semibold">Expert native mapping</h3>
              </div>
              <pre className="mt-3 overflow-x-auto border bg-white p-3 font-mono text-[11px] leading-5">{`Port.name = "${name}"\nPort.bond_mode = "${bondMode}"\nPort.lacp = "${lacp}"\nPort.interfaces = [${members.join(', ')}]\nPort.other_config:min-links = "${minLinks}"${scenario === 'advanced-config' ? '\nPort.other_config:bond-rebalance-interval = "10000" # preserved' : ''}`}</pre>
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-3 border-t px-5 py-4">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <LockKeyhole className="size-4" /> Network.Operator · change.stage
            </div>
            <div className="flex gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => go(creating ? 'bonds' : 'bond-detail')}
              >
                Discard
              </Button>
              <Button
                type="submit"
                disabled={structurallyBlocked || advancedLocked}
                className="bg-[#173b63] hover:bg-[#234f80]"
              >
                Add to workspace <ChevronRight />
              </Button>
            </div>
          </div>
        </form>
        <aside className="space-y-4">
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Structure preview</h2>
            <div className="mt-4 space-y-2">
              <div className="border bg-muted/40 p-3">
                <p className="font-mono text-[10px] text-muted-foreground">
                  Bridge
                </p>
                <p className="mt-1 font-mono text-sm font-semibold">{bridge}</p>
              </div>
              <div className="ml-5 border-l-2 border-slate-300 pl-3">
                <div className="border border-[#8bb7d1] bg-[#f2f8fc] p-3">
                  <p className="font-mono text-[10px] text-muted-foreground">
                    Port · Bond
                  </p>
                  <p className="mt-1 font-mono text-sm font-semibold">
                    {name || 'name required'}
                  </p>
                </div>
                <div className="ml-5 border-l-2 border-slate-300 pl-3 pt-2">
                  {members.length ? (
                    members.map((member) => (
                      <div
                        key={member}
                        className="mb-2 border bg-white p-2 font-mono text-xs"
                      >
                        Interface · {member}
                      </div>
                    ))
                  ) : (
                    <div className="border border-dashed p-3 text-xs text-muted-foreground">
                      Select at least two Interface members
                    </div>
                  )}
                </div>
              </div>
            </div>
          </section>
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Pre-stage checks</h2>
            <ul className="mt-3 space-y-3 text-xs">
              <li className="flex items-start gap-2">
                {name.trim() ? (
                  <CheckCircle2 className="mt-0.5 size-4 text-emerald-600" />
                ) : (
                  <XCircle className="mt-0.5 size-4 text-rose-600" />
                )}{' '}
                Unique Port name
              </li>
              <li className="flex items-start gap-2">
                {members.length >= 2 ? (
                  <CheckCircle2 className="mt-0.5 size-4 text-emerald-600" />
                ) : (
                  <XCircle className="mt-0.5 size-4 text-rose-600" />
                )}{' '}
                At least two Interface members
              </li>
              <li className="flex items-start gap-2">
                {advancedLocked ? (
                  <XCircle className="mt-0.5 size-4 text-rose-600" />
                ) : (
                  <CheckCircle2 className="mt-0.5 size-4 text-emerald-600" />
                )}{' '}
                Native fields are fully preserved
              </li>
              <li className="flex items-start gap-2">
                <Clock3 className="mt-0.5 size-4 text-amber-600" /> Connectivity
                change requires Safe Apply
              </li>
            </ul>
          </section>
          <div className="border border-dashed p-3 text-xs text-muted-foreground">
            Tablet may review this intent in Standard Mode. Creating or editing
            Bond membership and starting Apply remain desktop responsibilities.
          </div>
        </aside>
      </div>
    </>
  );
}

function BondDetail({
  bond,
  mode,
  scenario,
  go,
  onDiagnose,
}: {
  bond: Bond;
  mode: PrototypeMode;
  scenario: ReviewScenario;
  go: (view: P1View) => void;
  onDiagnose: (scope: string) => void;
}) {
  const editable = bond.scope === 'Manage';
  const state = bondState(bond, scenario);
  const memberDown = bond.name === 'bond-storage' && scenario === 'member-down';
  const mismatch = bond.name === 'bond-uplink' && scenario === 'lacp-mismatch';
  const providerDegraded =
    bond.authority === 'External' && scenario === 'provider-degraded';
  const advanced =
    bond.name === 'bond-uplink' && scenario === 'advanced-config';
  return (
    <>
      <PageHeader
        id="P1-06"
        eyebrow="Switching / Bond detail"
        title={bond.name}
        description="Desired Bond configuration and observed member/LACP evidence, separated without losing their relationship."
        scope={editable ? 'Manage + Observe' : 'Observe'}
        route={`/switching/bonds/${bond.name}`}
        actions={
          <>
            <Button variant="outline" onClick={() => go('bonds')}>
              <ArrowLeft /> Bonds
            </Button>
            <Button
              variant="outline"
              onClick={() => onDiagnose(`Port/${bond.name}`)}
            >
              <Activity /> Run diagnostic
            </Button>
            {editable && (
              <Button
                onClick={() => go('bond-edit')}
                className="bg-[#157a9e] hover:bg-[#11627f]"
              >
                Edit bond intent
              </Button>
            )}
          </>
        }
      />
      {memberDown && (
        <div className="mt-4 flex items-start gap-3 border border-amber-300 bg-amber-50 p-4">
          <Cable className="mt-0.5 size-5 text-amber-700" />
          <div>
            <p className="text-sm font-semibold">Member enp129s0f1 is down</p>
            <p className="mt-1 text-xs text-amber-900">
              The Bond remains active through enp129s0f0, but redundancy is
              reduced. Desired membership is unchanged.
            </p>
          </div>
        </div>
      )}
      {mismatch && (
        <div className="mt-4 flex items-start gap-3 border border-rose-300 bg-rose-50 p-4">
          <AlertTriangle className="mt-0.5 size-5 text-rose-700" />
          <div>
            <p className="text-sm font-semibold">LACP partner mismatch</p>
            <p className="mt-1 text-xs text-rose-900">
              Local actor is active; partner aggregation is not compatible. New
              configuration Apply is blocked until reconciliation.
            </p>
          </div>
        </div>
      )}
      {providerDegraded && (
        <div className="mt-4 flex items-start gap-3 border border-amber-300 bg-amber-50 p-4">
          <TriangleAlert className="mt-0.5 size-5 text-amber-700" />
          <div>
            <p className="text-sm font-semibold">Provider evidence is stale</p>
            <p className="mt-1 text-xs text-amber-900">
              Member and traffic values are unknown. The UI does not infer Down
              and does not expose Edit.
            </p>
          </div>
        </div>
      )}
      {advanced && (
        <div className="mt-4 flex items-start gap-3 border border-amber-300 bg-amber-50 p-4">
          <Code2 className="mt-0.5 size-5 text-amber-700" />
          <div>
            <p className="text-sm font-semibold">
              Advanced native option is active
            </p>
            <p className="mt-1 text-xs text-amber-900">
              bond-rebalance-interval is preserved outside the Standard field
              set. Enter Expert Mode before editing.
            </p>
          </div>
        </div>
      )}
      <div className="mt-5 grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(330px,0.6fr)]">
        <div className="space-y-4">
          <section className="border bg-card">
            <div className="border-b px-4 py-3">
              <h2 className="text-sm font-semibold">
                Operational and desired summary
              </h2>
            </div>
            <dl className="grid sm:grid-cols-2 xl:grid-cols-4">
              {[
                ['State', state],
                ['Bond mode', bond.mode],
                ['LACP intent', bond.lacp],
                ['Partner state', lacpState(bond, scenario)],
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
                Bridge → Port → Interface
              </Badge>
            </div>
            <div className="mt-4 grid items-stretch gap-2 md:grid-cols-[1fr_auto_1fr_auto_1fr]">
              <button
                type="button"
                onClick={() => go('bridge-detail')}
                className="border bg-white p-4 text-left hover:border-[#8bb7d1]"
              >
                <p className="font-mono text-[10px] text-muted-foreground">
                  Bridge
                </p>
                <p className="mt-1 font-mono text-sm font-semibold">
                  {bond.bridge}
                </p>
              </button>
              <ChevronRight className="m-auto size-4 text-muted-foreground" />
              <div className="border border-[#8bb7d1] bg-[#f2f8fc] p-4">
                <p className="font-mono text-[10px] text-muted-foreground">
                  Port · Bond
                </p>
                <p className="mt-1 font-mono text-sm font-semibold">
                  {bond.name}
                </p>
                <p className="mt-2 text-xs text-muted-foreground">
                  {bond.mode} · LACP {bond.lacp}
                </p>
              </div>
              <ChevronRight className="m-auto size-4 text-muted-foreground" />
              <div className="border bg-muted/35 p-4">
                <p className="font-mono text-[10px] text-muted-foreground">
                  Interface members
                </p>
                <p className="mt-1 text-sm font-semibold">
                  {bond.members.length} native children
                </p>
                <p className="mt-2 font-mono text-[10px] text-muted-foreground">
                  {bond.members.join(' · ')}
                </p>
              </div>
            </div>
          </section>
          <section className="border bg-card">
            <div className="flex items-center justify-between border-b px-4 py-3">
              <div>
                <h2 className="text-sm font-semibold">Member health</h2>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  Observed state does not mutate configured membership
                </p>
              </div>
              <Badge variant="outline" className="rounded-sm">
                fresh · {providerDegraded ? 'stale' : '8s'}
              </Badge>
            </div>
            <Table>
              <TableHeader className="bg-muted/45">
                <TableRow>
                  <TableHead>Interface</TableHead>
                  <TableHead>Link</TableHead>
                  <TableHead>Speed</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>LACP</TableHead>
                  <TableHead>Traffic</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {bond.members.map((member, index) => {
                  const down = memberDown && index === 1;
                  const unknown = providerDegraded;
                  const link = unknown ? 'Unknown' : down ? 'Down' : 'Up';
                  return (
                    <TableRow key={member}>
                      <TableCell className="font-mono text-xs font-semibold text-[#164f7b]">
                        {member}
                      </TableCell>
                      <TableCell>
                        <span
                          className={`inline-flex items-center gap-2 ${link === 'Up' ? 'text-emerald-700' : link === 'Down' ? 'text-rose-700' : 'text-slate-600'}`}
                        >
                          <span
                            className={`size-2 rounded-full ${link === 'Up' ? 'bg-emerald-500' : link === 'Down' ? 'bg-rose-500' : 'bg-slate-400'}`}
                          />
                          {link}
                        </span>
                      </TableCell>
                      <TableCell>
                        {unknown
                          ? 'Unknown'
                          : down
                            ? '—'
                            : bond.memberSpeeds[index]}
                      </TableCell>
                      <TableCell>
                        {bond.mode === 'active-backup'
                          ? index === 0
                            ? 'Active'
                            : 'Backup'
                          : 'Distributing'}
                      </TableCell>
                      <TableCell>
                        {bond.lacp === 'off'
                          ? 'N/A'
                          : mismatch
                            ? index === 0
                              ? 'Collecting only'
                              : 'Detached'
                            : 'Collecting + distributing'}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {unknown
                          ? 'Unknown'
                          : down
                            ? '0 bps'
                            : index === 0
                              ? '38.4 Gbps'
                              : '34.9 Gbps'}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </section>
        </div>
        <aside className="space-y-4">
          <section className="border bg-card p-4">
            <h2 className="text-sm font-semibold">Configuration authority</h2>
            <dl className="mt-3 space-y-3 text-xs">
              {[
                ['Authority', bond.authority],
                ['Provider', bond.provider],
                ['Scope', bond.scope],
                [
                  'Permission',
                  editable
                    ? 'change.stage: allowed'
                    : 'change.stage: unavailable',
                ],
                [
                  'Risk if changed',
                  bond.bridge === 'br-mgmt' ? 'High' : 'Medium',
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
          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <Info className="size-4 text-[#35627f]" />
              <h2 className="text-sm font-semibold">Apply contract</h2>
            </div>
            <ul className="mt-3 space-y-2 text-xs text-muted-foreground">
              <li>Membership changes stage to the global workspace</li>
              <li>Partner mismatch and drift block Apply</li>
              <li>Connectivity changes use Safe Apply</li>
              <li>Event and Audit share correlation_id</li>
            </ul>
          </section>
          {mode === 'expert' && (
            <section className="border border-dashed border-[#7aa4c2] bg-[#f5f9fc] p-4">
              <div className="flex items-center gap-2">
                <Code2 className="size-4 text-[#35627f]" />
                <h2 className="text-sm font-semibold">Expert native fields</h2>
              </div>
              <pre className="mt-3 overflow-x-auto border bg-white p-3 font-mono text-[11px] leading-5">{`_uuid: ${bond.uuid}\nPort.name: ${bond.name}\nPort.bond_mode: ${bond.mode}\nPort.lacp: ${bond.lacp}\nPort.other_config:min-links: ${bond.minLinks}${advanced ? '\nPort.other_config:bond-rebalance-interval: 10000' : ''}`}</pre>
            </section>
          )}
        </aside>
      </div>
    </>
  );
}

export function P1SwitchingView({
  view,
  mode,
  scenario,
  search,
  setSearch,
  selectedBridge,
  setSelectedBridge,
  selectedBond,
  setSelectedBond,
  go,
  onStageIntent,
  onDiagnose,
}: {
  view: P1View;
  mode: PrototypeMode;
  scenario: ReviewScenario;
  search: string;
  setSearch: (value: string) => void;
  selectedBridge: string;
  setSelectedBridge: (value: string) => void;
  selectedBond: string;
  setSelectedBond: (value: string) => void;
  go: (view: P1View) => void;
  onStageIntent: (intent: ChangeIntent) => void;
  onDiagnose: (scope: string) => void;
}) {
  const bridge =
    bridges.find((item) => item.name === selectedBridge) ?? bridges[0];
  const bond = bonds.find((item) => item.name === selectedBond);
  const createBond = (parentBridge: string) => {
    setSelectedBridge(parentBridge);
    setSelectedBond('__new__');
    go('bond-edit');
  };
  if (view === 'bridges')
    return (
      <BridgeList
        mode={mode}
        search={search}
        setSearch={setSearch}
        setSelectedBridge={setSelectedBridge}
        go={go}
        onStageBridge={onStageIntent}
      />
    );
  if (view === 'bridge-detail')
    return (
      <BridgeDetail
        bridge={bridge}
        mode={mode}
        go={go}
        onStageBridge={onStageIntent}
        onCreateBond={createBond}
      />
    );
  if (view === 'bonds')
    return (
      <BondList
        mode={mode}
        scenario={scenario}
        selectedBridge={selectedBridge}
        setSelectedBridge={setSelectedBridge}
        setSelectedBond={setSelectedBond}
        onCreateBond={createBond}
        go={go}
      />
    );
  if (view === 'bond-edit')
    return (
      <BondEditor
        key={`${selectedBond}:${selectedBridge}`}
        bond={bond}
        initialBridge={selectedBridge}
        mode={mode}
        scenario={scenario}
        go={go}
        onStageIntent={onStageIntent}
      />
    );
  if (view === 'bond-detail')
    return (
      <BondDetail
        bond={bond ?? bonds[0]}
        mode={mode}
        scenario={scenario}
        go={go}
        onDiagnose={onDiagnose}
      />
    );
  return (
    <SwitchingOverview
      scenario={scenario}
      go={go}
      setSelectedBridge={setSelectedBridge}
    />
  );
}
