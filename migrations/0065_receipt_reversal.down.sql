DROP INDEX IF EXISTS uq_receipts_client_ref;
ALTER TABLE receipts
    DROP CONSTRAINT IF EXISTS receipts_reversal_consistent,
    DROP COLUMN IF EXISTS reversal_journal_id,
    DROP COLUMN IF EXISTS reversal_reason,
    DROP COLUMN IF EXISTS reversed_by,
    DROP COLUMN IF EXISTS reversed_at,
    DROP COLUMN IF EXISTS client_ref;
