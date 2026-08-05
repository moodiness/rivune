
ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS avatar_preset text NOT NULL DEFAULT 'aurora'
    CHECK (avatar_preset = btrim(avatar_preset) AND length(avatar_preset) BETWEEN 1 AND 64);

CREATE TABLE IF NOT EXISTS profile_avatar_images (
    profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
    content_type text NOT NULL DEFAULT 'image/png' CHECK (content_type = 'image/png'),
    image_data bytea NOT NULL CHECK (octet_length(image_data) BETWEEN 1 AND 2097152),
    updated_at timestamptz NOT NULL DEFAULT now()
);
