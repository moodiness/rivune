
ALTER TABLE titles
    ADD COLUMN IF NOT EXISTS release_date date;

CREATE INDEX IF NOT EXISTS titles_release_calendar_idx
    ON titles (release_date, media_type, id)
    WHERE release_date IS NOT NULL AND media_type IN ('movie', 'episode');
