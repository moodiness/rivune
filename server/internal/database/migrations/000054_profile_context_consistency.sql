UPDATE auth_sessions
SET active_profile_id = NULL,
    profile_grant_expires_at = NULL,
    profile_context_hash = NULL
WHERE active_profile_id IS NOT NULL OR profile_grant_expires_at IS NOT NULL OR profile_context_hash IS NOT NULL;

ALTER TABLE auth_sessions
    DROP CONSTRAINT auth_sessions_profile_grant_consistent;

ALTER TABLE auth_sessions
    ADD CONSTRAINT auth_sessions_profile_grant_consistent CHECK (
        (active_profile_id IS NULL AND profile_grant_expires_at IS NULL AND profile_context_hash IS NULL)
        OR
        (active_profile_id IS NOT NULL AND profile_grant_expires_at IS NOT NULL AND profile_context_hash IS NOT NULL)
    );

CREATE OR REPLACE FUNCTION clear_profile_session_grants()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    UPDATE auth_sessions
    SET active_profile_id = NULL,
        profile_grant_expires_at = NULL,
        profile_context_hash = NULL
    WHERE active_profile_id = OLD.id;
    RETURN OLD;
END
$function$;
