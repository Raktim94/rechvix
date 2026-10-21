-- Online catalogue, shop and client area. Published products appear on a public page; a visitor's order becomes a draft
-- sales order for the team to confirm. Customers sign in to their client area with a one-time emailed link.
CREATE TABLE shop_settings (
    organisation_id  uuid PRIMARY KEY REFERENCES organisations(id),
    enabled          boolean NOT NULL DEFAULT false,
    slug             text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{2,39}$'),
    title            text NOT NULL,
    intro            text,
    legal_entity_id  uuid NOT NULL REFERENCES legal_entities(id),
    branch_id        uuid NOT NULL REFERENCES branches(id),
    warehouse_id     uuid NOT NULL REFERENCES warehouses(id),
    price_list_id    uuid REFERENCES price_lists(id),
    acting_user_id   uuid NOT NULL REFERENCES users(id),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE shop_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY shop_settings_tenant_isolation ON shop_settings
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE shop_products (
    organisation_id     uuid NOT NULL REFERENCES organisations(id),
    product_variant_id  uuid NOT NULL REFERENCES product_variants(id),
    published           boolean NOT NULL DEFAULT false,
    title               text,
    description         text,
    sort_order          integer NOT NULL DEFAULT 0,
    PRIMARY KEY (organisation_id, product_variant_id)
);
ALTER TABLE shop_products ENABLE ROW LEVEL SECURITY;
CREATE POLICY shop_products_tenant_isolation ON shop_products
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE shop_orders (
    id                 uuid PRIMARY KEY,
    organisation_id    uuid NOT NULL REFERENCES organisations(id),
    reference          text NOT NULL,
    party_id           uuid NOT NULL REFERENCES parties(id),
    sales_document_id  uuid REFERENCES sales_documents(id),
    customer_name      text NOT NULL,
    customer_email     text NOT NULL,
    customer_phone     text,
    delivery_address   text,
    notes              text,
    total_estimate     numeric(20,6) NOT NULL DEFAULT 0,
    currency_code      varchar(3) NOT NULL,
    lines              jsonb NOT NULL,
    status             text NOT NULL DEFAULT 'NEW' CHECK (status IN ('NEW', 'CONFIRMED', 'CANCELLED')),
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organisation_id, reference)
);
CREATE INDEX idx_shop_orders_org ON shop_orders(organisation_id, created_at DESC);
ALTER TABLE shop_orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY shop_orders_tenant_isolation ON shop_orders
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- Client-area sign-in: one-time link tokens and sessions. Both carry only a bearer token (like share_links), so token_hash is
-- the access control and they are not row-level-secured; every authenticated lookup is by token_hash.
CREATE TABLE portal_login_tokens (
    token_hash       text PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    party_id         uuid NOT NULL REFERENCES parties(id),
    expires_at       timestamptz NOT NULL,
    used_at          timestamptz
);
CREATE TABLE portal_sessions (
    token_hash       text PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    party_id         uuid NOT NULL REFERENCES parties(id),
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- The public shop page has only a slug; this narrow SECURITY DEFINER lookup resolves it (same pattern as outbox_claim_next).
CREATE FUNCTION shop_organisation_by_slug(p_slug text) RETURNS uuid
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
    SELECT organisation_id FROM shop_settings WHERE slug = p_slug AND enabled
$$;
