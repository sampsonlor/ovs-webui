export function qosMessage(code: string): string {
  const messages: Record<string, string> = {
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
      'Wait for a fresh OVS observation before editing rates.',
    QOS_DEPENDENCY_CHANGED:
      'The QoS, Queue or Port dependencies changed. Review the current configuration and stage a new intent.',
    QOS_BINDING_CHANGED:
      'The Port binding changed. Review the current QoS before staging a new intent.',
    QOS_DETACHMENT_DISCIPLINE_UNSUPPORTED:
      'Detachment is available for verified linear HFSC on a Linux veth Port.',
    QOS_DETACHMENT_RUNTIME_UNPROVEN:
      'Detachment is unavailable until the installed HFSC scheduler and its Linux veth are verified.',
    QOS_DETACHMENT_CHECK_AT_DISPATCH:
      'Safe Apply verifies the current egress scheduler and checks that the same Port returns to its unshaped Linux default.',
    QOS_INSTALLED_RATES_CHECK_AT_DISPATCH:
      'Safe Apply checks the currently installed Linux rates before sending the change.',
  };
  return messages[code] ?? code;
}
