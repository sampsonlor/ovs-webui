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

This delivery covers the read-only Health and Capabilities slice and background
health transitions under #45. Audited temporary Debug and supervised product
service controls have their own admission and acceptance work under that issue.
