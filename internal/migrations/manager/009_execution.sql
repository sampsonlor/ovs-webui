-- The original journal anchors receive evidence; field_executions adds typed
-- admission and a private, bounded native plan. No executable request payload
-- is exposed by public transaction/job readers.
CREATE TABLE field_executions (
 id TEXT PRIMARY KEY REFERENCES transaction_journal(transaction_id),
 job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id),
 owner_id TEXT NOT NULL REFERENCES principals(id),
 validation_id TEXT NOT NULL REFERENCES candidate_validations(id),
 state TEXT NOT NULL, updated_at_ms INTEGER NOT NULL,
 document BLOB NOT NULL CHECK(length(document)<=327680)
) STRICT;
CREATE INDEX field_execution_recovery ON field_executions(state,updated_at_ms,id);
CREATE INDEX field_execution_owner ON field_executions(owner_id,id);
