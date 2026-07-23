-- Multi-user: accounts become users (username + password), and sessions are
-- server-side rows so logins survive restarts and scale past one instance.

ALTER TABLE accounts ADD COLUMN IF NOT EXISTS password_hash TEXT;

CREATE TABLE IF NOT EXISTS sessions (
    token       TEXT PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_account ON sessions (account_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry  ON sessions (expires_at);
