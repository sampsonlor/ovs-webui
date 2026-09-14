CREATE TABLE principals (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, disabled INTEGER NOT NULL CHECK(disabled IN (0,1)),
 password_verifier BLOB NOT NULL CHECK(length(password_verifier)<=4096)
) STRICT;
CREATE TABLE roles (id TEXT PRIMARY KEY, capabilities BLOB NOT NULL) STRICT;
CREATE TABLE principal_roles (
 principal_id TEXT NOT NULL REFERENCES principals(id), role_id TEXT NOT NULL REFERENCES roles(id),
 PRIMARY KEY(principal_id,role_id)
) STRICT;
CREATE TABLE grants (
 grant_hash BLOB PRIMARY KEY CHECK(length(grant_hash)=32), principal_id TEXT NOT NULL REFERENCES principals(id),
 epoch TEXT NOT NULL, ceiling BLOB NOT NULL, expires_at INTEGER NOT NULL, revoked_at INTEGER
) STRICT;
CREATE TABLE api_tokens (
 token_hash BLOB PRIMARY KEY CHECK(length(token_hash)=32), principal_id TEXT NOT NULL REFERENCES principals(id),
 epoch TEXT NOT NULL, scopes BLOB NOT NULL, expires_at INTEGER NOT NULL, revoked_at INTEGER
) STRICT;
CREATE TABLE policy_revisions (id TEXT PRIMARY KEY, document BLOB NOT NULL) STRICT;
CREATE TABLE generations (id TEXT PRIMARY KEY, evidence BLOB NOT NULL, created_at INTEGER NOT NULL) STRICT;
CREATE TABLE identities (
 management_id TEXT PRIMARY KEY, generation TEXT NOT NULL REFERENCES generations(id),
 table_name TEXT NOT NULL, ovs_uuid TEXT NOT NULL, state TEXT NOT NULL,
 UNIQUE(generation,table_name,ovs_uuid)
) STRICT;
