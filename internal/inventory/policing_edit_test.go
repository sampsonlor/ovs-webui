package inventory

import (
	"context"
	"strings"
	"testing"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/repository"
)

func TestPolicingAdmissionAndPrivateKernelEvidenceFailClosed(t *testing.T) {
	for _, kind := range []string{"valid", "no-grant", "mtu-grant-only", "external", "offload", "custom-burst", "member", "physical", "local", "raw-options", "schema", "kernel-partial", "kernel-unqualified", "kernel-binding-change"} {
		t.Run(kind, func(t *testing.T) {
			s, o, d, claims := fixture(t)
			var port, bridge Row
			for _, r := range o.Rows["Port"] {
				port = r
			}
			for _, r := range o.Rows["Bridge"] {
				bridge = r
			}
			iface := o.Rows["Interface"][refs(port.Values["interfaces"])[0]]
			empty := true
			iface.InterfaceOptionsEmpty = &empty
			iface.Values["type"], iface.Values["ifindex"], iface.Values["ofport"], iface.Values["error"] = "internal", []any{"7"}, []any{"2"}, []any{}
			iface.Values["external_ids"] = map[string]any{}
			fields := []string{"ingress_policing_rate", "ingress_policing_burst", "ingress_policing_kpkts_rate", "ingress_policing_kpkts_burst"}
			for _, f := range fields {
				iface.Values[f] = "0"
			}
			port.Values["name"], port.Values["interfaces"] = iface.Values["name"], []any{iface.UUID}
			bridge.Values["datapath_type"] = "system"
			root := repository.NewID()
			o.Evidence.Root = root
			o.Rows["Open_vSwitch"][root] = Row{UUID: root, Values: map[string]any{"bridges": []any{bridge.UUID}, "external_ids": map[string]any{}, "other_config": map[string]any{}}}
			for n, table := range o.Schema.Tables {
				if table.Name == "Interface" {
					for _, f := range fields {
						o.Schema.Tables[n].Columns = append(o.Schema.Tables[n].Columns, Column{Name: f, Monitored: true, Mutable: true, PolicingCompatible: kind != "schema"})
					}
				}
			}
			b := d.Bindings[Key("Interface", iface.UUID)]
			binding := candidate.Binding{ManagementID: b.ManagementID, OVSUUID: b.UUID, Table: "Interface", Generation: d.Generation}
			switch kind {
			case "external":
				iface.Values["external_ids"] = map[string]any{"iface-id": "synthetic"}
			case "offload":
				o.Rows["Open_vSwitch"][root].Values["other_config"] = map[string]any{"hw-offload": "true"}
			case "custom-burst":
				iface.Values["ingress_policing_burst"] = "8000"
			case "member":
				port.Values["interfaces"] = []any{iface.UUID, repository.NewID()}
			case "physical":
				iface.Values["type"] = "system"
			case "local":
				port.Values["name"], iface.Values["name"] = bridge.Values["name"], bridge.Values["name"]
			case "raw-options":
				empty = false
			}
			o.Rows["Interface"][iface.UUID] = iface
			if kind != "no-grant" && kind != "mtu-grant-only" {
				if err := s.SetLocalPolicingInterfaces([]string{binding.ManagementID}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetLocalMTUInterfaces([]string{binding.ManagementID}); err != nil {
				t.Fatal(err)
			}
			s.install(o, d)
			snap, err := s.CandidateSnapshot(context.Background(), []candidate.Binding{binding})
			if err != nil {
				t.Fatal(err)
			}
			p := snap.Policings[binding.ManagementID]
			shouldEdit := kind == "valid" || strings.HasPrefix(kind, "kernel-")
			if candidate.PolicingEditable(p) != shouldEdit {
				t.Fatal(p)
			}
			calls := 0
			s.SetPolicingObserver(policingObserverFunc(func(context.Context, DeviceRequest) PolicingSample {
				calls++
				sample := PolicingSample{Availability: "known", ObservedAt: s.now(), IfIndex: 7, Actions: []PolicingAction{}, IngressKind: "none", WriteCompatible: true, ConfigurationDigest: Digest("empty")}
				if kind == "kernel-partial" {
					sample.Availability = "partial"
				}
				if kind == "kernel-unqualified" {
					sample.WriteCompatible = false
				}
				if kind == "kernel-binding-change" {
					iface.Values["ifindex"] = []any{"8"}
					o.Rows["Interface"][iface.UUID] = iface
					s.install(o, d)
				}
				return sample
			}))
			sample, err := s.PolicingEvidence(context.Background(), p)
			if (err == nil) != (kind == "valid") {
				t.Fatal(kind, err, sample)
			}
			if !shouldEdit && calls != 0 {
				t.Fatal("read unadmitted host device")
			}
			if kind == "valid" {
				claims.Capabilities = append(claims.Capabilities, "workspace.write", "ovs.interface.mtu.write")
				value, err := s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, nil, claims)
				if err != nil {
					t.Fatal(err)
				}
				if value.(map[string]any)["policing_editable"] == true {
					t.Fatal("MTU granted policing permission")
				}
				claims.Capabilities = append(claims.Capabilities, "ovs.interface.policing.write")
				value, err = s.Read(context.Background(), "readInterface", map[string]string{"interface_id": b.ManagementID}, nil, claims)
				if err != nil || value.(map[string]any)["policing_editable"] != true {
					t.Fatal(err, value)
				}
				if err = s.SetLocalPolicingInterfaces([]string{b.ManagementID, b.ManagementID}); err == nil {
					t.Fatal("duplicate policy")
				}
			}
		})
	}
}
func TestPolicingInstalledRatesUseExplicitKernelUnits(t *testing.T) {
	byteRate, packetRate := "125000", "5000"
	base := PolicingSample{Availability: "known", WriteCompatible: true, IngressKind: "ingress", Actions: []PolicingAction{{FilterKind: "matchall", Priority: 1, Handle: "0x00000001", BytesPerSecond: &byteRate, ExceedAction: "drop", ConformAction: "continue"}}}
	if !PolicingMatches(base, candidate.PolicingConfig{Rate: 1000}) || PolicingMatches(base, candidate.PolicingConfig{Rate: 1001}) {
		t.Fatal("byte-rate conversion")
	}
	base.Actions[0].BytesPerSecond = nil
	base.Actions[0].PacketsPerSecond = &packetRate
	if !PolicingMatches(base, candidate.PolicingConfig{PacketRate: 5}) || PolicingMatches(base, candidate.PolicingConfig{Rate: 5}) {
		t.Fatal("packet-rate conversion")
	}
	base.Actions[0].ConformAction = "accept"
	if PolicingMatches(base, candidate.PolicingConfig{PacketRate: 5}) {
		t.Fatal("foreign semantics")
	}
	base.Actions = []PolicingAction{}
	if PolicingMatches(base, candidate.PolicingConfig{}) {
		t.Fatal("leftover ingress qdisc considered disabled")
	}
	base.IngressKind = "none"
	if !PolicingMatches(base, candidate.PolicingConfig{}) {
		t.Fatal("disabled")
	}
}
