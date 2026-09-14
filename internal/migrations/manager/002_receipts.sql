CREATE TABLE request_receipts (
 id TEXT PRIMARY KEY, request_id TEXT NOT NULL UNIQUE, transaction_id TEXT NOT NULL UNIQUE,
 correlation_id TEXT NOT NULL UNIQUE, owner_id TEXT NOT NULL, candidate_id TEXT NOT NULL,
 candidate_revision TEXT NOT NULL, payload_hash TEXT NOT NULL,
 document BLOB NOT NULL CHECK(length(document)<=1048576),
 stage TEXT NOT NULL CHECK(stage IN ('received')), created_at INTEGER NOT NULL
) STRICT;
-- received is durable transport evidence, not authorized transaction admission.
CREATE TABLE transaction_journal (
 transaction_id TEXT PRIMARY KEY REFERENCES request_receipts(transaction_id),
 sequence INTEGER NOT NULL CHECK(sequence>0), state TEXT NOT NULL, document BLOB NOT NULL
) STRICT;
CREATE TABLE validations (id TEXT PRIMARY KEY, document BLOB NOT NULL, expires_at INTEGER NOT NULL) STRICT;
CREATE TABLE operation_protections (
 resource_id TEXT NOT NULL, field_path TEXT NOT NULL,
 transaction_id TEXT NOT NULL REFERENCES transaction_journal(transaction_id),
 PRIMARY KEY(resource_id,field_path)
) STRICT;
CREATE TABLE jobs (
 id TEXT PRIMARY KEY, transaction_id TEXT REFERENCES transaction_journal(transaction_id),
 state TEXT NOT NULL, document BLOB NOT NULL
) STRICT;
CREATE TABLE events (id TEXT PRIMARY KEY, correlation_id TEXT NOT NULL, kind TEXT NOT NULL, created_at INTEGER NOT NULL) STRICT;
CREATE TABLE audit (
 id TEXT PRIMARY KEY, request_id TEXT NOT NULL REFERENCES request_receipts(request_id),
 kind TEXT NOT NULL, created_at INTEGER NOT NULL
) STRICT;
