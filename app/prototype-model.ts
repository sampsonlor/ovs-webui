export type P1View =
  | 'switching-overview'
  | 'bridges'
  | 'bridge-detail'
  | 'bonds'
  | 'bond-edit'
  | 'bond-detail';

export type PrototypeMode = 'standard' | 'expert';

export type ReviewScenario =
  | 'normal'
  | 'drift'
  | 'member-down'
  | 'lacp-mismatch'
  | 'validation-blocked'
  | 'provider-degraded'
  | 'advanced-config'
  | 'outcome-unknown'
  | 'network-loss';

export type ChangeIntent = {
  kind: 'vlan' | 'bridge' | 'bond';
  objectType: 'Port' | 'Bridge';
  objectName: string;
  title: string;
  summary: string;
  current: string;
  candidate: string;
  risk: 'Low' | 'Medium' | 'High';
  capability: string;
  evidenceObject: string;
};

export const p1Steps: Array<{ id: string; label: string; view: P1View }> = [
  { id: 'P1-01', label: 'Switching', view: 'switching-overview' },
  { id: 'P1-02', label: 'Bridges', view: 'bridges' },
  { id: 'P1-03', label: 'Bridge detail', view: 'bridge-detail' },
  { id: 'P1-04', label: 'Bond / LACP', view: 'bonds' },
  { id: 'P1-05', label: 'Create / edit', view: 'bond-edit' },
  { id: 'P1-06', label: 'Bond detail', view: 'bond-detail' },
];

export const p1Views: P1View[] = [
  'switching-overview',
  'bridges',
  'bridge-detail',
  'bonds',
  'bond-edit',
  'bond-detail',
];
