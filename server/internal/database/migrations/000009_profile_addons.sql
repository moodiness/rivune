
CREATE TABLE IF NOT EXISTS profile_addons (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    transport_url text NOT NULL,
    manifest jsonb NOT NULL,
    manifest_id text NOT NULL,
    manifest_version text NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    installed_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (transport_url = btrim(transport_url) AND length(transport_url) BETWEEN 1 AND 8192),
    CHECK (manifest_id = btrim(manifest_id) AND length(manifest_id) BETWEEN 1 AND 512),
    CHECK (manifest_version = btrim(manifest_version) AND length(manifest_version) BETWEEN 1 AND 128),
    UNIQUE (profile_id, transport_url),
    UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS profile_addons_profile_manifest_idx
    ON profile_addons (profile_id, manifest_id, position);
