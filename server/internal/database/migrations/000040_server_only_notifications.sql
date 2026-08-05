
UPDATE profile_settings
SET settings = settings
    - 'notificationsEnabled'
    - 'notificationDurationSeconds'
    - 'notificationPollIntervalSeconds',
    updated_at = now()
WHERE settings ?| ARRAY[
    'notificationsEnabled',
    'notificationDurationSeconds',
    'notificationPollIntervalSeconds'
];

ALTER TABLE profile_settings
    DROP CONSTRAINT IF EXISTS profile_settings_server_only_notifications,
    ADD CONSTRAINT profile_settings_server_only_notifications
    CHECK (NOT (settings ?| ARRAY[
        'notificationsEnabled',
        'notificationDurationSeconds',
        'notificationPollIntervalSeconds'
    ]));
