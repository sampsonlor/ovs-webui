package ovswitness

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func sampleText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/known.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func wrap(text string) []byte {
	b, _ := json.Marshal(map[string]any{"id": 1, "error": nil, "result": text})
	return b
}
func expected(t *testing.T) (string, string) {
	var s Sample
	if json.Unmarshal([]byte(sampleText(t)), &s) != nil {
		t.Fatal("fixture")
	}
	return s.Nonce, s.Bridge.UUID
}

func TestWitnessClosedWireAndAllOrNothing(t *testing.T) {
	text := sampleText(t)
	if _, err := compiledSchema(); err != nil {
		t.Fatal("private schema", err)
	}
	nonce, bridge := expected(t)
	s, err := Decode(wrap(text), nonce, bridge)
	if err != nil || Exclusive(s) != nil || s.Native.STP.Local.MaxAgeMS != 20000 {
		t.Fatal(s, err)
	}
	cases := map[string][]byte{
		"duplicate_outer":       []byte(`{"id":1,"id":1,"error":null,"result":"{}"}`),
		"missing_outer_error":   []byte(`{"id":1,"something":null,"result":"{}"}`),
		"outer_error":           []byte(`{"id":1,"error":"unavailable","result":"{}"}`),
		"wrong_id":              bytes.Replace(wrap(text), []byte(`"id":1`), []byte(`"id":2`), 1),
		"numeric_id_alias":      bytes.Replace(wrap(text), []byte(`"id":1`), []byte(`"id":1.0`), 1),
		"inner_object_not_text": []byte(`{"id":1,"error":null,"result":{}}`),
		"duplicate_inner":       wrap(strings.Replace(text, `"state": "known"`, `"state":"known","state":"known"`, 1)),
		"unknown_inner":         wrap(strings.Replace(text, `"state": "known"`, `"state":"known","extra":false`, 1)),
		"case_alias":            wrap(strings.Replace(text, `"state"`, `"State"`, 1)),
		"invalid_version":       wrap(strings.Replace(text, ".stp-witness.v1", ".stp-witness.v2", 1)),
		"invalid_utf8":          []byte{0xff, '{', '}'},
		"invalid_surrogate":     wrap(strings.Replace(text, `"br-test"`, `"\ud800"`, 1)),
		"null_required_flag":    wrap(strings.Replace(text, `"rstp_enable": false`, `"rstp_enable":null`, 1)),
		"missing_false_flag":    wrap(strings.Replace(text, `"rstp_enable": false,`, "", 1)),
		"missing_null_key":      wrap(strings.Replace(text, `"stp-priority": null,`, "", 1)),
		"unknown_parameter":     wrap(strings.Replace(text, `"stp-priority"`, `"unknown-priority"`, 1)),
		"enabled_null_local":    wrap(strings.Replace(text, `"enabled": false`, `"enabled":true`, 1)),
		"negative_timer":        wrap(strings.Replace(text, `"hello_time_ms": 2000`, `"hello_time_ms":-1`, 1)),
		"timer_overflow":        wrap(strings.Replace(text, `"hello_time_ms": 2000`, `"hello_time_ms":4294967296`, 1)),
		"fractional_timer":      wrap(strings.Replace(text, `"hello_time_ms": 2000`, `"hello_time_ms":2000.5`, 1)),
		"zero_timer":            wrap(strings.Replace(text, `"hello_time_ms": 2000`, `"hello_time_ms":0`, 1)),
		"wrong_mac_format":      wrap(strings.Replace(text, "02:00:00:00:00:01", "0200.0000.0001", 1)),
		"priority_overflow":     wrap(strings.Replace(text, `"priority": 32768`, `"priority":65536`, 1)),
		"unowned_lock":          wrap(strings.Replace(text, `"lock_owned": true`, `"lock_owned":false`, 1)),
		"contended_lock":        wrap(strings.Replace(text, `"lock_contended": false`, `"lock_contended":true`, 1)),
		"wrong_lock":            wrap(strings.Replace(text, "ovs_vswitchd", "other_lock", 1)),
		"disconnected":          wrap(strings.Replace(text, `"connected": true`, `"connected":false`, 1)),
		"setter_error":          wrap(strings.Replace(text, `"setters_reported_error": false`, `"setters_reported_error":true`, 1)),
		"incomplete":            wrap(strings.Replace(text, `"completion": "complete"`, `"completion":"pending"`, 1)),
		"seqno_mismatch":        wrap(strings.Replace(text, `"idl_seqno": "17"`, `"idl_seqno":"18"`, 1)),
		"wire_whitespace":       append(bytes.Repeat([]byte(" "), MaxWireBytes), wrap(text)...),
		"inner_whitespace":      wrap(strings.Repeat(" ", MaxInnerBytes) + text),
		"truncated":             wrap(text[:len(text)-3]),
		"outer_trailing":        append(wrap(text), []byte(`{}`)...),
		"inner_trailing":        wrap(text + `{}`),
	}
	for _, value := range []string{"-1", "01", "+1", "18446744073709551616", "18446744073709551615", "1.5", ""} {
		cases["invalid_epoch_"+value] = wrap(strings.Replace(text, `"authority_epoch": "9"`, `"authority_epoch":"`+value+`"`, 1))
	}
	for _, value := range []string{"4294967296", "-1", "01"} {
		cases["invalid_seqno_"+value] = wrap(strings.ReplaceAll(text, `"idl_seqno": "17"`, `"idl_seqno":"`+value+`"`))
	}
	cases["cfg_signed_overflow"] = wrap(strings.Replace(text, `"root_next_cfg": "5"`, `"root_next_cfg":"9223372036854775808"`, 1))
	cases["parameter_budget"] = wrap(strings.Replace(text, `"stp-priority": null`, `"stp-priority":"`+strings.Repeat("1", 129)+`"`, 1))
	cases["parameter_nul"] = wrap(strings.Replace(text, `"stp-priority": null`, `"stp-priority":"\u0000"`, 1))
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := Decode(wire, nonce, bridge)
			if err == nil || !reflect.DeepEqual(out, Sample{}) {
				t.Fatal("unproven observation escaped", out, err)
			}
		})
	}
	for _, scope := range [][2]string{{strings.Repeat("a", 64), bridge}, {nonce, "55555555-5555-4555-8555-555555555555"}, {"", bridge}} {
		out, err := Decode(wrap(text), scope[0], scope[1])
		if err == nil || !reflect.DeepEqual(out, Sample{}) {
			t.Fatal("wrong request scope", err)
		}
	}
}

func TestWitnessWithheldAndObservationIsNotExclusiveAuthority(t *testing.T) {
	nonce, bridge := expected(t)
	for _, reason := range []string{"IDL_LOCK_UNOWNED", "IDL_LOCK_CONTENDED", "CONFIGURATION_IN_PROGRESS", "EPOCH_EXHAUSTED", "SETTER_REPORTED_ERROR", "WITNESS_BUDGET"} {
		text, _ := json.Marshal(map[string]any{"schema_version": "ovs-webui.stp-witness.v1", "nonce": nonce, "state": "withheld", "reason": reason})
		out, err := Decode(wrap(string(text)), nonce, bridge)
		if !errors.Is(err, ErrWithheld) || !reflect.DeepEqual(out, Sample{}) {
			t.Fatal(out, err)
		}
	}
	for _, text := range []string{
		`{"schema_version":"ovs-webui.stp-witness.v1","nonce":"` + nonce + `","state":"withheld","reason":"IDL_LOCK_UNOWNED|IDL_LOCK_CONTENDED"}`,
		`{"schema_version":"ovs-webui.stp-witness.v1","nonce":"` + nonce + `","state":"withheld","reason":"IDL_LOCK_UNOWNED","bridge":{}}`,
	} {
		if _, err := Decode(wrap(text), nonce, bridge); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, counterValue := range []string{"0", "4294967295"} {
		text := strings.ReplaceAll(sampleText(t), `"idl_seqno": "17"`, `"idl_seqno":"`+counterValue+`"`)
		if _, err := Decode(wrap(text), nonce, bridge); err != nil {
			t.Fatal("native seqno boundary cannot itself prove ABA", err)
		}
	}
	s, err := Decode(wrap(sampleText(t)), nonce, bridge)
	if err != nil {
		t.Fatal(err)
	}
	s.Native.RSTP = s.Native.STP
	if Exclusive(s) == nil {
		t.Fatal("both enabled")
	}
	s.Native.RSTP = Engine{}
	s.Native.STP.Local.HelloMS = 1001
	if Exclusive(s) == nil {
		t.Fatal("fractional seconds silently rounded")
	}
}

func TestWitnessTypedAtomsAndInputBudgets(t *testing.T) {
	vectors := []struct {
		value any
		kind  string
		want  string
	}{
		{"", "string", "73"}, {"a:b,\n雪", "string", "73613a622c0ae99baa"},
		{json.Number("-9223372036854775808"), "integer", "692d39323233333732303336383534373735383038"},
		{false, "boolean", "6230"}, {true, "boolean", "6231"},
	}
	for _, v := range vectors {
		got, err := atom(v.value, v.kind)
		if err != nil || got != v.want {
			t.Fatal(got, v.want, err)
		}
	}
	mapType := columnType{Key: "string", Value: "string", Min: 0, Max: "unlimited"}
	a := []any{"map", []any{[]any{"z", "last"}, []any{"a", "first"}}}
	b := []any{"map", []any{[]any{"a", "first"}, []any{"z", "last"}}}
	x, e1 := datum(a, mapType)
	y, e2 := datum(b, mapType)
	if e1 != nil || e2 != nil || x != y {
		t.Fatal(x, y, e1, e2)
	}
	for _, bad := range []any{nil, []any{"map", []any{[]any{"a", "x"}, []any{"a", "y"}}}, []any{"map", []any{[]any{strings.Repeat("x", 4097), "v"}}}} {
		if _, err := datum(bad, mapType); err == nil {
			t.Fatal("invalid map")
		}
	}
	for _, n := range []json.Number{"9223372036854775808", "01", "1.0", "1e0"} {
		if _, err := atom(n, "integer"); err == nil {
			t.Fatal(n)
		}
	}
	load := func() map[string][]map[string]any {
		t.Helper()
		data, err := os.ReadFile("testdata/input.json")
		if err != nil {
			t.Fatal(err)
		}
		var rows map[string][]map[string]any
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		if d.Decode(&rows) != nil {
			t.Fatal("input fixture")
		}
		return rows
	}
	want, err := os.ReadFile("testdata/input.sha256")
	if err != nil {
		t.Fatal(err)
	}
	rows := load()
	got, err := Digest(rows)
	if err != nil || got != strings.TrimSpace(string(want)) {
		t.Fatal("independent canonical golden", got, err)
	}
	rows["Open_vSwitch"][0]["bridges"] = []any{"set", []any{rows["Open_vSwitch"][0]["bridges"]}}
	if normalized, err := Digest(rows); err != nil || normalized != got {
		t.Fatal("native singleton set equivalence", normalized, err)
	}
	for name, change := range map[string]func(map[string][]map[string]any){
		"missing_column":    func(r map[string][]map[string]any) { delete(r["Bridge"][0], "ports") },
		"unknown_column":    func(r map[string][]map[string]any) { r["Bridge"][0]["unmodelled"] = false },
		"foreign_root_link": func(r map[string][]map[string]any) { r["Open_vSwitch"][0]["bridges"] = []any{"set", []any{}} },
		"foreign_port_link": func(r map[string][]map[string]any) { r["Bridge"][0]["ports"] = []any{"set", []any{}} },
		"foreign_interface_link": func(r map[string][]map[string]any) {
			r["Port"][0]["interfaces"] = []any{"uuid", "77777777-7777-4777-8777-777777777777"}
		},
		"duplicate_rows":     func(r map[string][]map[string]any) { r["Interface"] = append(r["Interface"], r["Interface"][0]) },
		"unsupported_type":   func(r map[string][]map[string]any) { r["Interface"][0]["type"] = "patch" },
		"unavailable_ofport": func(r map[string][]map[string]any) { r["Interface"][0]["ofport"] = json.Number("-1") },
		"unmodelled_qos": func(r map[string][]map[string]any) {
			r["Port"][0]["qos"] = []any{"uuid", "77777777-7777-4777-8777-777777777777"}
		},
		"input_budget": func(r map[string][]map[string]any) {
			pairs := []any{}
			for i := 0; i < 128; i++ {
				pairs = append(pairs, []any{strings.Repeat("k", i+1), strings.Repeat("x", 4096)})
			}
			r["Bridge"][0]["other_config"] = []any{"map", pairs}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := load()
			change(r)
			if out, err := Digest(r); err == nil || out != "" {
				t.Fatal("unproven input digest escaped", out, err)
			}
		})
	}
	if _, err := Digest(map[string][]map[string]any{}); err == nil {
		t.Fatal("incomplete input")
	}
}

func FuzzWitnessDecode(f *testing.F) {
	f.Add([]byte(`{"id":1,"error":null,"result":"{}"}`))
	f.Fuzz(func(t *testing.T, wire []byte) {
		s, err := Decode(wire, strings.Repeat("a", 64), "33333333-3333-4333-8333-333333333333")
		if err != nil && !reflect.DeepEqual(s, Sample{}) {
			t.Fatal("partial sample")
		}
	})
}
