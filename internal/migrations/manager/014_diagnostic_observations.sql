CREATE TABLE diagnostic_observations (
  job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
  expires_ms INTEGER NOT NULL,
  document BLOB NOT NULL CHECK(length(document) BETWEEN 1 AND 32768)
) STRICT;
CREATE INDEX diagnostic_observation_expiry ON diagnostic_observations(expires_ms, job_id);
