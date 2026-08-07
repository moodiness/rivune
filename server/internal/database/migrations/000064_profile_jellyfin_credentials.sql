CREATE TABLE profile_jellyfin_credentials (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL UNIQUE REFERENCES profiles(id) ON DELETE CASCADE,
    owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    password_hash bytea CHECK (password_hash IS NULL OR octet_length(password_hash) = 32),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    rotated_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT profile_jellyfin_credentials_state_consistent CHECK (
        (password_hash IS NOT NULL AND revoked_at IS NULL AND owner_user_id IS NOT NULL)
        OR (password_hash IS NULL AND revoked_at IS NOT NULL)
    )
);

CREATE INDEX profile_jellyfin_credentials_owner_idx
    ON profile_jellyfin_credentials (owner_user_id, profile_id);
CREATE UNIQUE INDEX profile_jellyfin_credentials_password_hash_key
    ON profile_jellyfin_credentials (password_hash)
    WHERE password_hash IS NOT NULL;


ALTER TABLE auth_sessions
    ADD COLUMN jellyfin_credential_id uuid
        REFERENCES profile_jellyfin_credentials(id) ON DELETE CASCADE,
    ADD COLUMN jellyfin_credential_generation bigint,
    ADD CONSTRAINT auth_sessions_jellyfin_credential_consistent CHECK (
        (jellyfin_credential_id IS NULL AND jellyfin_credential_generation IS NULL)
        OR (
            jellyfin_credential_id IS NOT NULL
            AND jellyfin_credential_generation IS NOT NULL
            AND jellyfin_credential_generation > 0
        )
    );

CREATE INDEX auth_sessions_jellyfin_credential_idx
    ON auth_sessions (jellyfin_credential_id, jellyfin_credential_generation)
    WHERE jellyfin_credential_id IS NOT NULL;

CREATE FUNCTION revoke_deleted_jellyfin_credential_owner()
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

CREATE TRIGGER users_revoke_owned_jellyfin_credentials
    BEFORE DELETE ON users
    FOR EACH ROW
    EXECUTE FUNCTION revoke_deleted_jellyfin_credential_owner();


-- Every compatibility session created before this migration was derived from
-- a Rivune account password. Revoke both sides before replacing the device
-- mapping so no password-derived bearer remains usable after the cutover.
UPDATE auth_sessions native_session
SET revoked_at = COALESCE(native_session.revoked_at, now()),
    revoked_reason = COALESCE(native_session.revoked_reason, 'jellyfin_profile_credential_cutover')
WHERE EXISTS (
    SELECT 1
    FROM jellyfin_compat_sessions compat_session
    WHERE compat_session.auth_session_id = native_session.id
)
OR EXISTS (
    SELECT 1
    FROM jellyfin_compat_devices compat_device
    WHERE compat_device.user_id = native_session.user_id
      AND compat_device.device_id = native_session.device_id
);

UPDATE jellyfin_compat_sessions
SET revoked_at = COALESCE(revoked_at, now()),
    revoked_reason = COALESCE(revoked_reason, 'jellyfin_profile_credential_cutover')
WHERE revoked_at IS NULL;

-- Compatibility mappings own dedicated device rows. Remove them after session
-- revocation so they cannot consume the native per-user device quota forever.
DELETE FROM devices
WHERE id IN (SELECT device_id FROM jellyfin_compat_devices);

DROP TABLE jellyfin_compat_devices;

CREATE TABLE jellyfin_compat_devices (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    client_device_id text NOT NULL CHECK (
        char_length(client_device_id) BETWEEN 1 AND 128 AND client_device_id = btrim(client_device_id)
    ),
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, profile_id, client_device_id),
    UNIQUE (user_id, profile_id, device_id)
);

CREATE INDEX jellyfin_compat_devices_device_idx
    ON jellyfin_compat_devices (device_id);
