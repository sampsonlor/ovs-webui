package candidate

import (
	"github.com/sampsonlor/ovs-webui/internal/multicast"
	"slices"
	"testing"
)

func TestMulticastSealsOriginalAndRequiresIndependentNativeAuthority(t *testing.T) {
	e, s, in := planFixture()
	in.Operation, in.Value, in.Object.Table = BridgeMulticastSet, VLAN{}, "Bridge"
	cfg := multicast.Configuration{Parameters: map[string]*string{multicast.Keys[0]: nil, multicast.Keys[1]: nil, multicast.Keys[2]: nil}}
	b := MulticastBridge{Binding: in.Object, Known: true, Supported: true, Configuration: cfg, Authority: "local-managed", Dependency: "captured-graph"}
	s.Multicasts = map[string]MulticastBridge{in.Object.ManagementID: b}
	in.Multicast = &multicast.Request{Enabled: true, Parameters: map[string]*string{multicast.Keys[0]: ptr("300")}}
	st := staged(t, e, s, in)
	checks, diff := Checks(st.Candidate, s)
	if !Passed(checks) || len(diff) != 4 || !slices.Equal(Capabilities(st.Candidate), []string{BridgeMulticastCapability}) {
		t.Fatal(checks, diff)
	}
	original := Digest(st.Candidate.Intents[0])
	rev := st.Candidate.Intents[0]
	Reverse(&rev)
	if !rev.Multicast.Before.Enabled || rev.Multicast.After.Enabled || Digest(st.Candidate.Intents[0]) != original {
		t.Fatal("original mutated")
	}
	Reverse(&rev)
	if Digest(rev) != original {
		t.Fatal("compensation round trip")
	}
	after := st.Candidate.Intents[0]
	AfterImage(&after)
	if !after.Multicast.Before.Enabled || Digest(st.Candidate.Intents[0]) != original {
		t.Fatal("after image alias")
	}
	for _, fault := range []string{"authority", "schema", "unknown", "graph", "dependency", "field", "identity", "generation"} {
		t.Run(fault, func(t *testing.T) {
			changed := s
			other := b
			other.Configuration = multicast.Clone(cfg)
			switch fault {
			case "authority":
				other.Authority = "unknown"
			case "schema":
				other.Supported = false
			case "unknown":
				other.Known = false
			case "graph":
				other.Gate = "MULTICAST_FOREIGN_DOMAIN_UNQUALIFIED"
			case "dependency":
				other.Dependency = "changed"
			case "field":
				other.Configuration.Enabled = true
			case "identity":
				other.Binding.OVSUUID = "retired"
			case "generation":
				changed.Generation = "changed"
			}
			changed.Multicasts = map[string]MulticastBridge{in.Object.ManagementID: other}
			checks, _ := Checks(st.Candidate, changed)
			if Passed(checks) {
				t.Fatal("fault accepted", fault)
			}
		})
	}
	b.Configuration.Enabled = true
	s.Multicasts[in.Object.ManagementID] = b
	in.Multicast = &multicast.Request{Enabled: false, Parameters: map[string]*string{multicast.Keys[0]: nil}}
	edited := staged(t, st, s, in)
	if edited.Candidate.Intents[0].Multicast.Before.Enabled || Passed(Compare(edited.Candidate, s).Checks) {
		t.Fatal("restaging replaced original")
	}
	for _, bad := range []Intent{func() Intent { i := in; i.Multicast = nil; return i }(), func() Intent { i := in; i.Protected = ptr(true); return i }(), func() Intent { i := in; i.Operation = "multicast.configure"; return i }()} {
		if _, err := Prepare(e, Command{Operation: "stage", Intents: []Intent{bad}}, s); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
