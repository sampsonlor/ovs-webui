package multicast

import (
	"strconv"
	"strings"
	"testing"
)

func TestMulticastGroupsKeepNativeProtocolsAndRejectUnprovenReplies(t *testing.T) {
	header := " port VLAN protocol GROUP Age\n"
	groups, err := DecodeGroups([]byte(header + "2 0 IGMPv2 239.192.0.7 3\nLOCAL 10 MLDv2 ff02::1234 0\n3 10 UNKNOWN querier 15\n"))
	if err != nil || len(groups) != 3 || groups[0].Port != 2 || groups[1].Port != 65534 || !groups[2].Router {
		t.Fatal(groups, err)
	}
	empty, err := DecodeGroups([]byte(header))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("native empty table lost", err)
	}
	var bounded strings.Builder
	bounded.WriteString(header)
	for n := 0; n < MaxGroups; n++ {
		bounded.WriteString(strconv.Itoa(n+1) + " 0 IGMPv2 239.192." + strconv.Itoa(n/256) + "." + strconv.Itoa(n%256) + " 0\n")
	}
	if values, err := DecodeGroups([]byte(bounded.String())); err != nil || len(values) != MaxGroups {
		t.Fatal("valid bounded table lost", err)
	}
	bounded.WriteString("257 0 IGMPv2 239.192.1.0 0\n")
	if values, err := DecodeGroups([]byte(bounded.String())); err == nil || values != nil {
		t.Fatal("over-budget table silently truncated")
	}
	for _, wire := range []string{header + "2 0 IGMPv2 239.192.0.7 0\n2 0 IGMPv3 239.192.0.7 1\n", header + "2 0 MLDv2 FF3E::1234 0\n2 0 MLDv2 ff3e::1234 1\n", strings.ReplaceAll(header, "\n", "\r\n")} {
		if values, err := DecodeGroups([]byte(wire)); err == nil || values != nil {
			t.Fatal("ambiguous/control-character reply accepted")
		}
	}
	for _, wire := range []string{"", "multicast snooping is disabled", header + "2 0 IGMPv2 192.0.2.1 0", header + "2 0 IGMPv2 ff02::1234 0", header + "2 4095 IGMPv2 239.192.0.7 0", header + "2 0 IGMPv4 239.192.0.7 0", header + "2 0 IGMPv2 239.192.0.7 -1", header + "2 0 IGMPv2 querier 0", header + "2 0 IGMPv2 239.192.0.7 0 extra", header + strings.Repeat("2 0 IGMPv2 239.192.0.7 0\n", 2), strings.Repeat("a", MaxGroupWire+1)} {
		if rows, err := DecodeGroups([]byte(wire)); err == nil || rows != nil {
			t.Fatal("unproven reply became data")
		}
	}
}
