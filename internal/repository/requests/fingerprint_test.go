package requests

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCanonicalFingerprintPreservesTypesAndExactNumbers(t *testing.T) {
	c := Command{Operation: "test", Method: "POST", URI: "/resource", Precondition: `"r1"`, Payload: json.RawMessage(`{"a":1,"b":[-0.0,123.40]}`)}
	first, err := fingerprint(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Payload = json.RawMessage(`{"b":[0,1.234e2],"a":1e0}`)
	second, err := fingerprint(c)
	if err != nil || first != second {
		t.Fatal("same typed payload differed", err)
	}
	for _, pair := range [][2]string{{`1`, `"1"`}, {`1`, `["number","1e0"]`}, {`9007199254740992`, `9007199254740993`}, {`{"a":1}`, `{"a":true}`}} {
		c.Payload = json.RawMessage(pair[0])
		a, _ := fingerprint(c)
		c.Payload = json.RawMessage(pair[1])
		b, _ := fingerprint(c)
		if a == b {
			t.Fatal("fingerprint collision", pair)
		}
	}
	c.Sensitive = true
	c.FingerprintKey = bytes.Repeat([]byte{1}, 32)
	a, _ := fingerprint(c)
	c.FingerprintKey = bytes.Repeat([]byte{2}, 32)
	b, _ := fingerprint(c)
	if a == b {
		t.Fatal("HMAC ignored authority key")
	}
}
