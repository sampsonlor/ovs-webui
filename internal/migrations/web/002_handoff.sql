-- The immutable copy survives subsequent edits or deletion of the draft.
CREATE TABLE handoff_outbox (
 request_id TEXT PRIMARY KEY, transaction_id TEXT NOT NULL UNIQUE,
 correlation_id TEXT NOT NULL UNIQUE, owner_id TEXT NOT NULL,
 candidate_id TEXT NOT NULL, candidate_revision TEXT NOT NULL,
 payload_hash TEXT NOT NULL, document BLOB NOT NULL CHECK(length(document)<=1048576),
 created_at INTEGER NOT NULL, receipt_id TEXT
) STRICT;
CREATE INDEX handoff_pending ON handoff_outbox(created_at) WHERE receipt_id IS NULL;
CREATE TABLE workspace_receipts (
 request_id TEXT PRIMARY KEY REFERENCES handoff_outbox(request_id),
 receipt_id TEXT NOT NULL UNIQUE, acknowledged_at INTEGER NOT NULL
) STRICT;
