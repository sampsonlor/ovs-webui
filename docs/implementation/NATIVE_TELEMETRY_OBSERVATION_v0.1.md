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
Collector receipt remains unverified. Exporter configuration changes and
Statistics have separate acceptance criteria in #26 and #43.
