BEGIN;

ALTER TABLE titles
    ADD COLUMN display_title text,
    ADD COLUMN poster_url text,
    ADD COLUMN background_url text,
    ADD COLUMN release_info text,
    ADD COLUMN resource_id text,
    ADD COLUMN resource_provider text,
    ADD CONSTRAINT titles_display_title_check CHECK (display_title IS NULL OR (display_title = btrim(display_title) AND char_length(display_title) BETWEEN 1 AND 500)),
    ADD CONSTRAINT titles_poster_url_check CHECK (poster_url IS NULL OR char_length(poster_url) BETWEEN 1 AND 4096),
    ADD CONSTRAINT titles_background_url_check CHECK (background_url IS NULL OR char_length(background_url) BETWEEN 1 AND 4096),
    ADD CONSTRAINT titles_release_info_check CHECK (release_info IS NULL OR char_length(release_info) BETWEEN 1 AND 120),
    ADD CONSTRAINT titles_resource_id_check CHECK (resource_id IS NULL OR (resource_id = btrim(resource_id) AND char_length(resource_id) BETWEEN 1 AND 512)),
    ADD CONSTRAINT titles_resource_provider_check CHECK (resource_provider IS NULL OR (resource_provider = lower(resource_provider) AND resource_provider ~ '^[a-z0-9][a-z0-9._-]{0,31}$'));

COMMIT;
