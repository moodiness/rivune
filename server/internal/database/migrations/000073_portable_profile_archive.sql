CREATE TABLE IF NOT EXISTS portable_profile_resource_bindings (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    resource_kind text NOT NULL CHECK (resource_kind IN ('addon', 'collection', 'title')),
    portable_key text NOT NULL CHECK (portable_key ~ '^sha256:[0-9a-f]{64}$'),
    resource_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, resource_kind, portable_key),
    UNIQUE (profile_id, resource_kind, resource_id)
);

CREATE TABLE IF NOT EXISTS profile_tracking_preferences (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('trakt', 'simkl')),
    sync_watched boolean NOT NULL DEFAULT true,
    sync_progress boolean NOT NULL DEFAULT true,
    sync_library boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, provider)
);

INSERT INTO profile_tracking_preferences (
    profile_id, provider, sync_watched, sync_progress, sync_library, updated_at
)
SELECT profile_id, provider, sync_watched, sync_progress, sync_library, updated_at
FROM profile_tracking_accounts
ON CONFLICT (profile_id, provider) DO UPDATE
SET sync_watched = EXCLUDED.sync_watched,
    sync_progress = EXCLUDED.sync_progress,
    sync_library = EXCLUDED.sync_library,
    updated_at = EXCLUDED.updated_at;

CREATE OR REPLACE FUNCTION validate_portable_profile_resource_binding() RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF NEW.resource_kind = 'addon' AND NOT EXISTS (
        SELECT 1 FROM addon_profile_access
        WHERE profile_id = NEW.profile_id AND addon_id = NEW.resource_id
    ) THEN
        RAISE EXCEPTION 'portable add-on binding must belong to the profile' USING ERRCODE = '23514';
    ELSIF NEW.resource_kind = 'collection' AND NOT EXISTS (
        SELECT 1 FROM collection_profile_access
        WHERE profile_id = NEW.profile_id AND collection_id = NEW.resource_id
    ) THEN
        RAISE EXCEPTION 'portable collection binding must belong to the profile' USING ERRCODE = '23514';
    ELSIF NEW.resource_kind = 'title' AND NOT EXISTS (
        SELECT 1 FROM titles WHERE id = NEW.resource_id
    ) THEN
        RAISE EXCEPTION 'portable title binding must reference a title' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS portable_profile_resource_binding_check ON portable_profile_resource_bindings;
CREATE TRIGGER portable_profile_resource_binding_check
    BEFORE INSERT OR UPDATE ON portable_profile_resource_bindings
    FOR EACH ROW EXECUTE FUNCTION validate_portable_profile_resource_binding();

CREATE OR REPLACE FUNCTION delete_portable_profile_resource_binding() RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF TG_TABLE_NAME = 'addon_profile_access' THEN
        DELETE FROM portable_profile_resource_bindings
        WHERE profile_id = OLD.profile_id AND resource_kind = 'addon' AND resource_id = OLD.addon_id;
    ELSIF TG_TABLE_NAME = 'collection_profile_access' THEN
        DELETE FROM portable_profile_resource_bindings
        WHERE profile_id = OLD.profile_id AND resource_kind = 'collection' AND resource_id = OLD.collection_id;
    ELSIF TG_TABLE_NAME = 'titles' THEN
        DELETE FROM portable_profile_resource_bindings
        WHERE resource_kind = 'title' AND resource_id = OLD.id;
    END IF;
    RETURN OLD;
END
$function$;

DROP TRIGGER IF EXISTS portable_addon_binding_cleanup ON addon_profile_access;
CREATE TRIGGER portable_addon_binding_cleanup
    AFTER DELETE ON addon_profile_access
    FOR EACH ROW EXECUTE FUNCTION delete_portable_profile_resource_binding();

DROP TRIGGER IF EXISTS portable_collection_binding_cleanup ON collection_profile_access;
CREATE TRIGGER portable_collection_binding_cleanup
    AFTER DELETE ON collection_profile_access
    FOR EACH ROW EXECUTE FUNCTION delete_portable_profile_resource_binding();

DROP TRIGGER IF EXISTS portable_title_binding_cleanup ON titles;
CREATE TRIGGER portable_title_binding_cleanup
    AFTER DELETE ON titles
    FOR EACH ROW EXECUTE FUNCTION delete_portable_profile_resource_binding();
