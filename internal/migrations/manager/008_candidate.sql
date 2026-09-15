-- A monotonic witness, not a second mutable workspace or a cross-DB commit.
CREATE TABLE candidate_witnesses (
 owner_id TEXT PRIMARY KEY REFERENCES principals(id), candidate_id TEXT NOT NULL UNIQUE,
 workspace_epoch TEXT NOT NULL, sequence INTEGER NOT NULL CHECK(sequence>=0),
 revision TEXT NOT NULL, envelope_hash TEXT NOT NULL
) STRICT;
CREATE TABLE candidate_validations (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES principals(id),
 candidate_id TEXT NOT NULL, candidate_revision TEXT NOT NULL,
 job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id), changeset_id TEXT NOT NULL UNIQUE,
 created_at_ms INTEGER NOT NULL,
 envelope BLOB NOT NULL CHECK(length(envelope)<=40960),
 document BLOB NOT NULL CHECK(length(document)<=49152)
) STRICT;
CREATE INDEX candidate_validations_owner ON candidate_validations(owner_id,candidate_id,created_at_ms DESC);
