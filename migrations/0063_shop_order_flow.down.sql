-- Orders already past CONFIRMED fold back to CONFIRMED so the narrower constraint still holds.
UPDATE shop_orders SET status = 'CONFIRMED' WHERE status IN ('PROCESSING', 'DISPATCHED', 'DELIVERED');
ALTER TABLE shop_orders DROP COLUMN status_changed_at;
ALTER TABLE shop_orders DROP COLUMN invoice_document_id;
ALTER TABLE shop_orders DROP CONSTRAINT shop_orders_status_check;
ALTER TABLE shop_orders ADD CONSTRAINT shop_orders_status_check CHECK (status IN ('NEW', 'CONFIRMED', 'CANCELLED'));
