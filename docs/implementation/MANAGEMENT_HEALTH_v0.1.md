# Management health and capabilities

`/operations/health` presents the current observations of `ovs-webd` and
`ovs-mgrd`. Each row records its source and sample time. The manager reports its
database and SecretStore state, published OVSDB and Linux cache evidence, IPC
queue pressure and completed watchdog cycles. A component can report stale or
unknown independently of the overall service. Web observations have their own
timestamp. `/healthz`, `/readyz` and `/api/v1/runtime` retain their bootstrap
semantics.

Detailed reads require a current `state.read` grant. The manager authenticates
the grant on its existing Unix inspection operation. A disconnected manager
leaves bootstrap readiness available as the coarse failure indicator. Job,
Event, Audit and transaction links use the existing permissions and routes.

`/admin/capabilities` also requires `capabilities.read`. It uses fixed identities
and shows readiness, prerequisites, authority, caller grant and recovery
separately. Readiness of a facility is descriptive. Object-level permission,
validation and recovery checks continue to run on the actual operations.

The manager observes changes in its published component states every two
seconds, independently of browser reads. Events group changes in eight-second
windows and carry the starting state, ending state and transition count.
Repeated unchanged samples and initial observations yield an empty transition
window. Event writes use a short budget and the regular evidence capacity.

## Managed controls

An elevated browser session with `management.debug.write` can set a five-,
ten- or fifteen-minute manager Debug lease or end it. The runtime lease is
process-scoped and expires on the Linux boot-time clock. The normal journald
handler applies the same redaction to every level. The durable receipt and
audit record capture acceptance, and a replay does not start the lease again.

The root-owned runtime environment can explicitly set
`MANAGEMENT_SERVICE_RESTART=true` for an installation that qualifies the
unmodified product units. The elevated `management.services.control` operation
accepts either the OVS WebUI manager or web service. The manager authenticates
the system bus and qualifies the canonical service and effective dependency
graph before a fixed `TryRestartUnit` request. [systemd service manager interface](https://www.freedesktop.org/software/systemd/man/latest/org.freedesktop.systemd1.html)

Restart admission and Safe Apply/TLS trial admission share the manager
database writer. The restart fence is persisted with its original Job and
receipt. After a transport gap the original intent is never replayed. For a
known queue acknowledgement and a now idle qualified target, terminal
evidence records whether a different invocation was observed. The business
outcome remains unknown without a correlated terminal supervisor result.
An unacknowledged dispatch keeps the exclusion across restarts of either
product service. After a planned host reboot, the manager detects the changed
kernel boot ID, releases the previous-boot exclusion, and keeps the original
Job and Audit outcome unknown.
