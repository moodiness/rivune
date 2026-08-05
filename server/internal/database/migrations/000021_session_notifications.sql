
CREATE TABLE IF NOT EXISTS auth_session_notifications (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    sender_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '7 days'),
    CHECK (expires_at > created_at)
);

CREATE INDEX IF NOT EXISTS auth_session_notifications_session_id_id_idx
    ON auth_session_notifications (session_id, id);
CREATE INDEX IF NOT EXISTS auth_session_notifications_expires_at_idx
    ON auth_session_notifications (expires_at);
