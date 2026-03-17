CREATE TABLE IF NOT EXISTS admin_users (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email            TEXT        NOT NULL UNIQUE,
    password         TEXT        NOT NULL,
    activated        BOOLEAN     NOT NULL DEFAULT FALSE,
    activation_token TEXT        NOT NULL DEFAULT '',
    token_expires_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
)