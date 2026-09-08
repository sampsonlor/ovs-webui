'use client';

import {
  Activity,
  ChevronRight,
  Eye,
  Gauge,
  Network,
  Settings,
} from 'lucide-react';
import type { Mode, View } from '@/lib/ovs-model';

type NavigationItem = {
  label: string;
  target?: View;
  views?: View[];
  note?: string;
  expertOnly?: boolean;
};

// Approved IA §4.1. Planned entries describe prototype coverage, not permission.
const domains = [
  {
    label: 'Overview',
    icon: Gauge,
    target: 'dashboard' as View,
    views: ['dashboard'],
  },
  {
    label: 'Switching',
    icon: Network,
    target: 'switching-overview' as View,
    views: [
      'switching-overview',
      'bridges',
      'bridge-detail',
      'ports',
      'port-detail',
      'vlan-edit',
      'bonds',
      'bond-edit',
      'bond-detail',
      'openflow-viewer',
    ],
  },
  {
    label: 'Visibility',
    icon: Eye,
    target: 'acceleration-overview' as View,
    views: ['acceleration-overview'],
  },
  {
    label: 'Operations',
    icon: Activity,
    target: 'system-health' as View,
    views: ['system-health', 'diagnostics-hub', 'diagnostic-run', 'evidence'],
  },
  {
    label: 'Administration',
    icon: Settings,
    views: [],
    note: 'Platform and security pages are not included in this prototype yet.',
  },
];

const switching: NavigationItem[] = [
  { label: 'Switching overview', target: 'switching-overview' },
  { label: 'Bridges', target: 'bridges', views: ['bridges', 'bridge-detail'] },
  {
    label: 'Ports',
    target: 'ports',
    views: ['ports', 'port-detail', 'vlan-edit'],
  },
  {
    label: 'Interfaces',
    expertOnly: true,
    note: 'A dedicated Interface list is not included yet. Native relationships are shown in object details.',
  },
  {
    label: 'VLANs',
    note: 'The VLAN-wide inventory is not included yet. Select a Port to review or edit its VLAN configuration.',
  },
  {
    label: 'Bonds / LACP',
    target: 'bonds',
    views: ['bonds', 'bond-edit', 'bond-detail'],
  },
  {
    label: 'STP / RSTP',
    note: 'A dedicated spanning-tree page is not included yet. Bridge details contain an STP/RSTP summary.',
  },
  { label: 'OpenFlow', target: 'openflow-viewer' },
];

const visibility: NavigationItem[] = [
  { label: 'DPDK / Offload', target: 'acceleration-overview' },
  {
    label: 'Endpoints / FDB',
    note: 'Discovery inventory is outside the current prototype batch.',
  },
  {
    label: 'Neighbors / LLDP',
    note: 'Neighbor discovery is outside the current prototype batch.',
  },
  {
    label: 'Statistics / Telemetry',
    note: 'The full telemetry inventory is planned. Acceleration includes bounded runtime observations.',
  },
];

const operations: NavigationItem[] = [
  { label: 'System Health', target: 'system-health' },
  {
    label: 'Diagnostics',
    target: 'diagnostics-hub',
    views: ['diagnostics-hub', 'diagnostic-run'],
  },
  { label: 'Events / Audit', target: 'evidence' },
];

const changes: NavigationItem[] = [
  { label: 'Candidate workspace', target: 'workspace' },
  { label: 'Diff / Validation', target: 'diff' },
  { label: 'Safe Apply', target: 'safe-apply' },
];

export function PrototypeNavigation({
  view,
  mode,
  go,
  compact = false,
}: {
  view: View;
  mode: Mode;
  go: (view: View) => void;
  compact?: boolean;
}) {
  const activeDomain = domains.find((domain) =>
    domain.views.some((value) => value === view),
  );
  const isChangeControl = changes.some((item) => item.target === view);
  const section = isChangeControl ? 'Change control' : activeDomain?.label;
  const entries = (
    isChangeControl
      ? changes
      : section === 'Switching'
        ? switching
        : section === 'Operations'
          ? operations
          : section === 'Visibility'
            ? visibility
            : []
  ).filter((item) => !item.expertOnly || mode === 'expert');

  return (
    <div className={compact ? 'border-b bg-card p-3 lg:hidden' : ''}>
      <nav
        aria-label={compact ? 'Compact navigation' : 'Primary navigation'}
        className={
          compact
            ? 'grid grid-cols-1 gap-1 min-[360px]:grid-cols-2'
            : 'space-y-1'
        }
      >
        {domains.map(({ label, icon: Icon, target, note }) => {
          const active = activeDomain?.label === label;
          return (
            <button
              key={label}
              type="button"
              disabled={!target}
              title={note}
              onClick={() => target && go(target)}
              aria-current={active ? 'page' : undefined}
              className={`flex w-full items-center gap-3 rounded px-3 py-2.5 text-left text-sm disabled:cursor-not-allowed ${active ? 'bg-sidebar-accent font-semibold text-sidebar-accent-foreground' : 'text-muted-foreground enabled:hover:bg-card'}`}
            >
              <Icon aria-hidden="true" className="size-4 shrink-0" />
              <span className="min-w-0 break-words">
                {label}
                {!target && <span className="block text-xs">Planned</span>}
              </span>
            </button>
          );
        })}
      </nav>
      {entries.length > 0 && (
        <nav
          aria-label={`${compact ? 'Compact ' : ''}${section} navigation`}
          className="mt-5 border-t pt-4"
        >
          <h2 className="px-3 pb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {section}
          </h2>
          <ul
            className={
              compact
                ? 'grid grid-cols-1 gap-1 min-[360px]:grid-cols-2'
                : 'space-y-1'
            }
          >
            {entries.map(({ label, target, views, note }) => {
              const active = (views ?? [target]).includes(view);
              return (
                <li key={label}>
                  <button
                    type="button"
                    disabled={!target}
                    title={note}
                    aria-describedby={
                      note
                        ? `${compact ? 'compact' : 'desktop'}-nav-${label.replace(/\W/g, '').toLowerCase()}`
                        : undefined
                    }
                    onClick={() => target && go(target)}
                    aria-current={active ? 'page' : undefined}
                    className={`flex w-full items-center justify-between gap-2 rounded px-3 py-2 text-left text-sm disabled:cursor-not-allowed ${active ? 'bg-sidebar-accent font-semibold text-sidebar-accent-foreground' : 'text-muted-foreground enabled:hover:bg-card'}`}
                  >
                    <span>{label}</span>
                    {target ? (
                      <ChevronRight
                        aria-hidden="true"
                        className="size-4 shrink-0"
                      />
                    ) : (
                      <span className="text-xs">Planned</span>
                    )}
                  </button>
                  {note && (
                    <p
                      id={`${compact ? 'compact' : 'desktop'}-nav-${label.replace(/\W/g, '').toLowerCase()}`}
                      className="sr-only"
                    >
                      {note}
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
          {section === 'Switching' && (
            <p className="mt-3 px-3 text-xs leading-5 text-muted-foreground">
              VLAN edits are available in Port details. STP/RSTP summaries are
              available in Bridge details.
            </p>
          )}
        </nav>
      )}
      <p className="mt-4 px-3 text-xs leading-5 text-muted-foreground">
        Planned pages are outside the current prototype.
      </p>
    </div>
  );
}
