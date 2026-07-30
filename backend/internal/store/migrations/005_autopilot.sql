-- Autopilot: per-account automated trading driven by the signals board.

CREATE TABLE IF NOT EXISTS autopilot_settings (
    account_id            BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    enabled               BOOLEAN        NOT NULL DEFAULT FALSE,
    max_positions         INT            NOT NULL DEFAULT 5,
    max_capital_per_trade NUMERIC(18, 2) NOT NULL DEFAULT 200000,
    trail_stops           BOOLEAN        NOT NULL DEFAULT TRUE,
    updated_at            TIMESTAMPTZ    NOT NULL DEFAULT now()
);

-- Every decision the autopilot takes (or deliberately does not take), with
-- its reasons. This is the trust surface: the user can audit each action.
CREATE TABLE IF NOT EXISTS autopilot_log (
    id         BIGSERIAL   PRIMARY KEY,
    account_id BIGINT      NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    action     TEXT        NOT NULL, -- BUY / SELL / SKIP / ERROR
    symbol     TEXT        NOT NULL DEFAULT '',
    detail     TEXT        NOT NULL,
    order_ref  TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS autopilot_log_account_at
    ON autopilot_log (account_id, at DESC);
