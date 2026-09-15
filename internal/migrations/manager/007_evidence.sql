CREATE TABLE evidence_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), revision INTEGER NOT NULL,
 retention_epoch TEXT NOT NULL, high_watermark_ms INTEGER NOT NULL,
 event_pruned_through INTEGER NOT NULL DEFAULT 0, audit_pruned_through INTEGER NOT NULL DEFAULT 0, job_pruned_through INTEGER NOT NULL DEFAULT 0
) STRICT;
INSERT INTO evidence_state SELECT 1,0,epoch,0,0,0,0 FROM api_authority;
CREATE TABLE evidence_jobs (
 id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL, capability TEXT NOT NULL, operation TEXT NOT NULL,
 version INTEGER NOT NULL, created_at_ms INTEGER NOT NULL, updated_at_ms INTEGER NOT NULL,
 completed_at_ms INTEGER, handler TEXT NOT NULL,
 document BLOB NOT NULL CHECK(length(document)<=8192)
) STRICT;
CREATE INDEX evidence_jobs_owner ON evidence_jobs(owner_id,id);
CREATE INDEX evidence_jobs_retention ON evidence_jobs(completed_at_ms);
CREATE TABLE evidence_records (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 collection TEXT NOT NULL CHECK(collection IN ('event','audit')),
 created_at_ms INTEGER NOT NULL, job_id TEXT, transaction_id TEXT, request_id TEXT,
 correlation_id TEXT NOT NULL, operation TEXT NOT NULL, object_id TEXT,
 source TEXT NOT NULL CHECK(source IN ('Manager','External','Unknown')),
 dedup_key TEXT, fingerprint TEXT NOT NULL,
 document BLOB NOT NULL CHECK(length(document)<=8192),
 UNIQUE(collection,source,dedup_key)
) STRICT;
CREATE INDEX evidence_records_page ON evidence_records(collection,sequence);
CREATE INDEX evidence_records_time ON evidence_records(collection,created_at_ms);
CREATE INDEX evidence_records_job ON evidence_records(job_id);
CREATE INDEX evidence_records_request ON evidence_records(request_id);
CREATE INDEX evidence_records_correlation ON evidence_records(correlation_id,collection,sequence);
CREATE INDEX evidence_job_state ON jobs(state,id);
CREATE INDEX evidence_job_correlation ON evidence_jobs(json_extract(document,'$.correlation_id'));
CREATE TABLE evidence_inventory_snapshot (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), digest TEXT NOT NULL
) STRICT;
-- Existing legacy rows are imported by the typed repository before the public
-- reader is enabled. Their original IDs and known references are retained;
-- missing actor/provider evidence is explicitly Unknown.
CREATE TABLE evidence_imports (source TEXT PRIMARY KEY) STRICT;
