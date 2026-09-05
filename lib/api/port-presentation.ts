import {
  nativeVlanFields,
  vlanLabel,
  type Port,
  type VlanValue,
} from '../ovs-model.ts';
import { validateVlan } from '../change-control.ts';
import type { PortResource, VlanIntent } from './types.generated';

export function fromWireVlan(value: VlanIntent): VlanValue {
  return { ...value, trunks: value.trunks.join(', ') };
}
export function toWireVlan(value: VlanValue): VlanIntent {
  const invalid = validateVlan(value);
  if (invalid) throw new Error(invalid);
  return {
    mode: value.mode,
    tag: value.tag,
    trunks: nativeVlanFields(value).trunks,
  } as VlanIntent;
}
export function portPresentation(port: PortResource): Port {
  const configuration = port.configuration;
  const config =
    configuration.availability === 'known'
      ? fromWireVlan(configuration.value)
      : { mode: 'access' as const, tag: null, trunks: '' };
  return {
    name: port.name,
    uuid: port.id,
    bridge: port.bridgeId,
    state:
      port.linkState === 'up'
        ? 'Up'
        : port.linkState === 'down'
          ? 'Down'
          : 'Unknown',
    speed:
      port.speedMbps === null
        ? 'Unknown'
        : port.speedMbps >= 1000
          ? `${port.speedMbps / 1000} Gbps`
          : `${port.speedMbps} Mbps`,
    vlan:
      configuration.availability === 'known'
        ? vlanLabel(config)
        : configuration.reason,
    config,
    provider: port.provider,
    authority:
      port.authority === 'ovs'
        ? 'OVS'
        : port.authority === 'external'
          ? 'External'
          : 'Unknown',
    scope:
      port.scope === 'manage'
        ? 'Manage'
        : port.scope === 'basic-manage'
          ? 'Basic Manage'
          : 'Observe',
    interfaceName: port.interfaces.map((item) => item.name).join(', '),
    ...(port.kind === 'bond'
      ? { members: port.interfaces.map((item) => item.name) }
      : {}),
  };
}
export function portEditable(port: PortResource) {
  return (
    port.authority === 'ovs' &&
    port.scope !== 'observe' &&
    port.writableFields.includes('vlan') &&
    port.configuration.availability === 'known'
  );
}
