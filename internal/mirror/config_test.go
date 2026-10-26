package mirror

import (
	"reflect"
	"strings"
	"testing"
)

const src = "11111111-1111-4111-8111-111111111111"
const dst = "22222222-2222-4222-8222-222222222222"
const output = "33333333-3333-4333-8333-333333333333"
const nativeSrc = "44444444-4444-4444-8444-444444444444"
const nativeDst = "55555555-5555-4555-8555-555555555555"
const nativeOutput = "66666666-6666-4666-8666-666666666666"

func ptr(v string) *string { return &v }
func sample() Configuration {
	return Configuration{SourcePorts: []string{src}, DestinationPorts: []string{dst}, VLANs: []string{"0", "100"}, OutputPort: ptr(output)}
}

func TestMirrorNativeSelectorsKeepDirectionVLANAndUnset(t *testing.T) {
	config := sample()
	forward := map[string]string{src: nativeSrc, dst: nativeDst, output: nativeOutput}
	backward := map[string]string{nativeSrc: src, nativeDst: dst, nativeOutput: output}
	resolve := func(id string) (string, bool) { value, ok := forward[id]; return value, ok }
	fields, err := Fields(config, resolve)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields["select_src_port"], []any{nativeSrc}) || !reflect.DeepEqual(fields["select_dst_port"], []any{nativeDst}) || !reflect.DeepEqual(fields["select_vlan"], []any{"0", "100"}) || !reflect.DeepEqual(fields["snaplen"], []any{}) {
		t.Fatalf("native selector directions, untagged VLAN or unset changed: %#v", fields)
	}
	fields["filter"] = []any{}
	fields["external_ids"] = map[string]any{"foreign": "retained-private-marker"}
	decoded, err := Decode(fields, func(id string) (string, bool) { value, ok := backward[id]; return value, ok })
	if err != nil || !reflect.DeepEqual(config, decoded) {
		t.Fatalf("native roundtrip: %#v, %v", decoded, err)
	}
	if fields["external_ids"].(map[string]any)["foreign"] != "retained-private-marker" {
		t.Fatal("decoder changed private native metadata")
	}
	fields["filter"] = []any{""}
	if _, err := Decode(fields, func(id string) (string, bool) { value, ok := backward[id]; return value, ok }); err != nil {
		t.Fatal("explicit native empty filter did not qualify", err)
	}
	if !reflect.DeepEqual(fields["filter"], []any{""}) {
		t.Fatal("native empty filter was normalized in the original image")
	}
	all := Configuration{SelectAll: true, SourcePorts: []string{}, DestinationPorts: []string{}, VLANs: []string{"100"}, OutputPort: ptr(output), Snaplen: ptr("14")}
	if err := Validate(all); err != nil {
		t.Fatalf("native all-Port selection still applies an independent VLAN filter: %v", err)
	}
	rspan := sample()
	rspan.OutputPort, rspan.OutputVLAN, rspan.Snaplen = nil, ptr("200"), ptr("65535")
	if err := Validate(rspan); err != nil {
		t.Fatal(err)
	}
	clone := Clone(config)
	clone.SourcePorts[0], clone.VLANs[0], *clone.OutputPort = dst, "200", dst
	if config.SourcePorts[0] != src || config.VLANs[0] != "0" || *config.OutputPort != output {
		t.Fatal("caller aliases changed captured originals")
	}
	if !reflect.DeepEqual(PortIDs(config), []string{src, dst, output}) {
		t.Fatal("immutable graph references omitted a direction or output")
	}
}

func TestMirrorRejectsUnknownAmbiguousAndUnqualifiedNativeState(t *testing.T) {
	for name, mutate := range map[string]func(*Configuration){
		"unknown-src":    func(c *Configuration) { c.SourcePorts = nil },
		"unknown-dst":    func(c *Configuration) { c.DestinationPorts = nil },
		"unknown-vlans":  func(c *Configuration) { c.VLANs = nil },
		"empty":          func(c *Configuration) { c.SourcePorts, c.DestinationPorts = []string{}, []string{} },
		"all-port-list":  func(c *Configuration) { c.SelectAll = true },
		"duplicate-src":  func(c *Configuration) { c.SourcePorts = []string{src, src} },
		"invalid-ref":    func(c *Configuration) { c.SourcePorts = []string{"port-name"} },
		"selector-limit": func(c *Configuration) { c.SourcePorts = make([]string, MaxSelectedPorts+1) },
		"vlan-limit":     func(c *Configuration) { c.VLANs = make([]string, MaxSelectedVLANs+1) },
		"duplicate-vlan": func(c *Configuration) { c.VLANs = []string{"100", "100"} },
		"reserved-vlan":  func(c *Configuration) { c.VLANs = []string{"4095"} },
		"leading-zero":   func(c *Configuration) { c.VLANs = []string{"0100"} },
		"signed-vlan":    func(c *Configuration) { c.VLANs = []string{"+100"} },
		"no-output":      func(c *Configuration) { c.OutputPort = nil },
		"two-outputs":    func(c *Configuration) { c.OutputVLAN = ptr("200") },
		"src-output":     func(c *Configuration) { c.OutputPort = ptr(src) },
		"dst-output":     func(c *Configuration) { c.OutputPort = ptr(dst) },
		"vlan-output":    func(c *Configuration) { c.OutputPort, c.OutputVLAN = nil, ptr("100") },
		"zero-output":    func(c *Configuration) { c.OutputPort, c.OutputVLAN = nil, ptr("0") },
		"name-output":    func(c *Configuration) { c.OutputPort = ptr("eth2") },
		"snaplen-small":  func(c *Configuration) { c.Snaplen = ptr("13") },
		"snaplen-large":  func(c *Configuration) { c.Snaplen = ptr("65536") },
		"snaplen-number": func(c *Configuration) { c.Snaplen = ptr("1e3") },
	} {
		t.Run(name, func(t *testing.T) {
			c := sample()
			mutate(&c)
			if Validate(c) == nil {
				t.Fatal("ambiguous or unsupported request accepted")
			}
		})
	}
	for _, name := range []string{"", " leading", "trailing ", "line\nbreak", "control\x00", strings.Repeat("a", 65)} {
		if ValidName(name) {
			t.Fatalf("invalid Mirror name accepted: %q", name)
		}
	}
	if !ValidName("native-span-1") || !ValidName("镜像会话") {
		t.Fatal("Mirror name was incorrectly limited to a host network-device name")
	}
	identity := func(id string) (string, bool) { return id, true }
	fields, err := Fields(sample(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fields["filter"] = []any{"ip"}
	if _, err := Decode(fields, identity); err == nil || err.Error() != "MIRROR_ADVANCED_FILTER_UNQUALIFIED" {
		t.Fatal("advanced packet filtering was discarded or silently adopted")
	}
	delete(fields, "filter")
	delete(fields, "select_vlan")
	if _, err := Decode(fields, identity); err == nil {
		t.Fatal("missing observation became a proven empty native set")
	}
	if _, err := Fields(sample(), func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("unresolved native Port binding accepted")
	}
	if _, err := Fields(sample(), func(string) (string, bool) { return "eth-name", true }); err == nil {
		t.Fatal("Port name was accepted as a native immutable UUID")
	}
}
