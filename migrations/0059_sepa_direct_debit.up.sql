-- SEPA Core direct debits: customer mandates (debtor IBAN encrypted at rest), collection batches and the individual
-- collections inside them. The app produces the pain.008 file; the company sends it to its bank.
CREATE TABLE sepa_settings (
    legal_entity_id  uuid PRIMARY KEY REFERENCES legal_entities(id),
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    creditor_name    text NOT NULL,
    creditor_id      text NOT NULL,
    iban             text NOT NULL,
    bic              text,
    updated_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE sepa_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY sepa_settings_tenant_isolation ON sepa_settings
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE sepa_mandates (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    party_id         uuid NOT NULL REFERENCES parties(id),
    mandate_ref      text NOT NULL CHECK (length(mandate_ref) BETWEEN 1 AND 35),
    signed_on        date NOT NULL,
    iban_encrypted   bytea NOT NULL,
    iban_last4       text NOT NULL,
    bic              text,
    account_holder   text NOT NULL,
    status           text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'REVOKED')),
    first_collected  boolean NOT NULL DEFAULT false,
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organisation_id, mandate_ref)
);
CREATE INDEX idx_sepa_mandates_party ON sepa_mandates(organisation_id, party_id);
ALTER TABLE sepa_mandates ENABLE ROW LEVEL SECURITY;
CREATE POLICY sepa_mandates_tenant_isolation ON sepa_mandates
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE sepa_batches (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    legal_entity_id  uuid NOT NULL REFERENCES legal_entities(id),
    msg_id           text NOT NULL,
    collection_date  date NOT NULL,
    total            numeric(20,2) NOT NULL,
    item_count       integer NOT NULL,
    xml              text NOT NULL,
    status           text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'SENT')),
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz,
    UNIQUE (organisation_id, msg_id)
);
ALTER TABLE sepa_batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY sepa_batches_tenant_isolation ON sepa_batches
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE sepa_collections (
    id                 uuid PRIMARY KEY,
    organisation_id    uuid NOT NULL REFERENCES organisations(id),
    batch_id           uuid NOT NULL REFERENCES sepa_batches(id),
    mandate_id         uuid NOT NULL REFERENCES sepa_mandates(id),
    sales_document_id  uuid NOT NULL REFERENCES sales_documents(id),
    amount             numeric(20,2) NOT NULL CHECK (amount > 0),
    end_to_end_id      text NOT NULL,
    sequence_type      text NOT NULL CHECK (sequence_type IN ('FRST', 'RCUR')),
    status             text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'COLLECTED', 'REJECTED')),
    receipt_id         uuid,
    reason             text,
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sepa_collections_batch ON sepa_collections(batch_id);
-- An invoice is collected once: a pending or collected direct debit blocks another for the same invoice.
CREATE UNIQUE INDEX uq_sepa_collection_open ON sepa_collections(sales_document_id) WHERE status IN ('PENDING', 'COLLECTED');
ALTER TABLE sepa_collections ENABLE ROW LEVEL SECURITY;
CREATE POLICY sepa_collections_tenant_isolation ON sepa_collections
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
