-- Customer receipts become safe to retry and safe to correct.
--
-- client_ref: an opaque id chosen by the caller (the mobile app sends one per payment it records). A second POST with the same
--   client_ref returns the receipt that already exists instead of booking the money twice, so a dropped connection can be retried.
-- reversed_*: a receipt entered by mistake is never deleted or edited. Reversing it posts an equal and opposite journal (the
--   original journal stays immutable) and stamps who, when and why on the receipt row, so the history stays complete.
ALTER TABLE receipts
    ADD COLUMN client_ref          text,
    ADD COLUMN reversed_at         timestamptz,
    ADD COLUMN reversed_by         uuid REFERENCES users(id),
    ADD COLUMN reversal_reason     text,
    ADD COLUMN reversal_journal_id uuid REFERENCES journals(id),
    ADD CONSTRAINT receipts_reversal_consistent CHECK (
        (reversed_at IS NULL AND reversed_by IS NULL AND reversal_journal_id IS NULL)
        OR (reversed_at IS NOT NULL AND reversed_by IS NOT NULL AND reversal_journal_id IS NOT NULL)
    );

CREATE UNIQUE INDEX uq_receipts_client_ref ON receipts (organisation_id, client_ref) WHERE client_ref IS NOT NULL;
