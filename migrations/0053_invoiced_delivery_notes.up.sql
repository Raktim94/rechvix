-- Billing delivery notes: an invoice created from one or more delivery challans remembers which ones. A challan has
-- already taken its goods out of stock, so the invoice built from it must not take them out again, and a challan can be
-- billed only once (unless the invoice is cancelled, which releases it).
CREATE TABLE sales_document_sources (
    target_document_id  uuid NOT NULL REFERENCES sales_documents(id),
    source_document_id  uuid NOT NULL REFERENCES sales_documents(id),
    organisation_id     uuid NOT NULL REFERENCES organisations(id),
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (target_document_id, source_document_id),
    UNIQUE (source_document_id)
);
CREATE INDEX idx_sales_document_sources_org ON sales_document_sources(organisation_id);
ALTER TABLE sales_document_sources ENABLE ROW LEVEL SECURITY;
CREATE POLICY sales_document_sources_tenant_isolation ON sales_document_sources
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
