DO $migration$
BEGIN
    CREATE TABLE playback_sessions (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        auth_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
        profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
        title_id text,
        media_type text NOT NULL CHECK (char_length(media_type) BETWEEN 1 AND 64),
        resource_id text NOT NULL CHECK (char_length(resource_id) BETWEEN 1 AND 2048),
        token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
        assets jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(assets) = 'array'),
        expires_at timestamptz NOT NULL,
        created_at timestamptz NOT NULL DEFAULT now(),
        CHECK (title_id IS NULL OR char_length(title_id) BETWEEN 1 AND 128),
        CHECK (expires_at > created_at)
    );
    CREATE INDEX playback_sessions_auth_session_idx ON playback_sessions (auth_session_id);
    CREATE INDEX playback_sessions_expiry_idx ON playback_sessions (expires_at);
END
$migration$;
