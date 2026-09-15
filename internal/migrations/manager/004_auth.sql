CREATE TABLE auth_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), epoch TEXT NOT NULL,
 revision TEXT NOT NULL, high_watermark_ms INTEGER NOT NULL
) STRICT;
INSERT INTO auth_state SELECT singleton,epoch,epoch,0 FROM api_authority;
CREATE TABLE auth_principals (
 principal_id TEXT PRIMARY KEY REFERENCES principals(id), revision TEXT NOT NULL,
 created_at INTEGER NOT NULL
) STRICT;
CREATE TABLE auth_roles (
 role_id TEXT PRIMARY KEY REFERENCES roles(id), name TEXT NOT NULL UNIQUE,
 revision TEXT NOT NULL
) STRICT;
CREATE TABLE auth_grants (
 grant_hash BLOB PRIMARY KEY REFERENCES grants(grant_hash), id TEXT NOT NULL UNIQUE,
 issued_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL,
 elevated_until INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE TABLE auth_tokens (
 token_hash BLOB PRIMARY KEY REFERENCES api_tokens(token_hash), id TEXT NOT NULL UNIQUE,
 name TEXT NOT NULL, created_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL
) STRICT;
CREATE TABLE auth_audit (
 id TEXT PRIMARY KEY, principal_id TEXT, credential_id TEXT,
 operation TEXT NOT NULL, target_id TEXT, result TEXT NOT NULL,
 request_id TEXT, created_at INTEGER NOT NULL
) STRICT;
CREATE TABLE auth_attempts (
 username_hash BLOB PRIMARY KEY CHECK(length(username_hash)=32),
 window_start INTEGER NOT NULL, attempts INTEGER NOT NULL
) STRICT;
CREATE INDEX auth_audit_time ON auth_audit(created_at,id);
