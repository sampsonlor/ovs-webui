package ovsdb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/inventory"
)

type Options struct {
	Socket       string
	DatabaseFile string
	PeerUID      uint32
}
type Sink interface {
	Previous(context.Context) (inventory.Evidence, error)
	Publish(context.Context, inventory.Observation) error
	Unavailable(string)
}
type Provider struct{ options Options }

func New(o Options) (*Provider, error) {
	if err := validateOptions(o); err != nil {
		return nil, err
	}
	return &Provider{options: o}, nil
}
func (p *Provider) Run(ctx context.Context, s Sink) {
	delay := 250 * time.Millisecond
	for ctx.Err() == nil {
		began := time.Now()
		err := p.session(ctx, s)
		if ctx.Err() != nil {
			return
		}
		code := "OVSDB_UNAVAILABLE"
		if err != nil {
			switch err.Error() {
			case "OVSDB_CORE_SCHEMA_UNSUPPORTED", "OVSDB_INVENTORY_BUDGET", "OVSDB_RELATION_BUDGET", "OVSDB_ROW_BUDGET", "OVSDB_SCHEMA_BUDGET", "OVSDB_PEER_DENIED", "OVSDB_MONITOR_INCONSISTENT", "OVSDB_ORPHAN_OBJECT", "OVSDB_REFERENCE_MISSING", "OVSDB_REFERENCE_AMBIGUOUS":
				code = err.Error()
			}
		}
		s.Unavailable(code)
		if time.Since(began) > 10*time.Second {
			delay = 250 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(5*time.Second, delay*2)
	}
}
func (p *Provider) session(ctx context.Context, s Sink) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", p.options.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	cancelClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer cancelClose()
	identity, pid, err := peer(conn, p.options.PeerUID)
	if err != nil {
		return err
	}
	r := bufio.NewReaderSize(conn, 32<<10)
	data, err := call(conn, r, 1, "get_schema", []any{"Open_vSwitch"})
	if err != nil {
		return err
	}
	d, err := discover(data)
	if err != nil {
		return err
	}
	monitorID := "inventory-v1"
	data, err = call(conn, r, 2, "monitor", []any{"Open_vSwitch", monitorID, d.requests})
	if err != nil {
		return err
	}
	rows := inventory.Rows{}
	for table := range selected {
		rows[table] = map[string]inventory.Row{}
	}
	if err = update(d, rows, data, true); err != nil {
		return err
	}
	prior, err := s.Previous(ctx)
	if err != nil {
		return err
	}
	continuous := false
	publish := func() error {
		if err := validateRelations(rows); err != nil {
			return err
		}
		encoded, err := json.Marshal(rows)
		if err != nil || len(encoded) > inventory.MaxSnapshotBytes {
			return errors.New("OVSDB_INVENTORY_BUDGET")
		}
		// Copy before handing ownership to the read service. Monitor application and
		// public reads cannot race over shared maps.
		copyRows := inventory.Rows{}
		if json.Unmarshal(encoded, &copyRows) != nil {
			return errors.New("OVSDB_SNAPSHOT_INVALID")
		}
		e := evidence(p.options, d, rows, identity, pid, prior, continuous)
		if err = s.Publish(ctx, inventory.Observation{Schema: d.public, Rows: copyRows, Evidence: e}); err != nil {
			return err
		}
		prior = e
		continuous = true
		return nil
	}
	if err = publish(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	messages := make(chan message, 8)
	failures := make(chan error, 1)
	go func() {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(inventory.FreshFor))
			m, err := readMessage(r)
			if err != nil {
				select {
				case failures <- err:
				case <-done:
				}
				return
			}
			select {
			case messages <- m:
			case <-done:
				return
			}
		}
	}()
	beat := time.NewTicker(2 * time.Second)
	defer beat.Stop()
	batch := time.NewTicker(250 * time.Millisecond)
	defer batch.Stop()
	dirty := false
	echoID := 3
	pendingEcho := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case m := <-messages:
			switch m.Method {
			case "echo":
				if err = replyEcho(conn, m); err != nil {
					return err
				}
			case "update":
				var id string
				if len(m.Params) != 2 || json.Unmarshal(m.Params[0], &id) != nil || id != monitorID {
					return errors.New("OVSDB_MONITOR_ID_INVALID")
				}
				if err = update(d, rows, m.Params[1], false); err != nil {
					return err
				}
				dirty = true
			case "":
				if pendingEcho == 0 || string(m.ID) != strconv.Itoa(pendingEcho) || (len(m.Error) > 0 && string(m.Error) != "null") {
					return errors.New("OVSDB_ECHO_INVALID")
				}
				pendingEcho = 0
				dirty = true
			default:
				return errors.New("OVSDB_MONITOR_CANCELLED")
			}
		case <-batch.C:
			if dirty {
				if err = publish(); err != nil {
					return err
				}
				dirty = false
			}
		case <-beat.C:
			if pendingEcho != 0 {
				return errors.New("OVSDB_ECHO_TIMEOUT")
			}
			pendingEcho = echoID
			echoID++
			if err = request(conn, pendingEcho, "echo", []any{}); err != nil {
				return err
			}
		}
	}
}
