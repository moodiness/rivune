-- Migration 64 reached development databases before the credential-owner lifecycle
-- was hardened. Reapply the final constraints and trigger for those databases;
-- these operations are also safe after the corrected migration 64 on a fresh install.
ALTER TABLE profile_jellyfin_credentials
    DROP CONSTRAINT profile_jellyfin_credentials_state_consistent,
    DROP CONSTRAINT profile_jellyfin_credentials_owner_user_id_fkey,
    ALTER COLUMN owner_user_id DROP NOT NULL;

ALTER TABLE profile_jellyfin_credentials
    ADD CONSTRAINT profile_jellyfin_credentials_owner_user_id_fkey
        FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE SET NULL,
    ADD CONSTRAINT profile_jellyfin_credentials_state_consistent CHECK (
        (password_hash IS NOT NULL AND revoked_at IS NULL AND owner_user_id IS NOT NULL)
        OR (password_hash IS NULL AND revoked_at IS NOT NULL)
    );

CREATE OR REPLACE FUNCTION revoke_deleted_jellyfin_credential_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    UPDATE auth_sessions
    SET revoked_at = COALESCE(revoked_at, now()),
        revoked_reason = COALESCE(revoked_reason, 'jellyfin_credential_owner_deleted')
    WHERE jellyfin_credential_id IN (
        SELECT id FROM profile_jellyfin_credentials WHERE owner_user_id = OLD.id
    );
    UPDATE profile_jellyfin_credentials
    SET password_hash = NULL,
        generation = generation + 1,
        revoked_at = COALESCE(revoked_at, now())
    WHERE owner_user_id = OLD.id AND password_hash IS NOT NULL;
    RETURN OLD;
END
$function$;

DROP TRIGGER IF EXISTS users_revoke_owned_jellyfin_credentials ON users;
CREATE TRIGGER users_revoke_owned_jellyfin_credentials
    BEFORE DELETE ON users
    FOR EACH ROW
    EXECUTE FUNCTION revoke_deleted_jellyfin_credential_owner();
