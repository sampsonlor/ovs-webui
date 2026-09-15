-- A reservation has no timeout: lost admission acknowledgement or webd restart
-- cannot unfreeze intent that mgrd may already have dispatched. Release needs a
-- reconciled manager result through the future Safe Apply coordinator.
CREATE TABLE candidate_execution_reservations (
 owner_id TEXT PRIMARY KEY REFERENCES candidate_workspaces(owner_id),
 request_id TEXT NOT NULL, validation_id TEXT NOT NULL,
 envelope_hash TEXT NOT NULL, created_at_ms INTEGER NOT NULL
) STRICT;
