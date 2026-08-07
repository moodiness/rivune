UPDATE profile_settings
SET settings = settings - 'jellyfinEnabled',
    updated_at = now()
WHERE settings ? 'jellyfinEnabled';

ALTER TABLE profile_settings
    DROP CONSTRAINT IF EXISTS profile_settings_server_only_jellyfin_enabled,
    ADD CONSTRAINT profile_settings_server_only_jellyfin_enabled
    CHECK (NOT (settings ? 'jellyfinEnabled'));

ALTER TABLE instance_settings
    DROP CONSTRAINT IF EXISTS instance_settings_jellyfin_enabled_boolean,
    ADD CONSTRAINT instance_settings_jellyfin_enabled_boolean
    CHECK (
        NOT (settings ? 'jellyfinEnabled')
        OR jsonb_typeof(settings -> 'jellyfinEnabled') = 'boolean'
    );
