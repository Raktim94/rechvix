-- History register for invoices brought over from another billing app. These are reference records only: they keep the old
-- numbers, dates and amounts searchable, but never touch stock, tax returns or the ledger (the books start from the opening
-- balances imported alongside them), so importing a year of old invoices cannot double-count anything.
CREATE TABLE historical_invoices (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    kind             text NOT NULL CHECK (kind IN ('SALE', 'PURCHASE')),
    party_id         uuid NOT NULL REFERENCES parties(id),
    document_number  text NOT NULL CHECK (length(document_number) BETWEEN 1 AND 60),
    document_date    date NOT NULL,
    total_amount     numeric(20,6) NOT NULL CHECK (total_amount >= 0),
    paid_amount      numeric(20,6) NOT NULL DEFAULT 0 CHECK (paid_amount >= 0),
    currency_code    varchar(3) NOT NULL,
    notes            text,
    batch_id         uuid NOT NULL,
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organisation_id, kind, document_number)
);
CREATE INDEX idx_historical_invoices_party ON historical_invoices(organisation_id, party_id, document_date DESC);
CREATE INDEX idx_historical_invoices_date ON historical_invoices(organisation_id, kind, document_date DESC);
ALTER TABLE historical_invoices ENABLE ROW LEVEL SECURITY;
CREATE POLICY historical_invoices_tenant_isolation ON historical_invoices
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
