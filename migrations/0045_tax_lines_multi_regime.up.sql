-- Multi-jurisdiction tax (expand step). Everything here is additive or a
-- relaxation; existing India rows are untouched.
--
-- 1. tax_lines.tax_rate_master_id pointed at the India GST rate master, so a
--    line taxed by another regime (VAT, US sales tax) had nothing to
--    reference. It becomes NULLable and the regime plus the dated rate-table
--    version are recorded instead. A CHECK guarantees every line has one
--    provenance or the other.
ALTER TABLE tax_lines ALTER COLUMN tax_rate_master_id DROP NOT NULL;
ALTER TABLE tax_lines ADD COLUMN tax_regime text;
ALTER TABLE tax_lines ADD COLUMN rate_pack_version text;
ALTER TABLE tax_lines ADD CONSTRAINT tax_lines_rate_source_present CHECK (
    tax_rate_master_id IS NOT NULL OR (tax_regime IS NOT NULL AND rate_pack_version IS NOT NULL)
);

-- 2. tax_documents carried two NOT NULL foreign keys into the GST state list.
--    Those become optional, and the supply's country and region are recorded.
ALTER TABLE tax_documents ALTER COLUMN supplier_state_code DROP NOT NULL;
ALTER TABLE tax_documents ALTER COLUMN place_of_supply_code DROP NOT NULL;
ALTER TABLE tax_documents ADD COLUMN supplier_country char(2);
ALTER TABLE tax_documents ADD COLUMN customer_country char(2);
ALTER TABLE tax_documents ADD COLUMN supply_region text;

-- 3. Sales documents: the tax facts a non-India regime needs, fixed at
--    creation like place_of_supply_state_code is. Existing rows default to
--    "domestic, no special treatment".
ALTER TABLE sales_documents ADD COLUMN customer_country char(2);
ALTER TABLE sales_documents ADD COLUMN supply_region text;
ALTER TABLE sales_documents ADD COLUMN customer_tax_exempt boolean NOT NULL DEFAULT false;
ALTER TABLE sales_documents ADD COLUMN equivalence_surcharge boolean NOT NULL DEFAULT false;

-- 4. US states ("US-CA") where this legal entity is registered to collect
--    sales tax. Tax is only charged in these states.
ALTER TABLE legal_entities ADD COLUMN us_collection_regions text[] NOT NULL DEFAULT '{}';
