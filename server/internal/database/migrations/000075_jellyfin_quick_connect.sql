ALTER TABLE device_authorizations
    ADD COLUMN purpose text NOT NULL DEFAULT 'native'
        CHECK (purpose IN ('native', 'jellyfin_quick_connect')),
    ADD COLUMN initiating_client_device_id text CHECK (
        initiating_client_device_id IS NULL OR (
            char_length(initiating_client_device_id) BETWEEN 1 AND 128
            AND initiating_client_device_id = btrim(initiating_client_device_id)
        )
    ),
    ADD COLUMN initiating_app_version text CHECK (
        initiating_app_version IS NULL OR (
            char_length(initiating_app_version) BETWEEN 1 AND 32
            AND initiating_app_version = btrim(initiating_app_version)
        )
    ),
    ADD COLUMN approved_profile_id uuid REFERENCES profiles(id) ON DELETE CASCADE,
    ADD CONSTRAINT device_authorizations_purpose_state_consistent CHECK (
        (purpose = 'native'
            AND initiating_client_device_id IS NULL
            AND initiating_app_version IS NULL
            AND approved_profile_id IS NULL)
        OR (purpose = 'jellyfin_quick_connect'
            AND initiating_client_device_id IS NOT NULL
            AND initiating_app_version IS NOT NULL
            AND (
                (approved_user_id IS NULL AND approved_profile_id IS NULL)
                OR (approved_user_id IS NOT NULL AND approved_profile_id IS NOT NULL)
            ))
    );

CREATE INDEX device_authorizations_approved_profile_id_idx
    ON device_authorizations (approved_profile_id)
    WHERE approved_profile_id IS NOT NULL;
