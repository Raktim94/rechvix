-- Invoice language and foreign-currency totals (additive).
--
-- invoice_language: the language an invoice prints in. NULL on a legal entity
-- means "the language of its country"; NULL on a document means "the company's
-- setting". Only the generic (non-India) layout uses it.
ALTER TABLE legal_entities ADD COLUMN invoice_language text
    CHECK (invoice_language IS NULL OR invoice_language IN ('en','es','fr','de','it','pt','nl'));
ALTER TABLE sales_documents ADD COLUMN invoice_language text
    CHECK (invoice_language IS NULL OR invoice_language IN ('en','es','fr','de','it','pt','nl'));

-- grand_total_base_amount: the document total converted to the company's
-- (ledger) currency at the document's exchange_rate, rounded once. For a document
-- in the company currency it equals grand_total_amount. The ledger and the
-- reports use this figure; grand_total_amount stays in the invoice currency.
ALTER TABLE sales_documents ADD COLUMN grand_total_base_amount numeric(20,6);
UPDATE sales_documents SET grand_total_base_amount = grand_total_amount WHERE grand_total_amount IS NOT NULL;

-- exchange_rate on the tax snapshot: units of the company (ledger) currency per
-- unit of the document currency, as on the document. 1 for every existing row and
-- for documents in the company currency. Reports multiply the snapshot's amounts
-- (which stay in the invoice currency, as printed) by it to report in company currency.
ALTER TABLE tax_documents ADD COLUMN exchange_rate numeric(24,12) NOT NULL DEFAULT 1 CHECK (exchange_rate > 0);
