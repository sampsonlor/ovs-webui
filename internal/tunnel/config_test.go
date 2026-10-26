package tunnel

import "testing"

func TestStaticTunnelKeepsUnsetAndNativeKeyWidthWithoutClamping(t *testing.T) {
	for _, typ := range Types {
		before := Configuration{Type: typ, Options: map[string]string{"remote_ip": "192.0.2.20"}}
		value := "16777215"
		if typ == "gre" {
			value = "4294967295"
		}
		after, err := Merge(before, Request{Options: map[string]*string{"key": &value}})
		if err != nil || after.Options["key"] != value || len(before.Options) != 1 {
			t.Fatalf("%s capture changed: %v %v", typ, after, err)
		}
		value = "1"
		if after.Options["key"] == value {
			t.Fatal("request alias retained")
		}
		after, err = Merge(after, Request{Options: map[string]*string{"key": nil}})
		if err != nil || len(after.Options) != 1 || len(before.Options) != 1 {
			t.Fatalf("exact unset lost: %v %v", after, err)
		}
		a, _ := NativeIdentity(before)
		zero, err := Merge(before, Request{Options: map[string]*string{"key": pointer("0")}})
		b, _ := NativeIdentity(zero)
		if err != nil || a != b || len(before.Options) != 1 {
			t.Fatal("semantic duplicate escaped without retaining raw unset")
		}
	}
}

func pointer(s string) *string { return &s }

func TestStaticTunnelRejectsDynamicForeignAndAmbiguousEndpointInput(t *testing.T) {
	cases := []struct{ typ, key, value string }{
		{"vxlan", "remote_ip", "flow"}, {"geneve", "key", "flow"}, {"gre", "in_key", "flow"},
		{"vxlan", "psk", "private"}, {"vxlan", "remote_ip", "peer.example"},
		{"vxlan", "remote_ip", "224.0.0.1"}, {"vxlan", "remote_ip", "0.0.0.0"},
		{"vxlan", "remote_ip", "::ffff:192.0.2.20"}, {"vxlan", "remote_ip", "fe80::1%eth0"},
		{"gre", "remote_ip", "2001:db8::20"}, {"vxlan", "key", "16777216"},
		{"gre", "key", "4294967296"}, {"geneve", "key", "01"}, {"geneve", "key", "-1"},
		{"gre", "dst_port", "4789"}, {"geneve", "dst_port", "0"}, {"geneve", "dst_port", "65536"},
		{"vxlan", "local_ip", "2001:db8::10"}, {"vxlan", "local_ip", "192.0.2.20"},
	}
	for _, row := range cases {
		c := Configuration{Type: row.typ, Options: map[string]string{"remote_ip": "192.0.2.20", row.key: row.value}}
		if Validate(c) == nil {
			t.Fatalf("accepted %s %s=%s", row.typ, row.key, row.value)
		}
	}
	for _, typ := range []string{"vxlan", "geneve"} {
		if Validate(Configuration{Type: typ, Options: map[string]string{"remote_ip": "2001:db8::20", "local_ip": "2001:db8::10"}}) != nil {
			t.Fatal("static IPv6 UDP endpoint rejected")
		}
	}
	if _, err := Decode("vxlan", map[string]any{"remote_ip": "192.0.2.20", "key": 3}); err == nil {
		t.Fatal("coerced unknown native value")
	}
}
