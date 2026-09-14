CREATE TABLE api_authority (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), epoch TEXT NOT NULL,
 high_watermark_ms INTEGER NOT NULL
) STRICT;
INSERT INTO api_authority VALUES(1,
 lower(hex(randomblob(4)))||'-'||lower(hex(randomblob(2)))||'-4'||substr(lower(hex(randomblob(2))),2)||'-'||substr('89ab',abs(random()%4)+1,1)||substr(lower(hex(randomblob(2))),2)||'-'||lower(hex(randomblob(6))),0);
CREATE TABLE api_receipts (
 principal_id TEXT NOT NULL, epoch TEXT NOT NULL, domain TEXT NOT NULL CHECK(domain='workspace'),
 request_id TEXT NOT NULL, fingerprint TEXT NOT NULL, initial_status INTEGER NOT NULL,
 status INTEGER NOT NULL, response BLOB NOT NULL CHECK(length(response)<=65536),
 receipt BLOB NOT NULL CHECK(length(receipt)<=4096), created_at_ms INTEGER NOT NULL,
 completed_at_ms INTEGER, operation TEXT NOT NULL,
 PRIMARY KEY(principal_id,epoch,domain,request_id)
) STRICT;
CREATE INDEX api_receipt_retention ON api_receipts(completed_at_ms) WHERE completed_at_ms IS NOT NULL;
