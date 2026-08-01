BEGIN;

CREATE TABLE auth_notification_broadcasts (
    id uuid PRIMARY KEY,
    sender_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message text CHECK (message IS NULL OR char_length(message) BETWEEN 1 AND 500),
    message_fingerprint text NOT NULL CHECK (message_fingerprint ~ '^[0-9a-f]{64}$'),
    recipient_count bigint NOT NULL DEFAULT 0 CHECK (recipient_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '7 days'),
    CHECK (expires_at > created_at)
);

ALTER TABLE auth_session_notifications
    ADD COLUMN broadcast_id uuid REFERENCES auth_notification_broadcasts(id) ON DELETE CASCADE,
    ADD COLUMN acknowledged_at timestamptz,
    ADD CONSTRAINT auth_session_notifications_acknowledged_at_check
        CHECK (acknowledged_at IS NULL OR acknowledged_at >= created_at);

CREATE UNIQUE INDEX auth_session_notifications_broadcast_session_idx
    ON auth_session_notifications (broadcast_id, session_id)
    WHERE broadcast_id IS NOT NULL;

CREATE INDEX auth_notification_broadcasts_unscrubbed_expires_at_idx
    ON auth_notification_broadcasts (expires_at)
    WHERE message IS NOT NULL;

COMMIT;
