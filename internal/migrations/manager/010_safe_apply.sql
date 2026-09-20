CREATE TABLE safe_applies (
 id TEXT PRIMARY KEY REFERENCES field_executions(id),
 owner_id TEXT NOT NULL, request_epoch TEXT NOT NULL, request_id TEXT NOT NULL,
 state TEXT NOT NULL, domain TEXT NOT NULL,
 document BLOB NOT NULL CHECK(length(document)<=640000),
 UNIQUE(owner_id,request_epoch,request_id)
) STRICT;
CREATE UNIQUE INDEX safe_apply_domain ON safe_applies(domain) WHERE state NOT IN ('confirmed','rolled-back','not-committed');
CREATE TABLE safe_resolutions (
 owner_id TEXT NOT NULL, request_epoch TEXT NOT NULL, request_id TEXT NOT NULL,
 fingerprint TEXT NOT NULL, document BLOB NOT NULL CHECK(length(document)<=50000),
 PRIMARY KEY(owner_id,request_epoch,request_id)
) STRICT;
CREATE TABLE last_known_good (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 transaction_id TEXT NOT NULL REFERENCES safe_applies(id),
 generation TEXT NOT NULL, confirmed_at_ms INTEGER NOT NULL,
 document BLOB NOT NULL CHECK(length(document)<=320000)
) STRICT;
