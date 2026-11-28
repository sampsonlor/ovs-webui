package ovsdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

const (
	MaxOpenFlowRows  = 128
	MaxOpenFlowBytes = 48 << 10
	openFlowTimeout  = 2 * time.Second
)

// OpenFlowBinding is a private root-recorded endpoint and current installed
// Bridge witness. The resolver must prove the native UUID independently of
// the observed datapath ID. A Bridge name, cookie or DPID alone cannot bind it.
type OpenFlowBinding struct {
	BridgeID, NativeBridgeUUID, InstanceGeneration string
	DatapathID, SocketDevice, SocketInode          uint64
	Socket, Daemon                                 string
	PeerUID                                        uint32
	installedProof                                 string
}

// OpenFlowResolver verifies a fresh, independently witnessed association with
// this exact Bridge. It may only look up endpoints recorded by local manager
// policy, and must respect the supplied deadline. No client-provided socket
// name or OVSDB controller target can be used as an OpenFlow endpoint.
type OpenFlowResolver func(context.Context, string) (OpenFlowBinding, error)

type OpenFlowQuery struct {
	BridgeID            string
	Table               *uint8
	Cookie              *uint64
	Limit               int
	IncludeGroupsMeters bool // Bridge-wide descriptions; Table and Cookie only filter flows.
	RawFields           bool
}

type OpenFlowCapturedQuery struct {
	Table               *uint8 `json:"table,omitempty"`
	Cookie              string `json:"cookie,omitempty"`
	Limit               int    `json:"limit"`
	IncludeGroupsMeters bool   `json:"include_groups_meters,omitempty"`
	RawFields           bool   `json:"raw_fields,omitempty"`
}

type OpenFlowMatchField struct {
	Class        uint16 `json:"class"`
	Field        uint8  `json:"field"`
	Masked       bool   `json:"masked"`
	PayloadBytes uint8  `json:"payload_bytes"`
	RawPayload   string `json:"raw_payload_hex,omitempty"`
}

type OpenFlowInstruction struct {
	Type       uint16   `json:"type"`
	Bytes      uint16   `json:"bytes"`
	Actions    []uint16 `json:"action_types,omitempty"`
	RawPayload string   `json:"raw_payload_hex,omitempty"`
}

type OpenFlowRow struct {
	Table               uint8                 `json:"table"`
	Priority            uint16                `json:"priority"`
	Cookie              string                `json:"cookie"`
	DurationSeconds     uint32                `json:"duration_seconds"`
	DurationNanoseconds uint32                `json:"duration_nanoseconds"`
	Packets             *string               `json:"packets"`
	Bytes               *string               `json:"bytes"`
	Match               []OpenFlowMatchField  `json:"match_fields"`
	Instructions        []OpenFlowInstruction `json:"instructions"`
	MetadataTruncated   bool                  `json:"metadata_truncated"`
}

type OpenFlowSnapshot struct {
	BridgeID             string                `json:"bridge_id"`
	InstanceGeneration   string                `json:"instance_generation"`
	Query                OpenFlowCapturedQuery `json:"query"`
	ObservedAt           time.Time             `json:"observed_at"`
	Source               string                `json:"source"`
	Ownership            string                `json:"ownership"`
	FieldCoverage        string                `json:"field_coverage"`
	Groups               string                `json:"groups"`
	GroupReason          string                `json:"group_reason,omitempty"`
	GroupRows            []OpenFlowGroup       `json:"group_rows,omitempty"`
	Meters               string                `json:"meters"`
	MeterReason          string                `json:"meter_reason,omitempty"`
	MeterRows            []OpenFlowMeter       `json:"meter_rows,omitempty"`
	GroupMeterStatistics string                `json:"group_meter_statistics,omitempty"`
	Rows                 []OpenFlowRow         `json:"rows"`
	Truncated            bool                  `json:"truncated"`
}

type OpenFlowObserver struct {
	resolve OpenFlowResolver
	read    chan struct{}
}

func NewOpenFlowObserver(resolve OpenFlowResolver) *OpenFlowObserver {
	return &OpenFlowObserver{resolve: resolve, read: make(chan struct{}, 1)}
}

// ObserveFlows retains only one bounded read at a time per configured observer.
// The caller owns current permission checks and any retention or export.
func (o *OpenFlowObserver) ObserveFlows(ctx context.Context, query OpenFlowQuery) (OpenFlowSnapshot, error) {
	frozen, err := freezeOpenFlowQuery(query)
	if err != nil {
		return OpenFlowSnapshot{}, err
	}
	if o == nil || o.resolve == nil {
		return OpenFlowSnapshot{}, apitypes.Fail(503, "OPENFLOW_SOURCE_UNAVAILABLE")
	}
	if err := ctx.Err(); err != nil {
		return OpenFlowSnapshot{}, err
	}
	select {
	case o.read <- struct{}{}:
		defer func() { <-o.read }()
	default:
		return OpenFlowSnapshot{}, apitypes.Fail(503, "OPENFLOW_PROVIDER_BUSY")
	}
	bounded, cancel := context.WithTimeout(ctx, openFlowTimeout)
	defer cancel()
	before, err := o.resolve(bounded, query.BridgeID)
	if err != nil {
		return OpenFlowSnapshot{}, openFlowFailure(bounded, err)
	}
	if !validOpenFlowBinding(before, query.BridgeID) {
		return OpenFlowSnapshot{}, apitypes.Fail(503, "OPENFLOW_IDENTITY_UNPROVEN")
	}
	wire, err := dialOpenFlowUnix(bounded, before)
	if err != nil {
		return OpenFlowSnapshot{}, openFlowFailure(bounded, err)
	}
	defer wire.Close()
	stop := context.AfterFunc(bounded, func() { _ = wire.Close() })
	defer stop()
	deadline, _ := bounded.Deadline()
	if wire.SetDeadline(deadline) != nil {
		return OpenFlowSnapshot{}, apitypes.Fail(503, "OPENFLOW_SOURCE_UNAVAILABLE")
	}
	var aux openFlowAuxiliary
	var rows []OpenFlowRow
	var limited bool
	if frozen.auxiliary {
		rows, limited, err = exchangeOpenFlowWithAuxiliary(wire, before.DatapathID, frozen, &aux)
	} else {
		rows, limited, err = exchangeOpenFlow(wire, before.DatapathID, frozen)
	}
	if err != nil {
		return OpenFlowSnapshot{}, openFlowFailure(bounded, err)
	}
	observed := time.Now().UTC()
	after, err := o.resolve(bounded, query.BridgeID)
	if err != nil || after != before || !verifyOpenFlowUnix(wire, before) {
		return OpenFlowSnapshot{}, openFlowFailure(bounded, errOpenFlowIdentity)
	}
	if bounded.Err() != nil {
		return OpenFlowSnapshot{}, openFlowFailure(bounded, bounded.Err())
	}
	result := OpenFlowSnapshot{
		BridgeID: before.BridgeID, InstanceGeneration: before.InstanceGeneration,
		Query: OpenFlowCapturedQuery{Limit: frozen.limit}, ObservedAt: observed,
		Source: "openflow-1.3-unix", Ownership: "unknown", FieldCoverage: "metadata-only",
		Groups: "unavailable", Meters: "unavailable", Rows: rows, Truncated: limited,
	}
	if frozen.table != openFlowAllTables {
		result.Query.Table = &frozen.table
	}
	if frozen.hasCookie {
		result.Query.Cookie = openFlowHex(frozen.cookie)
	}
	if frozen.rawFields {
		result.Query.RawFields = true
		result.FieldCoverage = "bounded-raw-fields"
	}
	if frozen.auxiliary {
		result.Query.IncludeGroupsMeters = true
		result.Groups, result.GroupReason, result.GroupRows = aux.groupState, aux.groupReason, aux.groups
		result.Meters, result.MeterReason, result.MeterRows = aux.meterState, aux.meterReason, aux.meters
		result.GroupMeterStatistics = "unknown"
	}
	if encoded, err := json.Marshal(result); err != nil || len(encoded) > MaxOpenFlowBytes {
		return OpenFlowSnapshot{}, apitypes.Fail(503, "OPENFLOW_RESPONSE_INVALID")
	}
	return result, nil
}

type openFlowQuery struct {
	table     uint8
	limit     int
	hasCookie bool
	cookie    uint64
	auxiliary bool
	rawFields bool
}

func freezeOpenFlowQuery(q OpenFlowQuery) (openFlowQuery, error) {
	if !apitypes.ManagementID(q.BridgeID) || q.Limit < 1 || q.Limit > MaxOpenFlowRows {
		return openFlowQuery{}, apitypes.Fail(422, "INVALID_OPENFLOW_QUERY")
	}
	out := openFlowQuery{table: openFlowAllTables, limit: q.Limit,
		auxiliary: q.IncludeGroupsMeters, rawFields: q.RawFields}
	if q.Table != nil {
		if *q.Table == openFlowAllTables {
			return openFlowQuery{}, apitypes.Fail(422, "INVALID_OPENFLOW_TABLE")
		}
		out.table = *q.Table
	}
	if q.Cookie != nil {
		out.cookie, out.hasCookie = *q.Cookie, true
	}
	return out, nil
}

func validOpenFlowBinding(b OpenFlowBinding, id string) bool {
	return b.BridgeID == id && apitypes.UUID(b.NativeBridgeUUID) &&
		apitypes.ManagementID(b.InstanceGeneration) && b.DatapathID != 0 &&
		b.Socket != "" && b.SocketInode != 0 && len(b.Daemon) > 0 && len(b.Daemon) <= 128 &&
		b.PeerUID != ^uint32(0)
}

var (
	errOpenFlowInvalid     = errors.New("invalid OpenFlow observation")
	errOpenFlowUnsupported = errors.New("unsupported OpenFlow observation")
	errOpenFlowIdentity    = errors.New("unproven OpenFlow identity")
	errOpenFlowWireBudget  = errors.New("OpenFlow wire budget")
	errOpenFlowNoEndpoint  = errors.New("OpenFlow endpoint not configured")
	errOpenFlowNoDPID      = errors.New("OpenFlow datapath ID unobserved")
	errOpenFlowRejected    = errors.New("OpenFlow read rejected")
)

func openFlowFailure(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return apitypes.Fail(503, "OPENFLOW_TIMEOUT")
	case errors.Is(err, errOpenFlowUnsupported):
		return apitypes.Fail(503, "OPENFLOW_UNSUPPORTED")
	case errors.Is(err, errOpenFlowIdentity):
		return apitypes.Fail(503, "OPENFLOW_IDENTITY_UNPROVEN")
	case errors.Is(err, errOpenFlowNoEndpoint):
		return apitypes.Fail(503, "OPENFLOW_ENDPOINT_NOT_CONFIGURED")
	case errors.Is(err, errOpenFlowNoDPID):
		return apitypes.Fail(503, "OPENFLOW_DPID_UNOBSERVED")
	case errors.Is(err, errOpenFlowRejected):
		return apitypes.Fail(503, "OPENFLOW_READ_REJECTED")
	case errors.Is(err, errOpenFlowInvalid), errors.Is(err, errOpenFlowWireBudget):
		return apitypes.Fail(503, "OPENFLOW_RESPONSE_INVALID")
	default:
		return apitypes.Fail(503, "OPENFLOW_SOURCE_UNAVAILABLE")
	}
}

func openFlowHex(v uint64) string { return fmt.Sprintf("0x%016x", v) }
