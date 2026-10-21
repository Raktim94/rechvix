-- Map coordinates for a customer's addresses (a customer can have many delivery or site points). Kept beside the contacts
-- tables, not in them, so addresses without a location are unchanged.
CREATE TABLE address_locations (
    address_id       uuid PRIMARY KEY REFERENCES party_addresses(id) ON DELETE CASCADE,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    latitude         numeric(9,6) NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude        numeric(9,6) NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_address_locations_org ON address_locations(organisation_id);
ALTER TABLE address_locations ENABLE ROW LEVEL SECURITY;
CREATE POLICY address_locations_tenant_isolation ON address_locations
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
