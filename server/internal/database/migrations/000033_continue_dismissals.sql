
CREATE TABLE IF NOT EXISTS profile_continue_dismissals (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    dismissed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, title_id)
);
