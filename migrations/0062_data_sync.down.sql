DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['units_of_measure', 'unit_conversions', 'categories', 'brands', 'products',
                             'product_variants', 'product_barcodes', 'parties', 'party_addresses', 'party_tax_registrations']
    LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I', 'sync_capture_' || t, t);
    END LOOP;
END $$;
DROP FUNCTION IF EXISTS sync_linked_orgs();
DROP FUNCTION IF EXISTS sync_capture();
DROP TABLE IF EXISTS sync_links;
DROP TABLE IF EXISTS sync_changes;
DELETE FROM role_permissions WHERE permission_code = 'sync.manage';
DELETE FROM permissions WHERE code = 'sync.manage';
