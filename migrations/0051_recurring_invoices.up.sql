-- Recurring billing: a schedule clones a finalized invoice on a fixed rhythm.
CREATE TABLE recurring_schedules (
    id                  uuid PRIMARY KEY,
    organisation_id     uuid NOT NULL REFERENCES organisations(id),
    legal_entity_id     uuid NOT NULL REFERENCES legal_entities(id),
    source_document_id  uuid NOT NULL REFERENCES sales_documents(id),
    frequency           text NOT NULL CHECK (frequency IN ('WEEKLY', 'MONTHLY', 'QUARTERLY', 'YEARLY')),
    interval_count      integer NOT NULL DEFAULT 1 CHECK (interval_count BETWEEN 1 AND 24),
    anchor_date         date NOT NULL,
    next_run_date       date NOT NULL,
    end_date            date,
    auto_finalize       boolean NOT NULL DEFAULT false,
    email_to            text,
    active              boolean NOT NULL DEFAULT true,
    run_count           integer NOT NULL DEFAULT 0,
    last_run_at         timestamptz,
    created_by          uuid NOT NULL REFERENCES users(id),
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_recurring_schedules_org ON recurring_schedules(organisation_id);
CREATE INDEX idx_recurring_schedules_due ON recurring_schedules(next_run_date) WHERE active;
ALTER TABLE recurring_schedules ENABLE ROW LEVEL SECURITY;
CREATE POLICY recurring_schedules_tenant_isolation ON recurring_schedules
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE recurring_runs (
    id                   uuid PRIMARY KEY,
    organisation_id      uuid NOT NULL REFERENCES organisations(id),
    schedule_id          uuid NOT NULL REFERENCES recurring_schedules(id) ON DELETE CASCADE,
    run_date             date NOT NULL,
    created_document_id  uuid REFERENCES sales_documents(id),
    status               text NOT NULL CHECK (status IN ('CREATED', 'FINALIZED', 'FAILED')),
    error                text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (schedule_id, run_date)
);
CREATE INDEX idx_recurring_runs_schedule ON recurring_runs(schedule_id, run_date DESC);
ALTER TABLE recurring_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY recurring_runs_tenant_isolation ON recurring_runs
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- The worker loop has no organisation yet; this narrow SECURITY DEFINER function lists those with schedules due
-- (same pattern as outbox_claim_next, migration 0023).
CREATE FUNCTION recurring_due_organisations() RETURNS TABLE (organisation_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
    SELECT DISTINCT organisation_id FROM recurring_schedules WHERE active AND next_run_date <= current_date
$$;
