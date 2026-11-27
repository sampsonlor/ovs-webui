CREATE TABLE runtime_service_restart (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  job_id TEXT NOT NULL REFERENCES jobs(id),
  target TEXT NOT NULL CHECK(target IN ('manager','web')),
  owner_instance TEXT NOT NULL,
  boot_id TEXT NOT NULL CHECK(length(boot_id) BETWEEN 1 AND 64),
  phase TEXT NOT NULL CHECK(phase IN ('prepared','dispatching','submitted')),
  invocation_id TEXT NOT NULL DEFAULT '',
  systemd_job TEXT NOT NULL DEFAULT '',
  updated_ms INTEGER NOT NULL
) STRICT;
