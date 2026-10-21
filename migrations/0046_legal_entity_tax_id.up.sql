-- The seller's own tax identifier outside India (Spanish NIF, EU VAT number,
-- UK VAT number, US EIN, ...). legal_entities only had gstin, which is an Indian
-- identifier; invoices in other countries are not valid without the seller's ID
-- (additive; India keeps using gstin).
ALTER TABLE legal_entities ADD COLUMN tax_id text;
