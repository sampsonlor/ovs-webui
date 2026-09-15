package ovsdb

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
	"unicode/utf8"
)

const maxFrame = 4 << 20

type message struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     json.RawMessage   `json:"id"`
	Result json.RawMessage   `json:"result"`
	Error  json.RawMessage   `json:"error"`
}

func frame(r *bufio.Reader) ([]byte, error) {
	data := make([]byte, 0, 4096)
	depth := 0
	quoted, escaped := false, false
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
				continue
			}
			if b != '{' {
				return nil, errors.New("OVSDB_FRAME_INVALID")
			}
		}
		data = append(data, b)
		if len(data) > maxFrame {
			return nil, errors.New("OVSDB_FRAME_BUDGET")
		}
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		switch b {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 32 {
				return nil, errors.New("OVSDB_DEPTH_BUDGET")
			}
		case '}', ']':
			depth--
			if depth == 0 {
				return data, nil
			}
		}
	}
}
func validateJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("OVSDB_UTF8_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	tokens := 0
	var value func(int) error
	value = func(depth int) error {
		tokens++
		if depth > 32 || tokens > 300000 {
			return errors.New("OVSDB_JSON_BUDGET")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if s, ok := t.(string); ok && len(s) > 65536 {
			return errors.New("OVSDB_STRING_BUDGET")
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || len(s) > 128 || seen[s] {
						return errors.New("OVSDB_KEY_INVALID")
					}
					seen[s] = true
					if err = value(depth + 1); err != nil {
						return err
					}
				}
				_, err = d.Token()
				return err
			case '[':
				n := 0
				for d.More() {
					n++
					if n > 4096 {
						return errors.New("OVSDB_ARRAY_BUDGET")
					}
					if err = value(depth + 1); err != nil {
						return err
					}
				}
				_, err = d.Token()
				return err
			default:
				return errors.New("OVSDB_JSON_INVALID")
			}
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("OVSDB_JSON_TRAILING")
	}
	return nil
}
func readMessage(r *bufio.Reader) (message, error) {
	var m message
	b, err := frame(r)
	if err != nil {
		return m, err
	}
	if err = validateJSON(b); err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}
func send(conn net.Conn, v any) error {
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	return json.NewEncoder(conn).Encode(v)
}
func request(conn net.Conn, id int, method string, params any) error {
	switch method {
	case "get_schema", "monitor", "echo":
	default:
		return errors.New("OVSDB_METHOD_DENIED")
	}
	return send(conn, map[string]any{"method": method, "params": params, "id": id})
}
func replyEcho(conn net.Conn, m message) error {
	return send(conn, map[string]any{"result": m.Params, "error": nil, "id": m.ID})
}
func call(conn net.Conn, r *bufio.Reader, id int, method string, params any) (json.RawMessage, error) {
	if err := request(conn, id, method, params); err != nil {
		return nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}
	for n := 0; n < 32; n++ {
		m, err := readMessage(r)
		if err != nil {
			return nil, err
		}
		if m.Method == "echo" {
			if err = replyEcho(conn, m); err != nil {
				return nil, err
			}
			continue
		}
		if m.Method != "" || string(m.ID) != strconv.Itoa(id) {
			return nil, errors.New("OVSDB_RESPONSE_INVALID")
		}
		if len(m.Error) > 0 && string(m.Error) != "null" {
			return nil, errors.New("OVSDB_REQUEST_REJECTED")
		}
		if len(m.Result) == 0 {
			return nil, errors.New("OVSDB_RESULT_MISSING")
		}
		return m.Result, nil
	}
	return nil, errors.New("OVSDB_RESPONSE_BUDGET")
}
