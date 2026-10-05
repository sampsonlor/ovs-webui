import type { Interface } from '../../clients/typescript/public-v1.generated';
import { field } from './interface-observation.ts';

export function nativeTypeContext(item: Interface): {
  label: string;
  detail: string;
  patch: boolean;
} {
  const f = field(item, 'type');
  if (f?.availability === 'withheld')
    return {
      label: 'Withheld',
      detail:
        'Configuration read permission is required for native type and configured associations.',
      patch: false,
    };
  if (f?.availability !== 'known' || typeof f.value !== 'string')
    return {
      label: 'Unknown',
      detail: 'The provider has no readable native type observation.',
      patch: false,
    };
  const type = f.value;
  let label: string;
  let detail: string;
  if (type === '' || type === 'system') {
    label = type === '' ? 'System (native default)' : 'System';
    detail =
      'Uses the system device type. A physical NIC or hardware role requires independent device evidence.';
  } else if (type === 'internal') {
    label = 'Internal';
    detail =
      'An OVS internal device. Host association and observed MTU have their own evidence.';
  } else if (type === 'patch') {
    label = 'Patch';
    detail =
      'Connects configured Interface peers. Reciprocal configuration does not prove packet forwarding.';
  } else if (
    [
      'vxlan',
      'geneve',
      'gre',
      'ip6gre',
      'erspan',
      'ip6erspan',
      'gtpu',
      'srv6',
    ].includes(type)
  ) {
    label = `Tunnel · ${type}`;
    detail =
      'A configured tunnel type. The reported safe options do not prove an operational remote endpoint.';
    const options = field(item, 'options');
    if (
      options?.availability === 'known' &&
      options.value &&
      typeof options.value === 'object' &&
      !Array.isArray(options.value) &&
      ['remote_ip', 'local_ip', 'key'].some(
        (key) => (options.value as Record<string, unknown>)[key] === 'flow',
      )
    ) {
      detail +=
        ' Reported flow-valued options depend on OpenFlow actions; no fixed peer is inferred.';
    }
  } else if (['dpdk', 'dpdkvhostuser', 'dpdkvhostuserclient'].includes(type)) {
    label = `DPDK · ${type}`;
    detail =
      'A configured DPDK type. It grants no hardware association, runtime readiness or tuning authority. Review independent reported device evidence.';
  } else if (type === 'dummy') {
    label = 'Dummy';
    detail =
      'The native dummy device type is reported. It is not evidence of a physical NIC.';
  } else {
    label = `Unrecognized native type · ${type}`;
    detail =
      'The original native type is preserved. No default type, device role or supported mutation is inferred.';
  }
  if (f.source?.freshness !== 'fresh' || f.source?.confidence !== 'proven') {
    return {
      label: `Last observed: ${label}`,
      detail: `This type observation is not current. ${detail}`,
      patch: type === 'patch',
    };
  }
  return { label, detail, patch: type === 'patch' };
}

export function patchPeerReason(item: Interface): string {
  const reason = item.patch_peer?.reason;
  return (
    (
      {
        CONFIGURATION_WITHHELD: 'Configured peer information is withheld.',
        PATCH_RECIPROCAL_CONFIGURATION:
          'Reciprocal configuration observed. This does not prove packet forwarding.',
        PATCH_OBSERVATION_STALE:
          'The association is not current. Refresh after the provider recovers.',
        PATCH_SCHEMA_UNSUPPORTED:
          'The discovered schema does not support this association interpretation.',
        NOT_PATCH_INTERFACE: 'This is not a configured patch Interface.',
        PATCH_TYPE_UNKNOWN: 'The native type is unknown.',
        PATCH_OPTIONS_UNKNOWN: 'The native peer options are unknown.',
        PATCH_PEER_UNSPECIFIED: 'No readable peer name was reported.',
        PATCH_IDENTITY_UNKNOWN: 'The current native name is unknown.',
        PATCH_PEER_SELF: 'The configured peer points to this same Interface.',
        PATCH_PEER_AMBIGUOUS:
          'More than one observed Interface has the configured peer name.',
        PATCH_PEER_NOT_FOUND:
          'The configured peer is absent from this inventory snapshot.',
        PATCH_PEER_TYPE_MISMATCH:
          'The named peer is not a configured patch Interface.',
        PATCH_PEER_NOT_RECIPROCAL:
          'The named peer does not point back to this Interface.',
        PATCH_PEER_RELATION_UNKNOWN:
          'The peer has no unique Port and Bridge relationship.',
        PATCH_DATAPATH_UNKNOWN: 'The Bridge datapath types are unknown.',
        PATCH_DATAPATH_MISMATCH:
          'The peers belong to different Bridge datapath types.',
        PATCH_PEER_IDENTITY_UNAVAILABLE:
          'The peer identity is unavailable. No substitute object is selected.',
      } as Record<string, string>
    )[reason ?? ''] ?? 'The configured peer association is unknown.'
  );
}

export function currentPatchPeer(item: Interface): boolean {
  const p = item.patch_peer;
  return (
    field(item, 'type')?.availability === 'known' &&
    field(item, 'type')?.value === 'patch' &&
    field(item, 'type')?.source?.freshness === 'fresh' &&
    field(item, 'type')?.source?.confidence === 'proven' &&
    p?.availability === 'known' &&
    p.reason === 'PATCH_RECIPROCAL_CONFIGURATION' &&
    p.source?.freshness === 'fresh' &&
    p.source?.confidence === 'proven' &&
    p.peer_ref?.kind === 'interface' &&
    p.peer_port_ref?.kind === 'port' &&
    p.peer_bridge_ref?.kind === 'bridge'
  );
}
