package inventory

import (
	"testing"
	"time"
)

func byHealthKey(t *testing.T, observations []HealthObservation, key string) HealthObservation {
	t.Helper()
	for _, obs := range observations {
		if obs.Key == key {
			return obs
		}
	}
	t.Fatalf("no health evidence for %q", key)
	return HealthObservation{}
}

func TestInventoryRuntimeHealthOnlyProjectsQualifiedCacheEvidence(t *testing.T) {
	f := newNeighborTest(t)
	read := func(key string) HealthObservation { return byHealthKey(t, f.service.RuntimeHealth(), key) }
	if ovs := read("ovsdb"); ovs.State != "ready" || ovs.ObservedAt != f.now {
		t.Fatalf("did not report the native OVS monitor sample: %+v", ovs)
	}
	if neighbor := read("cache-neighbors"); neighbor.State != "unknown" || !neighbor.ObservedAt.IsZero() || f.reads != 0 {
		t.Fatalf("read attempted collection or invented freshness: %+v, reads %d", neighbor, f.reads)
	}
	for _, key := range []string{"cache-lldp", "cache-fdb"} {
		if optional := read(key); optional.State != "disabled" || !optional.ObservedAt.IsZero() {
			t.Fatalf("inferred unconfigured provider state: %+v", optional)
		}
	}
	f.service.SampleNeighbors(t.Context())
	collected := f.now
	f.now = f.now.Add(time.Second)
	if neighbor := read("cache-neighbors"); neighbor.State != "ready" || neighbor.ObservedAt != collected || f.reads != 1 {
		t.Fatalf("Health refreshed the provider or lost its evidence: %+v, reads %d", neighbor, f.reads)
	}

	f.now = f.now.Add(neighborFreshFor)
	f.observation.Evidence.ObservedAt = f.now
	f.service.install(f.observation, f.decision)
	if neighbor := read("cache-neighbors"); neighbor.State != "stale" || neighbor.ObservedAt != collected {
		t.Fatalf("a fresh OVSDB monitor incorrectly refreshed the neighbor provider: %+v", neighbor)
	}
	f.service.Unavailable("OVSDB_PEER_DENIED")
	if ovs := read("ovsdb"); ovs.State != "unavailable" || ovs.Reason != "OVSDB_PEER_DENIED" {
		t.Fatalf("an OVS monitor failure became a dataplane assertion: %+v", ovs)
	}
	if neighbor := read("cache-neighbors"); neighbor.State != "unavailable" || !neighbor.ObservedAt.IsZero() || f.reads != 1 {
		t.Fatalf("dependent cache retained a misleading observation during source loss: %+v", neighbor)
	}

	f.service.install(f.observation, f.decision)
	if neighbor := read("cache-neighbors"); neighbor.State != "unknown" || !neighbor.ObservedAt.IsZero() {
		t.Fatalf("reconnecting OVS resurrected pre-outage neighbor evidence: %+v", neighbor)
	}
	f.service.mu.Lock()
	contended := f.service.RuntimeHealth()
	f.service.mu.Unlock()
	if ovs := byHealthKey(t, contended, "ovsdb"); ovs.State != "unknown" || ovs.Reason != "STATE_CACHE_BUSY" {
		t.Fatalf("manager read did not shed on cache contention: %+v", ovs)
	}
}
