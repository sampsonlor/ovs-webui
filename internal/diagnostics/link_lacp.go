// Package diagnostics runs fixed, typed management diagnostic operations.
package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/authn"
)

const (
	LinkLACPTemplate   = "diag.net.link-lacp"
	LinkLACPCapability = "diagnostics.read"
	MaxLinkLACPPorts   = 10
	MaxLinkLACPMembers = 64
	MaxResultBytes     = 64 << 10
)

type LinkLACPRequest struct {
	Target        apitypes.Ref
	SampleSeconds int
	PortLimit     int
}

func (r LinkLACPRequest) Authorize(c authn.Claims) error {
	if !slices.Contains(c.Capabilities, LinkLACPCapability) || !slices.Contains(c.Capabilities, "inventory.read") {
		return apitypes.Fail(403, "CAPABILITY_DENIED")
	}
	if (r.Target.Kind != "port" && r.Target.Kind != "bridge") || !apitypes.ManagementID(r.Target.ID) {
		return apitypes.Fail(422, "INVALID_DIAGNOSTIC_TARGET")
	}
	if r.SampleSeconds != 5 && r.SampleSeconds != 10 && r.SampleSeconds != 15 {
		return apitypes.Fail(422, "INVALID_DIAGNOSTIC_SAMPLE_BUDGET")
	}
	if r.PortLimit < 1 || r.PortLimit > MaxLinkLACPPorts {
		return apitypes.Fail(422, "INVALID_DIAGNOSTIC_PORT_LIMIT")
	}
	return nil
}

type LinkLACPMember struct {
	Ref         apitypes.Ref `json:"interface_ref"`
	Link        string       `json:"link"`
	LACPCurrent string       `json:"lacp_current"`
}

type LinkLACPPort struct {
	Ref     apitypes.Ref     `json:"port_ref"`
	LACP    string           `json:"configured_lacp"`
	Members []LinkLACPMember `json:"members"`
}

type LinkLACPSample struct {
	Target             apitypes.Ref
	ObservedAt         time.Time
	Ports              []LinkLACPPort
	Partial, Truncated bool
}

type LinkLACPSource interface {
	ObserveLinkLACP(context.Context, LinkLACPRequest, authn.Claims) (LinkLACPSample, error)
}

type LinkLACPResult struct {
	Template   string         `json:"template_id"`
	Target     apitypes.Ref   `json:"target_ref"`
	State      string         `json:"state"`
	Reason     string         `json:"reason_code,omitempty"`
	ObservedAt *time.Time     `json:"observed_at,omitempty"`
	Authority  string         `json:"authority,omitempty"`
	Ports      []LinkLACPPort `json:"ports"`
	Partial    bool           `json:"partial"`
	Truncated  bool           `json:"truncated"`
}

// RunLinkLACP samples one coherent snapshot under the selected time budget.
// The manager's Job worker supplies current claims and owns durable lifecycle,
// pressure limits, and authorization rechecks before committing a result.
func RunLinkLACP(ctx context.Context, source LinkLACPSource, req LinkLACPRequest, claims authn.Claims) (LinkLACPResult, error) {
	if err := req.Authorize(claims); err != nil {
		return LinkLACPResult{}, err
	}
	out := LinkLACPResult{Template: LinkLACPTemplate, Target: req.Target, Ports: []LinkLACPPort{}}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.SampleSeconds)*time.Second)
	defer cancel()
	sample, err := observeLinkLACP(ctx, source, req, claims)
	if err == nil && ctx.Err() == nil {
		finished := finishLinkLACP(out, sample, req.PortLimit, slices.Contains(claims.Capabilities, "configuration.read"))
		if ctx.Err() == nil {
			return finished, nil
		}
	}
	if stopped := ctx.Err(); stopped != nil {
		err = stopped
	}
	switch {
	case errors.Is(err, context.Canceled):
		out.State, out.Reason = "cancelled", "DIAGNOSTIC_CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		out.State, out.Reason = "timed-out", "DIAGNOSTIC_DEADLINE_EXCEEDED"
	case err != nil:
		var problem *apitypes.Problem
		if errors.As(err, &problem) {
			switch problem.Code {
			case "NOT_FOUND":
				return LinkLACPResult{}, apitypes.Fail(404, "NOT_FOUND")
			case "CAPABILITY_DENIED":
				return LinkLACPResult{}, apitypes.Fail(403, "CAPABILITY_DENIED")
			case "DIAGNOSTIC_INVENTORY_UNAVAILABLE":
				out.State, out.Reason = "provider-unavailable", "DIAGNOSTIC_INVENTORY_UNAVAILABLE"
			case "DIAGNOSTIC_EVIDENCE_UNAVAILABLE":
				out.State, out.Reason = "evidence-unavailable", "DIAGNOSTIC_EVIDENCE_UNAVAILABLE"
			}
		}
		if out.State == "" {
			out.State, out.Reason = "operation-failed", "DIAGNOSTIC_OPERATION_FAILED"
		}
	}
	return out, nil
}

func observeLinkLACP(ctx context.Context, source LinkLACPSource, req LinkLACPRequest, claims authn.Claims) (LinkLACPSample, error) {
	if err := ctx.Err(); err != nil {
		return LinkLACPSample{}, err
	}
	if source == nil {
		return LinkLACPSample{}, apitypes.Fail(503, "DIAGNOSTIC_INVENTORY_UNAVAILABLE")
	}
	return source.ObserveLinkLACP(ctx, req, claims)
}

func finishLinkLACP(out LinkLACPResult, sample LinkLACPSample, limit int, configAllowed bool) LinkLACPResult {
	unavailable := out
	unavailable.State, unavailable.Reason = "evidence-unavailable", "DIAGNOSTIC_EVIDENCE_UNAVAILABLE"
	if sample.Target != out.Target || sample.ObservedAt.IsZero() || len(sample.Ports) > limit ||
		(out.Target.Kind == "port" && len(sample.Ports) != 1) {
		return unavailable
	}
	seen := map[string]bool{}
	members := 0
	out.Partial, out.Truncated = sample.Partial, sample.Truncated
	for _, port := range sample.Ports {
		members += len(port.Members)
		if members > MaxLinkLACPMembers || !acceptLinkRef(port.Ref, "port", seen) ||
			(out.Target.Kind == "port" && port.Ref != out.Target) {
			return unavailable
		}
		next := LinkLACPPort{Ref: port.Ref, LACP: port.LACP, Members: []LinkLACPMember{}}
		out.Partial = out.Partial || len(port.Members) == 0
		if !configAllowed {
			next.LACP, out.Partial = "withheld", true
		} else if !slices.Contains([]string{"active", "passive", "off"}, next.LACP) {
			next.LACP, out.Partial = "unknown", true
		}
		for _, member := range port.Members {
			if !acceptLinkRef(member.Ref, "interface", seen) {
				return unavailable
			}
			if member.Link != "up" && member.Link != "down" {
				member.Link, out.Partial = "unknown", true
			}
			if !slices.Contains([]string{"current", "not-current", "not-applicable"}, member.LACPCurrent) ||
				(member.LACPCurrent == "not-applicable" && next.LACP != "off") {
				member.LACPCurrent, out.Partial = "unknown", true
			}
			next.Members = append(next.Members, member)
		}
		out.Ports = append(out.Ports, next)
	}
	switch {
	case out.Truncated:
		out.State, out.Reason = "truncated", "DIAGNOSTIC_OUTPUT_TRUNCATED"
	case out.Partial:
		out.State, out.Reason = "partial", "DIAGNOSTIC_OBSERVATION_INCOMPLETE"
	case len(out.Ports) == 0:
		out.State, out.Reason = "no-data", "DIAGNOSTIC_NO_PORTS"
	default:
		out.State = "complete"
	}
	at := sample.ObservedAt.UTC()
	out.ObservedAt, out.Authority = &at, "ovsdb-structured"
	if encoded, err := json.Marshal(out); err != nil || len(encoded) > MaxResultBytes {
		return unavailable
	}
	return out
}

func acceptLinkRef(ref apitypes.Ref, kind string, seen map[string]bool) bool {
	if ref.Kind != kind || !apitypes.ManagementID(ref.ID) || seen[ref.ID] {
		return false
	}
	seen[ref.ID] = true
	return true
}
