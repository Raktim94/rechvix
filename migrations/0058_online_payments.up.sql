-- Online card / PayPal payments for invoices. Provider secrets are encrypted at rest (AEAD) and never returned by the API.
CREATE TABLE payment_providers (
    organisation_id   uuid NOT NULL REFERENCES organisations(id),
    provider          text NOT NULL CHECK (provider IN ('STRIPE', 'PAYPAL')),
    enabled           boolean NOT NULL DEFAULT false,
    live              boolean NOT NULL DEFAULT false,
    client_id         text,
    secret_encrypted  bytea,
    webhook_secret_encrypted bytea,
    updated_by        uuid NOT NULL REFERENCES users(id),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organisation_id, provider)
);
ALTER TABLE payment_providers ENABLE ROW LEVEL SECURITY;
CREATE POLICY payment_providers_tenant_isolation ON payment_providers
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE payment_links (
    id                 uuid PRIMARY KEY,
    organisation_id    uuid NOT NULL REFERENCES organisations(id),
    sales_document_id  uuid NOT NULL REFERENCES sales_documents(id),
    provider           text NOT NULL CHECK (provider IN ('STRIPE', 'PAYPAL')),
    external_id        text NOT NULL,
    url                text NOT NULL,
    amount             numeric(20,6) NOT NULL CHECK (amount > 0),
    currency_code      varchar(3) NOT NULL,
    status             text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PAID', 'FAILED')),
    receipt_id         uuid,
    paid_at            timestamptz,
    note               text,
    created_by         uuid NOT NULL REFERENCES users(id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, external_id)
);
CREATE INDEX idx_payment_links_document ON payment_links(organisation_id, sales_document_id);
ALTER TABLE payment_links ENABLE ROW LEVEL SECURITY;
CREATE POLICY payment_links_tenant_isolation ON payment_links
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- The provider's return redirect carries only the link id and the order id, not an organisation, so this narrow
-- SECURITY DEFINER lookup finds the organisation that owns a PayPal link (same pattern as outbox_claim_next).
CREATE FUNCTION payment_link_organisation(p_link uuid, p_provider text, p_external text) RETURNS uuid
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
    SELECT organisation_id FROM payment_links WHERE id = p_link AND provider = p_provider AND external_id = p_external
$$;
