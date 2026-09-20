package safety

import (
	"github.com/sampsonlor/ovs-webui/internal/tlscontrol"
	"testing"
	"time"
)

func TestDeadlineUsesBootMonotonicAndWallEvidence(t *testing.T) {
	c := tlscontrol.Clock{BootID: "synthetic-boot", NS: 1000000000, Wall: time.Unix(200000, 0)}
	d := NewDeadline(c, 120*time.Second)
	for _, tc := range []struct {
		name    string
		clock   tlscontrol.Clock
		expired bool
	}{
		{"same-boot-restart", tlscontrol.Clock{BootID: c.BootID, NS: c.NS + 60e9, Wall: c.Wall.Add(60 * time.Second)}, false},
		{"monotonic-expired", tlscontrol.Clock{BootID: c.BootID, NS: d.EndNS, Wall: c.Wall.Add(time.Second)}, true},
		{"wall-expired", tlscontrol.Clock{BootID: c.BootID, NS: c.NS + 1, Wall: d.Wall}, true},
		{"new-boot", tlscontrol.Clock{BootID: "new", NS: c.NS, Wall: c.Wall}, true},
		{"wall-reversal", tlscontrol.Clock{BootID: c.BootID, NS: c.NS, Wall: c.Wall.Add(-time.Second)}, true},
		{"missing-clock", tlscontrol.Clock{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if d.Expired(tc.clock) != tc.expired {
				t.Fatal(tc)
			}
		})
	}
}
