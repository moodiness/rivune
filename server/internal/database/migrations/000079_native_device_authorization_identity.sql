ALTER TABLE device_authorizations
    ADD COLUMN native_installation_hash bytea CHECK (
        native_installation_hash IS NULL OR octet_length(native_installation_hash) = 32
    );

-- Native authorizations are short-lived and cannot satisfy the new client
-- identity invariant. Force clients to request a fresh code after upgrade.
DELETE FROM device_authorizations WHERE purpose = 'native';

ALTER TABLE device_authorizations
    DROP CONSTRAINT device_authorizations_purpose_state_consistent,
    ADD CONSTRAINT device_authorizations_purpose_state_consistent CHECK (
        (purpose = 'native'
            AND native_installation_hash IS NOT NULL
            AND initiating_client_device_id IS NULL
            AND initiating_app_version IS NULL
            AND approved_profile_id IS NULL)
        OR (purpose = 'jellyfin_quick_connect'
            AND native_installation_hash IS NULL
            AND initiating_client_device_id IS NOT NULL
            AND initiating_app_version IS NOT NULL
            AND (
                (approved_user_id IS NULL AND approved_profile_id IS NULL)
                OR (approved_user_id IS NOT NULL AND approved_profile_id IS NOT NULL)
            ))
    );

CREATE UNIQUE INDEX device_authorizations_native_installation_active_key
    ON device_authorizations (native_installation_hash)
    WHERE purpose = 'native' AND consumed_at IS NULL;
