// Package multicast defines the closed native Bridge snooping configuration.
// Configuration validity is not proof of runtime installation or forwarding.
package multicast

import (
	"errors"
	"strconv"
)

var Keys = []string{"mcast-snooping-aging-time", "mcast-snooping-table-size", "mcast-snooping-disable-flood-unregistered"}

type Configuration struct {
	Enabled    bool               `json:"enabled"`
	Parameters map[string]*string `json:"parameters"`
}

type Request struct {
	Enabled    bool               `json:"enabled"`
	Parameters map[string]*string `json:"parameters"`
}

func Clone(c Configuration) Configuration {
	out := Configuration{Enabled: c.Enabled, Parameters: map[string]*string{}}
	for k, v := range c.Parameters {
		if v == nil {
			out.Parameters[k] = nil
		} else {
			copy := *v
			out.Parameters[k] = &copy
		}
	}
	return out
}

// Validate uses the qualified operating ranges rather than allowing OVS to
// silently clamp a requested integer. Unset remains unset, never a default.
// See the pinned vswitch.xml documentation and native installation tests.
func Validate(c Configuration) error {
	if len(c.Parameters) != len(Keys) {
		return errors.New("MULTICAST_CONFIGURATION_INVALID")
	}
	for _, key := range Keys {
		value, exists := c.Parameters[key]
		if !exists {
			return errors.New("MULTICAST_CONFIGURATION_INVALID")
		}
		if value == nil {
			continue
		}
		if key == "mcast-snooping-disable-flood-unregistered" {
			if *value != "true" && *value != "false" {
				return errors.New("MULTICAST_BOOLEAN_INVALID")
			}
			continue
		}
		n, err := strconv.ParseInt(*value, 10, 32)
		if err != nil || strconv.FormatInt(n, 10) != *value {
			return errors.New("MULTICAST_INTEGER_INVALID")
		}
		min, max := int64(15), int64(3600)
		if key == "mcast-snooping-table-size" {
			min, max = 10, 1000000
		}
		if n < min || n > max {
			return errors.New("MULTICAST_INTEGER_OUT_OF_RANGE")
		}
	}
	return nil
}

// Merge preserves the captured raw value for an omitted key. Explicit null
// removes a native map entry. The caller retains all unrelated other_config.
func Merge(original Configuration, request Request) (Configuration, error) {
	if err := Validate(original); err != nil {
		return Configuration{}, err
	}
	out := Clone(original)
	out.Enabled = request.Enabled
	for key, value := range request.Parameters {
		if _, exists := out.Parameters[key]; !exists {
			return Configuration{}, errors.New("MULTICAST_PARAMETER_UNSUPPORTED")
		}
		if value == nil {
			out.Parameters[key] = nil
		} else {
			copy := *value
			out.Parameters[key] = &copy
		}
	}
	return out, Validate(out)
}
