-- Lets a shop owner attach a receipt/bill photo or PDF to a manual
-- expense (ExpensesPage) -- there was no way to keep supporting
-- documentation with an expense entry at all before this. Stored
-- directly as bytea in Postgres, same as legal_entities.logo_png
-- (migrations/0034) -- no separate object-storage service to stand up
-- for what is, for this app's self-hosted scale, a modest number of
-- small receipt images/PDFs; the file_size_bytes CHECK caps any one
-- attachment at 8MB so this can't be used to smuggle in an
-- unreasonably large blob.
CREATE TABLE expense_attachments (
    id               uuid PRIMARY KEY,
    organisation_id   uuid NOT NULL REFERENCES organisations(id),
    -- A manual expense is a journals row (source_type='manual_expense',
    -- see accounting.Service.Post via ExpensesPage) -- there is no
    -- separate "expense" entity to attach to, so this points at the
    -- journal directly.
    journal_id        uuid NOT NULL REFERENCES journals(id),
    filename          text NOT NULL,
    content_type      text NOT NULL,
    file_data         bytea NOT NULL,
    file_size_bytes   bigint NOT NULL CHECK (file_size_bytes > 0 AND file_size_bytes <= 8388608),
    created_by        uuid NOT NULL REFERENCES users(id),
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_expense_attachments_organisation_id ON expense_attachments(organisation_id);
CREATE INDEX idx_expense_attachments_journal_id ON expense_attachments(journal_id);

ALTER TABLE expense_attachments ENABLE ROW LEVEL SECURITY;
CREATE POLICY expense_attachments_tenant_isolation ON expense_attachments
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
