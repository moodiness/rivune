CREATE TABLE jellyfin_display_preferences (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    client text NOT NULL CHECK (
        char_length(client) BETWEEN 1 AND 64
        AND client = btrim(client)
        AND octet_length(client) <= 256
    ),
    display_preferences_id text NOT NULL CHECK (
        char_length(display_preferences_id) BETWEEN 1 AND 64
        AND display_preferences_id = btrim(display_preferences_id)
        AND display_preferences_id ~ '^[A-Za-z0-9._-]+$'
    ),
    preferences jsonb NOT NULL CHECK (
        jsonb_typeof(preferences) = 'object'
        AND octet_length(preferences::text) <= 32768
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, profile_id, client, display_preferences_id)
);

CREATE INDEX jellyfin_display_preferences_profile_idx
    ON jellyfin_display_preferences (profile_id, user_id, updated_at DESC);
