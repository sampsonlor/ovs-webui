// Package redact is the common boundary for operational logs and ordinary
// diagnostic/export documents. Explicit one-time credential responses bypass
// this boundary only in their dedicated authenticated response handlers.
package redact

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

const Marker = "[REDACTED]"

var eventCode = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,96}$`)

var messages = map[string]bool{
	"invalid_process_configuration": true, "auth_key_initialization_failed": true, "auth_key_initialized": true,
	"database_initialization_failed": true, "database_initialized": true, "storage_degraded": true,
	"authentication_degraded": true, "bootstrap_failed": true, "local_administrator_initialized": true,
	"service_start_failed": true, "service_started": true, "service_stopped": true, "service_stopping": true,
	"session_key_initialization_failed": true, "session_key_initialized": true,
	"invalid_public_api_configuration": true, "ipc_request_rejected": true,
	"security_action_failed": true, "security_action_completed": true,
}

// Diagnostic attributes are fixed vocabularies, not another channel for
// arbitrary operational strings, user labels, paths or driver errors.
var diagnosticValues = map[string]map[string]bool{
	"operation": {
		"runtime.inspect": true, "auth.authenticate": true, "auth.inspect": true, "auth.check": true,
		"auth.reauthenticate": true, "auth.revoke": true, "security.read": true, "security.execute": true,
		"inventory.read": true, "candidate.prepare": true, "candidate.read": true, "candidate.validate": true,
		"safe.admit": true, "safe.resolve": true, "safe.decide": true, "tls.execute": true, "tls.state": true,
		"handshake": true, "health": true, "unknown": true,
	},
	"phase": {"protocol": true, "queue": true, "operation": true},
	"error_class": {
		"none": true, "ipc_queue_full": true, "storage_busy": true, "storage_canceled": true,
		"storage_commit_unknown": true, "storage_unavailable": true, "storage_not_found": true,
		"deadline_exceeded": true, "canceled": true, "domain_rejection": true, "transport_timeout": true, "unclassified": true,
	},
	"request_state": {"active": true, "canceled": true, "deadline_exceeded": true},
}

func sensitive(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	for _, part := range []string{"password", "privatekey", "sharedsecret", "credential", "authorization", "cookie", "csrf", "grant", "token", "masterkey", "keymaterial", "plaintext"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return key == "secret" || key == "key"
}
func Text(value string) string {
	for _, part := range []string{"PRIVATE KEY-----", "ovsg_", "ovst_", "ovss_", "ovsc_", "$argon2id$"} {
		if strings.Contains(value, part) {
			return Marker
		}
	}
	return value
}
func Document(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range v {
			if sensitive(key) {
				out[key] = Marker
			} else {
				out[key] = Document(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = Document(child)
		}
		return out
	case string:
		return Text(v)
	default:
		return v
	}
}

type Handler struct{ next slog.Handler }

func New(next slog.Handler) slog.Handler { return Handler{next} }
func (h Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func safe(a slog.Attr) slog.Attr {
	if sensitive(a.Key) {
		return slog.String(a.Key, Marker)
	}
	if allowed, diagnostic := diagnosticValues[a.Key]; diagnostic {
		v := a.Value.Resolve()
		if v.Kind() == slog.KindString && allowed[v.String()] {
			return slog.String(a.Key, v.String())
		}
		return slog.String(a.Key, Marker)
	}
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		for i := range attrs {
			attrs[i] = safe(attrs[i])
		}
		a.Value = slog.GroupValue(attrs...)
		return a
	}
	// Only stable operational attributes enter daemon logs. Arbitrary message,
	// error, payload, URL and user-supplied labels cannot become a secret channel.
	switch a.Key {
	case "service", "code", "scope":
		v := a.Value.Resolve()
		if v.Kind() == slog.KindString && eventCode.MatchString(v.String()) && Text(v.String()) == v.String() {
			return slog.String(a.Key, v.String())
		}
	case "configuration_ready":
		if a.Value.Kind() == slog.KindBool {
			return a
		}
	}
	return slog.String(a.Key, Marker)
}
func (h Handler) Handle(ctx context.Context, record slog.Record) error {
	message := record.Message
	if !messages[message] {
		message = "redacted_event"
	}
	out := slog.NewRecord(record.Time, record.Level, message, record.PC)
	record.Attrs(func(a slog.Attr) bool { out.AddAttrs(safe(a)); return true })
	return h.next.Handle(ctx, out)
}
func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = safe(a)
	}
	return Handler{h.next.WithAttrs(out)}
}
func (h Handler) WithGroup(name string) slog.Handler {
	if !eventCode.MatchString(name) || sensitive(name) {
		name = "redacted"
	}
	return Handler{h.next.WithGroup(name)}
}
