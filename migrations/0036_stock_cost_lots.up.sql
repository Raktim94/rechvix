-- Cost lots: a distinct concept from stock_batches (migrations/0012,
-- unique per (organisation, product_variant, batch_code) — a
-- manufacturer/expiry identity with no quantity or cost of its own).
-- This table exists to answer a different question a distributor's
-- purchase bill actually raises: "we already hold this product, but
-- this delivery is priced differently from what's on the shelf — is
-- that the same stock, or does it need to be tracked separately so a
-- later sale's margin is still correct?" stock_balances.average_cost
-- (migrations/0011) blends every receipt into one number by design and
-- stays the authoritative valuation for reporting; this table is an
-- additive, informational cost-lot ledger alongside it, not a
-- replacement — nothing that already reads stock_balances changes.
--
-- One row per unique (warehouse, variant, unit_cost): a receipt at a
-- price that already has a row adds to quantity_received/remaining on
-- that same row ("same stock, same price"); a receipt at any other
-- price gets its own new row ("different batch, because price moved") —
-- exactly the ON CONFLICT target below, so the app layer never needs a
-- separate "does a lot at this price already exist?" query before
-- deciding which case it's in.
CREATE TABLE stock_cost_lots (
    id                     uuid PRIMARY KEY,
    organisation_id        uuid NOT NULL REFERENCES organisations(id),
    warehouse_id           uuid NOT NULL REFERENCES warehouses(id),
    product_variant_id     uuid NOT NULL REFERENCES product_variants(id),
    unit_cost              numeric(20,6) NOT NULL,
    quantity_received      numeric(20,6) NOT NULL DEFAULT 0,
    -- Decremented FIFO (oldest lot first) as outward movements are
    -- recorded — best-effort only: stock received before this table
    -- existed has no lot to decrement from, so an outward movement is
    -- never blocked or failed for lack of lot coverage (see
    -- app.Service.recordMovement's cost-lot call).
    quantity_remaining     numeric(20,6) NOT NULL DEFAULT 0,
    source_reference_type  text,
    source_reference_id    uuid,
    received_at            timestamptz NOT NULL DEFAULT now(),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT stock_cost_lots_remaining_nonnegative CHECK (quantity_remaining >= 0),
    CONSTRAINT stock_cost_lots_remaining_le_received CHECK (quantity_remaining <= quantity_received),
    UNIQUE (organisation_id, warehouse_id, product_variant_id, unit_cost)
);

CREATE INDEX idx_stock_cost_lots_org_wh_variant ON stock_cost_lots(organisation_id, warehouse_id, product_variant_id);
CREATE INDEX idx_stock_cost_lots_remaining ON stock_cost_lots(organisation_id, warehouse_id, product_variant_id, received_at)
    WHERE quantity_remaining > 0;

ALTER TABLE stock_cost_lots ENABLE ROW LEVEL SECURITY;
CREATE POLICY stock_cost_lots_tenant_isolation ON stock_cost_lots
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
