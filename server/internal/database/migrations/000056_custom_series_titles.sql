ALTER TABLE titles
    ADD COLUMN is_current boolean NOT NULL DEFAULT true;

ALTER TABLE profile_title_external_ids
    DROP CONSTRAINT IF EXISTS profile_title_external_ids_namespace_check,
    ADD CONSTRAINT profile_title_external_ids_namespace_check
        CHECK (namespace IN ('movie', 'series', 'season', 'episode'));

ALTER TABLE titles
    DROP CONSTRAINT IF EXISTS titles_parent_ordinal_unique;

CREATE UNIQUE INDEX titles_parent_ordinal_unique
    ON titles (parent_id, media_type, ordinal)
    WHERE is_current;
