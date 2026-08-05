DO $migration$
BEGIN
    CREATE TABLE IF NOT EXISTS demo_session_admissions (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        source_hash bytea NOT NULL CHECK (octet_length(source_hash) = 32),
        created_at timestamptz NOT NULL,
        expires_at timestamptz NOT NULL CHECK (expires_at > created_at)
    );

    CREATE INDEX IF NOT EXISTS demo_session_admissions_expiry_idx
        ON demo_session_admissions (expires_at, id);
    CREATE INDEX IF NOT EXISTS demo_session_admissions_source_expiry_idx
        ON demo_session_admissions (source_hash, expires_at);
END
$migration$;
