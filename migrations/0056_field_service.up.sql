-- Field service: the equipment a customer owns (assets), incidents raised against them (with a work log and time spent) and
-- maintenance plans that raise an incident automatically when a service is due. Sites can carry a latitude and longitude.
INSERT INTO permissions (code, module, description) VALUES
    ('fieldservice.view',   'fieldservice', 'View customer assets, incidents and maintenance plans'),
    ('fieldservice.manage', 'fieldservice', 'Create and update customer assets, incidents and maintenance plans');
INSERT INTO role_permissions (role_id, permission_code)
SELECT roles.id, p.code FROM roles, (VALUES ('fieldservice.view'), ('fieldservice.manage')) AS p(code)
WHERE roles.is_system = true
ON CONFLICT DO NOTHING;

CREATE TABLE customer_assets (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    party_id         uuid NOT NULL REFERENCES parties(id),
    name             text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 160),
    model            text,
    serial_number    text,
    location_note    text,
    latitude         numeric(9,6) CHECK (latitude BETWEEN -90 AND 90),
    longitude        numeric(9,6) CHECK (longitude BETWEEN -180 AND 180),
    installed_on     date,
    warranty_until   date,
    status           text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED')),
    notes            text,
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((latitude IS NULL) = (longitude IS NULL))
);
CREATE INDEX idx_customer_assets_party ON customer_assets(organisation_id, party_id);
ALTER TABLE customer_assets ENABLE ROW LEVEL SECURITY;
CREATE POLICY customer_assets_tenant_isolation ON customer_assets
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE incident_counters (
    organisation_id  uuid PRIMARY KEY REFERENCES organisations(id),
    next_number      bigint NOT NULL DEFAULT 1
);
ALTER TABLE incident_counters ENABLE ROW LEVEL SECURITY;
CREATE POLICY incident_counters_tenant_isolation ON incident_counters
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE incidents (
    id                uuid PRIMARY KEY,
    organisation_id   uuid NOT NULL REFERENCES organisations(id),
    number            text NOT NULL,
    party_id          uuid NOT NULL REFERENCES parties(id),
    asset_id          uuid REFERENCES customer_assets(id),
    title             text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
    description       text,
    priority          text NOT NULL DEFAULT 'NORMAL' CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    status            text NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'IN_PROGRESS', 'WAITING', 'RESOLVED', 'CLOSED')),
    assigned_user_id  uuid REFERENCES users(id),
    scheduled_for     timestamptz,
    latitude          numeric(9,6) CHECK (latitude BETWEEN -90 AND 90),
    longitude         numeric(9,6) CHECK (longitude BETWEEN -180 AND 180),
    source            text NOT NULL DEFAULT 'MANUAL' CHECK (source IN ('MANUAL', 'MAINTENANCE')),
    resolved_at       timestamptz,
    created_by        uuid NOT NULL REFERENCES users(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organisation_id, number),
    CHECK ((latitude IS NULL) = (longitude IS NULL))
);
CREATE INDEX idx_incidents_org_status ON incidents(organisation_id, status);
CREATE INDEX idx_incidents_party ON incidents(party_id);
CREATE INDEX idx_incidents_asset ON incidents(asset_id) WHERE asset_id IS NOT NULL;
ALTER TABLE incidents ENABLE ROW LEVEL SECURITY;
CREATE POLICY incidents_tenant_isolation ON incidents
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE incident_updates (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    incident_id      uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('COMMENT', 'STATUS', 'ASSIGN', 'SCHEDULE')),
    body             text NOT NULL,
    minutes_spent    integer CHECK (minutes_spent IS NULL OR minutes_spent BETWEEN 1 AND 1440),
    created_by       uuid REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_incident_updates_incident ON incident_updates(incident_id, created_at);
ALTER TABLE incident_updates ENABLE ROW LEVEL SECURITY;
CREATE POLICY incident_updates_tenant_isolation ON incident_updates
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE maintenance_plans (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    asset_id         uuid NOT NULL REFERENCES customer_assets(id),
    title            text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
    every_days       integer NOT NULL CHECK (every_days BETWEEN 1 AND 3650),
    next_due         date NOT NULL,
    priority         text NOT NULL DEFAULT 'NORMAL' CHECK (priority IN ('LOW', 'NORMAL', 'HIGH', 'URGENT')),
    assigned_user_id uuid REFERENCES users(id),
    active           boolean NOT NULL DEFAULT true,
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_maintenance_plans_due ON maintenance_plans(next_due) WHERE active;
ALTER TABLE maintenance_plans ENABLE ROW LEVEL SECURITY;
CREATE POLICY maintenance_plans_tenant_isolation ON maintenance_plans
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE FUNCTION maintenance_due_organisations() RETURNS TABLE (organisation_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
    SELECT DISTINCT organisation_id FROM maintenance_plans WHERE active AND next_due <= current_date
$$;
