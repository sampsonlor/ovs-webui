package ovsdb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"time"

	native "github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

var statisticsCounters = []string{
	"rx_bytes", "tx_bytes", "rx_packets", "tx_packets",
	"rx_errors", "tx_errors", "rx_dropped", "tx_dropped",
}

const statisticsReadBudget = 2 * time.Second

// ObserveStatistics has an independent read-only connection. The manager calls
// it once per shared sampling interval. It never modifies monitor snapshots,
// persistent configuration, native counters or the identity registry.
func (p *Provider) ObserveStatistics(ctx context.Context, req inventory.StatRequest) (inventory.StatObservation, error) {
	unknown := inventory.StatObservation{}
	if !validStatisticsRequest(req) {
		return unknown, errors.New("STATISTICS_SOURCE_CHANGED")
	}
	ctx, cancel := context.WithTimeout(ctx, statisticsReadBudget)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", p.options.Socket)
	if err != nil {
		return unknown, errors.New("STATISTICS_UNAVAILABLE")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, _, err = peer(conn, p.options.PeerUID); err != nil {
		return unknown, errors.New("STATISTICS_UNAVAILABLE")
	}
	// All frames, including ignored echo messages, share one response budget.
	reader := bufio.NewReaderSize(io.LimitReader(conn, maxFrame+1), 32<<10)
	body, err := call(conn, reader, 1, "get_schema", []any{"Open_vSwitch"})
	if err != nil {
		return unknown, errors.New("STATISTICS_UNAVAILABLE")
	}
	d, err := discover(body)
	if err != nil || !statisticsSchemaSupported(d, body) {
		return unknown, errors.New("STATISTICS_SCHEMA_UNSUPPORTED")
	}
	if d.public.Digest != req.Schema {
		return unknown, errors.New("STATISTICS_SOURCE_CHANGED")
	}
	if err = send(conn, map[string]any{"method": "transact", "params": statisticsOperations(), "id": 2}); err != nil {
		return unknown, errors.New("STATISTICS_UNAVAILABLE")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	body, err = statisticsReply(conn, reader)
	// Keep the monotonic component for the private cache's rate intervals.
	// Public projection may format this timestamp in UTC after deriving rates.
	observed := time.Now()
	if err != nil || ctx.Err() != nil {
		return unknown, errors.New("STATISTICS_UNAVAILABLE")
	}
	counters, err := decodeStatisticsResult(body, req)
	if err != nil {
		return unknown, err
	}
	if ctx.Err() != nil {
		return unknown, errors.New("STATISTICS_UNAVAILABLE")
	}
	return inventory.StatObservation{ObservedAt: observed, Root: req.Root, Schema: req.Schema, Counters: counters}, nil
}

func validStatisticsRequest(req inventory.StatRequest) bool {
	if !apitypes.UUID(req.Root) || len(req.Schema) != 64 || len(req.Interfaces) > inventory.MaxRows {
		return false
	}
	seen := map[string]bool{}
	for _, id := range req.Interfaces {
		if !apitypes.UUID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func statisticsSchemaSupported(d discovered, body []byte) bool {
	col := d.native.Tables["Interface"].Columns["statistics"]
	if col == nil || !col.Ephemeral() || col.Type != native.TypeMap || col.TypeObj == nil ||
		col.TypeObj.Key == nil || col.TypeObj.Key.Type != native.TypeString ||
		col.TypeObj.Value == nil || col.TypeObj.Value.Type != native.TypeInteger ||
		col.TypeObj.Min() != 0 || col.TypeObj.Max() != native.Unlimited {
		return false
	}
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&raw) != nil {
		return false
	}
	descriptor, ok := multicastRawColumnType(raw, "Interface", "statistics").(map[string]any)
	return ok && len(descriptor) == 4 && descriptor["min"] == json.Number("0") && descriptor["max"] == "unlimited" &&
		mirrorFreeAtom(descriptor["key"], native.TypeString) && mirrorFreeAtom(descriptor["value"], native.TypeInteger)
}

// Reading the two tables in one transaction pins the root identity and counter
// values to a coherent database read. Source sampling time is unreported by OVS.
func statisticsOperations() []any {
	return []any{"Open_vSwitch",
		map[string]any{"op": "select", "table": "Open_vSwitch", "where": []any{}, "columns": []string{"_uuid"}},
		map[string]any{"op": "select", "table": "Interface", "where": []any{}, "columns": []string{"_uuid", "statistics"}},
	}
}

func statisticsReply(conn net.Conn, reader *bufio.Reader) (json.RawMessage, error) {
	for count := 0; count < 32; count++ {
		message, err := readMessage(reader)
		if err != nil {
			return nil, err
		}
		if message.Method == "echo" {
			if err = replyEcho(conn, message); err != nil {
				return nil, err
			}
			continue
		}
		if message.Method != "" || string(message.ID) != "2" ||
			len(message.Error) != 0 && string(message.Error) != "null" || len(message.Result) == 0 {
			return nil, errors.New("STATISTICS_UNAVAILABLE")
		}
		return message.Result, nil
	}
	return nil, errors.New("STATISTICS_UNAVAILABLE")
}

func decodeStatisticsResult(body []byte, req inventory.StatRequest) (map[string]map[string]*string, error) {
	changed := errors.New("STATISTICS_SOURCE_CHANGED")
	var results []struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) > maxFrame || validateJSON(body) != nil || decoder.Decode(&results) != nil || len(results) != 2 ||
		len(results[0].Rows) != 1 || len(results[0].Rows[0]) != 1 || len(results[1].Rows) > inventory.MaxRows {
		return nil, errors.New("STATISTICS_UNAVAILABLE")
	}
	if id, valid := statisticsUUID(results[0].Rows[0]["_uuid"]); !valid || id != req.Root {
		return nil, changed
	}
	selected := map[string]bool{}
	for _, id := range req.Interfaces {
		selected[id] = true
	}
	out := map[string]map[string]*string{}
	seen := map[string]bool{}
	for _, row := range results[1].Rows {
		id, valid := statisticsUUID(row["_uuid"])
		_, hasStatistics := row["statistics"]
		if !valid || seen[id] || len(row) != 2 || !hasStatistics {
			return nil, errors.New("STATISTICS_UNAVAILABLE")
		}
		seen[id] = true
		if selected[id] {
			out[id] = decodeStatisticsCounters(row["statistics"])
		}
	}
	if len(out) != len(selected) {
		return nil, changed
	}
	return out, nil
}

func statisticsUUID(raw json.RawMessage) (string, bool) {
	var atom []string
	if json.Unmarshal(raw, &atom) != nil || len(atom) != 2 || atom[0] != "uuid" || !apitypes.UUID(atom[1]) {
		return "", false
	}
	return atom[1], true
}

func decodeStatisticsCounters(raw json.RawMessage) map[string]*string {
	out := make(map[string]*string, len(statisticsCounters))
	for _, key := range statisticsCounters {
		out[key] = nil
	}
	var tagged []json.RawMessage
	var tag string
	var pairs []json.RawMessage
	if len(raw) > 64<<10 || json.Unmarshal(raw, &tagged) != nil || len(tagged) != 2 ||
		json.Unmarshal(tagged[0], &tag) != nil || tag != "map" || json.Unmarshal(tagged[1], &pairs) != nil || len(pairs) > 512 {
		return out
	}
	seen := map[string]bool{}
	for _, pair := range pairs {
		var fields []json.RawMessage
		var key string
		if json.Unmarshal(pair, &fields) != nil || len(fields) != 2 || json.Unmarshal(fields[0], &key) != nil || seen[key] {
			// A malformed or ambiguous map cannot supply any known counter.
			for key := range out {
				out[key] = nil
			}
			return out
		}
		seen[key] = true
		if _, allowed := out[key]; !allowed {
			continue
		}
		text := string(fields[1])
		value, err := strconv.ParseInt(text, 10, 64)
		if err == nil && value >= 0 && strconv.FormatInt(value, 10) == text {
			out[key] = &text
		}
	}
	return out
}
