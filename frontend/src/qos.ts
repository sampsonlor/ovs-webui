export const qosRateLimit = 4294967288;

export function parseQoSRate(raw: string): number {
  const value = Number(raw);
  return /^[1-9][0-9]*$/.test(raw) && Number.isSafeInteger(value) && value % 8 === 0 && value <= qosRateLimit
    ? value
    : 0;
}

export function isQoSOperation(operation: string | undefined): boolean {
  return operation === 'qos.configure' || operation === 'qos.detach' || operation === 'qos.attach' || operation === 'qos.create';
}

export function qosMessage(code: string, fallback = code): string {
  const messages: Record<string, string> = {
    QOS_CREATION_SCHEMA_UNSUPPORTED: 'The current OVSDB schema cannot create an unbound QoS profile with dedicated Queues.',
    QOS_CREATION_AUTHORITY_REQUIRED: 'An administrator must authorize a profile name before you can create this QoS.',
    QOS_CREATION_NAME_IN_USE: 'The authorized profile name is in use. Choose another available name.',
    QOS_CREATED_PROFILE_BOUND: 'The new profile is bound to a Port. Review the current binding before recovery.',
    QOS_CREATED_CONFIGURATION_CHANGED: 'The new profile or its Queues changed before recovery. Review the current configuration.',
    EXCLUSIVE_QOS_AUTHORITY_REQUIRED:
      'An administrator must authorize this Port and QoS pair before rates can be edited.',
    QOS_SHARED_PORT_BINDING:
      'This QoS is shared by multiple Ports. Rate editing requires a dedicated binding.',
    QOS_PORT_UNBOUND: 'Bind this QoS to a Port before preparing rate changes.',
    QOS_PORT_BINDING_UNKNOWN: 'The current QoS binding could not be verified.',
    QOS_QUEUE_SHARING_UNPROVEN:
      'Rate editing requires each Queue to be dedicated to this QoS.',
    QOS_WRITE_SCHEMA_UNSUPPORTED:
      'The current OVSDB schema does not support guarded rate editing.',
    QOS_DEVICE_BINDING_UNPROVEN:
      'Rate editing requires a verified, standalone software Port.',
    QOS_NATIVE_RATES_UNSUPPORTED:
      'Rate editing requires explicit, supported rates on the existing QoS and Queues.',
    EXTERNALLY_CONTROLLED: 'This configuration is externally controlled.',
    OVS_ASSOCIATION_STALE:
      'Wait for a fresh OVS observation before preparing the QoS change.',
    QOS_DEPENDENCY_CHANGED:
      'The QoS, Queue or Port dependencies changed. Review the current configuration and stage a new intent.',
    QOS_BINDING_CHANGED:
      'The Port binding changed. Review the current QoS before staging a new intent.',
    QOS_ATTACHMENT_DISCIPLINE_UNSUPPORTED:
      'Attachment requires a supported linear HFSC configuration.',
    QOS_ATTACHMENT_RUNTIME_UNPROVEN:
      'Attachment requires verification that the target Linux veth Port is using its unshaped default scheduler.',
    QOS_ATTACHMENT_CHECK_AT_DISPATCH:
      'Safe Apply verifies that the target Port starts with the unshaped default and that Linux installs the reviewed HFSC profile.',
    QOS_DETACHMENT_DISCIPLINE_UNSUPPORTED:
      'Detachment is available for verified linear HFSC on a Linux veth Port.',
    QOS_DETACHMENT_RUNTIME_UNPROVEN:
      'Detachment is unavailable until the installed HFSC scheduler and its Linux veth are verified.',
    QOS_DETACHMENT_CHECK_AT_DISPATCH:
      'Safe Apply verifies the current egress scheduler and checks that the same Port returns to its unshaped Linux default.',
    QOS_INSTALLED_RATES_CHECK_AT_DISPATCH:
      'Safe Apply checks the currently installed Linux rates before sending the change.',
  };
  return messages[code] ?? fallback;
}
