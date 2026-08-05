
ALTER TABLE playback_sessions
    ADD COLUMN IF NOT EXISTS last_seen_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS playback_sessions_activity_idx
    ON playback_sessions (last_seen_at DESC, created_at DESC);
