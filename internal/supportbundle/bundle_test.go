package supportbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var sampleTime = time.Date(2026, 11, 28, 11, 2, 3, 456, time.UTC)

func TestBuildUsesFixedScopeDeterministicArchiveAndExpiry(t *testing.T) {
	created := sampleTime.In(time.FixedZone("fixture-zone", 8*3600))
	input := Inputs{
		Runtime: &RuntimeSnapshot{Version: "0.1.0-dev", Architecture: "arm64", Platform: "linux", AuthenticationReady: true},
		OVS:     &OVSSnapshot{Version: "3.3.9", State: "stale", Topology: &TopologyCounts{Bridges: 2, Ports: 4, Interfaces: 5}},
		Events:  &EvidencePage{Rows: []EvidenceRow{{Operation: "job-created", Outcome: "queued", ObservedAt: sampleTime}}},
		Audit:   &EvidencePage{},
	}
	requested := []Kind{KindAudit, KindEvents, KindHealth, KindOVS, KindRuntime}
	bundle, err := Build(t.Context(), created, requested, input)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(requested)
	same, err := Build(t.Context(), sampleTime, requested, input)
	if err != nil || !bytes.Equal(bundle.Bytes, same.Bytes) {
		t.Fatalf("same snapshot produced different ZIP: %v", err)
	}
	manifest := bundle.Manifest
	if manifest.Version != 1 || manifest.CreatedAt != sampleTime || manifest.ExpiresAt != sampleTime.Add(TTL) ||
		manifest.Expired(manifest.ExpiresAt.Add(-time.Nanosecond)) || !manifest.Expired(manifest.ExpiresAt) {
		t.Fatalf("manifest lifetime = %#v", manifest)
	}
	files, order := readBundle(t, bundle)
	expected := []string{"manifest.json", "runtime.json", "ovs.json", "events.json", "audit.json"}
	if !slices.Equal(order, expected) {
		t.Fatalf("file order %v, want %v", order, expected)
	}
	var archiveManifest Manifest
	if err := json.Unmarshal(files["manifest.json"], &archiveManifest); err != nil || !reflect.DeepEqual(archiveManifest, manifest) {
		t.Fatalf("archive/return manifest differ: %v", err)
	}
	for _, section := range archiveManifest.Sections {
		switch section.Kind {
		case KindHealth:
			if section.State != SectionUnavailable || section.Reason != "source-unavailable" || section.File != "" {
				t.Fatalf("unavailable signal %#v", section)
			}
		case KindAudit:
			if section.State != SectionNoData || !bytes.Contains(files[section.File], []byte(`"records": []`)) {
				t.Fatalf("no-data signal %#v", section)
			}
		default:
			if section.State != SectionIncluded {
				t.Fatalf("result %#v", section)
			}
		}
		if section.File != "" {
			payload, ok := files[section.File]
			digest := sha256.Sum256(payload)
			if !ok || len(payload) != section.Bytes || section.SHA256 != hex.EncodeToString(digest[:]) {
				t.Fatalf("section digest/size mismatch: %#v", section)
			}
		}
	}
}

func TestBuildProjectsOnlyKnownMetadataBeforeCentralRedaction(t *testing.T) {
	const (
		rawValue = "synthetic-private-marker"
		token    = "ovsg_example-private-fixture"
		path     = "/etc/shadow"
	)
	invalidClock := time.Time{}
	active, capacity := 3, 8
	input := Inputs{
		Runtime: &RuntimeSnapshot{Version: rawValue, Architecture: token, Platform: path},
		OVS:     &OVSSnapshot{Version: rawValue, State: token, Topology: &TopologyCounts{Bridges: 400}},
		Health: &HealthSnapshot{Components: []HealthComponent{
			{Key: rawValue, State: token},
			{Key: "manager", State: "ready"},
			{Key: "manager", State: token, ObservedAt: &invalidClock},
			{Key: "ipc-read", State: "ready", Active: &active, Capacity: &capacity},
		}},
		Events: &EvidencePage{Rows: []EvidenceRow{
			{Operation: rawValue, Outcome: path, ObservedAt: sampleTime},
			{Operation: "safe-apply-state", Outcome: "confirmed", ObservedAt: sampleTime.Add(-time.Minute)},
		}},
	}
	bundle, err := Build(t.Context(), sampleTime, []Kind{KindRuntime, KindOVS, KindHealth, KindEvents}, input)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := readBundle(t, bundle)
	for file, data := range files {
		for _, forbidden := range []string{rawValue, token, path} {
			if bytes.Contains(data, []byte(forbidden)) {
				t.Fatalf("%s contains unregistered metadata %q", file, forbidden)
			}
		}
	}
	if bytes.Contains(files["ovs.json"], []byte(`"topology"`)) {
		t.Fatal("unqualified OVS state exported topology counts")
	}
	var health struct{ Records []healthDocument }
	if err := json.Unmarshal(files["health.json"], &health); err != nil || len(health.Records) != 2 ||
		health.Records[0].Component != "ipc-read" || health.Records[0].Active == nil ||
		*health.Records[0].Active != 3 || health.Records[1].Component != "manager" || health.Records[1].State != "unknown" {
		t.Fatalf("health signal = %#v, %v", health.Records, err)
	}
	var events struct{ Records []evidenceDocument }
	if err := json.Unmarshal(files["events.json"], &events); err != nil || len(events.Records) != 2 ||
		events.Records[0].Operation != "other" || events.Records[0].Outcome != "other" ||
		events.Records[1].Operation != "safe-apply-state" || events.Records[1].Outcome != "confirmed" {
		t.Fatalf("audit/event projection = %#v, %v", events.Records, err)
	}
	raw, err := encodeDocument(map[string]any{"password": rawValue, "metadata": []any{token}, "operation": "recorded"})
	if err != nil || bytes.Contains(raw, []byte(rawValue)) || bytes.Contains(raw, []byte(token)) ||
		bytes.Count(raw, []byte("[REDACTED]")) != 2 {
		t.Fatalf("central redaction was bypassed: %s, %v", raw, err)
	}
}

func TestBuildBoundsRowsAndOnlyExportsRequestedKinds(t *testing.T) {
	rows := make([]EvidenceRow, MaxEvidenceRows+128)
	for i := range rows {
		rows[i] = EvidenceRow{Operation: "job-created", Outcome: "queued", ObservedAt: sampleTime.Add(-time.Duration(i) * time.Second)}
	}
	input := Inputs{Events: &EvidencePage{Rows: rows}, Audit: &EvidencePage{Rows: rows}}
	bundle, err := Build(t.Context(), sampleTime, []Kind{KindEvents}, input)
	if err != nil {
		t.Fatal(err)
	}
	files, order := readBundle(t, bundle)
	if !slices.Equal(order, []string{"manifest.json", "events.json"}) || len(bundle.Manifest.Sections) != 1 ||
		bundle.Manifest.Sections[0].State != SectionTruncated || bundle.Manifest.Sections[0].Reason != "record-limit" {
		t.Fatalf("truncation scope = %v, %#v", order, bundle.Manifest.Sections)
	}
	if count := bytes.Count(files["events.json"], []byte(`"job-created"`)); count != MaxEvidenceRows {
		t.Fatalf("record cap = %d, want %d", count, MaxEvidenceRows)
	}
	if len(bundle.Bytes) > MaxBundleBytes || len(files["events.json"]) > MaxSectionBytes {
		t.Fatal("archive or section byte ceiling exceeded")
	}
	limited, err := Build(t.Context(), sampleTime, []Kind{KindAudit}, Inputs{
		Audit: &EvidencePage{Rows: rows[:1], Truncated: true},
	})
	if err != nil || limited.Manifest.Sections[0].Reason != "source-truncated" {
		t.Fatalf("upstream truncation disappeared: %v", err)
	}
	var output archiveBuffer
	reader := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", MaxBundleBytes+1))}
	written, err := io.Copy(&output, reader)
	if !errors.Is(err, ErrTooLarge) || written != MaxBundleBytes {
		t.Fatalf("hard archive ceiling = %d, %v", written, err)
	}
}

func TestBuildRejectsInvalidScopeClockOrCancellation(t *testing.T) {
	for _, scope := range [][]Kind{nil, {KindOVS, KindOVS}, {"../../etc/shadow"}, {Kind("OVS")}} {
		if _, err := Build(t.Context(), sampleTime, scope, Inputs{}); !errors.Is(err, ErrInvalidScope) {
			t.Fatalf("scope %v: %v", scope, err)
		}
	}
	if _, err := Build(t.Context(), time.Time{}, []Kind{KindHealth}, Inputs{}); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("zero clock error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if build, err := Build(ctx, sampleTime, []Kind{KindOVS}, Inputs{}); !errors.Is(err, context.Canceled) || len(build.Bytes) != 0 {
		t.Fatalf("canceled Build produced %d bytes, %v", len(build.Bytes), err)
	}
}

func readBundle(t *testing.T, result Bundle) (map[string][]byte, []string) {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(result.Bytes), int64(len(result.Bytes)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte, len(archive.File))
	order := make([]string, 0, len(archive.File))
	for _, entry := range archive.File {
		order = append(order, entry.Name)
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(io.LimitReader(reader, MaxSectionBytes+1))
		reader.Close()
		if err != nil || len(payload) > MaxSectionBytes {
			t.Fatalf("entry %q exceeds bound or failed: %v", entry.Name, err)
		}
		files[entry.Name] = payload
	}
	return files, order
}
