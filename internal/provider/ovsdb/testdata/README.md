# Upstream OVS schema fixtures

Retrieved 2026-09-15 from the official Open vSwitch repository, unmodified. These
are schema inputs, not production data or evidence that each binary was tested.
Native CI records the actual ovsdb-server and ovs-vswitchd versions separately.

| Tag | Source | SHA-256 |
| --- | --- | --- |
| v3.3.9 | https://raw.githubusercontent.com/openvswitch/ovs/v3.3.9/vswitchd/vswitch.ovsschema | c18c754664bdb880c73d7ba49553fc898c43b1b8fbc80b6c0e53162b3cd36fba |
| v3.7.1 | https://raw.githubusercontent.com/openvswitch/ovs/v3.7.1/vswitchd/vswitch.ovsschema | 35663bcb750d28f743d95039bde4eed1eee3324791a8981cde31c7e1829b2232 |
| v4.0.0 | https://raw.githubusercontent.com/openvswitch/ovs/v4.0.0/vswitchd/vswitch.ovsschema | 35663bcb750d28f743d95039bde4eed1eee3324791a8981cde31c7e1829b2232 |

The last two tags contain byte-identical schemas. Capabilities must follow the
schema, not the OVS product version. Upstream copyright and Apache-2.0 terms:
https://github.com/openvswitch/ovs/blob/v3.3.9/LICENSE and NOTICE (local copies included).
