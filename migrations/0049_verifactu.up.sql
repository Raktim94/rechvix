-- VERI*FACTU (Spain, RD 1007/2023): tamper-evident invoice record chain, kept per issuing company.
--
-- verifactu_records is append-only and hash-chained. Immutability and chain continuity are enforced
-- here in the database, not only in application code: no UPDATE, DELETE or TRUNCATE is possible
-- (even for the table owner), and an insert must continue the chain exactly (sequence n+1, previous
-- hash equal to the stored hash of record n). Submission progress to the tax agency lives in the
-- separate, mutable verifactu_submissions table so the chained record itself never changes.

CREATE TABLE verifactu_settings (
    legal_entity_id          uuid PRIMARY KEY REFERENCES legal_entities(id),
    organisation_id          uuid NOT NULL REFERENCES organisations(id),
    enabled                  boolean NOT NULL DEFAULT false,
    environment              text NOT NULL DEFAULT 'test' CHECK (environment IN ('test', 'production')),
    installation_number      text NOT NULL,
    cert_p12_encrypted       bytea,
    cert_password_encrypted  bytea,
    cert_subject             text,
    cert_not_after           timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_verifactu_settings_org ON verifactu_settings(organisation_id);
ALTER TABLE verifactu_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY verifactu_settings_tenant_isolation ON verifactu_settings
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

-- One row per issuing company: the tip of its chain. Locking this row (SELECT ... FOR UPDATE) is what
-- serialises concurrent invoice finalizations so two of them can never claim the same position.
CREATE TABLE verifactu_chain_heads (
    legal_entity_id     uuid PRIMARY KEY REFERENCES legal_entities(id),
    organisation_id     uuid NOT NULL REFERENCES organisations(id),
    last_seq            bigint NOT NULL DEFAULT 0,
    last_hash           text NOT NULL DEFAULT '',
    last_invoice_number text NOT NULL DEFAULT '',
    last_issue_date     date
);
ALTER TABLE verifactu_chain_heads ENABLE ROW LEVEL SECURITY;
CREATE POLICY verifactu_chain_heads_tenant_isolation ON verifactu_chain_heads
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE TABLE verifactu_records (
    id                 uuid PRIMARY KEY,
    organisation_id    uuid NOT NULL REFERENCES organisations(id),
    legal_entity_id    uuid NOT NULL REFERENCES legal_entities(id),
    seq                bigint NOT NULL CHECK (seq >= 1),
    record_type        text NOT NULL CHECK (record_type IN ('ALTA', 'ANULACION')),
    sales_document_id  uuid REFERENCES sales_documents(id),
    issuer_nif         text NOT NULL,
    invoice_number     text NOT NULL,
    issue_date         date NOT NULL,
    invoice_type       text,
    tax_total          numeric(14,2),
    invoice_total      numeric(14,2),
    prev_hash          text NOT NULL DEFAULT '',
    hash               text NOT NULL CHECK (hash ~ '^[0-9A-F]{64}$'),
    generated_at_text  text NOT NULL,
    xml                text NOT NULL,
    qr_url             text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (legal_entity_id, seq)
);
CREATE INDEX idx_verifactu_records_org ON verifactu_records(organisation_id);
CREATE INDEX idx_verifactu_records_document ON verifactu_records(sales_document_id) WHERE sales_document_id IS NOT NULL;
-- An invoice number is issued once per company: a second ALTA with the same number is a duplicate.
CREATE UNIQUE INDEX uq_verifactu_alta_number ON verifactu_records(legal_entity_id, invoice_number, issue_date) WHERE record_type = 'ALTA';
ALTER TABLE verifactu_records ENABLE ROW LEVEL SECURITY;
CREATE POLICY verifactu_records_tenant_isolation ON verifactu_records
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);

CREATE FUNCTION verifactu_records_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'verifactu_records is append-only: % is not allowed', TG_OP USING ERRCODE = 'integrity_constraint_violation';
END $$;
CREATE TRIGGER verifactu_records_no_update_delete BEFORE UPDATE OR DELETE ON verifactu_records
    FOR EACH ROW EXECUTE FUNCTION verifactu_records_immutable();
CREATE TRIGGER verifactu_records_no_truncate BEFORE TRUNCATE ON verifactu_records
    FOR EACH STATEMENT EXECUTE FUNCTION verifactu_records_immutable();

CREATE FUNCTION verifactu_records_chain_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    prev_hash_stored text;
BEGIN
    IF NEW.seq = 1 THEN
        IF NEW.prev_hash <> '' THEN
            RAISE EXCEPTION 'verifactu chain: the first record must have an empty previous hash' USING ERRCODE = 'integrity_constraint_violation';
        END IF;
        RETURN NEW;
    END IF;
    SELECT hash INTO prev_hash_stored FROM verifactu_records WHERE legal_entity_id = NEW.legal_entity_id AND seq = NEW.seq - 1;
    IF prev_hash_stored IS NULL THEN
        RAISE EXCEPTION 'verifactu chain: record % has no predecessor', NEW.seq USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF prev_hash_stored <> NEW.prev_hash THEN
        RAISE EXCEPTION 'verifactu chain: previous hash does not match record %', NEW.seq - 1 USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER verifactu_records_chain_check BEFORE INSERT ON verifactu_records
    FOR EACH ROW EXECUTE FUNCTION verifactu_records_chain_check();

CREATE TABLE verifactu_submissions (
    record_id         uuid PRIMARY KEY REFERENCES verifactu_records(id),
    organisation_id   uuid NOT NULL REFERENCES organisations(id),
    legal_entity_id   uuid NOT NULL REFERENCES legal_entities(id),
    seq               bigint NOT NULL,
    status            text NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'ACCEPTED', 'ACCEPTED_WITH_ERRORS', 'REJECTED', 'ERROR')),
    attempts          integer NOT NULL DEFAULT 0,
    last_attempt_at   timestamptz,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    aeat_csv          text,
    error_code        text,
    error_message     text,
    response_xml      text,
    updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_verifactu_submissions_pending ON verifactu_submissions(legal_entity_id, seq) WHERE status IN ('PENDING', 'ERROR');
CREATE INDEX idx_verifactu_submissions_org ON verifactu_submissions(organisation_id);
ALTER TABLE verifactu_submissions ENABLE ROW LEVEL SECURITY;
CREATE POLICY verifactu_submissions_tenant_isolation ON verifactu_submissions
    USING (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid)
    WITH CHECK (organisation_id = NULLIF(current_setting('app.current_organisation_id', true), '')::uuid);
