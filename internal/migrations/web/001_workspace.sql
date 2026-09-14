CREATE TABLE candidates (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, revision TEXT NOT NULL,
 document BLOB NOT NULL CHECK(length(document) <= 1048576)
) STRICT;
CREATE TABLE metadata (
 kind TEXT NOT NULL CHECK(kind IN ('preference','label','profile','override')),
 owner_id TEXT NOT NULL, id TEXT NOT NULL, document BLOB NOT NULL CHECK(length(document)<=1048576),
 PRIMARY KEY(kind,owner_id,id)
) STRICT;
CREATE TABLE session_mappings (
 session_hash BLOB PRIMARY KEY CHECK(length(session_hash)=32), principal_id TEXT NOT NULL,
 grant_secret_ref TEXT NOT NULL, expires_at INTEGER NOT NULL
) STRICT;
