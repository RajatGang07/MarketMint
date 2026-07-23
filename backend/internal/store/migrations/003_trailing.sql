-- Trailing stops: an SL order may carry trail_by; the matcher ratchets its
-- trigger up as the price makes new highs (high_water tracks the best LTP
-- seen since the order was placed).

ALTER TABLE orders ADD COLUMN IF NOT EXISTS trail_by   NUMERIC(20, 4);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS high_water NUMERIC(20, 4);
