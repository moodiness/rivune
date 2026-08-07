CREATE TABLE jellyfin_compat_devices (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_device_id text NOT NULL CHECK (
        char_length(client_device_id) BETWEEN 1 AND 128 AND client_device_id = btrim(client_device_id)
    ),
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, client_device_id),
    UNIQUE (user_id, device_id)
);

CREATE INDEX jellyfin_compat_devices_device_idx
    ON jellyfin_compat_devices (device_id);

CREATE TABLE jellyfin_compat_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    client_name text NOT NULL CHECK (
        char_length(client_name) BETWEEN 1 AND 64 AND client_name = btrim(client_name)
    ),
    device_name text NOT NULL CHECK (
        char_length(device_name) BETWEEN 1 AND 120 AND device_name = btrim(device_name)
    ),
    client_device_id text NOT NULL CHECK (
        char_length(client_device_id) BETWEEN 1 AND 128 AND client_device_id = btrim(client_device_id)
    ),
    client_version text NOT NULL CHECK (
        char_length(client_version) BETWEEN 1 AND 32 AND client_version = btrim(client_version)
    ),
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    revoked_reason text CHECK (
        revoked_reason IS NULL OR (
            char_length(revoked_reason) BETWEEN 1 AND 128 AND revoked_reason = btrim(revoked_reason)
        )
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at),
    CHECK (
        (revoked_at IS NULL AND revoked_reason IS NULL)
        OR (revoked_at IS NOT NULL AND revoked_reason IS NOT NULL)
    )
);

CREATE INDEX jellyfin_compat_sessions_auth_session_idx
    ON jellyfin_compat_sessions (auth_session_id, profile_id);
CREATE INDEX jellyfin_compat_sessions_client_device_idx
    ON jellyfin_compat_sessions (client_device_id, last_seen_at DESC, auth_session_id);
CREATE INDEX jellyfin_compat_sessions_active_token_idx
    ON jellyfin_compat_sessions (token_hash, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX jellyfin_compat_sessions_active_expiry_idx
    ON jellyfin_compat_sessions (expires_at, id)
    WHERE revoked_at IS NULL;

CREATE FUNCTION revoke_linked_jellyfin_compat_sessions()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL THEN
        UPDATE jellyfin_compat_sessions
        SET revoked_at = NEW.revoked_at,
            revoked_reason = COALESCE(NEW.revoked_reason, 'linked_session_revoked')
        WHERE auth_session_id = NEW.id AND revoked_at IS NULL;
    END IF;
    RETURN NEW;
END
$function$;

CREATE TRIGGER auth_sessions_revoke_jellyfin_compat
    AFTER UPDATE OF revoked_at ON auth_sessions
    FOR EACH ROW
    EXECUTE FUNCTION revoke_linked_jellyfin_compat_sessions();
