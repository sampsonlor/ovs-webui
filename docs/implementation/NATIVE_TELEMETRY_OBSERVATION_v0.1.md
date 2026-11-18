# Native Bridge telemetry observation

The Telemetry page reads the sFlow, NetFlow and IPFIX references for each
Bridge from one OVSDB snapshot. The list is bounded and
uses the shared inventory cursor. Bridge details use the immutable Bridge
management ID and require inventory and configuration read permission.

The detail shows collector targets and selected native settings. An empty
reference means unconfigured. A missing referenced row means unknown. An
incompatible native column is unsupported, and unset settings remain unset.
Freshness applies to the OVSDB observation. Each exporter is evaluated
independently. The collector list shows at most 16 entries and reports its
count when known. Native external IDs and arbitrary options stay private.

The Bridge IPFIX reference covers per-Bridge sampling. OpenFlow-driven
sampling through Flow_Sample_Collector_Set is a separate native configuration.
Production collector receipt is outside the runtime evidence reported by the
page. Statistics has separate acceptance criteria in #26 and #43.

## Bridge exporter configuration

The desktop editor can configure, update or disable a Bridge's sFlow, NetFlow
or IPFIX exporter. Each Candidate stages one Bridge and protocol through the
shared Diff, validation and Safe Apply workflow.

Access requires a role with `ovs.telemetry.write` and the daemon's explicit
`--local-telemetry-bindings=<bridge-management-id>:<protocol>` grant. Use
`--grant-telemetry-role=<role-id>` or `--revoke-telemetry-role=<role-id>` with the
offline daemon to administer the role capability. Each deployment must grant
the intended protocol and Bridge explicitly. Controllers, external ownership,
shared exporter rows and hardware offload block the editor.

Collector destinations are numeric unicast UDP endpoints with explicit ports.
Use brackets around IPv6 literals. The editor exposes sFlow sampling and polling,
NetFlow active timeout, and IPFIX sampling, observation domain ID and observation
point ID. Updates preserve the remaining native settings and metadata.

Safe Apply checks the current Bridge binding and exporter state before writing.
It waits for OVSDB and the local `ovs-vswitchd` configuration acknowledgement.
Rollback removes a newly attached exporter, reverses an in-place update, or
recreates the saved exporter after disabling it. A recreated exporter has a new
OVSDB UUID. A changed reference, shared row or external configuration edit can
prevent automatic rollback. The execution then retains its uncertainty and
evidence for review in the shared workflow.
