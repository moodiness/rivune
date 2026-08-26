DROP INDEX IF EXISTS profile_media_notification_subscriptions_scan_idx;

CREATE TABLE media_notification_worker_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    after_profile_id uuid,
    after_title_id uuid,
    cycle_started_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((after_profile_id IS NULL) = (after_title_id IS NULL)),
    CHECK ((after_profile_id IS NULL) = (cycle_started_at IS NULL))
);

INSERT INTO media_notification_worker_state (singleton)
VALUES (true)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE media_notification_generation_skips (
    profile_id uuid NOT NULL,
    root_title_id uuid NOT NULL,
    reason text NOT NULL CHECK (reason IN ('series-subject-capacity')),
    occurrence_count bigint NOT NULL DEFAULT 1 CHECK (occurrence_count > 0),
    first_skipped_at timestamptz NOT NULL DEFAULT now(),
    last_skipped_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, root_title_id),
    FOREIGN KEY (profile_id, root_title_id)
        REFERENCES profile_media_notification_subscriptions(profile_id, title_id) ON DELETE CASCADE
);

CREATE INDEX media_notification_generation_skips_last_idx
    ON media_notification_generation_skips (last_skipped_at, profile_id, root_title_id);
