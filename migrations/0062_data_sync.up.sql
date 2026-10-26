-- Two-way sync between a desktop install and a central server (docs/adr/0003-offline-sync.md).
--
-- Phase 1 covers master data only: units, categories, brands, products, variants,
-- barcodes, contacts, addresses and tax registrations. Every change to one of those
-- tables is appended to sync_changes by a trigger; a device pushes its own changes and
-- pulls everyone else's, last write wins per record. Ledgers (sales, stock, accounting)
-- are deliberately NOT here: they need event-style sync, not row copying.

CREATE TABLE sync_changes (
    seq              bigserial PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    table_name       text NOT NULL,
    row_id           uuid NOT NULL,
    op               char(1) NOT NULL CHECK (op IN ('U', 'D')),
    row_data         jsonb,
    -- The device whose change this is; NULL means it was made on this server itself.
    origin           text,
    changed_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_sync_changes_org_seq ON sync_changes (organisation_id, seq);

ALTER TABLE sync_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY sync_changes_tenant_isolation ON sync_changes
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- One link per organisation on a desktop install: where the central server is and how far
-- this device has pushed and pulled.
CREATE TABLE sync_links (
    organisation_id   uuid PRIMARY KEY REFERENCES organisations(id),
    remote_url        text NOT NULL,
    remote_org_id     uuid NOT NULL,
    api_key_encrypted bytea NOT NULL,
    device_id         text NOT NULL,
    enabled           boolean NOT NULL DEFAULT true,
    last_pushed_seq   bigint NOT NULL DEFAULT 0,
    last_pulled_seq   bigint NOT NULL DEFAULT 0,
    last_sync_at      timestamptz,
    last_error        text,
    last_pushed       integer NOT NULL DEFAULT 0,
    last_pulled       integer NOT NULL DEFAULT 0,
    last_conflicts    integer NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE sync_links ENABLE ROW LEVEL SECURITY;
CREATE POLICY sync_links_tenant_isolation ON sync_links
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- rechvix.sync_skip = 'on'     : apply without logging (a device applying what it just pulled)
-- rechvix.sync_origin = <id>   : log with that origin (the central server applying a device's push)
CREATE FUNCTION sync_capture() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    rec record;
BEGIN
    IF current_setting('rechvix.sync_skip', true) = 'on' THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'DELETE' THEN
        rec := OLD;
        INSERT INTO sync_changes (organisation_id, table_name, row_id, op, row_data, origin)
        VALUES (rec.organisation_id, TG_TABLE_NAME, rec.id, 'D', NULL, NULLIF(current_setting('rechvix.sync_origin', true), ''));
    ELSE
        rec := NEW;
        INSERT INTO sync_changes (organisation_id, table_name, row_id, op, row_data, origin)
        VALUES (rec.organisation_id, TG_TABLE_NAME, rec.id, 'U', to_jsonb(rec), NULLIF(current_setting('rechvix.sync_origin', true), ''));
    END IF;
    RETURN NULL;
END $$;

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['units_of_measure', 'unit_conversions', 'categories', 'brands', 'products',
                             'product_variants', 'product_barcodes', 'parties', 'party_addresses', 'party_tax_registrations']
    LOOP
        EXECUTE format('CREATE TRIGGER %I AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION sync_capture()', 'sync_capture_' || t, t);
        -- Rows that exist before sync was introduced still have to reach the other side.
        EXECUTE format('INSERT INTO sync_changes (organisation_id, table_name, row_id, op, row_data) SELECT organisation_id, %L, id, ''U'', to_jsonb(x) FROM %I x', t, t);
    END LOOP;
END $$;

INSERT INTO permissions (code, module, description) VALUES
    ('sync.manage', 'sync', 'Link this install to a central server and sync master data');
INSERT INTO role_permissions (role_id, permission_code)
SELECT roles.id, 'sync.manage' FROM roles WHERE roles.is_system = true
ON CONFLICT DO NOTHING;

-- The background loop has no organisation yet; this lists the linked ones (the same pattern as auth_lookup_user_by_email).
CREATE FUNCTION sync_linked_orgs() RETURNS TABLE (organisation_id uuid) LANGUAGE sql SECURITY DEFINER SET search_path = public AS
$$ SELECT organisation_id FROM sync_links WHERE enabled $$;
REVOKE EXECUTE ON FUNCTION sync_linked_orgs() FROM PUBLIC;
DO $$ BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'billing_app') THEN
        EXECUTE 'GRANT EXECUTE ON FUNCTION sync_linked_orgs() TO billing_app';
        EXECUTE 'GRANT SELECT, INSERT, UPDATE, DELETE ON sync_changes, sync_links TO billing_app';
        EXECUTE 'GRANT USAGE, SELECT ON SEQUENCE sync_changes_seq_seq TO billing_app';
    END IF;
END $$;
