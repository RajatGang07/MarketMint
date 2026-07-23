-- Bracket exits: stop-loss / target orders linked as an OCO pair.

ALTER TABLE orders ADD COLUMN IF NOT EXISTS trigger_price NUMERIC(20, 4);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS stop_loss     NUMERIC(20, 4);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS target        NUMERIC(20, 4);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS oco_group     TEXT;

-- 'SL' (stop-market) joins MARKET/LIMIT as an order type.
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_order_type_check;
ALTER TABLE orders ADD CONSTRAINT orders_order_type_check
    CHECK (order_type IN ('MARKET', 'LIMIT', 'SL'));

CREATE INDEX IF NOT EXISTS idx_orders_oco ON orders (oco_group) WHERE oco_group IS NOT NULL;
