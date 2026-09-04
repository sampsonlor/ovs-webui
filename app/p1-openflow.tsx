'use client';

import { useMemo, useState } from 'react';
import {
  Activity,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Clock3,
  Code2,
  Download,
  ExternalLink,
  FileClock,
  Gauge,
  ListFilter,
  LockKeyhole,
  Network,
  RefreshCw,
  Search,
  ShieldAlert,
  ShieldCheck,
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
  OpenFlowReviewState,
  P1View,
  PrototypeMode,
} from './prototype-model';
import { openFlowReviewLabels } from './prototype-model';

type Flow = {
  id: string;
  table: number;
  priority: number;
  cookie: string;
  match: string;
  action: string;
  packets: string;
  bytes: string;
  duration: string;
  inPort: string;
  relatedPort: string;
  raw: string;
};

const flows: Flow[] = [
  {
    id: 'flow-00-a13f',
    table: 0,
    priority: 200,
    cookie: '0x00a1f04c',
    match: 'in_port=1, vlan_tci=0x1078/0x1fff',
    action: 'set_field:0x78→vlan_vid, resubmit(,10)',
    packets: '18.4M',
    bytes: '21.8 GB',
    duration: '3d 04:12:51',
    inPort: '1',
    relatedPort: 'uplink-01',
    raw: 'cookie=0x00a1f04c, duration=274371.212s, table=0, n_packets=18402382, n_bytes=21811343208, priority=200,in_port=1,dl_vlan=120 actions=load:0x78->NXM_OF_VLAN_TCI[],resubmit(,10)',
  },
  {
    id: 'flow-00-09c2',
    table: 0,
    priority: 180,
    cookie: '0x00a1f050',
    match: 'in_port=7, dl_vlan=300',
    action: 'resubmit(,10)',
    packets: '7.91M',
    bytes: '8.44 GB',
    duration: '3d 04:12:48',
    inPort: '7',
    relatedPort: 'bond-storage',
    raw: 'cookie=0x00a1f050, duration=274368.940s, table=0, n_packets=7910231, n_bytes=8440061382, priority=180,in_port=7,dl_vlan=300 actions=resubmit(,10)',
  },
  {
    id: 'flow-10-f821',
    table: 10,
    priority: 150,
    cookie: '0x10b4a113',
    match: 'dl_dst=00:16:3e:7a:4c:21',
    action: 'output:12',
    packets: '2.13M',
    bytes: '3.70 GB',
    duration: '19:42:07',
    inPort: 'any',
    relatedPort: 'server-07',
    raw: 'cookie=0x10b4a113, duration=70927.500s, table=10, n_packets=2134872, n_bytes=3701146228, priority=150,dl_dst=00:16:3e:7a:4c:21 actions=output:12',
  },
  {
    id: 'flow-10-4db8',
    table: 10,
    priority: 150,
    cookie: '0x10b4a118',
    match: 'dl_dst=00:16:3e:7a:4c:22',
    action: 'output:13',
    packets: '983K',
    bytes: '917 MB',
    duration: '19:41:55',
    inPort: 'any',
    relatedPort: 'server-08',
    raw: 'cookie=0x10b4a118, duration=70915.133s, table=10, n_packets=983044, n_bytes=917402103, priority=150,dl_dst=00:16:3e:7a:4c:22 actions=output:13',
  },
  {
    id: 'flow-20-8ea1',
    table: 20,
    priority: 110,
    cookie: '0x20c23e09',
    match: 'ip, nw_dst=10.120.7.0/24',
    action: 'set_field:12→reg0, resubmit(,30)',
    packets: '544K',
    bytes: '690 MB',
    duration: '08:19:31',
    inPort: 'any',
    relatedPort: 'server-07',
    raw: 'cookie=0x20c23e09, duration=29971.889s, table=20, n_packets=544013, n_bytes=690204712, priority=110,ip,nw_dst=10.120.7.0/24 actions=load:0xc->NXM_NX_REG0[],resubmit(,30)',
  },
  {
    id: 'flow-20-19af',
    table: 20,
    priority: 100,
    cookie: '0x20c23e10',
    match: 'arp, arp_tpa=10.120.7.1',
    action: 'NORMAL',
    packets: '94.1K',
    bytes: '8.91 MB',
    duration: '08:18:59',
    inPort: 'any',
    relatedPort: 'br-fabric',
    raw: 'cookie=0x20c23e10, duration=29939.115s, table=20, n_packets=94102, n_bytes=8911204, priority=100,arp,arp_tpa=10.120.7.1 actions=NORMAL',
  },
  {
    id: 'flow-30-7cc4',
    table: 30,
    priority: 90,
    cookie: '0x30fe1201',
    match: 'reg0=0xc/0xffff, ct_state=+trk+new',
    action: 'ct(commit,table=31)',
    packets: '77.8K',
    bytes: '61.2 MB',
    duration: '02:31:42',
    inPort: 'any',
    relatedPort: 'server-07',
    raw: 'cookie=0x30fe1201, duration=9102.008s, table=30, n_packets=77804, n_bytes=61223881, priority=90,reg0=0xc/0xffff,ct_state=+trk+new actions=ct(commit,table=31)',
  },
  {
    id: 'flow-90-0000',
    table: 90,
    priority: 0,
    cookie: '0x00000000',
    match: 'any',
    action: 'drop',
    packets: '12.4K',
    bytes: '1.48 MB',
    duration: '3d 04:12:51',
    inPort: 'any',
    relatedPort: 'br-fabric',
    raw: 'cookie=0x0, duration=274371.212s, table=90, n_packets=12402, n_bytes=1482011, priority=0 actions=drop',
  },
];

const reviewStates: OpenFlowReviewState[] = [
  'fresh',
  'external-authority',
  'stale',
  'truncated',
  'empty',
  'provider-unavailable',
  'permission-denied',
  'query-timeout',
];

function StateBadge({ state }: { state: OpenFlowReviewState }) {
  const tone =
    state === 'fresh'
      ? 'border-emerald-300 bg-emerald-50 text-emerald-800'
      : state === 'external-authority'
        ? 'border-sky-300 bg-sky-50 text-sky-800'
        : state === 'empty'
          ? 'border-slate-300 bg-slate-50 text-slate-700'
          : state === 'permission-denied' || state === 'provider-unavailable'
            ? 'border-rose-300 bg-rose-50 text-rose-800'
            : 'border-amber-300 bg-amber-50 text-amber-800';
  return (
    <Badge variant="outline" className={`rounded-sm ${tone}`}>
      {openFlowReviewLabels[state]}
    </Badge>
  );
}

function ReviewNotice({
  state,
  onRefresh,
  onNarrow,
  onHealth,
}: {
  state: OpenFlowReviewState;
  onRefresh: () => void;
  onNarrow: () => void;
  onHealth: () => void;
}) {
  if (state === 'fresh') return null;

  const notices: Record<
    Exclude<OpenFlowReviewState, 'fresh'>,
    { icon: typeof TriangleAlert; title: string; body: string; tone: string }
  > = {
    'external-authority': {
      icon: ShieldAlert,
      title: 'External controller is authoritative',
      body: 'The WebUI observes the effective pipeline returned by tcp:10.0.0.21:6653. Local edit, delete, and flow-install actions are intentionally unavailable.',
      tone: 'border-sky-300 bg-sky-50 text-sky-950',
    },
    stale: {
      icon: Clock3,
      title: 'Snapshot is stale',
      body: 'Observed 7 minutes ago; counters and flow membership may no longer represent the active datapath.',
      tone: 'border-amber-300 bg-amber-50 text-amber-950',
    },
    truncated: {
      icon: TriangleAlert,
      title: 'Result ceiling reached',
      body: '500 of 12,844 matched rows were retained. Narrow the query before treating the result as complete.',
      tone: 'border-amber-300 bg-amber-50 text-amber-950',
    },
    empty: {
      icon: CheckCircle2,
      title: 'Query completed with 0 rows',
      body: 'The provider responded successfully and the snapshot is fresh. No flow matched the current bounded query.',
      tone: 'border-slate-300 bg-slate-50 text-slate-900',
    },
    'provider-unavailable': {
      icon: XCircle,
      title: 'OpenFlow provider unavailable',
      body: 'The collection provider did not return data. This is not an empty flow table; the last known result is not shown as current.',
      tone: 'border-rose-300 bg-rose-50 text-rose-950',
    },
    'permission-denied': {
      icon: LockKeyhole,
      title: 'Permission denied',
      body: 'The signed-in role cannot query OpenFlow runtime data for this Bridge. Existing cached rows are not disclosed.',
      tone: 'border-rose-300 bg-rose-50 text-rose-950',
    },
    'query-timeout': {
      icon: Gauge,
      title: 'Query exceeded the 5-second budget',
      body: 'No unbounded background export was started. Narrow table, port, match, or action filters and run again.',
      tone: 'border-amber-300 bg-amber-50 text-amber-950',
    },
  };
  const notice = notices[state];
  const Icon = notice.icon;

  return (
    <section className={`mt-4 border p-4 ${notice.tone}`}>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex max-w-3xl items-start gap-3">
          <Icon className="mt-0.5 size-5 shrink-0" />
          <div>
            <h2 className="text-sm font-semibold">{notice.title}</h2>
            <p className="mt-1 text-xs leading-5 opacity-80">{notice.body}</p>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {state === 'stale' && (
            <Button variant="outline" size="sm" onClick={onRefresh}>
              <RefreshCw /> Refresh snapshot
            </Button>
          )}
          {(state === 'truncated' || state === 'query-timeout') && (
            <Button variant="outline" size="sm" onClick={onNarrow}>
              <ListFilter /> Narrow query
            </Button>
          )}
          {state === 'provider-unavailable' && (
            <Button variant="outline" size="sm" onClick={onHealth}>
              <Activity /> Inspect provider health
            </Button>
          )}
        </div>
      </div>
    </section>
  );
}

export function P1OpenFlowView({
  mode,
  reviewState,
  setReviewState,
  openBridge,
  openPort,
  go,
  notify,
}: {
  mode: PrototypeMode;
  reviewState: OpenFlowReviewState;
  setReviewState: (state: OpenFlowReviewState) => void;
  openBridge: (bridge: string) => void;
  openPort: (port: string) => void;
  go: (view: P1View) => void;
  notify: (message: string) => void;
}) {
  const [bridge, setBridge] = useState('br-fabric');
  const [table, setTable] = useState('all');
  const [priority, setPriority] = useState('');
  const [inPort, setInPort] = useState('');
  const [matchField, setMatchField] = useState('');
  const [actionFilter, setActionFilter] = useState('');
  const [query, setQuery] = useState('');
  const [selectedId, setSelectedId] = useState(flows[0].id);
  const [page, setPage] = useState(1);
  const [exported, setExported] = useState(false);
  const [detailView, setDetailView] = useState<'parsed' | 'raw'>('parsed');
  const [queryStamp, setQueryStamp] = useState('12:54:27 UTC');

  const blocked = [
    'provider-unavailable',
    'permission-denied',
    'query-timeout',
  ].includes(reviewState);

  const availableFlows = useMemo(() => {
    if (reviewState !== 'truncated') return flows;
    return Array.from({ length: 500 }, (_, index) => {
      const source = flows[index % flows.length];
      return {
        ...source,
        id: `${source.id}-${String(index + 1).padStart(3, '0')}`,
        cookie: `0x${(0xa1f04c + index).toString(16).padStart(8, '0')}`,
      };
    });
  }, [reviewState]);

  const matchedFlows = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const matchNeedle = matchField.trim().toLowerCase();
    const actionNeedle = actionFilter.trim().toLowerCase();
    const priorityValue = Number(priority);
    return availableFlows.filter((flow) => {
      if (table !== 'all' && flow.table !== Number(table)) return false;
      if (priority && flow.priority < priorityValue) return false;
      if (inPort && flow.inPort !== inPort) return false;
      if (matchNeedle && !flow.match.toLowerCase().includes(matchNeedle))
        return false;
      if (actionNeedle && !flow.action.toLowerCase().includes(actionNeedle))
        return false;
      if (
        needle &&
        !`${flow.cookie} ${flow.match} ${flow.action} ${flow.relatedPort}`
          .toLowerCase()
          .includes(needle)
      )
        return false;
      return true;
    });
  }, [
    actionFilter,
    availableFlows,
    inPort,
    matchField,
    priority,
    query,
    table,
  ]);

  const filtered = reviewState === 'empty' || blocked ? [] : matchedFlows;

  const pageSize = 4;
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const visibleRows = filtered.slice((page - 1) * pageSize, page * pageSize);
  const selected =
    filtered.find((flow) => flow.id === selectedId) ?? filtered[0] ?? flows[0];
  const observedAt = reviewState === 'stale' ? '12:47:03 UTC' : queryStamp;

  const runQuery = () => {
    setPage(1);
    setExported(false);
    setQueryStamp('12:55:18 UTC');
    if (reviewState !== 'external-authority') setReviewState('fresh');
    notify('OpenFlow query complete · fresh bounded snapshot collected');
  };

  const narrowQuery = () => {
    setTable('10');
    setPriority('100');
    setInPort('');
    setMatchField('');
    setActionFilter('');
    setQuery('');
    setPage(1);
    setReviewState('fresh');
    notify('Query narrowed to table 10 · priority ≥ 100');
  };

  const resetFilters = () => {
    setTable('all');
    setPriority('');
    setInPort('');
    setMatchField('');
    setActionFilter('');
    setQuery('');
    setPage(1);
    setExported(false);
    notify('OpenFlow filters reset');
  };

  return (
    <>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex flex-wrap items-center gap-2 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
            <span>P1-09</span>
            <span>·</span>
            <span>Switching / OpenFlow</span>
            <Badge
              variant="outline"
              className="rounded-sm border-slate-300 bg-slate-50 text-slate-700"
            >
              Observe
            </Badge>
          </div>
          <h1 className="mt-2 text-2xl font-semibold tracking-tight">
            OpenFlow viewer
          </h1>
          <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
            Inspect a bounded snapshot of the effective pipeline without
            implying local ownership or write authority.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <NativeSelect
            aria-label="OpenFlow review state"
            value={reviewState}
            onChange={(event) => {
              const next = event.target.value as OpenFlowReviewState;
              setReviewState(next);
              setPage(1);
              setExported(false);
              notify(`OpenFlow review state · ${openFlowReviewLabels[next]}`);
            }}
            size="sm"
          >
            {reviewStates.map((state) => (
              <NativeSelectOption key={state} value={state}>
                {openFlowReviewLabels[state]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <Button variant="outline" size="sm" onClick={runQuery}>
            <RefreshCw /> Refresh
          </Button>
        </div>
      </header>

      <section className="mt-5 border border-[#9bb9ce] bg-[#eef6fb]">
        <div className="grid divide-y divide-[#b9cfdd] lg:grid-cols-[1.15fr_1fr_1fr_1fr] lg:divide-x lg:divide-y-0">
          <div className="p-4">
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#45687f]">
              Query context
            </p>
            <div className="mt-2 flex items-center gap-2">
              <Network className="size-4 text-[#35627f]" />
              <span className="text-sm font-semibold">Bridge / {bridge}</span>
            </div>
            <p className="mt-1 font-mono text-[11px] text-[#45687f]">
              datapath system · OpenFlow 1.5
            </p>
          </div>
          <div className="p-4">
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#45687f]">
              Authority
            </p>
            <div className="mt-2 flex items-center gap-2 text-sm font-semibold text-[#153e5f]">
              <ShieldCheck className="size-4" /> External controller
            </div>
            <p className="mt-1 font-mono text-[11px] text-[#45687f]">
              tcp:10.0.0.21:6653
            </p>
          </div>
          <div className="p-4">
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#45687f]">
              Freshness
            </p>
            <div className="mt-2 flex items-center gap-2">
              <Clock3 className="size-4 text-[#35627f]" />
              <span className="text-sm font-semibold">{observedAt}</span>
            </div>
            <p className="mt-1 text-[11px] text-[#45687f]">
              collection duration · 184 ms
            </p>
          </div>
          <div className="p-4">
            <p className="font-mono text-[10px] uppercase tracking-[0.12em] text-[#45687f]">
              Completeness
            </p>
            <div className="mt-2">
              <StateBadge state={reviewState} />
            </div>
            <p className="mt-1 text-[11px] text-[#45687f]">
              provider · ovs-ofctl adapter
            </p>
          </div>
        </div>
        <div className="flex items-start gap-3 border-t border-[#b9cfdd] bg-[#e2eef6] px-4 py-3 text-xs text-[#254f6e]">
          <LockKeyhole className="mt-0.5 size-4 shrink-0" />
          <p>
            Controller-managed runtime data is read-only here. Standard and
            Expert modes change inspection depth only; neither exposes Edit,
            Delete, or Install Flow.
          </p>
        </div>
      </section>

      <ReviewNotice
        state={reviewState}
        onRefresh={() => {
          setReviewState('fresh');
          setQueryStamp('12:55:18 UTC');
          notify('Fresh OpenFlow snapshot collected');
        }}
        onNarrow={narrowQuery}
        onHealth={() => {
          notify('Provider health context opened in Diagnostics');
          go('diagnostics-hub');
        }}
      />

      <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
        <div className="min-w-0 space-y-4">
          <section className="border bg-card">
            <div className="flex flex-wrap items-start justify-between gap-4 border-b px-4 py-3">
              <div>
                <div className="flex items-center gap-2">
                  <ListFilter className="size-4 text-[#35627f]" />
                  <h2 className="text-sm font-semibold">Bounded query</h2>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  Filters execute against one snapshot and never create a
                  background unbounded export.
                </p>
              </div>
              <Badge variant="outline" className="rounded-sm font-mono">
                5s · 500 rows · 256 KiB
              </Badge>
            </div>
            <div className="grid gap-3 p-4 md:grid-cols-2 xl:grid-cols-4">
              <label
                htmlFor="openflow-bridge"
                className="grid gap-1.5 text-xs font-medium"
              >
                Bridge / datapath
                <NativeSelect
                  id="openflow-bridge"
                  value={bridge}
                  onChange={(event) => setBridge(event.target.value)}
                >
                  <NativeSelectOption value="br-fabric">
                    br-fabric · system
                  </NativeSelectOption>
                  <NativeSelectOption value="br-storage">
                    br-storage · system
                  </NativeSelectOption>
                  <NativeSelectOption value="br-offload">
                    br-offload · netdev
                  </NativeSelectOption>
                </NativeSelect>
              </label>
              <label
                htmlFor="openflow-table"
                className="grid gap-1.5 text-xs font-medium"
              >
                Table
                <NativeSelect
                  id="openflow-table"
                  value={table}
                  onChange={(event) => {
                    setTable(event.target.value);
                    setPage(1);
                  }}
                >
                  <NativeSelectOption value="all">
                    All tables
                  </NativeSelectOption>
                  {[0, 10, 20, 30, 90].map((value) => (
                    <NativeSelectOption key={value} value={String(value)}>
                      Table {value}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </label>
              <label
                htmlFor="openflow-priority"
                className="grid gap-1.5 text-xs font-medium"
              >
                Minimum priority
                <Input
                  id="openflow-priority"
                  value={priority}
                  inputMode="numeric"
                  placeholder="Any"
                  onChange={(event) => {
                    setPriority(event.target.value);
                    setPage(1);
                  }}
                />
              </label>
              <label
                htmlFor="openflow-in-port"
                className="grid gap-1.5 text-xs font-medium"
              >
                in_port
                <Input
                  id="openflow-in-port"
                  value={inPort}
                  placeholder="Any"
                  onChange={(event) => {
                    setInPort(event.target.value);
                    setPage(1);
                  }}
                />
              </label>
              <label
                htmlFor="openflow-match"
                className="grid gap-1.5 text-xs font-medium"
              >
                Match field
                <Input
                  id="openflow-match"
                  value={matchField}
                  placeholder="e.g. dl_dst, ct_state"
                  onChange={(event) => {
                    setMatchField(event.target.value);
                    setPage(1);
                  }}
                />
              </label>
              <label
                htmlFor="openflow-action"
                className="grid gap-1.5 text-xs font-medium"
              >
                Action
                <Input
                  id="openflow-action"
                  value={actionFilter}
                  placeholder="e.g. output, resubmit"
                  onChange={(event) => {
                    setActionFilter(event.target.value);
                    setPage(1);
                  }}
                />
              </label>
              <label
                htmlFor="openflow-query"
                className="grid gap-1.5 text-xs font-medium xl:col-span-2"
              >
                Cookie or text search
                <div className="relative">
                  <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
                  <Input
                    id="openflow-query"
                    value={query}
                    placeholder="e.g. ct_state, output:12"
                    className="pl-9"
                    onChange={(event) => {
                      setQuery(event.target.value);
                      setPage(1);
                    }}
                  />
                </div>
              </label>
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t bg-muted/25 px-4 py-3">
              <div className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-[10px] text-muted-foreground">
                <span>timeout 5 seconds</span>
                <span>result ceiling 500 rows</span>
                <span>snapshot TTL 30 seconds</span>
              </div>
              <div className="flex gap-2">
                <Button variant="ghost" size="sm" onClick={resetFilters}>
                  Reset
                </Button>
                <Button
                  size="sm"
                  onClick={runQuery}
                  className="bg-[#173b63] hover:bg-[#234f80]"
                >
                  <Search /> Run bounded query
                </Button>
              </div>
            </div>
          </section>

          <section className="border bg-card">
            <div className="flex flex-wrap items-start justify-between gap-3 border-b px-4 py-3">
              <div>
                <h2 className="text-sm font-semibold">Flow snapshot</h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  {blocked
                    ? 'No current rows are available for this collection state.'
                    : reviewState === 'truncated'
                      ? '500 retained rows · incomplete result · narrow before relying on absence.'
                      : `${filtered.length} rows · complete within the current bounded query.`}
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <StateBadge state={reviewState} />
                <Button
                  variant="outline"
                  size="sm"
                  disabled={blocked}
                  onClick={() => {
                    setExported(true);
                    notify(
                      `Export prepared · ${bridge}-openflow-${queryStamp.replaceAll(':', '')}.json`,
                    );
                  }}
                >
                  <Download /> Export current result
                </Button>
              </div>
            </div>

            {filtered.length > 0 ? (
              <>
                <div className="hidden overflow-x-auto lg:block">
                  <Table>
                    <TableHeader className="bg-muted/40">
                      <TableRow>
                        <TableHead className="w-16">Table</TableHead>
                        <TableHead className="w-20">Priority</TableHead>
                        {mode === 'expert' && <TableHead>Cookie</TableHead>}
                        <TableHead>Match</TableHead>
                        <TableHead>Instructions / actions</TableHead>
                        <TableHead className="text-right">Packets</TableHead>
                        <TableHead className="text-right">Bytes</TableHead>
                        {mode === 'expert' && <TableHead>Duration</TableHead>}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {visibleRows.map((flow) => (
                        <TableRow
                          key={flow.id}
                          data-state={
                            selected.id === flow.id ? 'selected' : undefined
                          }
                          className="cursor-pointer align-top"
                          tabIndex={0}
                          onClick={() => setSelectedId(flow.id)}
                          onKeyDown={(event) => {
                            if (event.key === 'Enter' || event.key === ' ') {
                              event.preventDefault();
                              setSelectedId(flow.id);
                            }
                          }}
                        >
                          <TableCell className="font-mono text-xs">
                            {flow.table}
                          </TableCell>
                          <TableCell className="font-mono text-xs">
                            {flow.priority}
                          </TableCell>
                          {mode === 'expert' && (
                            <TableCell className="font-mono text-[11px]">
                              {flow.cookie}
                            </TableCell>
                          )}
                          <TableCell className="max-w-[300px] font-mono text-[11px] leading-5">
                            {flow.match}
                          </TableCell>
                          <TableCell className="max-w-[320px] font-mono text-[11px] leading-5">
                            {flow.action}
                          </TableCell>
                          <TableCell className="text-right font-mono text-[11px]">
                            {flow.packets}
                          </TableCell>
                          <TableCell className="text-right font-mono text-[11px]">
                            {flow.bytes}
                          </TableCell>
                          {mode === 'expert' && (
                            <TableCell className="whitespace-nowrap font-mono text-[11px]">
                              {flow.duration}
                            </TableCell>
                          )}
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>

                <div className="grid gap-2 p-3 lg:hidden">
                  {visibleRows.map((flow) => (
                    <button
                      key={flow.id}
                      type="button"
                      onClick={() => setSelectedId(flow.id)}
                      className={`border p-3 text-left ${selected.id === flow.id ? 'border-[#8bb7d1] bg-[#f2f8fc]' : 'bg-white'}`}
                    >
                      <div className="flex items-center justify-between gap-3">
                        <span className="font-mono text-xs font-semibold">
                          table {flow.table} · priority {flow.priority}
                        </span>
                        <span className="font-mono text-[10px] text-muted-foreground">
                          {flow.packets} pkts
                        </span>
                      </div>
                      <p className="mt-2 truncate font-mono text-[11px]">
                        {flow.match}
                      </p>
                      <p className="mt-1 truncate font-mono text-[11px] text-[#35627f]">
                        → {flow.action}
                      </p>
                    </button>
                  ))}
                </div>

                <div className="flex flex-wrap items-center justify-between gap-3 border-t px-4 py-3">
                  <p className="text-xs text-muted-foreground">
                    Page {page} of {pageCount} · {filtered.length} bounded rows
                  </p>
                  <div className="flex gap-1">
                    <Button
                      variant="outline"
                      size="icon-sm"
                      aria-label="Previous flow page"
                      disabled={page === 1}
                      onClick={() =>
                        setPage((current) => Math.max(1, current - 1))
                      }
                    >
                      <ChevronLeft />
                    </Button>
                    <Button
                      variant="outline"
                      size="icon-sm"
                      aria-label="Next flow page"
                      disabled={page === pageCount}
                      onClick={() =>
                        setPage((current) => Math.min(pageCount, current + 1))
                      }
                    >
                      <ChevronRight />
                    </Button>
                  </div>
                </div>
              </>
            ) : (
              <div className="grid min-h-56 place-items-center p-8 text-center">
                <div className="max-w-md">
                  {reviewState === 'empty' ? (
                    <CheckCircle2 className="mx-auto size-6 text-slate-500" />
                  ) : blocked ? (
                    <XCircle className="mx-auto size-6 text-rose-600" />
                  ) : (
                    <Search className="mx-auto size-6 text-slate-500" />
                  )}
                  <h3 className="mt-3 text-sm font-semibold">
                    {reviewState === 'empty'
                      ? '0 matching flows'
                      : blocked
                        ? openFlowReviewLabels[reviewState]
                        : 'No rows match these filters'}
                  </h3>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">
                    {reviewState === 'empty'
                      ? 'Collection succeeded. The provider is healthy and returned a complete empty result.'
                      : blocked
                        ? 'No cached or inferred rows are presented as current data.'
                        : 'Adjust the bounded filters or reset the query.'}
                  </p>
                  {!blocked && reviewState !== 'empty' && (
                    <Button
                      className="mt-4"
                      variant="outline"
                      size="sm"
                      onClick={resetFilters}
                    >
                      Reset filters
                    </Button>
                  )}
                </div>
              </div>
            )}

            {exported && (
              <div className="border-t bg-emerald-50 px-4 py-3 text-xs text-emerald-800">
                Export prepared · current rows, query filters, authority,
                observed_at, duration, and truncation metadata included
              </div>
            )}
          </section>
        </div>

        <aside className="space-y-4">
          <section className="border bg-card">
            <div className="flex items-start justify-between gap-3 border-b px-4 py-3">
              <div>
                <h2 className="text-sm font-semibold">Flow detail</h2>
                <p className="mt-1 font-mono text-[10px] text-muted-foreground">
                  {selected.id}
                </p>
              </div>
              <Badge variant="outline" className="rounded-sm">
                Read-only
              </Badge>
            </div>

            {mode === 'expert' && (
              <div className="grid grid-cols-2 border-b p-1">
                {(['parsed', 'raw'] as const).map((kind) => (
                  <button
                    key={kind}
                    type="button"
                    aria-pressed={detailView === kind}
                    onClick={() => setDetailView(kind)}
                    className={`px-3 py-2 text-xs font-semibold capitalize ${detailView === kind ? 'bg-[#173b63] text-white' : 'text-muted-foreground'}`}
                  >
                    {kind}
                  </button>
                ))}
              </div>
            )}

            {mode === 'expert' && detailView === 'raw' ? (
              <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words p-4 font-mono text-[11px] leading-5 text-slate-700">
                {selected.raw}
              </pre>
            ) : (
              <dl className="space-y-3 p-4 text-xs">
                {[
                  [
                    'Table / priority',
                    `${selected.table} / ${selected.priority}`,
                  ],
                  ...(mode === 'expert'
                    ? ([['Cookie', selected.cookie]] as [string, string][])
                    : []),
                  ['Match', selected.match],
                  ['Instructions / actions', selected.action],
                  [
                    'Counters',
                    `${selected.packets} packets · ${selected.bytes}`,
                  ],
                  ...(mode === 'expert'
                    ? ([['Duration', selected.duration]] as [string, string][])
                    : []),
                ].map(([label, value]) => (
                  <div
                    key={label}
                    className="border-t pt-3 first:border-t-0 first:pt-0"
                  >
                    <dt className="text-muted-foreground">{label}</dt>
                    <dd className="mt-1 break-words font-mono text-[11px] leading-5">
                      {value}
                    </dd>
                  </div>
                ))}
              </dl>
            )}

            <div className="border-t bg-muted/25 p-4">
              <p className="font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground">
                Source & relationship
              </p>
              <p className="mt-2 text-xs">
                ovs-ofctl adapter · OpenFlow 1.5 · {observedAt}
              </p>
              <div className="mt-3 grid gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => openBridge(bridge)}
                >
                  <Network /> Bridge / {bridge}
                  <ExternalLink />
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => openPort(selected.relatedPort)}
                >
                  <ExternalLink /> Port / {selected.relatedPort}
                </Button>
              </div>
            </div>
          </section>

          <section className="border bg-card p-4">
            <div className="flex items-center gap-2">
              <ShieldCheck className="size-4 text-[#35627f]" />
              <h2 className="text-sm font-semibold">Authority contract</h2>
            </div>
            <ul className="mt-3 space-y-2 text-xs leading-5 text-muted-foreground">
              <li>• Controller owns flow lifecycle and intent.</li>
              <li>• WebUI reports observed effective state only.</li>
              <li>• Unknown fields stay verbatim; meaning is not inferred.</li>
              <li>
                • Absence is trustworthy only for a fresh, complete result.
              </li>
            </ul>
            <div className="mt-3 border border-dashed border-slate-300 bg-slate-50 p-3 text-xs text-slate-700">
              No Edit · No Delete · No Install Flow
            </div>
          </section>

          {mode === 'expert' ? (
            <section className="border bg-[#162c43] p-4 text-slate-100">
              <div className="flex items-center gap-2">
                <Code2 className="size-4 text-sky-300" />
                <h2 className="text-sm font-semibold">Expert metadata</h2>
              </div>
              <dl className="mt-3 space-y-2 font-mono text-[10px] leading-5 text-slate-300">
                <div className="flex justify-between gap-4">
                  <dt>provider</dt>
                  <dd>ovs-ofctl adapter</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt>snapshot_id</dt>
                  <dd>ofs-7b21</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt>collection_ms</dt>
                  <dd>184</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt>observed_at</dt>
                  <dd>2026-09-03T12:54:27Z</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt>unknown_fields</dt>
                  <dd>preserve</dd>
                </div>
              </dl>
            </section>
          ) : (
            <section className="border border-dashed bg-card p-4">
              <div className="flex items-center gap-2">
                <FileClock className="size-4 text-[#35627f]" />
                <h2 className="text-sm font-semibold">Standard view</h2>
              </div>
              <p className="mt-2 text-xs leading-5 text-muted-foreground">
                Common filters, summarized match/action, counters, authority,
                and freshness remain visible. Expert adds exact cookie,
                duration, raw output, and collection metadata—not new
                permissions.
              </p>
            </section>
          )}
        </aside>
      </div>
    </>
  );
}
