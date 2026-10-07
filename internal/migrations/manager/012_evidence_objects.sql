-- Exact immutable object associations. Existing records retain their known
-- primary object; no historical transaction target is inferred from names.
CREATE TABLE evidence_record_objects (
 record_id TEXT NOT NULL REFERENCES evidence_records(id) ON DELETE CASCADE,
 object_id TEXT NOT NULL,
 PRIMARY KEY(record_id,object_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX evidence_objects_page ON evidence_record_objects(object_id,record_id);
INSERT INTO evidence_record_objects(record_id,object_id)
 SELECT id,object_id FROM evidence_records WHERE object_id IS NOT NULL;
