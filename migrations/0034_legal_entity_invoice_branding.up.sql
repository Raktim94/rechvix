-- Additive extension to legal_entities (migrations/0001, extended by
-- 0017's gstin/gst_state_code) — the seller-side fields
-- internal/modules/sales/printing.SellerInfo/InvoiceData has supported
-- rendering since Stage 5b (logo, address, phone, email, bank details)
-- but internal/modules/sales/app/print.go's BuildInvoiceData never had
-- anywhere to read them FROM: every invoice ever printed rendered a bare
-- legal name + GSTIN, no matter what the print template could show. This
-- migration is that missing source of truth. Nullable and additive:
-- existing rows are unaffected, no backfill needed, exactly 0017's
-- precedent.
ALTER TABLE legal_entities ADD COLUMN phone text;
ALTER TABLE legal_entities ADD COLUMN email text;
ALTER TABLE legal_entities ADD COLUMN website text;
-- Free-text, newline-separated (printing.SellerInfo.AddressLines is
-- already a []string the template renders one line per entry — matches
-- that shape without a separate address-lines table for what is, for a
-- small business's own invoice header, never more than 3-4 lines).
ALTER TABLE legal_entities ADD COLUMN address text;
ALTER TABLE legal_entities ADD COLUMN bank_name text;
ALTER TABLE legal_entities ADD COLUMN bank_account_number text;
ALTER TABLE legal_entities ADD COLUMN bank_ifsc text;
ALTER TABLE legal_entities ADD COLUMN upi_id text;
ALTER TABLE legal_entities ADD COLUMN authorized_signatory_name text;
-- Org-level default; a sales_documents row's own terms_and_conditions
-- (already existed, per-document) still wins when set — see
-- sales/app/print.go's BuildInvoiceData, which only falls back to this
-- when the document didn't set its own.
ALTER TABLE legal_entities ADD COLUMN default_terms_and_conditions text;
-- Always re-encoded to PNG server-side before storage regardless of
-- upload format (internal/modules/organisation/httpapi/handlers.go's
-- decodeAndReencodeLogo) — never trust an uploaded image's bytes
-- directly, decode-then-reencode is the validation. A few hundred KB at
-- most for a logo; no size cap needed at the schema level, the HTTP
-- handler enforces one before this column is ever reached.
ALTER TABLE legal_entities ADD COLUMN logo_png bytea;
