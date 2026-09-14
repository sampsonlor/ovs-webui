CREATE TABLE browser_sessions (
 session_hash BLOB PRIMARY KEY CHECK(length(session_hash)=32),
 envelope BLOB NOT NULL CHECK(length(envelope)<=4096),
 expires_at INTEGER NOT NULL
) STRICT;
