'use client';

import { Button } from '@/components/ui/button';
import { Notice, PageHeader, StatusBadge } from './foundation';
import {
  inventoryMetadata,
  type InventoryInterface,
} from '@/lib/inventory-model';
import type { Mode, View } from '@/lib/ovs-model';

export function UnavailableObject({
  target,
  reason,
  go,
}: {
  target: string;
  reason: string;
  go: (view: View) => void;
}) {
  return (
    <section aria-label="Unavailable object reference" className="space-y-5">
      <PageHeader
        id="object-reference"
        eyebrow="Switching / Object reference"
        title="Object unavailable"
        description="The requested identity is retained. Select another object from the inventory."
        scope="Observe"
      />
      <Notice tone="uncertain" title="Requested object could not be resolved">
        <p className="break-all font-mono">{target}</p>
        <p className="mt-2">{reason}</p>
      </Notice>
      <Button variant="outline" onClick={() => go('switching-overview')}>
        Open switching inventory
      </Button>
    </section>
  );
}

export function InterfaceContext({
  item,
  mode,
  openObject,
  scenario,
}: {
  item: InventoryInterface;
  mode: Mode;
  openObject: (target: string) => void;
  scenario: string;
}) {
  if (['loading', 'empty', 'error', 'provider-unavailable'].includes(scenario))
    return (
      <Notice
        tone="uncertain"
        title={
          scenario === 'loading'
            ? 'Loading object context'
            : 'Object observations unavailable'
        }
      >
        A current Interface observation is unavailable in this review state. The
        requested identity is retained in the URL.
      </Notice>
    );
  return (
    <section aria-label="Interface relationship context" className="space-y-5">
      <PageHeader
        id="SW-07-context"
        eyebrow="Switching / Native Interface context"
        title={item.name}
        scope="Observe"
        description="Inspect this Interface's captured Port and Bridge relationship before following another resource."
      />
      {['stale', 'degraded', 'provider-degraded'].includes(scenario) && (
        <Notice tone="warning" title="Retained relationship snapshot">
          Provider evidence is stale or degraded. These captured relationships
          do not establish current runtime state.
        </Notice>
      )}
      <div className="ovs-surface p-5">
        <h2 className="font-semibold">Native relationship</h2>
        <div className="mt-4 grid gap-4 sm:grid-cols-3">
          <Button
            variant="outline"
            onClick={() => openObject(`Bridge/${item.bridge}`)}
            aria-label={`Open Bridge ${item.bridge}`}
          >
            {item.bridge}
          </Button>
          <Button
            variant="outline"
            onClick={() => openObject(`Port/${item.port}`)}
            aria-label={`Open Port ${item.port}`}
          >
            {item.port}
          </Button>
          <div className="min-w-0 rounded border p-3 text-sm">
            <span className="block text-muted-foreground">Interface</span>
            <span className="break-all font-mono">{item.name}</span>
          </div>
        </div>
        <dl className="mt-5 grid gap-5 sm:grid-cols-2">
          <div>
            <dt className="text-sm text-muted-foreground">Native type</dt>
            <dd className="mt-1">
              <StatusBadge
                tone={item.nativeType === 'Unknown' ? 'uncertain' : 'neutral'}
              >
                {item.nativeType}
              </StatusBadge>
            </dd>
          </div>
          <div>
            <dt className="text-sm text-muted-foreground">Coverage</dt>
            <dd className="mt-1 text-sm">
              {inventoryMetadata.coverage} · relationship only
            </dd>
          </div>
          {mode === 'expert' &&
            [
              ['Interface UUID', item.uuid],
              ['Source', item.source],
              ['Instance', inventoryMetadata.instance],
              ['Inventory generation', String(inventoryMetadata.generation)],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-sm text-muted-foreground">{label}</dt>
                <dd className="mt-1 break-all font-mono text-sm">{value}</dd>
              </div>
            ))}
        </dl>
      </div>
      <Notice tone="info" title="Bounded Interface context">
        This view does not infer link, ofport, PCI, PF/VF or hardware roles.
        Runtime evidence remains with its own provider. Interface field editing
        is not available in this slice.
      </Notice>
    </section>
  );
}
