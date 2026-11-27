CREATE TABLE capture_artifacts (
  id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
  interface_id TEXT NOT NULL,
  expires_ms INTEGER NOT NULL,
  digest TEXT NOT NULL CHECK(length(digest)=64),
  manifest BLOB NOT NULL CHECK(length(manifest) BETWEEN 1 AND 4096),
  content BLOB NOT NULL CHECK(length(content) BETWEEN 24 AND 1048576)
) STRICT;
CREATE INDEX capture_artifact_expiry ON capture_artifacts(expires_ms,id);
