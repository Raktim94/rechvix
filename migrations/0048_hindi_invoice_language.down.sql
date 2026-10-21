-- Fail rather than silently rewrite documents when Hindi settings still exist.
ALTER TABLE legal_entities DROP CONSTRAINT legal_entities_invoice_language_check;
ALTER TABLE legal_entities ADD CONSTRAINT legal_entities_invoice_language_check CHECK (invoice_language IS NULL OR invoice_language IN ('en','es','fr','de','it','pt','nl'));
ALTER TABLE sales_documents DROP CONSTRAINT sales_documents_invoice_language_check;
ALTER TABLE sales_documents ADD CONSTRAINT sales_documents_invoice_language_check CHECK (invoice_language IS NULL OR invoice_language IN ('en','es','fr','de','it','pt','nl'));
