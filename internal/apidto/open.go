package apidto

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Known states stay strings; extension fields survive decode/edit/encode.
func decodeOpen(data []byte, target any, extra *map[string]json.RawMessage) error {
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	t := reflect.TypeOf(target).Elem()
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		delete(values, name)
	}
	*extra = values
	return nil
}
func encodeOpen(value any, extra map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	values := map[string]json.RawMessage{}
	if err = json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	for key, item := range extra {
		if _, known := values[key]; !known {
			values[key] = item
		}
	}
	return json.Marshal(values)
}
