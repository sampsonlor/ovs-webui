# Interface statistics and short RAM trends

The Statistics page reads cumulative bytes, packets, errors and drops for each
native Interface. Bridge, Port and Interface management identities scope the
selection. A Port selection shows its member Interfaces. Each counter retains
its native decimal integer. Missing counters remain unknown.

One manager-owned collector reads OVSDB through a separate read-only connection.
It waits five seconds after a collection finishes before starting the next one.
Each read checks the schema, root identity and requested Interface UUIDs.
HTTP requests share the resulting samples. Statistics never enter the
configuration monitor, identity reconciliation or durable evidence.

The collection timestamp describes the manager's OVSDB observation. OVS updates
the database periodically and reports no underlying datapath capture timestamp.
Rates show changes in the reported values divided by the observation interval.
The page displays this source boundary alongside freshness and coverage.

The manager retains at most 60 fixed-size points per Interface for five minutes,
with at most 2048 native objects. Retained point storage is below 32 MiB. Native
reads have a two-second and 4 MiB budget. Interface statistics maps exceeding the
row budget yield unknown counters for that Interface. List replies retain the
published 100/500 requested pagination limits and return at most 50 entries or
40 KiB of items, with a continuation cursor. Detail history is bounded.

Counter decreases mark a reset. Missing fields, collection gaps, reconnects,
native type/device changes, membership changes and identity changes interrupt
rates. A fresh sequence establishes a new baseline. On failure, retained
counter observations are stale and current rates are unavailable. Restarting
the manager rebuilds the RAM history. The observer never resets native counters.

The list and detail require both inventory.read and state.read. Desktop,
tablet and mobile share observation permissions. Expert mode adds native
identity and source detail. The interface exposes no configuration commands.

Statistics detail uses the existing `ovs.v1` WebSocket. It subscribes to the
selected Interface's statistic identity. A single worker in `ovs-webd` batches
the distinct subscribed identities and checks for changed versions once per
second. The negotiated Unix operation has one background slot and returns only
the opaque REST resource versions of matching active Interfaces. It reads the
published cache, sharing the same collection results across all clients.

Each public push passes the existing resource and authorization checks. Counter
hints retain the one-second coalescing and bounded subscriber queue. The detail
page responds to a hint or resync by reading REST, and resumes its ordinary
refresh timer when the socket is unavailable. Statistics lists continue to use
their cursor and scheduled REST refresh.
