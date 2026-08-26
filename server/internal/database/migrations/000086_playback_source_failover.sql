CREATE TABLE playback_source_failovers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    candidate_refs jsonb NOT NULL CHECK (jsonb_typeof(candidate_refs) = 'array' AND jsonb_array_length(candidate_refs) BETWEEN 2 AND 8),
    failed_candidates jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(failed_candidates) = 'array' AND jsonb_array_length(failed_candidates) <= 8),
    current_source_ref text NOT NULL CHECK (char_length(current_source_ref) BETWEEN 16 AND 128),
    position_seconds double precision NOT NULL DEFAULT 0 CHECK (position_seconds >= 0 AND position_seconds <= 86400),
    attempt_count smallint NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    max_attempts smallint NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 3),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'exhausted', 'cancelled')),
    last_error text CHECK (last_error IS NULL OR last_error IN ('source_failed', 'source_timeout', 'ended_early')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE INDEX playback_source_failovers_owner_idx
    ON playback_source_failovers (auth_session_id, profile_id, updated_at DESC);
CREATE INDEX playback_source_failovers_expiry_idx
    ON playback_source_failovers (expires_at);
