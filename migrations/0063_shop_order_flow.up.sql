-- Online orders move through a fulfilment flow (confirm, process, dispatch, deliver) and can be turned into an invoice.
-- Additive: existing rows keep their status, and the new column is NULL until an invoice is created.
ALTER TABLE shop_orders DROP CONSTRAINT shop_orders_status_check;
ALTER TABLE shop_orders ADD CONSTRAINT shop_orders_status_check
    CHECK (status IN ('NEW', 'CONFIRMED', 'PROCESSING', 'DISPATCHED', 'DELIVERED', 'CANCELLED'));
ALTER TABLE shop_orders ADD COLUMN invoice_document_id uuid REFERENCES sales_documents(id);
ALTER TABLE shop_orders ADD COLUMN status_changed_at timestamptz;
