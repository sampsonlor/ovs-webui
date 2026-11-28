CREATE TABLE support_bundles (
  id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
  owner_id TEXT NOT NULL,
  expires_ms INTEGER NOT NULL,
  digest TEXT NOT NULL CHECK(length(digest)=64),
  manifest BLOB NOT NULL CHECK(length(manifest) BETWEEN 1 AND 16384),
  content BLOB NOT NULL CHECK(length(content) BETWEEN 1 AND 524288)
) STRICT;
CREATE INDEX support_bundle_expiry ON support_bundles(expires_ms,id);
