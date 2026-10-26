// Package tunnel defines the closed native static tunnel configuration group.
// It has no host provisioning, OpenFlow, provider or execution authority.
package tunnel

import (
	"errors"
	"net/netip"
	"slices"
	"strconv"
)

var Types = []string{"vxlan", "geneve", "gre"}
var Keys = []string{"remote_ip", "local_ip", "key", "dst_port"}

type Configuration struct {
	Type    string            `json:"native_type"`
	Options map[string]string `json:"options"`
}

// An omitted key preserves its exact captured value; null removes it. There is
// no implicit default insertion or raw Interface option rewrite request.
type Request struct {
	Type    string             `json:"native_type,omitempty"`
	Options map[string]*string `json:"options"`
}

func Clone(c Configuration) Configuration {
	out := Configuration{Type: c.Type, Options: map[string]string{}}
	for k, v := range c.Options {
		out.Options[k] = v
	}
	return out
}

func endpoint(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	return a, err == nil && a.Zone() == "" && !a.Is4In6() && a.String() == s && a.IsGlobalUnicast() && !a.IsLoopback() && !a.IsLinkLocalUnicast()
}

func integer(s string, min, max uint64) bool {
	n, err := strconv.ParseUint(s, 10, 64)
	return err == nil && n >= min && n <= max && strconv.FormatUint(n, 10) == s
}

func Validate(c Configuration) error {
	bad := func(code string) error { return errors.New(code) }
	if !slices.Contains(Types, c.Type) {
		return bad("TUNNEL_TYPE_UNSUPPORTED")
	}
	for k, v := range c.Options {
		if v == "flow" && slices.Contains([]string{"remote_ip", "local_ip", "key", "in_key", "out_key"}, k) {
			return bad("TUNNEL_DYNAMIC_READ_ONLY")
		}
	}
	for k := range c.Options {
		if !slices.Contains(Keys, k) {
			return bad("TUNNEL_ADVANCED_OPTIONS_READ_ONLY")
		}
	}
	remote, ok := endpoint(c.Options["remote_ip"])
	if !ok {
		return bad("TUNNEL_STATIC_REMOTE_IP_REQUIRED")
	}
	if c.Type == "gre" && !remote.Is4() {
		return bad("TUNNEL_GRE_IPV4_REQUIRED")
	}
	if value, exists := c.Options["local_ip"]; exists {
		local, valid := endpoint(value)
		if !valid || local.Is4() != remote.Is4() || local == remote {
			return bad("TUNNEL_LOCAL_IP_INVALID")
		}
	}
	if value, exists := c.Options["key"]; exists {
		max := uint64(16777215)
		if c.Type == "gre" {
			max = 4294967295
		}
		if !integer(value, 0, max) {
			return bad("TUNNEL_KEY_OUT_OF_RANGE")
		}
	}
	if value, exists := c.Options["dst_port"]; exists {
		if c.Type == "gre" || !integer(value, 1, 65535) {
			return bad("TUNNEL_DESTINATION_PORT_INVALID")
		}
	}
	return nil
}

func Merge(before Configuration, r Request) (Configuration, error) {
	after := Clone(before)
	if r.Type != "" {
		after.Type = r.Type
	}
	for k, v := range r.Options {
		if !slices.Contains(Keys, k) {
			return Configuration{}, errors.New("INVALID_TUNNEL_OPTION")
		}
		if v == nil {
			delete(after.Options, k)
		} else {
			after.Options[k] = *v
		}
	}
	if err := Validate(after); err != nil {
		return Configuration{}, err
	}
	return after, nil
}

// Decode retains the complete private native options map. Its error makes
// dynamic/advanced configurations observable but ineligible for this writer.
// Public projection must select Keys explicitly; unknown options may be secret.
func Decode(typ string, raw any) (Configuration, error) {
	c := Configuration{Type: typ, Options: map[string]string{}}
	m, ok := raw.(map[string]any)
	if !ok || len(m) > 128 {
		return c, errors.New("TUNNEL_OPTIONS_UNKNOWN")
	}
	for k, v := range m {
		s, ok := v.(string)
		if !ok || len(k) > 128 || len(s) > 4096 {
			return Configuration{}, errors.New("TUNNEL_OPTIONS_UNKNOWN")
		}
		c.Options[k] = s
	}
	return c, Validate(c)
}

// NativeIdentity uses native semantic defaults only to detect a conflicting
// tunnel endpoint. Captured configuration and compensation never use defaults.
func NativeIdentity(c Configuration) (string, error) {
	if err := Validate(c); err != nil {
		return "", err
	}
	key := c.Options["key"]
	if key == "" {
		key = "0"
	}
	// The documented receive identity excludes UDP destination port. Keep
	// collision admission conservative even if a datapath can distinguish it.
	return c.Type + "/" + c.Options["remote_ip"] + "/" + c.Options["local_ip"] + "/" + key, nil
}
