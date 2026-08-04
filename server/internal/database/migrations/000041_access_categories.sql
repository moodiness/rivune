DO $migration$
DECLARE
    default_category_id uuid;
BEGIN
    CREATE TABLE access_categories (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        name text NOT NULL CHECK (
            char_length(name) BETWEEN 1 AND 80
            AND name = btrim(name)
        ),
        normalized_name text NOT NULL UNIQUE CHECK (
            normalized_name = btrim(normalized_name)
            AND normalized_name <> ''
        ),
        description text CHECK (description IS NULL OR char_length(description) <= 500),
        color text CHECK (color IS NULL OR color ~ '^#[0-9A-F]{6}$'),
        icon text CHECK (icon IS NULL OR icon ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
        position integer NOT NULL CHECK (position >= 0),
        is_default boolean NOT NULL DEFAULT false,
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now(),
        CONSTRAINT access_categories_position_key UNIQUE (position) DEFERRABLE INITIALLY DEFERRED
    );

    CREATE UNIQUE INDEX access_categories_single_default_key
        ON access_categories (is_default)
        WHERE is_default;

    INSERT INTO access_categories (name, normalized_name, position, is_default)
    VALUES ('Uncategorized', 'uncategorized', 0, true)
    RETURNING id INTO default_category_id;

    CREATE FUNCTION require_default_access_category()
    RETURNS trigger
    LANGUAGE plpgsql
    AS $function$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM access_categories WHERE is_default) THEN
            RAISE EXCEPTION 'exactly one default access category is required'
                USING ERRCODE = '23514';
        END IF;
        RETURN NULL;
    END
    $function$;

    CREATE CONSTRAINT TRIGGER access_categories_require_default
        AFTER INSERT OR UPDATE OR DELETE ON access_categories
        DEFERRABLE INITIALLY DEFERRED
        FOR EACH ROW
        EXECUTE FUNCTION require_default_access_category();

    ALTER TABLE profiles ADD COLUMN category_id uuid;
    UPDATE profiles SET category_id = default_category_id;
    ALTER TABLE profiles
        ALTER COLUMN category_id SET NOT NULL,
        ADD CONSTRAINT profiles_category_id_fkey
            FOREIGN KEY (category_id) REFERENCES access_categories(id) ON DELETE RESTRICT;

    ALTER TABLE devices
        ADD COLUMN category_id uuid,
        ADD COLUMN approved_at timestamptz,
        ADD COLUMN internal_note text CHECK (
            internal_note IS NULL OR char_length(internal_note) <= 500
        );
    UPDATE devices
    SET category_id = default_category_id,
        approved_at = COALESCE(approved_at, created_at);
    ALTER TABLE devices
        ALTER COLUMN category_id SET NOT NULL,
        ALTER COLUMN approved_at SET DEFAULT now(),
        ALTER COLUMN approved_at SET NOT NULL,
        ADD CONSTRAINT devices_category_id_fkey
            FOREIGN KEY (category_id) REFERENCES access_categories(id) ON DELETE RESTRICT;

    ALTER TABLE auth_sessions
        ADD COLUMN authorization_scope text,
        ADD COLUMN category_id uuid;
    UPDATE auth_sessions
    SET authorization_scope = 'category',
        category_id = default_category_id;
    ALTER TABLE auth_sessions
        ALTER COLUMN authorization_scope SET DEFAULT 'category',
        ALTER COLUMN authorization_scope SET NOT NULL,
        ADD CONSTRAINT auth_sessions_authorization_scope_check
            CHECK (authorization_scope IN ('global_admin', 'category')),
        ADD CONSTRAINT auth_sessions_category_id_fkey
            FOREIGN KEY (category_id) REFERENCES access_categories(id) ON DELETE RESTRICT,
        ADD CONSTRAINT auth_sessions_authorization_category_consistent
            CHECK (
                (authorization_scope = 'global_admin' AND category_id IS NULL)
                OR (authorization_scope = 'category' AND category_id IS NOT NULL)
            );

    ALTER TABLE device_authorizations
        ADD COLUMN approved_category_id uuid,
        ADD COLUMN approved_device_name text CHECK (
            approved_device_name IS NULL
            OR (char_length(approved_device_name) BETWEEN 1 AND 120 AND approved_device_name = btrim(approved_device_name))
        ),
        ADD COLUMN approved_internal_note text CHECK (
            approved_internal_note IS NULL OR char_length(approved_internal_note) <= 500
        );
    UPDATE device_authorizations
    SET approved_category_id = default_category_id,
        approved_device_name = device_name
    WHERE approved_user_id IS NOT NULL;
    ALTER TABLE device_authorizations
        ADD CONSTRAINT device_authorizations_approved_category_id_fkey
            FOREIGN KEY (approved_category_id) REFERENCES access_categories(id) ON DELETE RESTRICT,
        ADD CONSTRAINT device_authorizations_category_approval_consistent
            CHECK (
                (approved_user_id IS NULL
                    AND approved_category_id IS NULL
                    AND approved_device_name IS NULL
                    AND approved_internal_note IS NULL)
                OR (approved_user_id IS NOT NULL
                    AND approved_category_id IS NOT NULL)
            );

    CREATE FUNCTION assign_default_resource_access_category()
    RETURNS trigger
    LANGUAGE plpgsql
    AS $function$
    BEGIN
        IF NEW.category_id IS NULL THEN
            SELECT id INTO NEW.category_id
            FROM access_categories
            WHERE is_default;
        END IF;
        RETURN NEW;
    END
    $function$;

    CREATE TRIGGER profiles_assign_default_access_category
        BEFORE INSERT ON profiles
        FOR EACH ROW
        EXECUTE FUNCTION assign_default_resource_access_category();
    CREATE TRIGGER devices_assign_default_access_category
        BEFORE INSERT ON devices
        FOR EACH ROW
        EXECUTE FUNCTION assign_default_resource_access_category();

    CREATE FUNCTION assign_default_session_access_category()
    RETURNS trigger
    LANGUAGE plpgsql
    AS $function$
    BEGIN
        IF NEW.authorization_scope = 'category' AND NEW.category_id IS NULL THEN
            SELECT id INTO NEW.category_id
            FROM access_categories
            WHERE is_default;
        END IF;
        RETURN NEW;
    END
    $function$;

    CREATE TRIGGER auth_sessions_assign_default_access_category
        BEFORE INSERT ON auth_sessions
        FOR EACH ROW
        EXECUTE FUNCTION assign_default_session_access_category();

    CREATE FUNCTION assign_default_device_authorization_category()
    RETURNS trigger
    LANGUAGE plpgsql
    AS $function$
    BEGIN
        IF NEW.approved_user_id IS NOT NULL AND NEW.approved_category_id IS NULL THEN
            SELECT id INTO NEW.approved_category_id
            FROM access_categories
            WHERE is_default;
        END IF;
        RETURN NEW;
    END
    $function$;

    CREATE TRIGGER device_authorizations_assign_default_access_category
        BEFORE INSERT OR UPDATE OF approved_user_id, approved_category_id ON device_authorizations
        FOR EACH ROW
        EXECUTE FUNCTION assign_default_device_authorization_category();

    CREATE TABLE access_category_audit_events (
        id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
        action text NOT NULL CHECK (action = btrim(action) AND action <> ''),
        entity_type text NOT NULL CHECK (entity_type IN ('category', 'profile', 'device')),
        entity_id uuid NOT NULL,
        old_category_id uuid,
        new_category_id uuid,
        details jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
        created_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE INDEX profiles_category_id_idx ON profiles (category_id, lower(name), id);
    CREATE INDEX devices_category_id_idx ON devices (category_id, lower(name), id);
    CREATE INDEX auth_sessions_category_id_idx
        ON auth_sessions (category_id, revoked_at, last_seen_at DESC);
    CREATE INDEX device_authorizations_approved_category_id_idx
        ON device_authorizations (approved_category_id)
        WHERE approved_category_id IS NOT NULL;
    CREATE INDEX access_category_audit_events_entity_idx
        ON access_category_audit_events (entity_type, entity_id, created_at DESC);
    CREATE INDEX access_category_audit_events_actor_idx
        ON access_category_audit_events (actor_user_id, created_at DESC);
END
$migration$;
