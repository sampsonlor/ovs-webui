package mirror

import (
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/apitypes"
)

// Decode consumes the provider's complete private native image. Native integer
// values are canonical decimal strings, as for the existing configuration codec.
// Resolve returns the current management identity for an exact native Port UUID.
func Decode(raw map[string]any, resolve func(string) (string, bool)) (Configuration, error) {
	c := Configuration{SourcePorts: []string{}, DestinationPorts: []string{}, VLANs: []string{}}
	all, ok := raw["select_all"].(bool)
	if !ok {
		return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
	}
	c.SelectAll = all
	for key, target := range map[string]*[]string{"select_src_port": &c.SourcePorts, "select_dst_port": &c.DestinationPorts, "select_vlan": &c.VLANs} {
		values, known := raw[key].([]any)
		if !known || len(values) > MaxSelectedVLANs {
			return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
		}
		for _, v := range values {
			text, known := v.(string)
			if !known {
				return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
			}
			if key != "select_vlan" {
				text, known = resolve(text)
				if !known {
					return c, errors.New("MIRROR_PORT_BINDING_UNPROVEN")
				}
			}
			*target = append(*target, text)
		}
	}
	for key, target := range map[string]**string{"output_port": &c.OutputPort, "output_vlan": &c.OutputVLAN, "snaplen": &c.Snaplen} {
		values, known := raw[key].([]any)
		if !known || len(values) > 1 {
			return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
		}
		if len(values) == 1 {
			text, known := values[0].(string)
			if !known {
				return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
			}
			if key == "output_port" {
				text, known = resolve(text)
				if !known {
					return c, errors.New("MIRROR_PORT_BINDING_UNPROVEN")
				}
			}
			*target = &text
		}
	}
	// OVS 3.3 has no filter column; newer schemas use an optional string set.
	// Both absent and explicitly empty expressions retain their exact raw image.
	if filter, exists := raw["filter"]; exists {
		values, ok := filter.([]any)
		if !ok || len(values) > 1 {
			return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
		}
		if len(values) == 1 {
			text, ok := values[0].(string)
			if !ok {
				return c, errors.New("MIRROR_CONFIGURATION_UNKNOWN")
			}
			if text != "" {
				return c, errors.New("MIRROR_ADVANCED_FILTER_UNQUALIFIED")
			}
		}
	}
	return c, Validate(c)
}

// Fields derives only the managed native group. The caller must merge these
// fields into the captured complete image, retaining unrelated native metadata.
func Fields(c Configuration, resolve func(string) (string, bool)) (map[string]any, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	out := map[string]any{"select_all": c.SelectAll, "select_src_port": []any{}, "select_dst_port": []any{}, "select_vlan": []any{}, "output_port": []any{}, "output_vlan": []any{}, "snaplen": []any{}}
	for key, values := range map[string][]string{"select_src_port": c.SourcePorts, "select_dst_port": c.DestinationPorts, "select_vlan": c.VLANs} {
		a := []any{}
		for _, value := range values {
			if key != "select_vlan" {
				var ok bool
				value, ok = resolve(value)
				if !ok || !apitypes.UUID(value) {
					return nil, errors.New("MIRROR_PORT_BINDING_UNPROVEN")
				}
			}
			a = append(a, value)
		}
		out[key] = a
	}
	for key, value := range map[string]*string{"output_port": c.OutputPort, "output_vlan": c.OutputVLAN, "snaplen": c.Snaplen} {
		if value != nil {
			text := *value
			if key == "output_port" {
				var ok bool
				text, ok = resolve(text)
				if !ok || !apitypes.UUID(text) {
					return nil, errors.New("MIRROR_PORT_BINDING_UNPROVEN")
				}
			}
			out[key] = []any{text}
		}
	}
	return out, nil
}
