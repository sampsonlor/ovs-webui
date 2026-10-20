# STP/RSTP M2c1 review record

Scope: private fixed-purpose runtime reads, local/operational field mapping and same-protocol, bidirectional basic parameter analysis. There is no production STP executor, new execution authority, public write endpoint or Applied proof in this batch.

Review decisions:

- Read only `stp/show` / `rstp/show` with one validated name; bound the entire request lifetime and wire/text/line/JSON budgets. Reject errors/duplicates/unknown fields and wrong IDs/peers; return no partial/default sample. Daemon boot/PID/start evidence is explicit but does not qualify the manager's OVSDB/Bridge binding.
- STP's two text sections combine identity and timer sources differently. Select local identity from Bridge ID and local timers from Root ID; retain operational timers separately. Synthetic unequal sections and a real non-root two-Bridge fixture must agree. RSTP local identity/timers come from Bridge ID.
- Validate both raw before/after images including inactive parameters and exact installed originals. For RSTP require both native forward and reverse cross-timer inequalities. Reject unsafe single-step transitions; never infer retries, disable/re-enable or intermediate recovery. Compatibility is not authorization.
- The initial source's amd64 formal browser failed the retained dark screenshot expansion assertion. Preserve that failure separately. Real component reproduction confirms unbound disclosure state is lost on eligibility/desktop branch reconstruction. Explicit state binding and stronger real native eligibility withdrawal/restoration and responsive assertions repair that concrete defect without claiming the screenshot alone proves its only trigger.
- Preserve the M2b unconditional execution blocker, all old native/browser/auth/recovery checks, and #71's deferred investigation. Do not close #23/#43 or claim Basic Manage complete.

Acceptance requires an independently complete passing final-source CI and merged-main CI on both architectures, official artifact digests, the original 286 Go test names plus seven new guards, the original 42 formal browser names, 13 new native cases on each of three schema fixtures, and retained source/main screenshots. A failed/cancelled/intermediate attempt cannot be combined with another run to manufacture full acceptance. Final run identities and digests are recorded in the delivery PR and annotated acceptance tag after verification.
