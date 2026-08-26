CREATE TABLE addon_verifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    requested_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_by_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    addon_id uuid REFERENCES profile_addons(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    transport_url text NOT NULL,
    manifest jsonb,
    manifest_id text,
    manifest_version text,
    profile_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    category_ids uuid[] NOT NULL DEFAULT ARRAY[]::uuid[],
    status text NOT NULL CHECK (status IN ('passed', 'failed')),
    checks jsonb NOT NULL CHECK (jsonb_typeof(checks) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '15 minutes'),
    CHECK (consumed_at IS NULL OR consumed_at >= created_at),
    CHECK ((status = 'passed') = (manifest IS NOT NULL AND manifest_id IS NOT NULL AND manifest_version IS NOT NULL))
);

CREATE INDEX addon_verifications_candidate_history_idx
    ON addon_verifications (requested_by_user_id, created_at DESC, id)
    WHERE addon_id IS NULL;
CREATE INDEX addon_verifications_addon_history_idx
    ON addon_verifications (addon_id, created_at DESC, id)
    WHERE addon_id IS NOT NULL;
CREATE INDEX addon_verifications_expiry_idx
    ON addon_verifications (expires_at, id)
    WHERE consumed_at IS NULL;
