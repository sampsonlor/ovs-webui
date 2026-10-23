package multicast

import "testing"

func text(s string) *string { return &s }
func defaults() Configuration {
	return Configuration{Parameters: map[string]*string{Keys[0]: nil, Keys[1]: nil, Keys[2]: nil}}
}

func TestMulticastPreservesUnsetOriginalAndRejectsSilentClamping(t *testing.T) {
	before := defaults()
	before.Parameters[Keys[0]] = text("300")
	request := Request{Enabled: true, Parameters: map[string]*string{Keys[1]: text("100"), Keys[2]: text("true")}}
	after, err := Merge(before, request)
	if err != nil || !after.Enabled || *after.Parameters[Keys[0]] != "300" || before.Enabled || before.Parameters[Keys[1]] != nil {
		t.Fatal("original or omitted/unset values changed", err)
	}
	*request.Parameters[Keys[1]] = "500"
	if *after.Parameters[Keys[1]] != "100" {
		t.Fatal("request alias changed sealed image")
	}
	removed, err := Merge(after, Request{Enabled: true, Parameters: map[string]*string{Keys[0]: nil}})
	if err != nil || removed.Parameters[Keys[0]] != nil || *after.Parameters[Keys[0]] != "300" {
		t.Fatal("explicit unset or original lost", err)
	}
	for _, invalid := range []struct{ key, value string }{
		{Keys[0], "14"}, {Keys[0], "3601"}, {Keys[1], "9"}, {Keys[1], "1000001"},
		{Keys[0], "0300"}, {Keys[0], "+300"}, {Keys[0], "300\n"}, {Keys[1], "2147483648"},
		{Keys[2], "1"}, {Keys[2], "TRUE"}, {"unknown-native-key", "1"},
	} {
		if _, err := Merge(defaults(), Request{Parameters: map[string]*string{invalid.key: text(invalid.value)}}); err == nil {
			t.Fatal("unqualified value accepted", invalid.key, invalid.value)
		}
	}
	for _, edge := range []struct{ key, value string }{{Keys[0], "15"}, {Keys[0], "3600"}, {Keys[1], "10"}, {Keys[1], "1000000"}, {Keys[2], "false"}} {
		if _, err := Merge(defaults(), Request{Parameters: map[string]*string{edge.key: text(edge.value)}}); err != nil {
			t.Fatal("qualified boundary refused", edge, err)
		}
	}
}
