//go:build linux

package fieldexecution

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/execution"
	"github.com/sampsonlor/ovs-webui/internal/repository"
	"github.com/sampsonlor/ovs-webui/internal/safety"
)

const reviewedQoSProfile = "lab-qos-profile"

type qosCreationFixture struct {
	*fixture
	offset *atomic.Int64
}

func newQoSCreationFixture(t *testing.T) qosCreationFixture {
	t.Helper()
	f := newFixture(t)
	var role string
	must(t, f.store.Read(f.ctx, func(ctx context.Context, q *sql.Conn) error {
		return q.QueryRowContext(ctx, "SELECT role_id FROM principal_roles WHERE principal_id=?", f.login.Claims.PrincipalID).Scan(&role)
	}))
	must(t, f.auth.SetQoSExecutionRole(f.ctx, role, true))
	fresh, err := f.auth.Authenticate(f.ctx, authn.Login{Provider: "local", Username: "admin", Password: "synthetic-native-field-password"})
	must(t, err)
	f.login = fresh
	offset := new(atomic.Int64)
	f.configureSafety(offset)
	return qosCreationFixture{fixture: f, offset: offset}
}

func (f qosCreationFixture) creation() execution.Request {
	f.t.Helper()
	must(f.t, f.inventory.SetLocalQoSCreateNames([]string{reviewedQoSProfile}))
	snapshot, err := f.inventory.CandidateSnapshot(f.ctx, nil)
	must(f.t, err)
	return f.prepareIntents([]any{map[string]any{
		"intent_id": repository.NewID(), "operation": candidate.QoSCreate,
		"instance_generation": snapshot.Generation, "name": reviewedQoSProfile,
		"type": "linux-hfsc", "max_rate_bps": 8000000,
		"queues": []candidate.QoSQueueRate{{ID: 7, Minimum: 1000000, Maximum: 3000000}, {ID: 9, Minimum: 2000000, Maximum: 4000000}},
	}})
}

func (f qosCreationFixture) rowIDs(table string, conditions ...string) []string {
	f.t.Helper()
	rows := strings.Fields(f.vs(append([]string{"--data=bare", "--no-headings", "--columns=_uuid", "find", table}, conditions...)...))
	slices.Sort(rows)
	return rows
}

func (f qosCreationFixture) createdProfile() (string, []string) {
	f.t.Helper()
	matches := f.rowIDs("QoS", "external_ids:"+candidate.QoSProfileName+"="+reviewedQoSProfile)
	if len(matches) != 1 {
		f.t.Fatal("expected a single saved QoS profile", matches)
	}
	profile := matches[0]
	get := func(table, id, key string) string { return strings.Trim(f.vs("get", table, id, key), `"`) }
	if get("QoS", profile, "type") != "linux-hfsc" ||
		get("QoS", profile, "other_config:max-rate") != "8000000" ||
		len(f.rowIDs("Port", "qos="+profile)) != 0 {
		f.t.Fatal("saved profile differs from the reviewed unbound HFSC configuration")
	}
	q7, q9 := get("QoS", profile, "queues:7"), get("QoS", profile, "queues:9")
	if !slices.Equal(f.rowIDs("Queue"), sortedPair(q7, q9)) ||
		get("Queue", q7, "other_config:min-rate") != "1000000" || get("Queue", q7, "other_config:max-rate") != "3000000" ||
		get("Queue", q9, "other_config:min-rate") != "2000000" || get("Queue", q9, "other_config:max-rate") != "4000000" {
		f.t.Fatal("the transaction did not create exactly the reviewed Queue rows")
	}
	return profile, []string{q7, q9}
}

func sortedPair(a, b string) []string {
	pair := []string{a, b}
	slices.Sort(pair)
	return pair
}

func TestNativeQoSCreation(t *testing.T) {
	if os.Getenv("OVS_EXECUTION_NATIVE_TEST") != "1" {
		t.Skip("requires an explicit disposable OVSDB fixture")
	}
	t.Run("confirmation_retains_exact_unbound_profile_and_queues", func(t *testing.T) {
		f := newQoSCreationFixture(t)
		in := f.creation()
		if f.proxy.sent.Load() != 0 || len(f.rowIDs("QoS")) != 0 || len(f.rowIDs("Queue")) != 0 {
			t.Fatal("staging or validation wrote live OVSDB")
		}
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		profile, queues := f.createdProfile()
		intent := in.Envelope.Candidate.Intents[0]
		if profile != intent.Object.OVSUUID || queues[0] != intent.QoSCreation.Queues["7"].OVSUUID ||
			queues[1] != intent.QoSCreation.Queues["9"].OVSUUID {
			t.Fatal("OVSDB replaced the reviewed native identities")
		}
		f.graphStates(intent, "active")
		f.decide(id, "confirm")
		f.waitSafety(id, "confirmed")
		gotProfile, gotQueues := f.createdProfile()
		if profile != gotProfile || !slices.Equal(queues, gotQueues) || f.proxy.sent.Load() != 1 {
			t.Fatal("confirmation replaced rows or changed Port associations")
		}
	})

	t.Run("rollback_deletes_created_rows_and_preserves_unrelated_root", func(t *testing.T) {
		f := newQoSCreationFixture(t)
		external := f.vs("create", "QoS", "type=linux-hfsc", "external_ids:synthetic=foreign")
		waitFor(t, func() bool {
			view, err := f.inventory.ExecutionView(f.ctx, nil)
			return err == nil && view.Observation.Rows["QoS"][external].UUID == external
		})
		in := f.creation()
		id := f.safeApply(in)
		f.waitSafety(id, "awaiting-confirmation")
		f.createdProfile()
		f.decide(id, "rollback")
		f.waitSafety(id, "rolled-back")
		f.graphAbsent(in.Envelope.Candidate.Intents[0])
		if !slices.Equal(f.rowIDs("QoS"), []string{external}) || len(f.rowIDs("Queue")) != 0 || f.proxy.sent.Load() != 2 {
			t.Fatal("rollback did not remove only the saved profile and its queues")
		}
	})

	t.Run("revoked_profile_name_admission_blocks_dispatch", func(t *testing.T) {
		f := newQoSCreationFixture(t)
		in := f.creation()
		must(t, f.inventory.SetLocalQoSCreateNames([]string{"different-approved-name"}))
		_, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("revoked-qos-create-name"), in.Envelope)
		if err == nil || f.proxy.sent.Load() != 0 || len(f.rowIDs("QoS")) != 0 || len(f.rowIDs("Queue")) != 0 {
			t.Fatal("revoked profile name retained create authority", err)
		}
	})

	t.Run("native_alias_race_rejects_all_inserts", func(t *testing.T) {
		f := newQoSCreationFixture(t)
		in := f.creation()
		plan, err := f.executor.Prepare(f.ctx, repository.NewID(), candidate.Digest("qos-create-name-race"), in.Envelope)
		must(t, err)
		var external string
		f.proxy.hook(func() {
			external = f.vs("create", "QoS", "type=linux-hfsc", "external_ids:"+candidate.QoSProfileName+"="+reviewedQoSProfile)
		})
		result := f.executor.Commit(f.ctx, plan, func() error { return nil })
		if result.Commit != "rejected" || f.proxy.sent.Load() != 1 ||
			!slices.Equal(f.rowIDs("QoS"), []string{external}) || len(f.rowIDs("Queue")) != 0 {
			t.Fatal("alias collision was adopted, replaced, or leaked Queue rows", result)
		}
	})

	for _, collision := range []string{"queue_edit", "foreign_queue_reference", "foreign_port_reference"} {
		t.Run("late_"+collision+"_blocks_destructive_rollback", func(t *testing.T) {
			f := newQoSCreationFixture(t)
			id := f.safeApply(f.creation())
			f.waitSafety(id, "awaiting-confirmation")
			profile, queues := f.createdProfile()
			// The proxy runs this after preparation and immediately before
			// forwarding the destructive OVSDB transaction.
			var foreign string
			f.proxy.hook(func() {
				switch collision {
				case "queue_edit":
					f.vs("set", "Queue", queues[0], "external_ids:synthetic=updated")
				case "foreign_queue_reference":
					foreign = f.vs("create", "QoS", "type=linux-hfsc", "queues:7="+queues[0])
				case "foreign_port_reference":
					f.vs("set", "Port", "field-p1", "qos="+profile)
				}
			})
			f.decide(id, "rollback")
			f.waitSafety(id, "rollback-conflict")
			if f.proxy.sent.Load() != 2 || !slices.Contains(f.rowIDs("QoS"), profile) ||
				!slices.Equal(f.rowIDs("Queue"), sortedPair(queues[0], queues[1])) {
				t.Fatal("conflicting rollback removed or replaced a root row")
			}
			switch collision {
			case "queue_edit":
				if f.vs("get", "Queue", queues[0], "external_ids:synthetic") != "updated" {
					t.Fatal("the independent Queue edit was lost")
				}
			case "foreign_queue_reference":
				if f.vs("get", "QoS", foreign, "queues:7") != queues[0] {
					t.Fatal("the independent QoS reference was lost")
				}
			case "foreign_port_reference":
				if f.vs("get", "Port", "field-p1", "qos") != profile {
					t.Fatal("the independent Port attachment was lost")
				}
			}
		})
	}

	t.Run("lost_creation_reply_recovers_without_a_second_insert", func(t *testing.T) {
		f := newQoSCreationFixture(t)
		in := f.creation()
		f.proxy.dropReply.Store(true)
		id := f.safeApply(in)
		r := f.observe(id, func(r execution.Record) bool { return r.Outcome.Commit == "committed" })
		if r.Outcome.Target != nil || r.Outcome.Applied != "unknown" || f.proxy.sent.Load() != 1 {
			t.Fatal("lost reply fabricated applied evidence or replayed the insert", r.Outcome)
		}
		f.createdProfile()
		f.offset.Store(int64(safety.PreparationBudget + time.Second))
		f.waitSafety(id, "rolled-back")
		if len(f.rowIDs("QoS")) != 0 || len(f.rowIDs("Queue")) != 0 || f.proxy.sent.Load() != 2 {
			t.Fatal("deadline recovery did not compensate the original creation once")
		}
	})
}
