# STP/RSTP optional OVS 3.3.9 witness prototype review

This P1 review covers an isolated, default-off upstream source extension and a private observation codec. The [prototype contract](../implementation/SPANNING_TREE_WITNESS_PROTOTYPE_v0.1.md) defines the fixed input registry, source identity, budgets and qualification requirements. Accepted evidence must be attached to the exact tested source and independently verified merged main, with official artifact digests and both native architectures.

- Default builds expose no extension command; opt-in builds add only the UUID/nonce read command on the existing control socket.
- Configuration bindings are captured after the relevant pass and provider handling, then rechecked. Name/MAC/current cfg pointer/cfg counters alone do not establish installed identity.
- Runtime and daemon incarnations accompany monotonic, saturating authority/binding/configuration epochs. Epoch exhaustion is terminal within the process.
- Engine pointer lifetime is main-thread scoped; local parameters are copied under each library mutex without nested protocol locks. Native units, setter order and error flow remain unchanged.
- Every unsupported, unobserved, failed or mismatching prerequisite withholds the whole sample. A successful known sample has no reservation, seal, Applied flag or write authority.
- C and Go canonicalize the same pinned 52-field input registry and actual interface associations; omitted management/output fields are explicit, and unmodelled table references are withheld.
- Actual kernel namespaces and real lock/restart/UUID-reuse cases are required alongside strict codec, C boundary/stress and ASan/UBSan tests. Unimplemented production P2/P3/P4 cases are not counted as prototype passes.
- The current authorization/source/fenced-preflight implementation and all existing CI gates remain independent and unchanged. No daemon imports the new package; prepare is still unconditionally blocked.
- The P1 fixture's root-owned file/actual executable FD checks do not claim a production cached build trust scheme. Native exit leak checking, C ThreadSanitizer, signed deployment and other OVS runtime versions are not accepted by this slice.

Only the complete per-commit reports establish test success; this document is not a substitute for those reports. #23 / #43 remain open and #71 remains deferred. The extension reports processed IDL state, not instantaneous server lock authority or an atomic prerequisite for an external write transaction.
