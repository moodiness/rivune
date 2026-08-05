CREATE TABLE calendar_refresh_state (
    profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
    after_title_id uuid,
    resume_title_id uuid,
    resume_after_season_number integer,
    resume_after_season_id text,
    range_from date NOT NULL,
    range_to date NOT NULL,
    language text NOT NULL,
    claim_token text,
    claim_expires_at timestamptz,
    next_eligible_at timestamptz NOT NULL DEFAULT '-infinity',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT calendar_refresh_season_cursor_complete CHECK (
        (resume_after_season_number IS NULL AND resume_after_season_id IS NULL)
        OR (
            resume_title_id IS NOT NULL
            AND resume_after_season_number IS NOT NULL
            AND resume_after_season_id IS NOT NULL
        )
    ),
    CONSTRAINT calendar_refresh_claim_complete CHECK (
        (claim_token IS NULL) = (claim_expires_at IS NULL)
    )
);
