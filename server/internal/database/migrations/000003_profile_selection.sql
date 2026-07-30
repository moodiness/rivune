DO $migration$
BEGIN
    ALTER TABLE auth_sessions
        ADD COLUMN active_profile_id uuid REFERENCES profiles(id) ON DELETE SET NULL,
        ADD COLUMN profile_grant_expires_at timestamptz;

    ALTER TABLE auth_sessions
        ADD CONSTRAINT auth_sessions_profile_grant_consistent CHECK (
            (active_profile_id IS NULL AND profile_grant_expires_at IS NULL)
            OR (active_profile_id IS NOT NULL AND profile_grant_expires_at IS NOT NULL)
        );

    CREATE INDEX auth_sessions_active_profile_idx
        ON auth_sessions (active_profile_id)
        WHERE active_profile_id IS NOT NULL AND revoked_at IS NULL;

    CREATE OR REPLACE FUNCTION clear_profile_session_grants()
    RETURNS trigger
    LANGUAGE plpgsql
    AS $function$
    BEGIN
        UPDATE auth_sessions
        SET active_profile_id = NULL, profile_grant_expires_at = NULL
        WHERE active_profile_id = OLD.id;
        RETURN OLD;
    END
    $function$;

    CREATE TRIGGER profiles_clear_session_grants
        BEFORE DELETE ON profiles
        FOR EACH ROW
        EXECUTE FUNCTION clear_profile_session_grants();
END
$migration$;
