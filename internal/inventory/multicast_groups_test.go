package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sampsonlor/ovs-webui/internal/multicast"
)

type multicastObserverFunc func(context.Context, MulticastGroupRequest) (MulticastGroupSample, error)

func (f multicastObserverFunc) ObserveMulticast(ctx context.Context, r MulticastGroupRequest) (MulticastGroupSample, error) {
	return f(ctx, r)
}

func TestMulticastGroupsRequireFreshBoundObservationsAndDoNotInventEmptyMembership(t *testing.T) {
	s, o, d, b := multicastFixture(t)
	bridge := o.Rows["Bridge"][b.OVSUUID]
	bridge.Configuration["mcast_snooping_enable"] = true
	bridge.Values["mcast_snooping_enable"] = true
	for id, i := range o.Rows["Interface"] {
		i.Values["ofport"] = "1"
		o.Rows["Interface"][id] = i
		break
	}
	s.install(o, d)
	binding := d.Bindings[Key("Bridge", b.OVSUUID)]
	read := func(fresh string, config bool) map[string]any {
		return s.readMulticastGroups(context.Background(), s.current, binding, fresh, config)
	}
	if x := read("fresh", true); x["availability"] != "unavailable" || x["items"] != nil {
		t.Fatal("absent source became empty", x)
	}
	calls := 0
	sample := MulticastGroupSample{ObservedAt: time.Now().UTC(), Groups: []multicast.Group{{Port: 1, VLAN: 0, Protocol: "IGMPv2", Address: "239.192.0.7", Age: 1}}}
	s.SetMulticastGroupObserver(multicastObserverFunc(func(_ context.Context, r MulticastGroupRequest) (MulticastGroupSample, error) {
		calls++
		if r.Bridge != binding || r.Name != bridge.Values["name"] {
			t.Fatal("caller changed native identity", r)
		}
		return sample, nil
	}))
	if x := read("fresh", false); x["availability"] != "withheld" || x["items"] != nil {
		t.Fatal("configuration leaked", x)
	}
	if x := read("stale", true); x["items"] != nil {
		t.Fatal("stale association retained memberships", x)
	}
	if calls != 0 {
		t.Fatal("withheld/stale dispatched native read")
	}
	if x := read("fresh", true); x["availability"] != "known" || len(x["items"].([]any)) != 1 {
		t.Fatal("bound observation lost", x)
	}
	sample.Groups = []multicast.Group{}
	if x := read("fresh", true); x["availability"] != "known" || len(x["items"].([]any)) != 0 {
		t.Fatal("observed empty table became unavailable", x)
	}
	sample.Groups = nil
	if x := read("fresh", true); x["items"] != nil {
		t.Fatal("absent sample became empty", x)
	}
	sample.Groups = []multicast.Group{{Port: 65000, Protocol: "IGMPv2", Address: "239.192.0.7"}}
	if x := read("fresh", true); x["reason"] != "MULTICAST_PORT_ASSOCIATION_UNPROVEN" || x["items"] != nil {
		t.Fatal("unknown port fabricated", x)
	}
	sample.Groups = []multicast.Group{}
	sample.ObservedAt = time.Now().Add(-FreshFor - time.Second)
	if x := read("fresh", true); x["items"] != nil {
		t.Fatal("expired sample retained", x)
	}
	sample.ObservedAt = time.Now().UTC()
	s.SetMulticastGroupObserver(multicastObserverFunc(func(context.Context, MulticastGroupRequest) (MulticastGroupSample, error) {
		for id, i := range o.Rows["Interface"] {
			i.Values["ofport"] = "7"
			o.Rows["Interface"][id] = i
			break
		}
		s.install(o, d)
		return sample, nil
	}))
	// Association fingerprint must be captured before the observer can publish
	// a new immutable view, rather than comparing two pointers to latest data.
	before := s.current
	if x := s.readMulticastGroups(context.Background(), before, binding, "fresh", true); x["items"] != nil {
		t.Fatal("changed ofport association accepted", x)
	}
	s.SetMulticastGroupObserver(multicastObserverFunc(func(context.Context, MulticastGroupRequest) (MulticastGroupSample, error) {
		return MulticastGroupSample{}, errors.New("private-source-details")
	}))
	if x := read("fresh", true); x["reason"] != "MULTICAST_GROUP_OBSERVATION_UNPROVEN" || x["items"] != nil {
		t.Fatal("source failure leaked or invented membership", x)
	}
}
