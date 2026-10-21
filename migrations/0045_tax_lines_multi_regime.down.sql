-- Fails if any non-India tax snapshot exists (no rate-master row to point at,
-- NULL state codes). Archive or delete those documents' snapshots first.
ALTER TABLE legal_entities DROP COLUMN us_collection_regions;
ALTER TABLE sales_documents DROP COLUMN equivalence_surcharge;
ALTER TABLE sales_documents DROP COLUMN customer_tax_exempt;
ALTER TABLE sales_documents DROP COLUMN supply_region;
ALTER TABLE sales_documents DROP COLUMN customer_country;
ALTER TABLE tax_documents DROP COLUMN supply_region;
ALTER TABLE tax_documents DROP COLUMN customer_country;
ALTER TABLE tax_documents DROP COLUMN supplier_country;
ALTER TABLE tax_documents ALTER COLUMN place_of_supply_code SET NOT NULL;
ALTER TABLE tax_documents ALTER COLUMN supplier_state_code SET NOT NULL;
ALTER TABLE tax_lines DROP CONSTRAINT tax_lines_rate_source_present;
ALTER TABLE tax_lines DROP COLUMN rate_pack_version;
ALTER TABLE tax_lines DROP COLUMN tax_regime;
ALTER TABLE tax_lines ALTER COLUMN tax_rate_master_id SET NOT NULL;
