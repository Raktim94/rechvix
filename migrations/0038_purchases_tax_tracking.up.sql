-- Purchase-side GST tracking (brief §8's GSTR-3B follow-up, flagged as
-- remaining work when GSTR-1 shipped): purchase_documents/
-- purchase_document_lines never carried a tax snapshot pointer or HSN
-- code at all — unlike sales_documents/sales_document_lines, which have
-- had both since migrations/0019. Without hsn_sac_code there is nothing
-- for the taxation module's rate lookup to resolve against, and without
-- tax_document_id there is nowhere to point at the resulting
-- tax_documents/tax_lines/tax_components snapshot once computed. Same
-- shape as sales_documents.tax_document_id's own comment: a
-- denormalized quick-access pointer, not the authoritative store (that
-- stays tax_documents, joined by reference_type='purchase_document').

ALTER TABLE purchase_documents ADD COLUMN tax_document_id uuid;

-- NOT NULL DEFAULT '' rather than nullable: matches
-- sales_document_lines.hsn_sac_code's own NOT NULL constraint (every
-- line added going forward always snapshots the product's HSN, even if
-- that happens to be an empty string) while letting existing rows
-- backfill to '' without a data migration — no retroactive tax snapshot
-- is computed for a document already finalized before this migration,
-- consistent with this codebase's "immutable historical record" rule.
ALTER TABLE purchase_document_lines ADD COLUMN hsn_sac_code text NOT NULL DEFAULT '';
