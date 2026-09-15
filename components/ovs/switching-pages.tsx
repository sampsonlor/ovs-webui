'use client';

import { useEffect, useState, type ReactNode } from 'react';
import {
  ArrowLeft,
  ChevronRight,
  Code2,
  Plus,
  Search,
  ShieldCheck,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Notice,
  PageHeader,
  ScopeBadge,
  StatusBadge,
  type Tone,
} from './foundation';
import {
  bridges,
  bonds,
  currentBridgeChildren,
  bridgeObservation,
  bondObservation,
  bondDraftErrors,
  bondNativeValue,
  bondChangeIntent,
  memberOptions,
  providerStale,
  type Bridge,
  type Bond,
  type BondDraft,
} from '@/lib/switching-model';
import type {
  ChangeIntent,
  P1View,
  PrototypeMode,
} from '@/app/prototype-model';
import type { Scenario } from '@/lib/change-control';
import { UnavailableObject } from './object-context';
import type { View, VlanValue } from '@/lib/ovs-model';

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
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-4">
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

function Properties({
  values,
  stacked = false,
}: {
  values: Array<[string, ReactNode]>;
  stacked?: boolean;
}) {
  return (
    <dl
      className={
        stacked
          ? 'divide-y px-5'
          : 'grid gap-5 p-5 sm:grid-cols-2 2xl:grid-cols-4'
      }
    >
      {values.map(([label, value]) => (
        <div
          key={label}
          className={
            stacked
              ? 'flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-2 py-4'
              : 'min-w-0'
          }
        >
          <dt className="text-sm text-muted-foreground">{label}</dt>
          <dd
            className={
              (stacked ? 'text-right' : 'mt-2') +
              ' min-w-0 break-words text-sm font-medium'
            }
          >
            {value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function State({ value }: { value: string }) {
  const tone: Tone =
    value === 'Up' || value === 'Negotiated'
      ? 'success'
      : value === 'Down' || value === 'Partner mismatch'
        ? 'danger'
        : value === 'Degraded'
          ? 'warning'
          : value === 'Unknown' || value === 'Unverified'
            ? 'uncertain'
            : 'neutral';
  return <StatusBadge tone={tone}>{value}</StatusBadge>;
}

function Freshness({ value }: { value: string }) {
  return (
    <StatusBadge
      tone={
        value.startsWith('Stale')
          ? 'warning'
          : value === 'Evidence unavailable'
            ? 'uncertain'
            : 'info'
      }
    >
      {value}
    </StatusBadge>
  );
}

function ExpertFields({
  title = 'Expert native fields',
  lines,
}: {
  title?: string;
  lines: string[];
}) {
  return (
    <section className="ovs-surface min-w-0 border-dashed p-5">
      <h2 className="flex items-center gap-2 font-semibold">
        <Code2 aria-hidden="true" className="size-4 text-primary" />
        {title}
      </h2>
      <pre className="mt-4 overflow-x-auto rounded border bg-muted/40 p-4 font-mono text-xs leading-6">
        {lines.join('\n')}
      </pre>
    </section>
  );
}

function Hierarchy({
  items,
}: {
  items: Array<{
    kind: string;
    name: string;
    detail: string;
    current?: boolean;
    open?: () => void;
  }>;
}) {
  return (
    <ol
      aria-label="Bridge to Port to Interface relationship"
      className="grid gap-3 p-5 lg:grid-cols-3"
    >
      {items.map((item, index) => (
        <li
          key={item.kind}
          className="ovs-native-object min-w-0 rounded border p-4"
          data-current={item.current || undefined}
        >
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="font-mono">{index + 1}</span>
            {item.kind}
          </p>
          {item.open ? (
            <button
              type="button"
              onClick={item.open}
              className="ovs-object-link mt-3 break-words"
            >
              {item.name}
              <ChevronRight aria-hidden="true" className="ml-1 inline size-4" />
            </button>
          ) : (
            <p className="mt-3 break-words font-mono text-sm font-semibold">
              {item.name}
            </p>
          )}
          <p className="mt-2 break-words text-sm text-muted-foreground">
            {item.detail}
          </p>
        </li>
      ))}
    </ol>
  );
}

function EmptyInventory({
  title,
  clear,
}: {
  title: string;
  clear: () => void;
}) {
  return (
    <div className="px-6 py-12 text-center">
      <Search
        aria-hidden="true"
        className="mx-auto size-6 text-muted-foreground"
      />
      <h2 className="mt-3 font-semibold">{title}</h2>
      <p className="mt-2 text-sm text-muted-foreground">
        The inventory is available. Try a different filter.
      </p>
      <Button variant="outline" className="mt-4" onClick={clear}>
        Clear filters
      </Button>
    </div>
  );
}

function BridgeInventory({
  rows,
  mode,
  scenario,
  open,
}: {
  rows: Bridge[];
  mode: PrototypeMode;
  scenario: Scenario;
  open: (name: string) => void;
}) {
  return (
    <>
      <div className="hidden lg:block">
        <Table className="ovs-data-table">
          <caption className="sr-only">
            Bridge inventory. Open a Bridge name to inspect its native children.
          </caption>
          <TableHeader>
            <TableRow>
              <TableHead>Bridge / datapath</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Native children</TableHead>
              <TableHead>VLANs</TableHead>
              <TableHead>Management scope</TableHead>
              {mode === 'expert' && <TableHead>Authority / identity</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((bridge) => (
              <TableRow key={bridge.name}>
                <TableCell>
                  <button
                    className="ovs-object-link"
                    onClick={() => open(bridge.name)}
                  >
                    {bridge.name}
                  </button>
                  <p className="mt-1 font-mono text-xs text-muted-foreground">
                    {bridge.datapath}
                  </p>
                </TableCell>
                <TableCell>
                  <State value={bridgeObservation(bridge, scenario).state} />
                </TableCell>
                <TableCell>
                  <p>
                    {bridge.ports} Ports · {bridge.interfaces} Interfaces
                  </p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    {bridge.bonds} Bond {bridge.bonds === 1 ? 'Port' : 'Ports'}
                  </p>
                </TableCell>
                <TableCell>{bridge.vlans}</TableCell>
                <TableCell>
                  <ScopeBadge scope={bridge.scope} />
                </TableCell>
                {mode === 'expert' && (
                  <TableCell>
                    <p>
                      {bridge.authority} · {bridge.provider}
                    </p>
                    <p className="mt-1 font-mono text-xs text-muted-foreground">
                      {bridge.uuid}
                    </p>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <div className="divide-y lg:hidden">
        {rows.map((bridge) => (
          <article key={bridge.name} className="p-5">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <button
                className="ovs-object-link"
                onClick={() => open(bridge.name)}
              >
                {bridge.name}
              </button>
              <ScopeBadge scope={bridge.scope} />
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-3 text-sm">
              <State value={bridgeObservation(bridge, scenario).state} />
              <span>{bridge.datapath}</span>
            </div>
            <p className="mt-3 text-sm">
              {bridge.ports} Ports · {bridge.interfaces} Interfaces ·{' '}
              {bridge.bonds} Bonds
            </p>
            <p className="mt-2 text-sm text-muted-foreground">
              VLANs · {bridge.vlans}
            </p>
            {mode === 'expert' && (
              <p className="mt-3 break-words text-xs text-muted-foreground">
                {bridge.authority} · {bridge.provider} · {bridge.uuid}
              </p>
            )}
          </article>
        ))}
      </div>
    </>
  );
}

function SwitchingOverview({
  scenario,
  go,
  openBridge,
}: {
  scenario: Scenario;
  go: (view: P1View) => void;
  openBridge: (name: string) => void;
}) {
  const unknown = bridges.filter(
    (bridge) => bridgeObservation(bridge, scenario).state === 'Unknown',
  ).length;
  const degraded = bridges.filter(
    (bridge) => bridgeObservation(bridge, scenario).state === 'Degraded',
  ).length;
  return (
    <>
      <PageHeader
        id="P1-01"
        eyebrow="Switching / Overview"
        title="Switching overview"
        description="Review switching domains, their native children and the evidence available for each object."
        actions={
          <Button onClick={() => go('bridges')}>
            Open bridges
            <ChevronRight />
          </Button>
        }
      />
      <div
        className="ovs-inventory-summary"
        aria-label="Switching inventory summary"
      >
        <span>
          <strong>{bridges.length}</strong> Bridges
        </span>
        <span>
          <strong>{bridges.reduce((n, bridge) => n + bridge.ports, 0)}</strong>{' '}
          Ports
        </span>
        <span>
          <strong>
            {bridges.reduce((n, bridge) => n + bridge.interfaces, 0)}
          </strong>{' '}
          Interfaces
        </span>
        <span>
          <strong>{bonds.length}</strong> Bonds
        </span>
        <span>
          <StatusBadge tone="uncertain">{unknown} Bridge unknown</StatusBadge>
        </span>
        {degraded > 0 && (
          <span>
            <StatusBadge tone="warning">{degraded} Bridge degraded</StatusBadge>
          </span>
        )}
      </div>
      <Panel
        title="Bridge posture"
        description="Bridge → Port → Interface. A Bond is one Port with member Interfaces."
      >
        <BridgeInventory
          rows={bridges}
          mode="standard"
          scenario={scenario}
          open={openBridge}
        />
      </Panel>
      <div className="mt-5 grid gap-5 xl:grid-cols-2">
        <Panel
          title="Current attention"
          description="Known conditions and missing evidence are shown separately."
        >
          <div className="space-y-4 p-5 text-sm">
            <p>
              <strong>server-08</strong> · Interface down
            </p>
            <p>
              <strong>br-offload</strong> ·{' '}
              {providerStale(scenario)
                ? 'Provider evidence is stale'
                : 'Provider-owned health is unknown'}
            </p>
            {degraded > 0 && (
              <p>
                <strong>
                  {scenario === 'member-down' ? 'bond-storage' : 'bond-uplink'}
                </strong>{' '}
                ·{' '}
                {scenario === 'member-down'
                  ? 'Reduced redundancy'
                  : 'LACP partner mismatch'}
              </p>
            )}
          </div>
        </Panel>
        <Panel
          title="Change workflow"
          description="Configuration intent stays separate from running state."
        >
          <ol className="grid gap-3 p-5 text-sm sm:grid-cols-2">
            {[
              'Prepare Candidate',
              'Review Diff / Validation',
              'Apply with protection',
              'Inspect Event / Audit evidence',
            ].map((label, index) => (
              <li key={label} className="flex gap-3">
                <span className="font-mono text-muted-foreground">
                  {index + 1}.
                </span>
                {label}
              </li>
            ))}
          </ol>
        </Panel>
      </div>
    </>
  );
}

function BridgeList({
  mode,
  scenario,
  search,
  setSearch,
  openBridge,
  stage,
  blocked,
}: {
  mode: PrototypeMode;
  scenario: Scenario;
  search: string;
  setSearch: (value: string) => void;
  openBridge: (name: string) => void;
  stage: (intent: ChangeIntent) => void;
  blocked: boolean;
}) {
  const [authority, setAuthority] = useState('all');
  const shown = bridges.filter(
    (bridge) =>
      (authority === 'all' || bridge.authority.toLowerCase() === authority) &&
      (bridge.name + ' ' + bridge.datapath + ' ' + bridge.vlans)
        .toLowerCase()
        .includes(search.toLowerCase()),
  );
  return (
    <>
      <PageHeader
        id="P1-02"
        eyebrow="Switching / Bridges"
        title="Bridges"
        description="Inspect switching domains, ownership and child objects before preparing a change."
        actions={
          <Button
            disabled={blocked}
            onClick={() =>
              stage({
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
              })
            }
          >
            <Plus />
            New bridge intent
          </Button>
        }
      />
      <div
        className="ovs-inventory-summary"
        aria-label="Bridge inventory summary"
      >
        <span>
          <strong>{bridges.length}</strong> Bridges
        </span>
        <span>3 managed · 1 observed</span>
        <span>
          <StatusBadge tone="uncertain">1 with unknown evidence</StatusBadge>
        </span>
      </div>
      <div className="ovs-toolbar">
        <div className="relative min-w-0 flex-1">
          <Search
            aria-hidden="true"
            className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            aria-label="Search bridges"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search bridge, datapath, or VLAN…"
            className="h-10 pl-9"
          />
        </div>
        <NativeSelect
          aria-label="Filter bridge authority"
          value={authority}
          onChange={(event) => setAuthority(event.target.value)}
        >
          <NativeSelectOption value="all">All authorities</NativeSelectOption>
          <NativeSelectOption value="ovs">OVS managed</NativeSelectOption>
          <NativeSelectOption value="external">External</NativeSelectOption>
        </NativeSelect>
      </div>
      <section
        className="ovs-surface overflow-hidden"
        aria-label="Bridge inventory"
      >
        <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-4">
          <output className="text-sm text-muted-foreground">
            {shown.length} of {bridges.length} Bridges
          </output>
          <Freshness
            value={
              providerStale(scenario)
                ? 'Stale provider evidence'
                : 'Synthetic snapshot · 8s'
            }
          />
        </div>
        {shown.length ? (
          <BridgeInventory
            rows={shown}
            mode={mode}
            scenario={scenario}
            open={openBridge}
          />
        ) : (
          <EmptyInventory
            title="No matching bridges"
            clear={() => {
              setSearch('');
              setAuthority('all');
            }}
          />
        )}
      </section>
      <p className="mt-4 text-sm text-muted-foreground">
        Observe-only Bridges remain inspectable. Configuration changes use the
        shared Candidate workspace.
      </p>
    </>
  );
}

function BridgeDetail({
  bridge,
  mode,
  scenario,
  go,
  openObject,
  live,
  createBond,
  stage,
  blocked,
}: {
  bridge: Bridge;
  mode: PrototypeMode;
  scenario: Scenario;
  go: (view: P1View) => void;
  openObject: (target: string) => void;
  live: Record<string, VlanValue>;
  createBond: (name: string) => void;
  stage: (intent: ChangeIntent) => void;
  blocked: boolean;
}) {
  const editable = bridge.scope === 'Manage';
  const observation = bridgeObservation(bridge, scenario);
  const children = currentBridgeChildren(bridge.name, live);
  const stageRstp = () =>
    stage({
      kind: 'bridge',
      objectType: 'Bridge',
      objectName: bridge.name,
      title: 'Update Bridge / ' + bridge.name,
      summary: (bridge.rstp === 'Enabled' ? 'Disable' : 'Enable') + ' RSTP',
      current: 'rstp_enable: ' + (bridge.rstp === 'Enabled'),
      candidate: 'rstp_enable: ' + (bridge.rstp !== 'Enabled'),
      risk: bridge.name === 'br-mgmt' ? 'High' : 'Medium',
      capability: 'bridge.manage',
      evidenceObject: 'Bridge/' + bridge.name,
      bridgeName: bridge.name,
    });
  return (
    <>
      <PageHeader
        id="P1-03"
        eyebrow="Switching / Bridge detail"
        title={bridge.name}
        scope={bridge.scope}
        description="Review observed posture, native children and the configuration owned by this Bridge."
        actions={
          <>
            <Button variant="outline" onClick={() => go('bridges')}>
              <ArrowLeft />
              Bridges
            </Button>
            {editable && (
              <>
                <Button
                  variant="outline"
                  disabled={blocked}
                  onClick={stageRstp}
                >
                  Stage RSTP change
                </Button>
                <Button
                  disabled={blocked}
                  onClick={() => createBond(bridge.name)}
                >
                  <Plus />
                  Add bond
                </Button>
              </>
            )}
          </>
        }
      />
      {!editable && (
        <Notice tone="uncertain" title="Provider-controlled Bridge">
          {bridge.provider} owns this object. Member evidence is{' '}
          {providerStale(scenario) ? 'stale' : 'unavailable'}; unknown values
          remain unknown. Expert adds native detail without enabling
          configuration.
        </Notice>
      )}
      {bridge.name === 'br-mgmt' && (
        <Notice tone="warning" title="Management-plane Bridge">
          Connectivity changes require Safe Apply. The active management route
          prevents deleting this Bridge.
        </Notice>
      )}
      <div className="mt-5 grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-5">
          <Panel
            title="Operational summary"
            actions={<Freshness value={observation.freshness} />}
          >
            <Properties
              values={[
                ['State', <State key="state" value={observation.state} />],
                ['Datapath', bridge.datapath],
                ['VLAN domain', bridge.vlans],
                ['RSTP intent', bridge.rstp],
              ]}
            />
          </Panel>
          <Panel title="Native object hierarchy">
            <Hierarchy
              items={[
                {
                  kind: 'Bridge',
                  name: bridge.name,
                  detail: 'Logical switching domain',
                  current: true,
                },
                {
                  kind: 'Port children',
                  name: bridge.ports + ' Ports · ' + bridge.bonds + ' Bonds',
                  detail: 'Open Bond/LACP inventory',
                  open: () => go('bonds'),
                },
                {
                  kind: 'Interface children',
                  name: bridge.interfaces + ' Interfaces',
                  detail: 'Native attachment points',
                },
              ]}
            />
          </Panel>
          <Panel
            title="Representative Port children"
            description="Every Port counted for this Bridge is listed in this shared bounded snapshot."
          >
            <div className="hidden lg:block">
              <Table className="ovs-data-table">
                <caption className="sr-only">
                  Representative Port children of {bridge.name}.
                </caption>
                <TableHeader>
                  <TableRow>
                    <TableHead>Port / kind</TableHead>
                    <TableHead>Interfaces</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead>VLAN intent</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {children.map((child) => {
                    const bond = bonds.find((item) => item.name === child.name);
                    return (
                      <TableRow key={child.name}>
                        <TableCell>
                          <button
                            className="ovs-object-link"
                            onClick={() => openObject(`Port/${child.name}`)}
                          >
                            {child.name}
                          </button>
                          <p className="mt-1 text-xs text-muted-foreground">
                            {child.kind}
                          </p>
                        </TableCell>
                        <TableCell className="font-mono text-xs">
                          {child.interfaces.split(', ').map((name) => (
                            <button
                              key={name}
                              className="ovs-object-link block"
                              onClick={() => openObject(`Interface/${name}`)}
                            >
                              {name}
                            </button>
                          ))}
                        </TableCell>
                        <TableCell>
                          <State
                            value={
                              bond
                                ? bondObservation(bond, scenario).state
                                : child.state
                            }
                          />
                        </TableCell>
                        <TableCell>{child.vlan}</TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            <div className="divide-y lg:hidden">
              {children.map((child) => {
                const bond = bonds.find((item) => item.name === child.name);
                return (
                  <article key={child.name} className="p-5">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <button
                        className="ovs-object-link"
                        onClick={() => openObject(`Port/${child.name}`)}
                      >
                        {child.name}
                      </button>
                      <State
                        value={
                          bond
                            ? bondObservation(bond, scenario).state
                            : child.state
                        }
                      />
                    </div>
                    <p className="mt-3 text-sm">
                      {child.kind} · {child.vlan}
                    </p>
                    <p className="mt-2 break-words font-mono text-xs text-muted-foreground">
                      {child.interfaces.split(', ').map((name) => (
                        <button
                          key={name}
                          className="ovs-object-link block"
                          onClick={() => openObject(`Interface/${name}`)}
                        >
                          {name}
                        </button>
                      ))}
                    </p>
                  </article>
                );
              })}
            </div>
          </Panel>
        </div>
        <aside className="min-w-0 space-y-5">
          <Panel title="Identity & authority">
            <Properties
              stacked
              values={[
                ['OVSDB configuration authority', bridge.authority],
                ['Provider', bridge.provider],
                ['Generation', '1842 · fixture'],
                [
                  'Configuration',
                  editable ? 'Candidate required' : 'Observe only',
                ],
              ]}
            />
          </Panel>
          <Notice tone="info" title="Protected configuration">
            RSTP and membership changes enter Candidate, then Diff / Validation.
            Management risk selects Safe Apply.
          </Notice>
          {mode === 'expert' && (
            <ExpertFields
              lines={[
                '_uuid: ' + bridge.uuid,
                'name: ' + bridge.name,
                'datapath_type: ' + bridge.datapath,
                'rstp_enable: ' +
                  (bridge.rstp === 'Unknown'
                    ? 'unknown'
                    : String(bridge.rstp === 'Enabled')),
              ]}
            />
          )}
        </aside>
      </div>
    </>
  );
}

function BondList({
  mode,
  scenario,
  selectedBridge,
  openBond,
  createBond,
  blocked,
}: {
  mode: PrototypeMode;
  scenario: Scenario;
  selectedBridge: string;
  openBond: (bond: Bond) => void;
  createBond: (name: string) => void;
  blocked: boolean;
}) {
  const [bridgeFilter, setBridgeFilter] = useState(
    bridges.some((bridge) => bridge.name === selectedBridge)
      ? selectedBridge
      : 'all',
  );
  const shown = bonds.filter(
    (bond) => bridgeFilter === 'all' || bond.bridge === bridgeFilter,
  );
  return (
    <>
      <PageHeader
        id="P1-04"
        eyebrow="Switching / Bonds / LACP"
        title="Bonds / LACP"
        description="Review Bond Ports, configured membership and observed negotiation before staging a change."
        actions={
          <Button
            disabled={blocked}
            onClick={() =>
              createBond(
                bridges.some(
                  (bridge) =>
                    bridge.name === bridgeFilter && bridge.scope === 'Manage',
                )
                  ? bridgeFilter
                  : 'br-fabric',
              )
            }
          >
            <Plus />
            New bond intent
          </Button>
        }
      />
      <div
        className="ovs-inventory-summary"
        aria-label="Bond inventory summary"
      >
        <span>
          <strong>{bonds.length}</strong> Bond Ports
        </span>
        <span>2 managed · 1 observed</span>
        <span>
          <strong>
            {bonds.reduce((n, bond) => n + bond.members.length, 0)}
          </strong>{' '}
          member Interfaces
        </span>
      </div>
      <div className="ovs-toolbar">
        <NativeSelect
          aria-label="Filter bonds by bridge"
          value={bridgeFilter}
          onChange={(event) => setBridgeFilter(event.target.value)}
        >
          <NativeSelectOption value="all">All bridges</NativeSelectOption>
          {bridges.map((bridge) => (
            <NativeSelectOption key={bridge.name} value={bridge.name}>
              {bridge.name}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </div>
      <section
        className="ovs-surface overflow-hidden"
        aria-label="Bond inventory"
      >
        <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-4">
          <output className="text-sm text-muted-foreground">
            {shown.length} of {bonds.length} Bond Ports
          </output>
          <Freshness
            value={
              providerStale(scenario)
                ? 'Stale provider evidence'
                : 'Synthetic snapshot · 8s'
            }
          />
        </div>
        {!shown.length ? (
          <EmptyInventory
            title="No Bonds in this Bridge"
            clear={() => setBridgeFilter('all')}
          />
        ) : (
          <>
            <div className="hidden lg:block">
              <Table className="ovs-data-table">
                <caption className="sr-only">
                  Bond Ports and member evidence. Open a Bond name to inspect
                  its Interfaces.
                </caption>
                <TableHeader>
                  <TableRow>
                    <TableHead>Bond Port / Bridge</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead>Bond mode</TableHead>
                    <TableHead>LACP</TableHead>
                    <TableHead>Members / capacity</TableHead>
                    <TableHead>Management scope</TableHead>
                    {mode === 'expert' && (
                      <TableHead>Native identity</TableHead>
                    )}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((bond) => {
                    const observation = bondObservation(bond, scenario);
                    return (
                      <TableRow key={bond.name}>
                        <TableCell>
                          <button
                            className="ovs-object-link"
                            onClick={() => openBond(bond)}
                          >
                            {bond.name}
                          </button>
                          <p className="mt-1 font-mono text-xs text-muted-foreground">
                            {bond.bridge}
                          </p>
                        </TableCell>
                        <TableCell>
                          <State value={observation.state} />
                        </TableCell>
                        <TableCell className="font-mono text-sm">
                          {bond.mode}
                        </TableCell>
                        <TableCell>
                          <State value={observation.negotiation} />
                          <p className="mt-1 text-xs text-muted-foreground">
                            Intent · {bond.lacp}
                          </p>
                        </TableCell>
                        <TableCell>
                          <p>{bond.members.length} Interfaces</p>
                          <p className="mt-1 text-sm text-muted-foreground">
                            {observation.capacity}
                          </p>
                        </TableCell>
                        <TableCell>
                          <ScopeBadge scope={bond.scope} />
                        </TableCell>
                        {mode === 'expert' && (
                          <TableCell>
                            <p className="font-mono text-xs">{bond.uuid}</p>
                            <p className="mt-1 text-xs text-muted-foreground">
                              min-links · {bond.minLinks ?? 'Unknown'}
                            </p>
                          </TableCell>
                        )}
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
            <div className="divide-y lg:hidden">
              {shown.map((bond) => {
                const observation = bondObservation(bond, scenario);
                return (
                  <article key={bond.name} className="p-5">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <button
                        className="ovs-object-link"
                        onClick={() => openBond(bond)}
                      >
                        {bond.name}
                      </button>
                      <ScopeBadge scope={bond.scope} />
                    </div>
                    <div className="mt-3 flex flex-wrap gap-3">
                      <State value={observation.state} />
                      <State value={observation.negotiation} />
                    </div>
                    <p className="mt-3 text-sm">
                      {bond.bridge} · {bond.mode} · LACP {bond.lacp}
                    </p>
                    <p className="mt-2 text-sm text-muted-foreground">
                      {bond.members.length} Interfaces · {observation.capacity}
                    </p>
                    {mode === 'expert' && (
                      <p className="mt-3 font-mono text-xs text-muted-foreground">
                        {bond.uuid} · min-links {bond.minLinks ?? 'Unknown'}
                      </p>
                    )}
                  </article>
                );
              })}
            </div>
          </>
        )}
      </section>
      <p className="mt-4 text-sm text-muted-foreground">
        Active-backup capacity counts the active member. Missing evidence does
        not imply a healthy link or a zero traffic sample.
      </p>
    </>
  );
}

function BondDetail({
  bond,
  mode,
  scenario,
  go,
  openBridge,
  openObject,
  diagnose,
  blocked,
}: {
  bond: Bond;
  mode: PrototypeMode;
  scenario: Scenario;
  go: (view: P1View) => void;
  openBridge: (name: string) => void;
  openObject: (target: string) => void;
  diagnose: (scope: string) => void;
  blocked: boolean;
}) {
  const observation = bondObservation(bond, scenario);
  const advanced =
    bond.name === 'bond-uplink' && scenario === 'advanced-config';
  return (
    <>
      <PageHeader
        id="P1-06"
        eyebrow="Switching / Bond detail"
        title={bond.name}
        scope={bond.scope}
        description="Compare desired Bond configuration with observed member and LACP evidence."
        actions={
          <>
            <Button variant="outline" onClick={() => go('bonds')}>
              <ArrowLeft />
              Bonds
            </Button>
            <Button
              variant="outline"
              onClick={() => diagnose('Port/' + bond.name)}
            >
              Run diagnostic
            </Button>
            {bond.scope === 'Manage' && (
              <Button disabled={blocked} onClick={() => go('bond-edit')}>
                Edit bond intent
              </Button>
            )}
          </>
        }
      />
      {observation.memberDown && (
        <Notice tone="warning" title={`Member ${bond.members[1]} is down`}>
          {bond.name} remains active through {bond.members[0]}. Redundancy is
          reduced; configured membership is unchanged.
        </Notice>
      )}
      {observation.mismatch && (
        <Notice tone="danger" title="LACP partner mismatch">
          Partner aggregation is incompatible. Corrective intent can be staged,
          but Apply remains blocked until validation succeeds.
        </Notice>
      )}
      {observation.unknown && (
        <Notice
          tone={providerStale(scenario) ? 'warning' : 'uncertain'}
          title={
            providerStale(scenario)
              ? 'Provider evidence is stale'
              : 'Member evidence unavailable'
          }
        >
          Link, forwarding role, LACP and traffic are unknown. {bond.provider}{' '}
          controls this object; configuration remains Observe-only in both
          modes.
        </Notice>
      )}
      {advanced && (
        <Notice tone="warning" title="Advanced native option is active">
          bond-rebalance-interval is outside the Standard field set. Expert
          review preserves its value before staging.
        </Notice>
      )}
      <div className="mt-5 grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-5">
          <Panel
            title="Observed posture"
            actions={<Freshness value={observation.freshness} />}
          >
            <Properties
              values={[
                ['State', <State key="state" value={observation.state} />],
                [
                  'Partner state',
                  <State key="partner" value={observation.negotiation} />,
                ],
                ['Capacity', observation.capacity],
                ['Configured members', bond.members.length + ' Interfaces'],
              ]}
            />
          </Panel>
          <Panel title="OVS object relationship">
            <Hierarchy
              items={[
                {
                  kind: 'Bridge',
                  name: bond.bridge,
                  detail: 'Open parent Bridge',
                  open: () => openBridge(bond.bridge),
                },
                {
                  kind: 'Port · Bond',
                  name: bond.name,
                  detail: bond.mode + ' · LACP ' + bond.lacp,
                  current: true,
                },
                {
                  kind: 'Interface members',
                  name: bond.members.length + ' native children',
                  detail: bond.members.join(' · '),
                },
              ]}
            />
          </Panel>
          <Panel
            title="Member health"
            description="Observed state does not rewrite configured membership."
          >
            <div className="hidden lg:block">
              <Table className="ovs-data-table">
                <caption className="sr-only">
                  Member Interface evidence for {bond.name}.
                </caption>
                <TableHeader>
                  <TableRow>
                    <TableHead>Interface</TableHead>
                    <TableHead>Link</TableHead>
                    <TableHead>Speed</TableHead>
                    <TableHead>Observed role</TableHead>
                    <TableHead>LACP</TableHead>
                    <TableHead>Traffic sample</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {observation.members.map((member) => (
                    <TableRow key={member.name}>
                      <TableCell className="font-mono text-sm font-medium">
                        <button
                          className="ovs-object-link"
                          onClick={() => openObject(`Interface/${member.name}`)}
                        >
                          {member.name}
                        </button>
                      </TableCell>
                      <TableCell>
                        <State value={member.link} />
                      </TableCell>
                      <TableCell>{member.speed}</TableCell>
                      <TableCell>{member.role}</TableCell>
                      <TableCell>{member.lacp}</TableCell>
                      <TableCell className="font-mono text-sm">
                        {member.traffic}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="divide-y lg:hidden">
              {observation.members.map((member) => (
                <article key={member.name} className="p-5">
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <button
                      className="ovs-object-link"
                      onClick={() => openObject(`Interface/${member.name}`)}
                    >
                      {member.name}
                    </button>
                    <State value={member.link} />
                  </div>
                  <dl className="mt-4 grid grid-cols-2 gap-3 text-sm">
                    {[
                      ['Speed', member.speed],
                      ['Observed role', member.role],
                      ['LACP', member.lacp],
                      ['Traffic sample', member.traffic],
                    ].map(([label, value]) => (
                      <div key={label}>
                        <dt className="text-muted-foreground">{label}</dt>
                        <dd className="mt-1">{value}</dd>
                      </div>
                    ))}
                  </dl>
                </article>
              ))}
            </div>
          </Panel>
        </div>
        <aside className="min-w-0 space-y-5">
          <Panel title="Desired configuration">
            <Properties
              stacked
              values={[
                ['Bond mode', bond.mode],
                ['LACP intent', bond.lacp],
                ['Minimum links', bond.minLinks ?? 'Unknown'],
                ['Provider', bond.provider],
                ['OVSDB configuration authority', bond.authority],
              ]}
            />
          </Panel>
          <Notice tone="info" title="Shared change control">
            Membership changes enter Candidate. Validation, Safe Apply and Event
            / Audit evidence use the same resources as other switching changes.
          </Notice>
          {mode === 'expert' && (
            <ExpertFields
              lines={[
                '_uuid: ' + bond.uuid,
                'Port.name: ' + bond.name,
                'Port.bond_mode: ' + bond.mode,
                'Port.lacp: ' + bond.lacp,
                'Port.other_config:min-links: ' + (bond.minLinks ?? 'unknown'),
                ...(advanced
                  ? ['Port.other_config:bond-rebalance-interval: 10000']
                  : []),
              ]}
            />
          )}
        </aside>
      </div>
    </>
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
        <p id={id + '-error'} className="mt-2 text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

function BondEditor({
  bond,
  initialBridge,
  mode,
  scenario,
  go,
  stage,
  blocked,
}: {
  bond?: Bond;
  initialBridge: string;
  mode: PrototypeMode;
  scenario: Scenario;
  go: (view: P1View) => void;
  stage: (intent: ChangeIntent) => void;
  blocked: boolean;
}) {
  const creating = !bond;
  const parent = bridges.some(
    (bridge) => bridge.name === initialBridge && bridge.scope === 'Manage',
  )
    ? initialBridge
    : 'br-fabric';
  const [draft, setDraft] = useState<BondDraft>({
    name: bond?.name ?? 'bond-edge-02',
    bridge: bond?.bridge ?? parent,
    mode: bond?.mode ?? 'balance-tcp',
    lacp: bond?.lacp ?? 'active',
    minLinks: bond ? (bond.minLinks == null ? '' : String(bond.minLinks)) : '1',
    members:
      bond?.members ??
      (parent === 'br-fabric' ? ['enp65s0f2', 'enp65s0f3'] : []),
  });
  const update = (patch: Partial<BondDraft>) =>
    setDraft((current) => ({ ...current, ...patch }));
  const options = memberOptions(draft.bridge, bond);
  const errors = bondDraftErrors(draft, bond);
  const advanced =
    !creating && bond.name === 'bond-uplink' && scenario === 'advanced-config';
  const advancedLocked = advanced && mode === 'standard';
  const readOnly = bond?.scope === 'Observe';
  const native = (value: BondDraft) => bondNativeValue(value, advanced);
  const before = bond
    ? native({
        name: bond.name,
        bridge: bond.bridge,
        mode: bond.mode,
        lacp: bond.lacp,
        minLinks: String(bond.minLinks ?? 'unknown'),
        members: bond.members,
      })
    : 'object: absent';
  const candidate = native(draft);
  const unchanged = before === candidate;
  const cannotStage =
    blocked ||
    readOnly ||
    advancedLocked ||
    Object.values(errors).some(Boolean) ||
    unchanged;
  const submit = () => {
    if (cannotStage) return;
    stage(bondChangeIntent(draft, bond, advanced, scenario === 'member-down'));
  };
  return (
    <>
      <PageHeader
        id="P1-05"
        eyebrow="Switching / Bond / Candidate editor"
        title={(creating ? 'Create' : 'Edit') + ' Bond / LACP'}
        description="Prepare one Bond Port and its member Interfaces. Review and validate the Candidate before Apply."
        scope={readOnly ? 'Observe' : 'Manage'}
        actions={
          <Button
            variant="outline"
            onClick={() => go(creating ? 'bonds' : 'bond-detail')}
          >
            <ArrowLeft />
            Cancel
          </Button>
        }
      />
      {readOnly && (
        <Notice tone="uncertain" title="Provider-controlled Bond">
          This object is Observe-only. Expert mode does not grant write
          authority.
        </Notice>
      )}
      {advanced && (
        <Notice tone="warning" title="Advanced native field detected">
          bond-rebalance-interval = 10000 will be preserved.{' '}
          {advancedLocked
            ? 'Review it in Expert mode before staging.'
            : 'The retained value is included in the native mapping.'}
        </Notice>
      )}
      {scenario === 'lacp-mismatch' && (
        <Notice tone="danger" title="Observed LACP partner mismatch">
          You may prepare corrective intent. Apply requires fresh, compatible
          partner evidence and successful validation.
        </Notice>
      )}
      <div className="mt-5 grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <form
          className="ovs-surface min-w-0"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <div className="flex flex-wrap items-center justify-between gap-3 border-b p-5">
            <div>
              <h2 className="font-semibold">Bond intent</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Port configuration and Interface membership
              </p>
            </div>
            <StatusBadge tone="info">Candidate only</StatusBadge>
          </div>
          <fieldset
            disabled={blocked || readOnly || advancedLocked}
            className="min-w-0"
          >
            <legend className="sr-only">Bond configuration</legend>
            <div className="grid gap-5 p-5 sm:grid-cols-2">
              <Field id="bond-name" label="Bond Port name" error={errors.name}>
                <Input
                  id="bond-name"
                  value={draft.name}
                  disabled={!creating}
                  aria-invalid={!!errors.name}
                  aria-describedby={errors.name ? 'bond-name-error' : undefined}
                  onChange={(event) => update({ name: event.target.value })}
                  className="h-10"
                />
              </Field>
              <Field
                id="bond-bridge"
                label="Parent Bridge"
                error={errors.bridge}
              >
                <NativeSelect
                  id="bond-bridge"
                  value={draft.bridge}
                  disabled={!creating}
                  aria-invalid={!!errors.bridge}
                  aria-describedby={
                    errors.bridge ? 'bond-bridge-error' : undefined
                  }
                  onChange={(event) =>
                    update({ bridge: event.target.value, members: [] })
                  }
                  className="h-10 w-full"
                >
                  {bridges
                    .filter(
                      (bridge) =>
                        bridge.scope === 'Manage' ||
                        bridge.name === bond?.bridge,
                    )
                    .map((bridge) => (
                      <NativeSelectOption key={bridge.name} value={bridge.name}>
                        {bridge.name}
                      </NativeSelectOption>
                    ))}
                </NativeSelect>
              </Field>
              <Field id="bond-mode" label="Bond mode">
                <NativeSelect
                  id="bond-mode"
                  value={draft.mode}
                  onChange={(event) =>
                    update({ mode: event.target.value as Bond['mode'] })
                  }
                  className="h-10 w-full"
                >
                  {['balance-tcp', 'active-backup', 'balance-slb'].map(
                    (value) => (
                      <NativeSelectOption key={value} value={value}>
                        {value}
                      </NativeSelectOption>
                    ),
                  )}
                </NativeSelect>
              </Field>
              <Field id="bond-lacp" label="LACP" error={errors.policy}>
                <NativeSelect
                  id="bond-lacp"
                  value={draft.lacp}
                  aria-invalid={!!errors.policy}
                  aria-describedby={
                    errors.policy ? 'bond-lacp-error' : undefined
                  }
                  onChange={(event) =>
                    update({ lacp: event.target.value as Bond['lacp'] })
                  }
                  className="h-10 w-full"
                >
                  {['active', 'passive', 'off'].map((value) => (
                    <NativeSelectOption key={value} value={value}>
                      {value}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </Field>
              <Field
                id="bond-min-links"
                label="Minimum active links"
                error={errors.minLinks}
              >
                <Input
                  id="bond-min-links"
                  type="number"
                  min="0"
                  max={draft.members.length}
                  value={draft.minLinks}
                  placeholder={readOnly ? 'Unknown' : undefined}
                  aria-invalid={!!errors.minLinks}
                  aria-describedby={
                    errors.minLinks ? 'bond-min-links-error' : undefined
                  }
                  onChange={(event) => update({ minLinks: event.target.value })}
                  className="h-10"
                />
              </Field>
              <Field id="bond-hash-policy" label="Hash policy">
                <Input
                  id="bond-hash-policy"
                  readOnly
                  value={
                    draft.mode === 'balance-tcp'
                      ? 'L2 / L3 / L4'
                      : draft.mode === 'active-backup'
                        ? 'Active member only'
                        : 'Source MAC + VLAN'
                  }
                  className="h-10"
                />
              </Field>
            </div>
            <div className="border-t p-5">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <h3 className="font-semibold">Interface members</h3>
                  <p className="mt-1 text-sm text-muted-foreground">
                    Keep current members or select a host attachment candidate.
                  </p>
                </div>
                <StatusBadge tone={errors.members ? 'warning' : 'info'}>
                  {draft.members.length} selected
                </StatusBadge>
              </div>
              <div className="mt-4 grid gap-3 sm:grid-cols-2">
                {options.map((option) => {
                  const checked = draft.members.includes(option.name);
                  return (
                    <label
                      key={option.name}
                      htmlFor={'bond-member-' + option.name}
                      className="ovs-native-object flex min-w-0 items-start gap-3 rounded border p-4"
                      data-current={checked || undefined}
                    >
                      <Checkbox
                        id={'bond-member-' + option.name}
                        aria-label={'Select Interface ' + option.name}
                        checked={checked}
                        disabled={
                          !option.available ||
                          blocked ||
                          readOnly ||
                          advancedLocked
                        }
                        onCheckedChange={(value) =>
                          update({
                            members: value
                              ? [...draft.members, option.name]
                              : draft.members.filter(
                                  (member) => member !== option.name,
                                ),
                          })
                        }
                        className="mt-0.5"
                      />
                      <span className="min-w-0">
                        <span className="block break-words font-mono text-sm font-medium">
                          {option.name}
                        </span>
                        <span className="mt-2 block text-xs text-muted-foreground">
                          {option.owner === bond?.name && option.owner
                            ? 'Current Bond member'
                            : option.owner
                              ? 'Assigned to ' + option.owner
                              : 'Host attachment candidate · system'}
                        </span>
                      </span>
                    </label>
                  );
                })}
              </div>
              {errors.members && (
                <p className="mt-3 text-sm text-destructive">
                  {errors.members}
                </p>
              )}
            </div>
          </fieldset>
          {mode === 'expert' && (
            <div className="px-5 pb-5">
              <ExpertFields
                title="Expert native mapping"
                lines={['Port.name: ' + draft.name, candidate]}
              />
            </div>
          )}
          <div className="flex flex-wrap items-center justify-between gap-3 border-t p-5">
            <p className="text-sm text-muted-foreground">
              {unchanged
                ? 'No changes to stage.'
                : 'Full validation follows in the shared workspace.'}
            </p>
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => go(creating ? 'bonds' : 'bond-detail')}
              >
                Discard
              </Button>
              <Button type="submit" disabled={cannotStage}>
                Add to workspace
                <ChevronRight />
              </Button>
            </div>
          </div>
        </form>
        <aside className="min-w-0 space-y-5">
          <Panel title="Structure preview">
            <div className="p-5">
              <p className="ovs-eyebrow">Bridge</p>
              <p className="mt-2 break-words font-mono text-sm font-semibold">
                {draft.bridge}
              </p>
              <div className="mt-4 border-l-2 pl-4">
                <p className="ovs-eyebrow">Port · Bond</p>
                <p className="mt-2 break-words font-mono text-sm font-semibold">
                  {draft.name || 'Name required'}
                </p>
                <ul
                  aria-label="Candidate member Interfaces"
                  className="mt-4 space-y-2 border-l-2 pl-4"
                >
                  {draft.members.map((member) => (
                    <li key={member} className="break-words font-mono text-sm">
                      {member}
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </Panel>
          <Panel
            title="Pre-stage checks"
            description="Checks use the bounded synthetic inventory."
          >
            <div className="space-y-3 p-5">
              {[
                ['Port name', errors.name],
                ['Parent Bridge', errors.bridge],
                ['Interface membership', errors.members],
                ['LACP policy', errors.policy],
                ['Minimum links', errors.minLinks],
              ].map(([label, error]) => (
                <div key={label} className="flex items-start gap-3 text-sm">
                  <span
                    className={
                      'mt-1 size-2 shrink-0 rounded-full ' +
                      (error ? 'bg-destructive' : 'bg-primary')
                    }
                    aria-hidden="true"
                  />
                  <span className="min-w-0 break-words">
                    {label}
                    <span className="mt-1 block text-xs text-muted-foreground">
                      {error ? 'Needs review' : 'Valid in sample'}
                    </span>
                  </span>
                </div>
              ))}
            </div>
          </Panel>
          <Notice tone="info" title="Connectivity protection">
            <ShieldCheck aria-hidden="true" className="mr-2 inline size-4" />
            Membership changes require validation and Safe Apply before they
            become retained configuration.
          </Notice>
        </aside>
      </div>
    </>
  );
}

function ResourceState({
  view,
  scenario,
}: {
  view: P1View;
  scenario: Scenario;
}) {
  const label = view.startsWith('bridge')
    ? 'Bridges'
    : view.startsWith('bond')
      ? 'Bonds / LACP'
      : 'Switching';
  const titles: Record<string, string> = {
    loading: 'Loading switching inventory',
    empty: 'No objects in this inventory',
    error: 'Unable to load switching inventory',
    'permission-denied': 'Permission required',
    'provider-unavailable': 'Inventory provider unavailable',
  };
  return (
    <>
      <PageHeader
        id="P1-resource"
        eyebrow="Switching / Inventory"
        title={label}
        description="Read object identity, ownership and observed state before preparing intent."
      />
      {scenario === 'loading' ? (
        <section
          className="ovs-surface mt-5 space-y-4 p-5"
          aria-label="Loading switching inventory"
          aria-busy="true"
        >
          <p className="text-sm">Loading switching inventory…</p>
          {[1, 2, 3].map((row) => (
            <Skeleton key={row} className="h-14 w-full" />
          ))}
        </section>
      ) : (
        <Notice
          tone={
            scenario === 'permission-denied'
              ? 'danger'
              : scenario === 'empty'
                ? 'neutral'
                : 'uncertain'
          }
          title={titles[scenario] ?? 'Inventory unavailable'}
        >
          {scenario === 'empty'
            ? 'The inventory request succeeded and returned no objects.'
            : scenario === 'permission-denied'
              ? 'switching.read is required. Object details are withheld in both modes.'
              : 'A successful inventory response is unavailable. This does not mean the inventory is empty.'}
        </Notice>
      )}
    </>
  );
}

export function P1SwitchingView({
  view,
  mode,
  scenario,
  stageBlock,
  search,
  setSearch,
  selectedBridge,
  setSelectedBridge,
  selectedBond,
  setSelectedBond,
  go,
  onStageIntent,
  onDiagnose,
  openObject,
  live,
}: {
  view: P1View;
  mode: PrototypeMode;
  scenario: Scenario;
  stageBlock: string | null;
  search: string;
  setSearch: (value: string) => void;
  selectedBridge: string;
  setSelectedBridge: (value: string) => void;
  selectedBond: string;
  setSelectedBond: (value: string) => void;
  go: (view: View) => void;
  onStageIntent: (intent: ChangeIntent) => void;
  onDiagnose: (scope: string) => void;
  openObject: (target: string) => void;
  live: Record<string, VlanValue>;
}) {
  const [desktop, setDesktop] = useState(false);
  useEffect(() => {
    const query = window.matchMedia('(min-width: 1024px)');
    const update = () => setDesktop(query.matches);
    update();
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);
  const bridge = bridges.find((item) => item.name === selectedBridge);
  const bond = bonds.find((item) => item.name === selectedBond);
  const openBridge = (name: string) => {
    setSelectedBridge(name);
    go('bridge-detail');
  };
  const openBond = (item: Bond) => {
    setSelectedBridge(item.bridge);
    setSelectedBond(item.name);
    go('bond-detail');
  };
  const createBond = (name: string) => {
    setSelectedBridge(name);
    setSelectedBond('__new__');
    go('bond-edit');
  };
  const blocked = !!stageBlock || !desktop;
  if (
    [
      'loading',
      'empty',
      'error',
      'permission-denied',
      'provider-unavailable',
    ].includes(scenario)
  )
    return <ResourceState view={view} scenario={scenario} />;
  if ((view === 'bridge-detail' || view === 'bond-edit') && !bridge)
    return (
      <UnavailableObject
        target={`Bridge/${selectedBridge}`}
        reason="The selected Bridge is outside this inventory snapshot."
        go={go}
      />
    );
  if (
    (view === 'bond-detail' ||
      (view === 'bond-edit' && selectedBond !== '__new__')) &&
    !bond
  )
    return (
      <UnavailableObject
        target={`Port/${selectedBond}`}
        reason="The selected Bond is outside this inventory snapshot. Confirmed intent does not supply a new inventory observation."
        go={go}
      />
    );
  let content: ReactNode;
  if (view === 'bridges')
    content = (
      <BridgeList
        mode={mode}
        scenario={scenario}
        search={search}
        setSearch={setSearch}
        openBridge={openBridge}
        stage={onStageIntent}
        blocked={blocked}
      />
    );
  else if (view === 'bridge-detail')
    content = (
      <BridgeDetail
        bridge={bridge!}
        mode={mode}
        scenario={scenario}
        go={go}
        openObject={openObject}
        live={live}
        createBond={createBond}
        stage={onStageIntent}
        blocked={blocked}
      />
    );
  else if (view === 'bonds')
    content = (
      <BondList
        mode={mode}
        scenario={scenario}
        selectedBridge={selectedBridge}
        openBond={openBond}
        createBond={createBond}
        blocked={blocked}
      />
    );
  else if (view === 'bond-detail')
    content = (
      <BondDetail
        bond={bond!}
        mode={mode}
        scenario={scenario}
        go={go}
        openBridge={openBridge}
        openObject={openObject}
        diagnose={onDiagnose}
        blocked={blocked}
      />
    );
  else if (view === 'bond-edit')
    content = (
      <BondEditor
        key={selectedBond + ':' + selectedBridge}
        bond={bond}
        initialBridge={selectedBridge}
        mode={mode}
        scenario={scenario}
        go={go}
        stage={onStageIntent}
        blocked={blocked}
      />
    );
  else
    content = (
      <SwitchingOverview scenario={scenario} go={go} openBridge={openBridge} />
    );
  return (
    <div className="ovs-switching-pages">
      {stageBlock && (
        <Notice tone="uncertain" title="Configuration unavailable">
          {stageBlock}
        </Notice>
      )}
      {!desktop && (
        <Notice tone="info" title="Review on this device">
          New Bridge and Bond configuration requires desktop. Existing
          transactions remain available through the global Safe Apply banner.
        </Notice>
      )}
      {content}
    </div>
  );
}
