CREATE TABLE introdb_segment_cache (
    imdb_id text NOT NULL,
    season_number integer NOT NULL CHECK (season_number > 0),
    episode_number integer NOT NULL CHECK (episode_number > 0),
    segments jsonb NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (imdb_id, season_number, episode_number),
    CHECK (imdb_id ~ '^tt[0-9]{7,8}$'),
    CHECK (jsonb_typeof(segments) = 'array')
);

CREATE INDEX introdb_segment_cache_expiry_idx ON introdb_segment_cache (expires_at);
