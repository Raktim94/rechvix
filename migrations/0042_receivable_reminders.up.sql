-- Tracks WhatsApp payment-reminder sends against the receivables report
-- (brief follow-up: "Receivables (who owes you)" needs to show when the
-- first reminder was sent for a customer). Records only that the user
-- clicked "send reminder" and a wa.me link was opened — same
-- click-to-chat honesty level as every other WhatsApp integration in
-- this codebase (internal/modules/notifications), since there is no way
-- to confirm actual delivery without the WhatsApp Business API.
CREATE TABLE receivable_reminders (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    party_id         uuid NOT NULL REFERENCES parties(id),
    first_sent_at    timestamptz NOT NULL,
    last_sent_at     timestamptz NOT NULL,
    sent_count       integer NOT NULL DEFAULT 1,
    UNIQUE (organisation_id, party_id)
);

CREATE INDEX idx_receivable_reminders_organisation_id ON receivable_reminders(organisation_id);

ALTER TABLE receivable_reminders ENABLE ROW LEVEL SECURITY;
CREATE POLICY receivable_reminders_tenant_isolation ON receivable_reminders
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
