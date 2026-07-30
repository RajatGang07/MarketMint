-- Exit style for the autopilot: 'trail' rides the trend on a trailing stop
-- alone; 'bracket' also caps the win at a fixed target.
ALTER TABLE autopilot_settings
    ADD COLUMN IF NOT EXISTS exit_style TEXT NOT NULL DEFAULT 'trail';

-- Every directional forecast the Forecast tab hands out, recorded at issue
-- time and scored once the horizon matures. Measured accuracy is the only
-- accuracy that counts.
CREATE TABLE IF NOT EXISTS forecast_log (
    id             BIGSERIAL      PRIMARY KEY,
    symbol         TEXT           NOT NULL,
    horizon        TEXT           NOT NULL, -- intraday / close / next_day
    direction      TEXT           NOT NULL, -- up / down
    probability_up DOUBLE PRECISION NOT NULL,
    price_at       NUMERIC(18, 2) NOT NULL,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    matures_at     TIMESTAMPTZ    NOT NULL,
    resolved_at    TIMESTAMPTZ,
    price_after    NUMERIC(18, 2),
    correct        BOOLEAN
);

CREATE INDEX IF NOT EXISTS forecast_log_unresolved
    ON forecast_log (matures_at) WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS forecast_log_horizon
    ON forecast_log (horizon) WHERE resolved_at IS NOT NULL;
