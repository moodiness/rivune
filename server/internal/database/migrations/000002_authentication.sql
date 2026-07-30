DO $migration$
BEGIN
    ALTER TABLE users
        ADD COLUMN IF NOT EXISTS failed_login_count integer NOT NULL DEFAULT 0 CHECK (failed_login_count >= 0),
        ADD COLUMN IF NOT EXISTS locked_until timestamptz;

    CREATE TABLE IF NOT EXISTS auth_sessions (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
        access_token_hash bytea NOT NULL UNIQUE CHECK (octet_length(access_token_hash) = 32),
        access_expires_at timestamptz NOT NULL,
        refresh_expires_at timestamptz NOT NULL,
        last_seen_at timestamptz NOT NULL DEFAULT now(),
        revoked_at timestamptz,
        revoked_reason text,
        created_at timestamptz NOT NULL DEFAULT now(),
        CHECK (access_expires_at <= refresh_expires_at)
    );
    CREATE INDEX IF NOT EXISTS auth_sessions_user_id_idx ON auth_sessions (user_id);
    CREATE INDEX IF NOT EXISTS auth_sessions_active_access_idx
        ON auth_sessions (access_token_hash, access_expires_at)
        WHERE revoked_at IS NULL;

    CREATE TABLE IF NOT EXISTS auth_refresh_tokens (
        token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
        session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
        expires_at timestamptz NOT NULL,
        consumed_at timestamptz,
        created_at timestamptz NOT NULL DEFAULT now()
    );
    CREATE INDEX IF NOT EXISTS auth_refresh_tokens_session_id_idx ON auth_refresh_tokens (session_id);
END
$migration$;
