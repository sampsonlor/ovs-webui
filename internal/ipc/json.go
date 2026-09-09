package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrInvalidJSON = errors.New("invalid typed JSON")

// DecodeStrict rejects ambiguity before encoding/json's case-insensitive struct
// matching or replacement of malformed Unicode can change request meaning.
func DecodeStrict(data []byte, target any) error {
	if len(data) > MaxBodyBytes || !utf8.Valid(data) || !validEscapes(data) {
		return ErrInvalidJSON
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tokens := 0
	value, err := readValue(d, 0, &tokens)
	if err != nil {
		return ErrInvalidJSON
	}
	if _, ok := value.(map[string]any); !ok {
		return ErrInvalidJSON
	}
	if _, err = d.Token(); err != io.EOF {
		return ErrInvalidJSON
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer || !exactFields(value, t.Elem()) {
		return ErrInvalidJSON
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(target); err != nil {
		return ErrInvalidJSON
	}
	return nil
}

func readValue(d *json.Decoder, depth int, count *int) (any, error) {
	*count++
	if depth > MaxJSONDepth || *count > MaxJSONTokens {
		return nil, ErrInvalidJSON
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := token.(type) {
	case json.Delim:
		switch v {
		case '{':
			result := make(map[string]any)
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				name, ok := key.(string)
				if !ok || len(name) > MaxJSONString || len(result) >= MaxJSONArray {
					return nil, ErrInvalidJSON
				}
				if _, exists := result[name]; exists {
					return nil, ErrInvalidJSON
				}
				child, err := readValue(d, depth+1, count)
				if err != nil {
					return nil, err
				}
				result[name] = child
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalidJSON
			}
			return result, nil
		case '[':
			result := make([]any, 0)
			for d.More() {
				if len(result) >= MaxJSONArray {
					return nil, ErrInvalidJSON
				}
				child, err := readValue(d, depth+1, count)
				if err != nil {
					return nil, err
				}
				result = append(result, child)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalidJSON
			}
			return result, nil
		default:
			return nil, ErrInvalidJSON
		}
	case string:
		if len(v) > MaxJSONString {
			return nil, ErrInvalidJSON
		}
	}
	return token, nil
}

func exactFields(value any, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if value == nil {
		return true
	} // The typed decoder and semantic validator decide nullability.
	switch v := value.(type) {
	case map[string]any:
		if t.Kind() == reflect.Map {
			if t.Key().Kind() != reflect.String {
				return false
			}
			for _, child := range v {
				if !exactFields(child, t.Elem()) {
					return false
				}
			}
			return true
		}
		if t.Kind() != reflect.Struct {
			return false
		}
		fields := make(map[string]reflect.Type)
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for key, child := range v {
			field, exists := fields[key]
			if !exists || !exactFields(child, field) {
				return false
			}
		}
	case []any:
		if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
			return false
		}
		for _, child := range v {
			if !exactFields(child, t.Elem()) {
				return false
			}
		}
	}
	return true
}

func validEscapes(data []byte) bool {
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		code, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return !quoted
}
