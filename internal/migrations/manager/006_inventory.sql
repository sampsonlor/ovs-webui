CREATE TABLE inventory_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 generation TEXT NOT NULL REFERENCES generations(id),
 state TEXT NOT NULL,
 reason TEXT NOT NULL,
 evidence BLOB NOT NULL CHECK(length(evidence)<=524288),
 pending_digest TEXT NOT NULL DEFAULT '',
 restore_required INTEGER NOT NULL DEFAULT 0 CHECK(restore_required IN (0,1))
) STRICT;
CREATE TABLE inventory_reconciliations (
 id TEXT PRIMARY KEY, generation TEXT NOT NULL, prior_generation TEXT NOT NULL,
 decision TEXT NOT NULL, evidence_digest TEXT NOT NULL, reason TEXT NOT NULL,
 created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX identities_active ON identities(state,generation,table_name);
