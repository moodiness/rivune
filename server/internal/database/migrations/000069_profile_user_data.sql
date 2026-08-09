CREATE TABLE profile_user_data (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    rating double precision CHECK (rating >= 0 AND rating <= 10),
    rating_set boolean NOT NULL DEFAULT false CHECK (rating_set OR rating IS NULL),
    played_percentage double precision CHECK (played_percentage >= 0 AND played_percentage <= 100),
    played_percentage_set boolean NOT NULL DEFAULT false CHECK (played_percentage_set OR played_percentage IS NULL),
    unplayed_item_count integer CHECK (unplayed_item_count >= 0),
    unplayed_item_count_set boolean NOT NULL DEFAULT false CHECK (unplayed_item_count_set OR unplayed_item_count IS NULL),
    play_count integer CHECK (play_count >= 0),
    play_count_set boolean NOT NULL DEFAULT false CHECK ((play_count_set AND play_count IS NOT NULL) OR (NOT play_count_set AND play_count IS NULL)),
    likes boolean,
    likes_set boolean NOT NULL DEFAULT false CHECK (likes_set OR likes IS NULL),
    last_played_date timestamptz,
    last_played_date_submicrosecond smallint CHECK (last_played_date_submicrosecond >= 0 AND last_played_date_submicrosecond <= 999),
    last_played_date_set boolean NOT NULL DEFAULT false
        CHECK (last_played_date_set OR (last_played_date IS NULL AND last_played_date_submicrosecond IS NULL)),
    CHECK ((last_played_date IS NULL) = (last_played_date_submicrosecond IS NULL)),
    CHECK (last_played_date IS NULL OR (
        isfinite(last_played_date)
        AND EXTRACT(YEAR FROM last_played_date AT TIME ZONE 'UTC') BETWEEN 1 AND 9999
    )),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, title_id)
);
