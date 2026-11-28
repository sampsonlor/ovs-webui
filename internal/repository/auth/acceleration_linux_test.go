package auth

import (
	"context"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

type staleAccelerationInventory struct{}

func (staleAccelerationInventory) Read(context.Context, string, map[string]string, url.Values, authn.Claims) (any, error) {
	return nil, apitypes.Fail(503, "INVENTORY_UNAVAILABLE")
}

func (staleAccelerationInventory) ObserveAcceleration(context.Context, authn.Claims) (inventory.AccelerationObservation, error) {
	return inventory.AccelerationObservation{}, apitypes.Fail(503, "ACCELERATION_OVSDB_STALE")
}

func TestRuntimeAccelerationKeepsKernelCounterEvidenceIndependent(t *testing.T) {
	r, _ := fixture(t)
	actor := login(t, r, "admin", false)
	at := time.Now().UTC().Add(-time.Minute)
	pages := uint64(math.MaxUint64)
	r.WithInventory(staleAccelerationInventory{}).WithAccelerationHost(func() *inventory.HostAccelerationObservation {
		return &inventory.HostAccelerationObservation{State: "partial", Source: "linux-global-sysfs", ObservedAt: &at,
			OnlineNUMA: inventory.AccelerationField[uint16]{State: "unknown"},
			HugePages: []inventory.HugePagePool{{
				SizeKiB: 2048, PersistentPages: inventory.AccelerationField[uint64]{State: "observed", Value: &pages},
				FreePages:     inventory.AccelerationField[uint64]{State: "unknown"},
				ReservedPages: inventory.AccelerationField[uint64]{State: "unknown"},
				SurplusPages:  inventory.AccelerationField[uint64]{State: "unknown"},
			}}}
	})
	observed, err := r.ReadAuth(t.Context(), actor.Grant, authn.Query{Method: "GET", URI: "/api/v1/runtime/acceleration"})
	if err != nil || observed.Status != 200 {
		t.Fatalf("both sources were lost when OVSDB became stale: %s, %v", observed.Body, err)
	}
	if !strings.Contains(string(observed.Body), `"state":"stale","reason_code":"ACCELERATION_OVSDB_STALE"`) ||
		!strings.Contains(string(observed.Body), `"value":"18446744073709551615"`) {
		t.Fatalf("source state or decimal precision changed: %s", observed.Body)
	}
}
