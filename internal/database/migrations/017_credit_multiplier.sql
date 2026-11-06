ALTER TABLE credits_ledger ALTER COLUMN delta TYPE numeric(14,2) USING delta::numeric(14,2);
ALTER TABLE redemption_requests ALTER COLUMN points_cost TYPE numeric(14,2) USING points_cost::numeric(14,2);

-- Preserve existing points while granting the matching credit balance at 1.5x.
-- Entries already reversed or already represented in the credit ledger are skipped.
INSERT INTO credits_ledger(user_id,event_type,delta,source_type,source_id,idempotency_key,rule_version,created_at)
SELECT p.user_id,p.event_type,round(p.delta::numeric*1.5,2),p.source_type,p.source_id,
       'credit-multiplier-1.5:ledger:'||p.id::text,p.rule_version,p.created_at
FROM points_ledger p
WHERE p.delta>0
  AND NOT EXISTS(SELECT 1 FROM points_ledger reversal WHERE reversal.reversal_of=p.id)
  AND NOT EXISTS(
    SELECT 1 FROM credits_ledger c
    WHERE c.user_id=p.user_id AND c.event_type=p.event_type AND c.source_type=p.source_type
      AND c.source_id IS NOT DISTINCT FROM p.source_id AND c.delta>0
  )
ON CONFLICT(idempotency_key) DO NOTHING;
