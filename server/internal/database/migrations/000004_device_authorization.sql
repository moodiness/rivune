DO $migration$
BEGIN
    CREATE TABLE device_authorizations (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        device_code_hash bytea NOT NULL UNIQUE CHECK (octet_length(device_code_hash) = 32),
        user_code text NOT NULL UNIQUE CHECK (
            char_length(user_code) = 8
            AND user_code = upper(user_code)
            AND user_code !~ '[^A-Z0-9]'
        ),
        device_name text NOT NULL CHECK (char_length(device_name) BETWEEN 1 AND 120 AND device_name = btrim(device_name)),
        platform text NOT NULL CHECK (char_length(platform) BETWEEN 1 AND 32 AND platform = btrim(platform)),
        approved_user_id uuid REFERENCES users(id) ON DELETE CASCADE,
        approved_at timestamptz,
        last_polled_at timestamptz,
        consumed_at timestamptz,
        expires_at timestamptz NOT NULL,
        created_at timestamptz NOT NULL DEFAULT now(),
        CHECK (
            (approved_user_id IS NULL AND approved_at IS NULL)
            OR (approved_user_id IS NOT NULL AND approved_at IS NOT NULL)
        )
    );

    CREATE INDEX device_authorizations_expires_at_idx
        ON device_authorizations (expires_at)
        WHERE consumed_at IS NULL;
END
$migration$;
