-- Formal per-principal workspace; legacy bootstrap candidates stay untouched.
CREATE TABLE candidate_workspaces (
 owner_id TEXT PRIMARY KEY, id TEXT NOT NULL UNIQUE, revision TEXT NOT NULL,
 sequence INTEGER NOT NULL CHECK(sequence>=0),
 envelope BLOB NOT NULL CHECK(length(envelope)<=40960)
) STRICT;
CREATE TABLE candidate_validation_outbox (
 owner_id TEXT NOT NULL, request_epoch TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, envelope BLOB NOT NULL CHECK(length(envelope)<=40960),
 command BLOB NOT NULL CHECK(length(command)<=4096),
 receipt BLOB, created_at_ms INTEGER NOT NULL,
 PRIMARY KEY(owner_id,request_epoch,request_id)
) STRICT;
