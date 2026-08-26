CREATE TABLE profile_media_notification_subscriptions (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    timezone text NOT NULL CHECK (timezone = btrim(timezone) AND length(timezone) BETWEEN 1 AND 64),
    horizon_days smallint NOT NULL DEFAULT 30 CHECK (horizon_days BETWEEN 1 AND 366),
    lead_days smallint NOT NULL DEFAULT 1 CHECK (lead_days BETWEEN 0 AND 30 AND lead_days <= horizon_days),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, title_id)
);


CREATE TABLE profile_media_notification_observations (
    profile_id uuid NOT NULL,
    root_title_id uuid NOT NULL,
    subject_title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    subject_kind text NOT NULL CHECK (subject_kind IN ('season', 'episode')),
    season_number integer NOT NULL CHECK (season_number >= 0),
    episode_number integer,
    first_observed_at timestamptz NOT NULL DEFAULT now(),
    last_observed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, root_title_id, subject_kind, season_number, subject_title_id),
    FOREIGN KEY (profile_id, root_title_id)
        REFERENCES profile_media_notification_subscriptions(profile_id, title_id) ON DELETE CASCADE,
    CHECK (
        (subject_kind = 'season' AND episode_number IS NULL)
        OR (subject_kind = 'episode' AND episode_number IS NOT NULL AND episode_number >= 0)
    )
);

CREATE UNIQUE INDEX profile_media_notification_observation_coordinate_key
    ON profile_media_notification_observations (
        profile_id, root_title_id, subject_kind, season_number, COALESCE(episode_number, -1)
    );

CREATE TABLE profile_media_notifications (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    root_title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    subject_title_id uuid REFERENCES titles(id) ON DELETE SET NULL,
    kind text NOT NULL CHECK (kind IN (
        'calendar-event-upcoming',
        'season-available',
        'episode-available',
        'movie-release'
    )),
    dedupe_key text NOT NULL CHECK (dedupe_key = btrim(dedupe_key) AND length(dedupe_key) BETWEEN 1 AND 240),
    title text NOT NULL CHECK (title = btrim(title) AND length(title) BETWEEN 1 AND 500),
    series_title text CHECK (series_title IS NULL OR (series_title = btrim(series_title) AND length(series_title) BETWEEN 1 AND 500)),
    release_date date,
    season_number integer CHECK (season_number IS NULL OR season_number >= 0),
    episode_number integer CHECK (episode_number IS NULL OR episode_number >= 0),
    available_at timestamptz NOT NULL,
    state text NOT NULL DEFAULT 'scheduled' CHECK (state IN ('scheduled', 'unread', 'read', 'dismissed')),
    read_at timestamptz,
    dismissed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    UNIQUE (profile_id, dedupe_key),
    CHECK ((state = 'read') = (read_at IS NOT NULL)),
    CHECK ((state = 'dismissed') = (dismissed_at IS NOT NULL)),
    CHECK (expires_at > created_at)
);

CREATE INDEX profile_media_notifications_inbox_idx
    ON profile_media_notifications (profile_id, id DESC)
    WHERE state IN ('unread', 'read');
CREATE INDEX profile_media_notifications_due_idx
    ON profile_media_notifications (available_at, id)
    WHERE state = 'scheduled';
CREATE INDEX profile_media_notifications_expiry_idx
    ON profile_media_notifications (expires_at, id);
