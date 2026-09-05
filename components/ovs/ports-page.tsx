'use client';

import { useMemo, useState } from 'react';
import { ChevronRight, Search, SlidersHorizontal } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Checkbox } from '@/components/ui/checkbox';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover';
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
  StateDot,
  StatusBadge,
} from './foundation';
import {
  ports,
  vlanLabel,
  type Mode,
  type Port,
  type View,
  type VlanValue,
} from '@/lib/ovs-model';

export function PortsPage({
  mode,
  search,
  setSearch,
  selected,
  setSelected,
  go,
  scenario = 'normal',
  onRefresh,
  live = {},
  inventory = ports,
  snapshotLabel = 'Snapshot · synthetic inventory',
  identityLabel = 'Port UUID',
}: {
  mode: Mode;
  search: string;
  setSearch: (value: string) => void;
  selected: string;
  setSelected: (value: string) => void;
  go: (view: View) => void;
  scenario?: string;
  onRefresh?: () => void;
  live?: Record<string, VlanValue>;
  inventory?: Port[];
  snapshotLabel?: string;
  identityLabel?: string;
}) {
  const [stateFilter, setStateFilter] = useState('all');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [showSpeed, setShowSpeed] = useState(true);
  const [showBridge, setShowBridge] = useState(true);
  const shown = useMemo(
    () =>
      scenario === 'empty'
        ? []
        : inventory
            .map((port) =>
              live[port.name]
                ? { ...port, vlan: vlanLabel(live[port.name]) }
                : port,
            )
            .filter(
              (port) =>
                `${port.name} ${port.bridge} ${port.interfaceName}`
                  .toLowerCase()
                  .includes(search.trim().toLowerCase()) &&
                (stateFilter === 'all' || port.state === stateFilter) &&
                (scopeFilter === 'all' || port.scope === scopeFilter),
            ),
    [search, stateFilter, scopeFilter, scenario, live, inventory],
  );
  const filtered = Boolean(
    search || stateFilter !== 'all' || scopeFilter !== 'all',
  );
  const reset = () => {
    setSearch('');
    setStateFilter('all');
    setScopeFilter('all');
  };
  const unavailable = [
    'error',
    'permission-denied',
    'provider-unavailable',
  ].includes(scenario);
  const openPort = (name: string) => {
    setSelected(name);
    go('port-detail');
  };

  return (
    <>
      <PageHeader
        id="P0-02"
        eyebrow="Switching / Ports"
        title="Ports"
        description="Inspect links, ownership and VLAN intent before preparing a change."
        actions={
          <Button
            variant="outline"
            onClick={() => {
              reset();
              onRefresh?.();
            }}
          >
            Refresh inventory
          </Button>
        }
      />
      <div className="ovs-inventory-summary" aria-label="Inventory summary">
        <span>
          <strong>
            {scenario === 'empty'
              ? '0'
              : unavailable || scenario === 'loading'
                ? '—'
                : inventory.length}
          </strong>{' '}
          ports
        </span>
        {!unavailable && scenario !== 'loading' && scenario !== 'empty' && (
          <>
            <span>
              <StateDot state="Up" />{' '}
              {inventory.filter((port) => port.state === 'Up').length} up
            </span>
            <span>
              <StateDot state="Down" />{' '}
              {inventory.filter((port) => port.state === 'Down').length} down
            </span>
            <span>
              <StateDot state="Unknown" />{' '}
              {inventory.filter((port) => port.state === 'Unknown').length}{' '}
              unknown
            </span>
            <span className="ml-auto text-muted-foreground">
              {snapshotLabel}
            </span>
          </>
        )}
      </div>
      {scenario === 'degraded' && (
        <Notice tone="warning" title="Provider data is incomplete">
          Last known inventory is available. SmartNIC link state is unknown; no
          missing values are treated as zero or Down.
        </Notice>
      )}
      <div className="ovs-toolbar">
        <div className="relative min-w-0 flex-1">
          <Search
            aria-hidden="true"
            className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
          />
          <Input
            aria-label="Search ports"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search port, bridge or interface…"
            className="h-10 pl-9"
          />
        </div>
        <NativeSelect
          aria-label="Filter state"
          value={stateFilter}
          onChange={(e) => setStateFilter(e.target.value)}
        >
          <NativeSelectOption value="all">All states</NativeSelectOption>
          {['Up', 'Down', 'Unknown'].map((value) => (
            <NativeSelectOption key={value} value={value}>
              {value}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <NativeSelect
          aria-label="Filter management scope"
          value={scopeFilter}
          onChange={(e) => setScopeFilter(e.target.value)}
        >
          <NativeSelectOption value="all">All scopes</NativeSelectOption>
          {['Manage', 'Basic Manage', 'Observe'].map((value) => (
            <NativeSelectOption key={value} value={value}>
              {value}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        <Popover>
          <PopoverTrigger render={<Button variant="outline" />}>
            <SlidersHorizontal /> Columns
          </PopoverTrigger>
          <PopoverContent align="end">
            <PopoverTitle>Display columns</PopoverTitle>
            <label
              htmlFor="column-speed"
              className="flex items-center gap-3 p-2"
            >
              <Checkbox
                id="column-speed"
                checked={showSpeed}
                onCheckedChange={setShowSpeed}
              />
              Speed
            </label>
            <label
              htmlFor="column-bridge"
              className="flex items-center gap-3 p-2"
            >
              <Checkbox
                id="column-bridge"
                checked={showBridge}
                onCheckedChange={setShowBridge}
              />
              Bridge
            </label>
            <p className="text-xs text-muted-foreground">
              Identity, state and scope stay visible. Expert adds native
              identity and authority.
            </p>
          </PopoverContent>
        </Popover>
      </div>
      {scenario === 'loading' ? (
        <section
          aria-busy="true"
          aria-label="Loading ports"
          className="ovs-surface space-y-4 p-6"
        >
          <output className="text-sm">Loading port inventory…</output>
          {[1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </section>
      ) : unavailable ? (
        <Notice
          tone={scenario === 'permission-denied' ? 'danger' : 'uncertain'}
          title={
            scenario === 'permission-denied'
              ? 'Permission required'
              : scenario === 'provider-unavailable'
                ? 'Inventory provider unavailable'
                : 'Unable to load port inventory'
          }
          actions={
            scenario !== 'permission-denied' && (
              <Button variant="outline" onClick={onRefresh}>
                Refresh inventory
              </Button>
            )
          }
        >
          {scenario === 'permission-denied'
            ? 'switching.read is required. Port details are withheld.'
            : 'A successful inventory response is unavailable. This does not mean there are no ports.'}
        </Notice>
      ) : (
        <section
          className="ovs-surface overflow-hidden"
          aria-label="Port inventory"
        >
          <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
            <output className="text-sm text-muted-foreground">
              {shown.length} {shown.length === 1 ? 'port' : 'ports'}
              {filtered ? ' match your filters' : ' in this snapshot'}
            </output>
            <StatusBadge tone={scenario === 'degraded' ? 'warning' : 'info'}>
              {scenario === 'degraded'
                ? 'Partial snapshot'
                : 'Inventory snapshot'}
            </StatusBadge>
          </div>
          {!shown.length ? (
            <div className="px-6 py-14 text-center">
              <Search
                aria-hidden="true"
                className="mx-auto size-7 text-muted-foreground"
              />
              <h2 className="mt-3 font-semibold">
                {filtered ? 'No matching ports' : 'No ports in this inventory'}
              </h2>
              <p className="mt-2 text-sm text-muted-foreground">
                {filtered
                  ? 'Try a different name, link state or management scope.'
                  : 'The inventory query succeeded and returned no objects.'}
              </p>
              {filtered && (
                <Button variant="outline" className="mt-4" onClick={reset}>
                  Clear filters
                </Button>
              )}
            </div>
          ) : (
            <>
              <div className="hidden lg:block">
                <Table className="ovs-data-table">
                  <caption className="sr-only">
                    Managed ports. Select a port name to inspect its details.
                  </caption>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Port / interface</TableHead>
                      <TableHead>Link</TableHead>
                      {showSpeed && <TableHead>Speed</TableHead>}
                      <TableHead>VLAN configuration</TableHead>
                      {showBridge && <TableHead>Bridge</TableHead>}
                      <TableHead>Management scope</TableHead>
                      {mode === 'expert' && (
                        <>
                          <TableHead>Authority / provider</TableHead>
                          <TableHead>{identityLabel}</TableHead>
                        </>
                      )}
                      <TableHead>
                        <span className="sr-only">Inspect</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {shown.map((port) => (
                      <TableRow
                        key={port.name}
                        data-state={
                          selected === port.name ? 'selected' : undefined
                        }
                      >
                        <TableCell>
                          <button
                            className="ovs-object-link"
                            onClick={() => openPort(port.name)}
                          >
                            {port.name}
                          </button>
                          <p className="mt-1 font-mono text-xs text-muted-foreground">
                            {port.members
                              ? `${port.members.length} member Interfaces`
                              : port.interfaceName}
                          </p>
                        </TableCell>
                        <TableCell>
                          <span className="inline-flex items-center gap-2">
                            <StateDot state={port.state} />
                            {port.state}
                          </span>
                        </TableCell>
                        {showSpeed && (
                          <TableCell className="tabular-nums">
                            {port.speed}
                          </TableCell>
                        )}
                        <TableCell>{port.vlan}</TableCell>
                        {showBridge && (
                          <TableCell className="font-mono">
                            {port.bridge}
                          </TableCell>
                        )}
                        <TableCell>
                          <ScopeBadge scope={port.scope} />
                        </TableCell>
                        {mode === 'expert' && (
                          <>
                            <TableCell>
                              <p>{port.authority}</p>
                              <p className="text-xs text-muted-foreground">
                                {port.provider}
                              </p>
                            </TableCell>
                            <TableCell className="font-mono">
                              {port.uuid}
                            </TableCell>
                          </>
                        )}
                        <TableCell>
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={`Inspect ${port.name}`}
                            onClick={() => openPort(port.name)}
                          >
                            <ChevronRight />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <div className="divide-y lg:hidden">
                {shown.map((port) => (
                  <article key={port.name} className="p-4">
                    <div className="flex items-center justify-between gap-3">
                      <button
                        className="ovs-object-link"
                        onClick={() => openPort(port.name)}
                      >
                        {port.name}
                      </button>
                      <ScopeBadge scope={port.scope} />
                    </div>
                    <p className="mt-2 flex items-center gap-2 text-sm">
                      <StateDot state={port.state} />
                      {port.state}
                      <span className="text-muted-foreground">
                        · {port.speed}
                      </span>
                    </p>
                    <p className="mt-2 text-sm">
                      {port.vlan} · {port.bridge}
                    </p>
                    {mode === 'expert' && (
                      <p className="mt-2 break-words font-mono text-xs text-muted-foreground">
                        {port.interfaceName} · {port.authority} /{' '}
                        {port.provider} · {port.uuid}
                      </p>
                    )}
                  </article>
                ))}
              </div>
            </>
          )}
        </section>
      )}
      <p className="mt-4 flex items-center gap-2 text-sm text-muted-foreground">
        <span className="size-1.5 rounded-full bg-current" />
        Observe-only ports remain inspectable. Unknown link state is distinct
        from Down.
      </p>
    </>
  );
}
