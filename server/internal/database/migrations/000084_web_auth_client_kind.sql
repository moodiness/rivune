ALTER TABLE auth_sessions
    ADD COLUMN client_kind text NOT NULL DEFAULT 'native'
    CHECK (client_kind IN ('native', 'web'));

CREATE TABLE auth_web_access_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX auth_web_access_tokens_session_idx
    ON auth_web_access_tokens (session_id, expires_at);

CREATE INDEX auth_web_access_tokens_expiry_idx
    ON auth_web_access_tokens (expires_at, token_hash);
