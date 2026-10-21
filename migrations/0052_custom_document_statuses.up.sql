-- Custom statuses: labels a business defines to follow its own workflow ("Waiting for client", "Ready to ship").
-- They are labels only: they never change a document's accounting state (DRAFT, FINALIZED, CANCELLED).
CREATE TABLE document_statuses (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    name             text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 40),
    color            text NOT NULL DEFAULT 'neutral' CHECK (color IN ('neutral', 'blue', 'green', 'amber', 'red', 'purple')),
    sort_order       integer NOT NULL DEFAULT 0,
    active           boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organisation_id, name)
);
CREATE INDEX idx_document_statuses_org ON document_statuses(organisation_id);
ALTER TABLE document_statuses ENABLE ROW LEVEL SECURITY;
CREATE POLICY document_statuses_tenant_isolation ON document_statuses
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE sales_document_statuses (
    sales_document_id  uuid PRIMARY KEY REFERENCES sales_documents(id) ON DELETE CASCADE,
    organisation_id    uuid NOT NULL REFERENCES organisations(id),
    status_id          uuid NOT NULL REFERENCES document_statuses(id),
    set_by             uuid NOT NULL REFERENCES users(id),
    set_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sales_document_statuses_status ON sales_document_statuses(status_id);
ALTER TABLE sales_document_statuses ENABLE ROW LEVEL SECURITY;
CREATE POLICY sales_document_statuses_tenant_isolation ON sales_document_statuses
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
