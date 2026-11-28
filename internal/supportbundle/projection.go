package supportbundle

import (
	"regexp"
	"slices"
	"sort"
	"time"
)

var releaseVersion = regexp.MustCompile(`^v?[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(-dev)?$`)

func publicTime(t time.Time) *time.Time {
	if t.IsZero() || t.Year() < 1970 || t.Year() > 9999 {
		return nil
	}
	public := t.UTC()
	if public.Year() < 1970 || public.Year() > 9999 {
		return nil
	}
	return &public
}

func observedTime(t time.Time) *time.Time { return publicTime(t.Truncate(time.Second)) }

func version(value string) string {
	if len(value) > 24 || !releaseVersion.MatchString(value) {
		return ""
	}
	return value
}

func fixed(value string, known ...string) string {
	if slices.Contains(known, value) {
		return value
	}
	return ""
}

func healthState(value string) string {
	return fixed(value, "ready", "degraded", "stale", "unavailable", "disabled", "unsupported", "unknown", "busy")
}

func project(kind Kind, in Inputs) (Section, any) {
	section := Section{Kind: kind, State: SectionIncluded}
	switch kind {
	case KindRuntime:
		if in.Runtime != nil {
			document, complete := projectRuntime(*in.Runtime)
			return sectionStatus(section, complete, ""), document
		}
	case KindOVS:
		if in.OVS != nil {
			document, complete := projectOVS(*in.OVS)
			return sectionStatus(section, complete, ""), document
		}
	case KindHealth:
		if in.Health != nil {
			components, complete, truncated := projectHealth(*in.Health)
			return collection(section, components, complete, truncated)
		}
	case KindEvents:
		if in.Events != nil {
			rows, complete, truncated := projectEvidence(*in.Events)
			return collection(section, rows, complete, truncated)
		}
	case KindAudit:
		if in.Audit != nil {
			rows, complete, truncated := projectEvidence(*in.Audit)
			return collection(section, rows, complete, truncated)
		}
	}
	section.State, section.Reason = SectionUnavailable, "source-unavailable"
	return section, nil
}

func collection[T any](section Section, records []T, complete bool, truncated string) (Section, any) {
	section = sectionStatus(section, complete, truncated)
	if len(records) == 0 && section.State == SectionIncluded {
		section.State, section.Reason = SectionNoData, "no-observations"
	}
	return section, struct {
		Records []T `json:"records"`
	}{Records: records}
}

func sectionStatus(section Section, complete bool, truncated string) Section {
	switch {
	case truncated != "":
		section.State, section.Reason = SectionTruncated, truncated
	case !complete:
		section.State, section.Reason = SectionPartial, "unqualified-metadata"
	}
	return section
}

func truncationReason(recordLimit, upstream bool) string {
	switch {
	case recordLimit:
		return "record-limit"
	case upstream:
		return "source-truncated"
	}
	return ""
}

type runtimeDocument struct {
	Version             string `json:"version,omitempty"`
	Architecture        string `json:"architecture,omitempty"`
	Platform            string `json:"platform,omitempty"`
	ConfigurationReady  bool   `json:"configuration_ready"`
	AuthenticationReady bool   `json:"authentication_ready"`
}

func projectRuntime(raw RuntimeSnapshot) (runtimeDocument, bool) {
	document := runtimeDocument{
		Version: version(raw.Version), Architecture: fixed(raw.Architecture, "amd64", "arm64"),
		Platform: fixed(raw.Platform, "linux"), ConfigurationReady: raw.ConfigurationReady,
		AuthenticationReady: raw.AuthenticationReady,
	}
	return document, document.Version != "" && document.Architecture != "" && document.Platform != ""
}

type ovsDocument struct {
	Version  string          `json:"version,omitempty"`
	State    string          `json:"state"`
	Topology *TopologyCounts `json:"topology,omitempty"`
}

func projectOVS(raw OVSSnapshot) (ovsDocument, bool) {
	document := ovsDocument{Version: version(raw.Version), State: healthState(raw.State)}
	if document.State == "" {
		document.State = "unknown"
	}
	if raw.Topology != nil && (document.State == "ready" || document.State == "stale" || document.State == "degraded") {
		counts := *raw.Topology
		if counts.Bridges >= 0 && counts.Ports >= 0 && counts.Interfaces >= 0 {
			document.Topology = &counts
		}
	}
	return document, document.Version != "" && document.Topology != nil && raw.State == document.State
}

type healthDocument struct {
	Component  string     `json:"component"`
	State      string     `json:"state"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	Active     *int       `json:"active,omitempty"`
	Capacity   *int       `json:"capacity,omitempty"`
}

var knownComponents = []string{
	"manager", "manager-db", "manager-secret-store", "webd", "web-db",
	"ovsdb", "cache-interface-devices", "cache-statistics", "cache-neighbors", "cache-lldp", "cache-fdb",
	"safety-watchdog", "ipc-auth", "ipc-control", "ipc-read", "ipc-heavy", "ipc-observation",
	"management-restart", "management-debug",
}

var queueComponents = []string{"ipc-auth", "ipc-control", "ipc-read", "ipc-heavy", "ipc-observation"}

func projectHealth(raw HealthSnapshot) ([]healthDocument, bool, string) {
	count := min(len(raw.Components), MaxHealthComponents)
	qualified := make(map[string]healthDocument, count)
	complete := true
	for _, row := range raw.Components[:count] {
		if !slices.Contains(knownComponents, row.Key) {
			complete = false
			continue
		}
		if _, duplicate := qualified[row.Key]; duplicate {
			qualified[row.Key] = healthDocument{Component: row.Key, State: "unknown"}
			complete = false
			continue
		}
		signal := healthDocument{Component: row.Key, State: healthState(row.State)}
		if signal.State == "" {
			signal.State = "unknown"
			complete = false
		}
		if row.ObservedAt != nil {
			signal.ObservedAt = observedTime(*row.ObservedAt)
			complete = complete && signal.ObservedAt != nil
		}
		if row.Active != nil || row.Capacity != nil {
			if row.Active != nil && row.Capacity != nil && slices.Contains(queueComponents, row.Key) &&
				*row.Capacity > 0 && *row.Active >= 0 && *row.Active <= *row.Capacity {
				active, capacity := *row.Active, *row.Capacity
				signal.Active, signal.Capacity = &active, &capacity
			} else {
				complete = false
			}
		}
		qualified[row.Key] = signal
	}
	components := make([]healthDocument, 0, len(qualified))
	for _, signal := range qualified {
		components = append(components, signal)
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Component < components[j].Component })
	return components, complete, truncationReason(count < len(raw.Components), raw.Truncated)
}

type evidenceDocument struct {
	Operation  string    `json:"operation"`
	Outcome    string    `json:"outcome"`
	ObservedAt time.Time `json:"observed_at"`
}

// Unregistered event or audit values collapse into generic metadata. Their text
// may be derived from an upstream error or extension and has no export slot.
var operations = []string{
	"health.transition", "ovsdb-inventory-changed", "job-created", "job-state-changed", "job-recovery-required",
	"safe-apply-state", "safe-apply-decision", "validate-candidate", "execute-fields", "field-execution-state",
	"createSupportBundle", "readArtifact", "createDiagnostic", "createCapture", "cancelJob",
	"management-service-restart", "management-debug", "tls-recovery",
}
var outcomes = []string{
	"requested", "received", "admitted", "queued", "running", "succeeded", "failed", "cancelled",
	"observed", "recorded", "ready", "degraded", "disabled", "stale", "unknown", "unavailable",
	"applied", "confirmed", "rolled-back", "timeout", "expired", "denied", "rejected",
	"recovery-required", "completed", "success", "failure",
}

func projectEvidence(raw EvidencePage) ([]evidenceDocument, bool, string) {
	count := min(len(raw.Rows), MaxEvidenceRows)
	rows := make([]evidenceDocument, 0, count)
	complete := true
	for _, row := range raw.Rows[:count] {
		at := observedTime(row.ObservedAt)
		if at == nil {
			complete = false
			continue
		}
		operation, outcome := fixed(row.Operation, operations...), fixed(row.Outcome, outcomes...)
		if operation == "" {
			operation = "other"
		}
		if outcome == "" {
			outcome = "other"
		}
		rows = append(rows, evidenceDocument{Operation: operation, Outcome: outcome, ObservedAt: *at})
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.After(right.ObservedAt)
		}
		if left.Operation != right.Operation {
			return left.Operation < right.Operation
		}
		return left.Outcome < right.Outcome
	})
	return rows, complete, truncationReason(count < len(raw.Rows), raw.Truncated)
}
