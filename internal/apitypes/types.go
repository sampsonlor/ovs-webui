// Package apitypes contains the public transport's authority-neutral primitives.
package apitypes

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Problem struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail"`
	Code          string         `json:"code"`
	CorrelationID string         `json:"correlation_id"`
	RequestID     *string        `json:"request_id"`
	RequestDomain *string        `json:"request_domain"`
	CommandEffect string         `json:"command_effect"`
	ResourceRef   *Ref           `json:"resource_ref"`
	Details       map[string]any `json:"details"`
}

func (p *Problem) Error() string      { return p.Code }
func (*Problem) RepositoryRejection() {}
func Fail(status int, code string) *Problem {
	return &Problem{Type: "urn:ovs-webui:problem:" + strings.ToLower(strings.ReplaceAll(code, "_", "-")), Title: code, Status: status, Detail: code, Code: code, CommandEffect: "unknown", Details: map[string]any{}}
}
func UUID(s string) bool {
	if len(s) != 36 || s != strings.ToLower(s) || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return err == nil && len(b) == 16 && b[8]&0xc0 == 0x80 && b[6]>>4 >= 1 && b[6]>>4 <= 8
}
func RequestTime(s string) (time.Time, bool) {
	if !UUID(s) || s[14] != '7' {
		return time.Time{}, false
	}
	b, _ := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	var n int64
	for _, v := range b[:6] {
		n = n<<8 | int64(v)
	}
	return time.UnixMilli(n), true
}
func ManagementID(s string) bool { return UUID(s) && s[14] == '4' }
func RequestID(now time.Time) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system randomness unavailable")
	}
	n := now.UnixMilli()
	for i := 5; i >= 0; i-- {
		b[i] = byte(n)
		n >>= 8
	}
	b[6] = b[6]&15 | 0x70
	b[8] = b[8]&63 | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
}

type Receipt struct {
	RequestID     string `json:"request_id"`
	Domain        string `json:"request_domain"`
	Epoch         string `json:"request_epoch"`
	State         string `json:"state"`
	Effect        string `json:"command_effect"`
	Resource      *Ref   `json:"resource_ref"`
	Job           *Ref   `json:"job_ref"`
	CorrelationID string `json:"correlation_id"`
}
type Accepted struct {
	RequestID     string `json:"request_id"`
	Domain        string `json:"request_domain"`
	Epoch         string `json:"request_epoch"`
	JobID         string `json:"job_id"`
	Resource      *Ref   `json:"resource_ref"`
	CorrelationID string `json:"correlation_id"`
}
type Result struct {
	Status   int
	Body     json.RawMessage
	Receipt  Receipt
	Replayed bool
}
