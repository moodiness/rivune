DO $migration$
BEGIN
    CREATE TABLE IF NOT EXISTS instances (
        id smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
        public_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
        name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80 AND name = btrim(name)),
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE TABLE IF NOT EXISTS users (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        username text NOT NULL CHECK (char_length(username) BETWEEN 3 AND 64 AND username = btrim(username)),
        password_hash text NOT NULL,
        role text NOT NULL CHECK (role IN ('admin', 'member')),
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now()
    );
    CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_key ON users (lower(username));

    CREATE TABLE IF NOT EXISTS profiles (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80 AND name = btrim(name)),
        pin_hash text,
        is_child boolean NOT NULL DEFAULT false,
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE TABLE IF NOT EXISTS user_profile_access (
        user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
        can_manage boolean NOT NULL DEFAULT false,
        created_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (user_id, profile_id)
    );

    CREATE TABLE IF NOT EXISTS devices (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        user_id uuid REFERENCES users(id) ON DELETE SET NULL,
        name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120 AND name = btrim(name)),
        platform text NOT NULL,
        last_seen_at timestamptz,
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE TABLE IF NOT EXISTS instance_settings (
        instance_id smallint PRIMARY KEY DEFAULT 1 REFERENCES instances(id) ON DELETE CASCADE CHECK (instance_id = 1),
        schema_version integer NOT NULL DEFAULT 1 CHECK (schema_version > 0),
        settings jsonb NOT NULL DEFAULT '{}'::jsonb,
        updated_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE TABLE IF NOT EXISTS profile_settings (
        profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
        schema_version integer NOT NULL DEFAULT 1 CHECK (schema_version > 0),
        settings jsonb NOT NULL DEFAULT '{}'::jsonb,
        updated_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE TABLE IF NOT EXISTS profile_device_settings (
        profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
        device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
        schema_version integer NOT NULL DEFAULT 1 CHECK (schema_version > 0),
        settings jsonb NOT NULL DEFAULT '{}'::jsonb,
        updated_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (profile_id, device_id)
    );
END
$migration$;
