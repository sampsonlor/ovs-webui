// Synthetic, read-only observations. Configuration and runtime have separate authorities.
export type AccelerationFamily = 'dpdk' | 'offload';
export const accelerationStates = [
  'Enabled',
  'Available',
  'Missing prerequisites',
  'Unsupported',
  'Unknown',
] as const;
export type AccelerationState = (typeof accelerationStates)[number];
export const accelerationReviewLabels = {
  normal: 'Normal · DPDK enabled / offload unknown',
  enabled: 'Enabled',
  available: 'Available',
  missing: 'Missing prerequisites',
  unsupported: 'Unsupported',
  unknown: 'Unknown support',
  mismatch: 'Configured / operational mismatch',
  degraded: 'Provider degraded · partial telemetry',
  fallback: 'Software fallback observed',
  stale: 'Stale observations',
  'generation-mismatch': 'Instance generation changed',
  unavailable: 'Provider unavailable',
  empty: 'No observations',
  failed: 'Read failed',
} as const;
export type AccelerationReviewCase = keyof typeof accelerationReviewLabels;
export type Observation = {
  key: string;
  label: string;
  value: string | number | boolean | null;
  source: string;
  authority: 'Configuration' | 'Runtime' | 'Capability';
  observedAt: number;
  generation: number;
  instance: string;
  note: string;
};
export type AccelerationRecord = {
  id: AccelerationFamily;
  title: string;
  scope: string;
  related: string;
  provider: 'Available' | 'Degraded' | 'Unavailable';
  facts: Observation[];
  prerequisites: string[];
};
export type AccelerationSnapshot = {
  id: string;
  capturedAt: number;
  generation: number;
  instance: string;
  reviewCase: AccelerationReviewCase;
  records: AccelerationRecord[];
};
export const accelerationTtlMs = 60_000;
export const accelerationInstance = 'ovs-synthetic-01';

export function observationText(value: Observation['value']) {
  return value === null
    ? 'Unknown'
    : value === true
      ? 'Yes'
      : value === false
        ? 'No'
        : String(value);
}

export function observationFreshness(
  fact: Observation,
  now: number,
  generation: number,
) {
  if (fact.instance !== accelerationInstance || fact.generation !== generation)
    return 'Generation mismatch';
  if (now < fact.observedAt || now - fact.observedAt >= accelerationTtlMs)
    return 'Stale';
  return 'Fresh';
}

export function accelerationServiceBlock(scenario: string) {
  if (scenario === 'permission-denied')
    return 'Permission denied · acceleration.observe is required.';
  if (scenario === 'loading') return 'Observation service is still loading.';
  if (['error', 'network-loss', 'provider-unavailable'].includes(scenario))
    return 'Observation provider is unavailable. Retained evidence is historical.';
  return null;
}

export function accelerationAdmission(
  scenario: string,
  width: number,
  busy: boolean,
) {
  return (
    accelerationServiceBlock(scenario) ??
    (width < 768
      ? 'Use tablet or desktop to collect observations. Mobile retains incident evidence.'
      : busy
        ? 'An observation read is already running.'
        : null)
  );
}

export function assessAcceleration(
  record: AccelerationRecord,
  now: number,
  generation: number,
  scenario = 'normal',
): { state: AccelerationState; reason: string } {
  const unknown = (reason: string) => ({ state: 'Unknown' as const, reason });
  if (accelerationServiceBlock(scenario) || record.provider === 'Unavailable')
    return unknown(
      'The provider cannot supply current capability and runtime evidence.',
    );
  if (scenario === 'stale')
    return unknown(
      'The shared inventory is stale. Refresh authoritative observations before drawing a conclusion.',
    );
  const fact = (key: string) => record.facts.find((item) => item.key === key);
  const current = (key: string) => {
    const item = fact(key);
    return item && observationFreshness(item, now, generation) === 'Fresh'
      ? item.value
      : null;
  };
  if (current('supported') === false)
    return { state: 'Unsupported', reason: fact('supported')!.note };
  if (current('supported') !== true)
    return unknown(
      'Support is not established by current provider evidence. A netdev datapath or OS name is insufficient.',
    );
  const required = ['configured', 'operational', ...record.prerequisites];
  if (
    required.some(
      (key) =>
        !fact(key) ||
        observationFreshness(fact(key)!, now, generation) !== 'Fresh',
    )
  )
    return unknown(
      'Required observations are stale or belong to a different instance generation.',
    );
  if (current('configured') === true && current('operational') === true) {
    if (record.prerequisites.some((key) => current(key) !== true))
      return unknown(
        'Runtime and prerequisite evidence disagree or are incomplete.',
      );
    return {
      state: 'Enabled',
      reason:
        'Configuration and authoritative runtime signals agree. Individual ports and flows may still have incomplete coverage.',
    };
  }
  if (current('configured') === true || current('operational') === true)
    return unknown(
      'Configuration and runtime do not establish the same enabled state. Inspect the sources; do not infer operational readiness.',
    );
  const missing = record.prerequisites.filter((key) => current(key) === false);
  if (missing.length)
    return {
      state: 'Missing prerequisites',
      reason: missing.map((key) => fact(key)!.note).join(' '),
    };
  if (
    record.prerequisites.some((key) => current(key) !== true) ||
    current('configured') === null ||
    current('operational') === null
  )
    return unknown(
      'Required facts are missing. Unknown values are not failed prerequisites.',
    );
  return {
    state: 'Available',
    reason:
      'Recognized prerequisites are met and the capability is not enabled. Phase 1 provides observation only.',
  };
}

// Degradation masks only optional telemetry. Essential readiness signals may remain authoritative.
export function projectAccelerationRecord(
  record: AccelerationRecord,
  scenario: string,
): AccelerationRecord {
  const unavailable = !!accelerationServiceBlock(scenario);
  const degraded = ['degraded', 'provider-degraded'].includes(scenario);
  if (!unavailable && !degraded) return record;
  return {
    ...record,
    provider: unavailable
      ? 'Unavailable'
      : record.provider === 'Unavailable'
        ? 'Unavailable'
        : 'Degraded',
    facts: record.facts.map((item) =>
      degraded &&
      ['pmd-cycles', 'rx-drops', 'hw-flows', 'sw-flows'].includes(item.key)
        ? {
            ...item,
            value: null,
            note: 'Telemetry provider degraded; this counter is unavailable.',
          }
        : item,
    ),
  };
}

export function filterAcceleration(
  records: AccelerationRecord[],
  search: string,
  state: string,
  now: number,
  generation: number,
  scenario: string,
) {
  const query = search.trim().toLowerCase();
  return records.filter(
    (item) =>
      `${item.title} ${item.scope} ${item.related}`
        .toLowerCase()
        .includes(query) &&
      (state === 'all' ||
        assessAcceleration(item, now, generation, scenario).state === state),
  );
}

export function captureAcceleration(
  reviewCase: AccelerationReviewCase,
  now: number,
  generation: number,
  scenario = 'normal',
): AccelerationSnapshot {
  const observedAt =
    reviewCase === 'stale' ? now - accelerationTtlMs - 1000 : now;
  const capturedGeneration =
    reviewCase === 'generation-mismatch' ? generation - 1 : generation;
  const make = (id: AccelerationFamily): AccelerationRecord => {
    const selectedCase =
      reviewCase === 'normal'
        ? id === 'dpdk'
          ? 'enabled'
          : 'unknown'
        : reviewCase;
    const missing = selectedCase === 'missing';
    const disabled = [
      'available',
      'missing',
      'unsupported',
      'unknown',
    ].includes(selectedCase);
    const provider =
      selectedCase === 'unavailable'
        ? 'Unavailable'
        : selectedCase === 'degraded'
          ? 'Degraded'
          : 'Available';
    const fact = (
      key: string,
      label: string,
      value: Observation['value'],
      source: string,
      authority: Observation['authority'],
      note = '',
    ): Observation => ({
      key,
      label,
      value,
      source,
      authority,
      note,
      observedAt,
      generation: capturedGeneration,
      instance: accelerationInstance,
    });
    const facts = [
      fact(
        'supported',
        'Provider support',
        selectedCase === 'unknown' || provider === 'Unavailable'
          ? null
          : selectedCase !== 'unsupported',
        `${id} capability provider`,
        'Capability',
        selectedCase === 'unsupported'
          ? `The synthetic ${id} provider explicitly reports this platform unsupported.`
          : 'Explicit provider capability evidence; never inferred from interface names or datapath type.',
      ),
      fact(
        'configured',
        'Configured',
        !disabled,
        'OVSDB / Open_vSwitch.other_config',
        'Configuration',
        id === 'dpdk'
          ? 'dpdk-init=true requests initialization. This setting alone does not prove EAL initialization.'
          : 'hw-offload=true requests hardware offload. It does not prove that any particular flow is offloaded.',
      ),
      fact(
        'operational',
        'Operational',
        provider === 'Unavailable' || selectedCase === 'unknown'
          ? null
          : selectedCase === 'mismatch'
            ? false
            : !disabled,
        id === 'dpdk'
          ? 'ovs-vswitchd / dpdk_initialized'
          : 'NIC offload provider / operational signal',
        'Runtime',
        'Read independently from configuration. This is capability-level readiness, not a health verdict for every Interface or flow.',
      ),
    ];
    if (id === 'dpdk')
      facts.push(
        fact(
          'build',
          'DPDK build support',
          true,
          'ovs-vswitchd build capability',
          'Capability',
        ),
        fact(
          'memory',
          'EAL memory reservation',
          !missing,
          'Host memory provider / NUMA 0',
          'Runtime',
          missing
            ? 'EAL memory reservation is not satisfied on NUMA 0 for this declared profile.'
            : '2 MiB pages · 512 reserved · NUMA 0. Values belong to this synthetic profile.',
        ),
        fact(
          'pmd',
          'PMD / RXQ assignment',
          disabled ? 'No active PMD sample' : 'PMD core 4 · NUMA 0 · RXQ 0',
          'ovs-appctl / dpif-netdev/pmd-rxq-show',
          'Runtime',
          'Queue-level sample only. This does not establish complete Interface coverage.',
        ),
        fact(
          'pmd-cycles',
          'RXQ measured PMD cycles',
          provider === 'Degraded' || disabled ? null : '18% · last 60 seconds',
          'ovs-appctl / dpif-netdev/pmd-rxq-show',
          'Runtime',
          'Sampled PMD cycle usage is not forwarding health or link utilization.',
        ),
        fact(
          'rx-drops',
          'RX drops in sample',
          provider === 'Degraded' || disabled ? null : 0,
          'DPDK statistics provider',
          'Runtime',
          'Bounded sample; a zero counter is a reported value, not a substitute for missing evidence.',
        ),
        fact(
          'vhost',
          'vhost readiness',
          null,
          'vhost provider',
          'Runtime',
          'No socket or peer evidence was returned. No vhost Interface or guest attachment is inferred.',
        ),
        fact(
          'datapath',
          'Related Bridge datapath',
          'netdev',
          'OVSDB / Bridge.datapath_type',
          'Configuration',
          'Bridge/br-offload is a contextual link. Host DPDK initialization does not prove that this Bridge uses DPDK Interfaces.',
        ),
      );
    else
      facts.push(
        fact(
          'driver',
          'NIC / driver support',
          true,
          'NIC capability provider',
          'Capability',
        ),
        fact(
          'representor',
          'Representor discovery',
          missing ? false : selectedCase === 'unknown' ? null : true,
          'Hardware topology provider',
          'Runtime',
          missing
            ? 'The provider cannot resolve the required representor in this declared hardware path.'
            : selectedCase === 'unknown' || provider === 'Unavailable'
              ? 'The provider has not established a hardware role for Interface/pf0hpf.'
              : 'This fixture declares Interface/pf0hpf a representor. Native OVS Interface type and PF/VF association are separate observations.',
        ),
        fact(
          'native-type',
          'Native Interface type',
          null,
          'OVSDB / Interface.type',
          'Configuration',
          'Not supplied by this bounded observation. The name pf0hpf does not establish a native Interface type.',
        ),
        fact(
          'pf-vf',
          'PF / VF association',
          null,
          'Hardware topology provider',
          'Runtime',
          'Physical connector and PF/VF mapping were not supplied. A representor is not a separate physical connector.',
        ),
        fact(
          'hw-flows',
          'Hardware flow entries',
          provider === 'Degraded' || disabled
            ? null
            : selectedCase === 'fallback'
              ? 0
              : 126,
          'ovs-appctl / dpctl/dump-flows + NIC provider',
          'Runtime',
          'A bounded datapath observation. OpenFlow rule count is a different measurement.',
        ),
        fact(
          'sw-flows',
          'Software fallback entries',
          provider === 'Degraded' || disabled
            ? null
            : selectedCase === 'fallback'
              ? 12
              : 3,
          'NIC offload provider',
          'Runtime',
          'Some matches or actions may fall back to software. Fallback is not automatically a packet drop.',
        ),
        fact(
          'fallback-reason',
          'Fallback explanation',
          selectedCase === 'fallback'
            ? 'Provider reports unsupported action for 12 sampled entries'
            : null,
          'NIC offload provider',
          'Runtime',
          'Reasons require provider evidence; absence of a reason is not evidence of success.',
        ),
      );
    if (provider === 'Unavailable')
      facts.forEach((item) => {
        if (item.authority !== 'Configuration') item.value = null;
      });
    return {
      id,
      title: id === 'dpdk' ? 'DPDK runtime' : 'Hardware offload',
      scope: id === 'dpdk' ? 'Host / OVS instance' : 'Bridge/br-offload',
      related:
        id === 'dpdk'
          ? 'Bridge/br-offload · Interface coverage unknown'
          : missing || selectedCase === 'unknown' || provider === 'Unavailable'
            ? 'Interface/pf0hpf · hardware role unknown'
            : 'Interface/pf0hpf · provider-declared representor',
      provider,
      facts,
      prerequisites:
        id === 'dpdk' ? ['build', 'memory'] : ['driver', 'representor'],
    };
  };
  return {
    id: `acc-${now}`,
    capturedAt: now,
    generation: capturedGeneration,
    instance: accelerationInstance,
    reviewCase,
    records:
      reviewCase === 'empty'
        ? []
        : [make('dpdk'), make('offload')].map((record) =>
            projectAccelerationRecord(record, scenario),
          ),
  };
}
