CREATE TABLE profile_accessibility_preferences (
    profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    reduced_motion text NOT NULL DEFAULT 'system' CHECK (reduced_motion IN ('system', 'reduce', 'no-preference')),
    high_contrast text NOT NULL DEFAULT 'system' CHECK (high_contrast IN ('system', 'more', 'standard')),
    text_scale smallint NOT NULL DEFAULT 100 CHECK (text_scale IN (100, 115, 130)),
    captions text NOT NULL DEFAULT 'system' CHECK (captions IN ('system', 'on', 'off')),
    audio_description boolean NOT NULL DEFAULT false,
    focus_indicators text NOT NULL DEFAULT 'standard' CHECK (focus_indicators IN ('standard', 'enhanced')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
