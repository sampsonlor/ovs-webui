CREATE TABLE safe_apply_outbox (
 owner_id TEXT NOT NULL REFERENCES candidate_workspaces(owner_id),
 request_epoch TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, reservation_id TEXT NOT NULL UNIQUE,
 document BLOB NOT NULL CHECK(length(document)<=50000),
 receipt BLOB, resolved INTEGER NOT NULL DEFAULT 0 CHECK(resolved IN (0,1)),
 created_at_ms INTEGER NOT NULL,
 PRIMARY KEY(owner_id,request_epoch,request_id)
) STRICT;
CREATE UNIQUE INDEX safe_apply_owner ON safe_apply_outbox(owner_id) WHERE resolved=0;
