'use client';

import { ArrowLeft, ChevronRight } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';
import { Notice, PageHeader } from './foundation';
import { validateVlan } from '@/lib/change-control';
import {
  vlanLabel,
  nativeVlanFields,
  type Mode,
  type Port,
  type View,
  type VlanMode,
} from '@/lib/ovs-model';

export function VlanEdit({
  port,
  mode,
  vlanMode,
  setVlanMode,
  allowedVlans,
  setAllowedVlans,
  nativeTag,
  setNativeTag,
  onStage,
  go,
  locked,
}: {
  port: Port;
  mode: Mode;
  vlanMode: string;
  setVlanMode: (value: string) => void;
  allowedVlans: string;
  setAllowedVlans: (value: string) => void;
  nativeTag: string;
  setNativeTag: (value: string) => void;
  onStage: () => void;
  go: (view: View) => void;
  locked: boolean;
}) {
  const value = {
    mode: vlanMode as VlanMode,
    tag: vlanMode === 'trunk' ? null : Number(nativeTag),
    trunks: vlanMode === 'access' ? '' : allowedVlans,
  };
  const error = validateVlan(value);
  const readOnly = port.scope === 'Observe' || port.authority !== 'OVS';
  return (
    <>
      <PageHeader
        id="P0-04"
        eyebrow="Switching / Ports / VLAN"
        title={`Edit VLAN · ${port.name}`}
        description="Prepare intent in your candidate. Validation and Safe Apply follow before running configuration changes."
        actions={
          <Button variant="outline" onClick={() => go('port-detail')}>
            <ArrowLeft /> Port detail
          </Button>
        }
      />
      <Notice
        tone="warning"
        title={
          port.name === 'mgmt0'
            ? 'Management path change'
            : 'Connectivity change'
        }
      >
        Affected bridge: {port.bridge}. This change requires a checkpoint,
        validation and confirmation through Safe Apply.
      </Notice>
      {locked && (
        <Notice
          tone="uncertain"
          title="An active transaction owns the workspace"
        >
          Read the existing transaction before editing or staging another
          change.
        </Notice>
      )}
      {readOnly && (
        <Notice tone="info" title="Observe-only port">
          The provider owns this object. Expert Mode does not grant write
          access.
        </Notice>
      )}
      <div className="mt-5 grid gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
        <form
          className="ovs-surface p-5"
          onSubmit={(event) => {
            event.preventDefault();
            onStage();
          }}
        >
          <fieldset disabled={readOnly || locked}>
            <legend className="font-semibold">VLAN intent</legend>
            <div className="mt-5 grid gap-5 sm:grid-cols-2">
              <label htmlFor="vlan-mode" className="grid gap-2 text-sm">
                VLAN mode
                <NativeSelect
                  id="vlan-mode"
                  value={vlanMode}
                  onChange={(event) => setVlanMode(event.target.value)}
                >
                  <NativeSelectOption value="access">Access</NativeSelectOption>
                  <NativeSelectOption value="trunk">Trunk</NativeSelectOption>
                  <NativeSelectOption value="native-tagged">
                    Native tagged
                  </NativeSelectOption>
                </NativeSelect>
              </label>
              {vlanMode !== 'trunk' && (
                <label htmlFor="vlan-tag" className="grid gap-2 text-sm">
                  {vlanMode === 'access' ? 'Access VLAN' : 'Native VLAN'}
                  <Input
                    id="vlan-tag"
                    type="number"
                    min={1}
                    max={4094}
                    required
                    value={nativeTag}
                    onChange={(event) => setNativeTag(event.target.value)}
                  />
                </label>
              )}
              {vlanMode !== 'access' && (
                <div className="grid gap-2 text-sm sm:col-span-2">
                  <label htmlFor="vlan-trunks">Allowed VLANs</label>
                  <Input
                    id="vlan-trunks"
                    required
                    value={allowedVlans}
                    onChange={(event) => setAllowedVlans(event.target.value)}
                    placeholder="120, 240-250"
                    aria-describedby="vlan-help"
                  />
                  <span
                    id="vlan-help"
                    className="text-xs text-muted-foreground"
                  >
                    Comma-separated IDs or ascending ranges, from 1 to 4094.
                  </span>
                </div>
              )}
            </div>
            {error && (
              <Notice tone="warning" title="Check VLAN intent">
                {error}
              </Notice>
            )}
            <div className="mt-6 flex flex-wrap justify-end gap-2 border-t pt-4">
              <Button
                type="button"
                variant="outline"
                onClick={() => go('port-detail')}
              >
                Discard edits
              </Button>
              <Button
                disabled={Boolean(error)}
                type="submit"
                className="hidden lg:inline-flex"
              >
                Add to workspace <ChevronRight />
              </Button>
            </div>
            <p className="mt-3 text-sm text-muted-foreground lg:hidden">
              New configuration changes require desktop.
            </p>
          </fieldset>
        </form>
        <aside className="ovs-surface h-fit p-5">
          <h2 className="font-semibold">Intent preview</h2>
          <dl className="mt-4 space-y-5 text-sm">
            <div>
              <dt className="text-muted-foreground">
                Last confirmed configuration
              </dt>
              <dd className="mt-1">{vlanLabel(port.config)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Candidate</dt>
              <dd className="mt-1 font-medium text-primary">
                {error ? 'Complete a valid VLAN intent' : vlanLabel(value)}
              </dd>
            </div>
          </dl>
          {mode === 'expert' && (
            <div className="mt-5 border-t pt-4">
              <p className="text-sm font-medium">OVS mapping</p>
              <pre className="mt-3 overflow-auto font-mono text-xs leading-6">
                {JSON.stringify({ Port: nativeVlanFields(value) }, null, 2)}
              </pre>
            </div>
          )}
        </aside>
      </div>
    </>
  );
}
