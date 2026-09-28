-- Creation reservations are private, bounded by the immutable identity registry.
-- Only a matching atomic native commit marker can activate a reserved identity.
CREATE TABLE identity_creations (
 management_id TEXT PRIMARY KEY REFERENCES identities(management_id),
 transaction_id TEXT NOT NULL REFERENCES transaction_journal(transaction_id),
 marker TEXT NOT NULL CHECK(length(marker)=64)
) STRICT;
CREATE INDEX identity_creations_transaction ON identity_creations(transaction_id);
