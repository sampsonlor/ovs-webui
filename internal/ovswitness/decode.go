// Package ovswitness is an isolated experimental codec. Production daemons do
// not import it; a decoded observation is never an execution capability.
package ovswitness

import (
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/sampsonlor/ovs-webui/internal/apitypes"
	"github.com/sampsonlor/ovs-webui/internal/ipc"
	"github.com/sampsonlor/ovs-webui/internal/spanningtree"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const MaxWireBytes = 64 << 10
const MaxInnerBytes = 16 << 10

var ErrInvalid = errors.New("SPANNING_TREE_WITNESS_INVALID")
var ErrWithheld = errors.New("SPANNING_TREE_WITNESS_WITHHELD")

//go:embed witness.schema.json
var witnessSchema []byte

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	var root any
	if err := json.Unmarshal(witnessSchema, &root); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	const id = "https://ovs-webui.invalid/experimental/stp-witness-v1.schema.json"
	if err := c.AddResource(id, root); err != nil {
		return nil, err
	}
	return c.Compile(id)
})

type Database struct {
	RootUUID      string `json:"root_uuid"`
	Connected     bool   `json:"connected"`
	LockName      string `json:"lock_name"`
	LockOwned     bool   `json:"lock_owned"`
	LockContended bool   `json:"lock_contended"`
	Authority     string `json:"authority_epoch"`
	IDLSeqno      string `json:"idl_seqno"`
}
type Bridge struct {
	UUID     string `json:"ovs_uuid"`
	Name     string `json:"name"`
	Instance string `json:"runtime_instance_id"`
	Epoch    string `json:"binding_epoch"`
}
type Source struct {
	STP        bool               `json:"stp_enable"`
	RSTP       bool               `json:"rstp_enable"`
	Parameters map[string]*string `json:"parameters"`
}
type Configuration struct {
	Epoch       string `json:"epoch"`
	IDLSeqno    string `json:"idl_seqno"`
	NextCfg     string `json:"root_next_cfg"`
	Completion  string `json:"completion"`
	SetterError bool   `json:"setters_reported_error"`
	InputFormat string `json:"runtime_input_format"`
	InputDigest string `json:"runtime_input_digest"`
	Source      Source `json:"source"`
}
type Local struct {
	Priority  uint16 `json:"priority"`
	SystemID  string `json:"system_id"`
	HelloMS   uint32 `json:"hello_time_ms"`
	MaxAgeMS  uint32 `json:"max_age_ms"`
	ForwardMS uint32 `json:"forward_delay_ms"`
}
type Engine struct {
	Enabled bool   `json:"enabled"`
	Local   *Local `json:"local"`
}
type Native struct {
	STP  Engine `json:"stp"`
	RSTP Engine `json:"rstp"`
}
type Sample struct {
	Version       string        `json:"schema_version"`
	Nonce         string        `json:"nonce"`
	State         string        `json:"state"`
	Daemon        string        `json:"daemon_instance_id"`
	Database      Database      `json:"database"`
	Bridge        Bridge        `json:"bridge"`
	Configuration Configuration `json:"configuration"`
	Native        Native        `json:"native"`
}

var parameterKeys = []string{"stp-priority", "stp-hello-time", "stp-max-age", "stp-forward-delay", "rstp-priority", "rstp-max-age", "rstp-forward-delay"}

func hex64(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}
func uuid(s string) bool { return apitypes.UUID(s) && s == strings.ToLower(s) }
func counter(s string, bits int, terminal bool) bool {
	if s == "" || len(s) > 20 || len(s) > 1 && s[0] == '0' || strings.Trim(s, "0123456789") != "" {
		return false
	}
	n, err := strconv.ParseUint(s, 10, bits)
	return err == nil && (!terminal || n != math.MaxUint64)
}

// Decode accepts one bounded JSON-RPC frame containing one strict JSON string.
// Every failure returns a zero Sample. It does not read a socket, acquire a
// lock, trust a self-reported build, authorize a caller or dispatch any write.
func Decode(wire []byte, nonce, bridgeUUID string) (Sample, error) {
	if len(wire) > MaxWireBytes || !hex64(nonce) || !uuid(bridgeUUID) {
		return Sample{}, ErrInvalid
	}
	outer, err := ipc.ParseObject(wire)
	_, hasError := outer["error"]
	if err != nil || len(outer) != 3 || !hasError || outer["id"] != json.Number("1") || outer["error"] != nil {
		return Sample{}, ErrInvalid
	}
	text, ok := outer["result"].(string)
	if !ok || len(text) > MaxInnerBytes {
		return Sample{}, ErrInvalid
	}
	value, err := ipc.ParseObject([]byte(text))
	if err != nil || value["schema_version"] != "ovs-webui.stp-witness.v1" || value["nonce"] != nonce {
		return Sample{}, ErrInvalid
	}
	schema, err := compiledSchema()
	if err != nil || schema.Validate(value) != nil {
		return Sample{}, ErrInvalid
	}
	if value["state"] == "withheld" {
		r, ok := value["reason"].(string)
		if len(value) != 4 || !ok {
			return Sample{}, ErrInvalid
		}
		switch r {
		case "IDL_NOT_CONNECTED", "IDL_LOCK_UNOWNED", "IDL_LOCK_CONTENDED", "CONFIGURATION_IN_PROGRESS", "BRIDGE_NOT_CONFIGURED", "BINDING_UNPROVEN", "PROVIDER_SNAPSHOT_UNSUPPORTED", "SETTER_REPORTED_ERROR", "WITNESS_BUDGET", "EPOCH_EXHAUSTED":
			return Sample{}, ErrWithheld
		default:
			return Sample{}, ErrInvalid
		}
	}
	var s Sample
	if value["state"] != "known" || ipc.DecodeStrict([]byte(text), &s) != nil {
		return Sample{}, ErrInvalid
	}
	if !uuid(s.Daemon) || !uuid(s.Database.RootUUID) || !uuid(s.Bridge.Instance) || s.Bridge.UUID != bridgeUUID || !spanningtree.RuntimeName(s.Bridge.Name) || !s.Database.Connected || !s.Database.LockOwned || s.Database.LockContended || s.Database.LockName != "ovs_vswitchd" || !counter(s.Database.Authority, 64, true) || !counter(s.Bridge.Epoch, 64, true) || !counter(s.Configuration.Epoch, 64, true) || !counter(s.Database.IDLSeqno, 32, false) || s.Database.IDLSeqno != s.Configuration.IDLSeqno || !counter(s.Configuration.NextCfg, 63, false) || s.Configuration.SetterError || s.Configuration.Completion != "complete" || s.Configuration.InputFormat != "ovs-webui.stp-input.v1" || !hex64(s.Configuration.InputDigest) {
		return Sample{}, ErrInvalid
	}
	if len(s.Configuration.Source.Parameters) != len(parameterKeys) {
		return Sample{}, ErrInvalid
	}
	for _, key := range parameterKeys {
		p, ok := s.Configuration.Source.Parameters[key]
		if !ok || p != nil && (len(*p) > 128 || strings.ContainsRune(*p, 0)) {
			return Sample{}, ErrInvalid
		}
	}
	for _, engine := range []Engine{s.Native.STP, s.Native.RSTP} {
		if engine.Enabled != (engine.Local != nil) {
			return Sample{}, ErrInvalid
		}
		if engine.Local != nil {
			mac, err := net.ParseMAC(engine.Local.SystemID)
			if err != nil || len(mac) != 6 || mac.String() != engine.Local.SystemID || engine.Local.HelloMS == 0 || engine.Local.MaxAgeMS == 0 || engine.Local.ForwardMS == 0 {
				return Sample{}, ErrInvalid
			}
		}
	}
	return s, nil
}

// Exclusive checks a point-in-time protocol/local relationship, never Applied.
// Milliseconds must convert losslessly; existing parameter/transition admission
// remains an additional independent gate in any future production integration.
func Exclusive(s Sample) error {
	if s.State != "known" || s.Native.STP.Enabled == s.Native.RSTP.Enabled || s.Native.STP.Enabled != s.Configuration.Source.STP || s.Native.RSTP.Enabled != s.Configuration.Source.RSTP {
		return ErrInvalid
	}
	engine := s.Native.STP
	if s.Native.RSTP.Enabled {
		engine = s.Native.RSTP
	}
	if engine.Local == nil || engine.Local.HelloMS%1000 != 0 || engine.Local.MaxAgeMS%1000 != 0 || engine.Local.ForwardMS%1000 != 0 {
		return ErrInvalid
	}
	return nil
}
