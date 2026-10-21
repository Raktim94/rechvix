-- Read receipts for shared documents: the public share link records when and how often it is opened.
ALTER TABLE share_links ADD COLUMN first_viewed_at timestamptz;
ALTER TABLE share_links ADD COLUMN last_viewed_at timestamptz;
ALTER TABLE share_links ADD COLUMN view_count integer NOT NULL DEFAULT 0;

-- Overdue-invoice reminders, per organisation. created_by is the user the generated share links belong to.
CREATE TABLE reminder_settings (
    organisation_id    uuid PRIMARY KEY REFERENCES organisations(id),
    enabled            boolean NOT NULL DEFAULT false,
    days_after_due     integer NOT NULL DEFAULT 3 CHECK (days_after_due BETWEEN 0 AND 365),
    repeat_every_days  integer NOT NULL DEFAULT 7 CHECK (repeat_every_days BETWEEN 1 AND 365),
    max_reminders      integer NOT NULL DEFAULT 3 CHECK (max_reminders BETWEEN 1 AND 20),
    subject            text NOT NULL DEFAULT 'Payment reminder: invoice {{number}}',
    message            text NOT NULL DEFAULT 'This is a friendly reminder that invoice {{number}} for {{amount}} was due on {{due_date}}. Please arrange payment or contact us if you have already paid.',
    created_by         uuid NOT NULL REFERENCES users(id),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE reminder_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY reminder_settings_tenant_isolation ON reminder_settings
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE reminder_log (
    id                 uuid PRIMARY KEY,
    organisation_id    uuid NOT NULL REFERENCES organisations(id),
    sales_document_id  uuid NOT NULL REFERENCES sales_documents(id),
    party_id           uuid NOT NULL REFERENCES parties(id),
    sent_to            text NOT NULL,
    reminder_number    integer NOT NULL,
    outstanding        numeric(20,6) NOT NULL,
    queued_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_reminder_log_document ON reminder_log(sales_document_id, queued_at DESC);
CREATE INDEX idx_reminder_log_org ON reminder_log(organisation_id, queued_at DESC);
ALTER TABLE reminder_log ENABLE ROW LEVEL SECURITY;
CREATE POLICY reminder_log_tenant_isolation ON reminder_log
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- The background loop has no organisation yet, so this one narrow SECURITY DEFINER function lists the
-- organisations that switched reminders on (same pattern as outbox_claim_next).
CREATE FUNCTION reminder_organisations() RETURNS TABLE (organisation_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
    SELECT organisation_id FROM reminder_settings WHERE enabled
$$;
-- EXECUTE is left at the default for the same reason as outbox_claim_next (migration 0023): migrations must not assume a runtime role name.
