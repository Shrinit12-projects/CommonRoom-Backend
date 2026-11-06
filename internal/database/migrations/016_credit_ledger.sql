CREATE TABLE credits_ledger (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id),
 event_type text NOT NULL,
 delta integer NOT NULL CHECK (delta <> 0),
 source_type text NOT NULL,
 source_id uuid,
 idempotency_key text NOT NULL UNIQUE,
 rule_version integer NOT NULL,
 reversal_of bigint REFERENCES credits_ledger(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(reversal_of)
);
CREATE INDEX credits_ledger_user_date ON credits_ledger(user_id,created_at DESC);
