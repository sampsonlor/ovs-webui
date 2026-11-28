package auth

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

type accelerationOVS struct {
	State       string                             `json:"state"`
	Reason      string                             `json:"reason_code,omitempty"`
	Observation *inventory.AccelerationObservation `json:"observation,omitempty"`
}

type accelerationView struct {
	OVS  accelerationOVS   `json:"ovs"`
	Host *hostAcceleration `json:"host,omitempty"`
}

type hostAcceleration struct {
	State      string                              `json:"state"`
	Reason     string                              `json:"reason_code,omitempty"`
	Source     string                              `json:"source"`
	ObservedAt *time.Time                          `json:"observed_at,omitempty"`
	OnlineNUMA inventory.AccelerationField[uint16] `json:"online_numa_nodes"`
	HugePages  []hostHugePages                     `json:"huge_pages"`
}

type hostHugePages struct {
	SizeKiB         uint32                              `json:"size_kib"`
	PersistentPages inventory.AccelerationField[string] `json:"persistent_pages"`
	FreePages       inventory.AccelerationField[string] `json:"free_pages"`
	ReservedPages   inventory.AccelerationField[string] `json:"reserved_pages"`
	SurplusPages    inventory.AccelerationField[string] `json:"surplus_pages"`
}

func pageCount(in inventory.AccelerationField[uint64]) inventory.AccelerationField[string] {
	out := inventory.AccelerationField[string]{State: in.State, Reason: in.Reason}
	if in.Value != nil {
		value := strconv.FormatUint(*in.Value, 10)
		out.Value = &value
	}
	return out
}

func (r *Repository) WithAccelerationHost(observation func() *inventory.HostAccelerationObservation) *Repository {
	r.accelerationHost = observation
	return r
}

func (r *Repository) readAcceleration(ctx context.Context, claims authn.Claims) (any, error) {
	if !slices.Contains(claims.Capabilities, "inventory.read") {
		return nil, apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	out := accelerationView{OVS: accelerationOVS{State: "unavailable", Reason: "ACCELERATION_OVSDB_UNAVAILABLE"}}
	if source, ok := r.inventory.(interface {
		ObserveAcceleration(context.Context, authn.Claims) (inventory.AccelerationObservation, error)
	}); ok {
		observed, err := source.ObserveAcceleration(ctx, claims)
		if err == nil {
			out.OVS = accelerationOVS{State: "ready", Observation: &observed}
		} else {
			var issue *apitypes.Problem
			if !errors.As(err, &issue) || issue.Status != 503 {
				return nil, err
			}
			out.OVS.Reason = issue.Code
			if issue.Code == "ACCELERATION_OVSDB_STALE" {
				out.OVS.State = "stale"
			} else if issue.Code == "ACCELERATION_CACHE_BUSY" {
				out.OVS.State = "unknown"
			}
		}
	}
	if r.accelerationHost != nil {
		if value := r.accelerationHost(); value != nil {
			sample := *value
			if sample.ObservedAt != nil {
				now := r.now()
				if now.Before(*sample.ObservedAt) || now.Sub(*sample.ObservedAt) > 90*time.Second {
					sample.State, sample.Reason = "stale", "ACCELERATION_HOST_EVIDENCE_EXPIRED"
				}
			}
			host := &hostAcceleration{State: sample.State, Reason: sample.Reason, Source: sample.Source,
				ObservedAt: sample.ObservedAt, OnlineNUMA: sample.OnlineNUMA, HugePages: make([]hostHugePages, 0, len(sample.HugePages))}
			for _, pool := range sample.HugePages {
				host.HugePages = append(host.HugePages, hostHugePages{SizeKiB: pool.SizeKiB,
					PersistentPages: pageCount(pool.PersistentPages), FreePages: pageCount(pool.FreePages),
					ReservedPages: pageCount(pool.ReservedPages), SurplusPages: pageCount(pool.SurplusPages)})
			}
			out.Host = host
		}
	}
	return out, nil
}
