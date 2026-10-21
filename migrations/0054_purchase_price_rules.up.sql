-- Automatic selling-price updates when a purchase is finalized: price = cost x (1 + markup%), on the default price list.
CREATE TABLE purchase_price_rules (
    organisation_id  uuid PRIMARY KEY REFERENCES organisations(id),
    enabled          boolean NOT NULL DEFAULT false,
    markup_percent   numeric(7,2) NOT NULL DEFAULT 30 CHECK (markup_percent >= 0 AND markup_percent <= 1000),
    mode             text NOT NULL DEFAULT 'RAISE_ONLY' CHECK (mode IN ('RAISE_ONLY', 'ALWAYS')),
    updated_by       uuid NOT NULL REFERENCES users(id),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE purchase_price_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY purchase_price_rules_tenant_isolation ON purchase_price_rules
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
