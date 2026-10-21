-- CRM: a simple sales pipeline. Opportunities (leads and deals) move through stages; activities record calls, emails,
-- meetings, notes and follow-up tasks. A lead without a customer record can be turned into one when it is worth quoting.
CREATE TABLE crm_stages (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    name             text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 40),
    sort_order       integer NOT NULL DEFAULT 0,
    kind             text NOT NULL CHECK (kind IN ('OPEN', 'WON', 'LOST')),
    UNIQUE (organisation_id, name)
);
ALTER TABLE crm_stages ENABLE ROW LEVEL SECURITY;
CREATE POLICY crm_stages_tenant_isolation ON crm_stages
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE crm_opportunities (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    title            text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 160),
    party_id         uuid REFERENCES parties(id),
    company_name     text,
    contact_name     text,
    contact_email    text,
    contact_phone    text,
    value_amount     numeric(20,6) NOT NULL DEFAULT 0 CHECK (value_amount >= 0),
    currency_code    varchar(3) NOT NULL,
    stage_id         uuid NOT NULL REFERENCES crm_stages(id),
    owner_user_id    uuid REFERENCES users(id),
    expected_close   date,
    source           text,
    notes            text,
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    closed_at        timestamptz
);
CREATE INDEX idx_crm_opportunities_org ON crm_opportunities(organisation_id, stage_id);
CREATE INDEX idx_crm_opportunities_party ON crm_opportunities(party_id) WHERE party_id IS NOT NULL;
ALTER TABLE crm_opportunities ENABLE ROW LEVEL SECURITY;
CREATE POLICY crm_opportunities_tenant_isolation ON crm_opportunities
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE crm_activities (
    id               uuid PRIMARY KEY,
    organisation_id  uuid NOT NULL REFERENCES organisations(id),
    opportunity_id   uuid NOT NULL REFERENCES crm_opportunities(id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('CALL', 'EMAIL', 'MEETING', 'NOTE', 'TASK')),
    subject          text NOT NULL CHECK (length(btrim(subject)) BETWEEN 1 AND 200),
    notes            text,
    due_at           timestamptz,
    done_at          timestamptz,
    created_by       uuid NOT NULL REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_crm_activities_opp ON crm_activities(opportunity_id, created_at DESC);
CREATE INDEX idx_crm_activities_due ON crm_activities(organisation_id, due_at) WHERE done_at IS NULL AND due_at IS NOT NULL;
ALTER TABLE crm_activities ENABLE ROW LEVEL SECURITY;
CREATE POLICY crm_activities_tenant_isolation ON crm_activities
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
