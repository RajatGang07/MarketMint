-- Paper trading schema.
-- Money is NUMERIC (never float) so cash and P&L stay exact.

CREATE TABLE IF NOT EXISTS accounts (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    starting_cash  NUMERIC(20, 4) NOT NULL DEFAULT 0,
    cash           NUMERIC(20, 4) NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS positions (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id     BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    trading_symbol TEXT NOT NULL,
    exchange       TEXT NOT NULL,
    segment        TEXT NOT NULL,
    quantity       BIGINT NOT NULL DEFAULT 0,
    avg_price      NUMERIC(20, 4) NOT NULL DEFAULT 0,
    realized_pnl   NUMERIC(20, 4) NOT NULL DEFAULT 0,
    UNIQUE (account_id, trading_symbol, segment)
);

CREATE TABLE IF NOT EXISTS orders (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id       BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    order_ref        TEXT NOT NULL UNIQUE,
    trading_symbol   TEXT NOT NULL,
    exchange         TEXT NOT NULL,
    segment          TEXT NOT NULL,
    product          TEXT NOT NULL,
    transaction_type TEXT NOT NULL CHECK (transaction_type IN ('BUY', 'SELL')),
    order_type       TEXT NOT NULL CHECK (order_type IN ('MARKET', 'LIMIT')),
    quantity         BIGINT NOT NULL CHECK (quantity > 0),
    limit_price      NUMERIC(20, 4),
    status           TEXT NOT NULL CHECK (status IN ('OPEN', 'FILLED', 'REJECTED', 'CANCELLED')),
    fill_price       NUMERIC(20, 4),
    filled_quantity  BIGINT NOT NULL DEFAULT 0,
    message          TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS trades (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id       BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    order_ref        TEXT NOT NULL,
    trading_symbol   TEXT NOT NULL,
    transaction_type TEXT NOT NULL,
    quantity         BIGINT NOT NULL,
    price            NUMERIC(20, 4) NOT NULL,
    realized_pnl     NUMERIC(20, 4) NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_positions_account ON positions (account_id);
CREATE INDEX IF NOT EXISTS idx_orders_account_created ON orders (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_open ON orders (account_id) WHERE status = 'OPEN';
CREATE INDEX IF NOT EXISTS idx_trades_account_created ON trades (account_id, created_at DESC);
