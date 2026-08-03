BEGIN;

ALTER TABLE titles
    DROP CONSTRAINT titles_media_type_check,
    DROP CONSTRAINT titles_hierarchy_check,
    ADD COLUMN source_addon_id uuid,
    ADD COLUMN source_catalog_id text,
    ADD COLUMN source_name text,
    ADD COLUMN country text,
    ADD COLUMN language text,
    ADD COLUMN category text,
    ADD CONSTRAINT titles_media_type_check
        CHECK (media_type IN ('movie', 'series', 'season', 'episode', 'tv')),
    ADD CONSTRAINT titles_hierarchy_check
        CHECK (
            (media_type IN ('movie', 'series', 'tv') AND parent_id IS NULL AND ordinal IS NULL)
            OR (media_type IN ('season', 'episode') AND parent_id IS NOT NULL AND ordinal >= 0)
        ),
    ADD CONSTRAINT titles_source_catalog_id_check
        CHECK (source_catalog_id IS NULL OR (source_catalog_id = btrim(source_catalog_id) AND char_length(source_catalog_id) BETWEEN 1 AND 512)),
    ADD CONSTRAINT titles_source_name_check
        CHECK (source_name IS NULL OR (source_name = btrim(source_name) AND char_length(source_name) BETWEEN 1 AND 500)),
    ADD CONSTRAINT titles_country_check
        CHECK (country IS NULL OR (country = btrim(country) AND char_length(country) BETWEEN 1 AND 128)),
    ADD CONSTRAINT titles_language_check
        CHECK (language IS NULL OR (language = btrim(language) AND char_length(language) BETWEEN 1 AND 128)),
    ADD CONSTRAINT titles_category_check
        CHECK (category IS NULL OR (category = btrim(category) AND char_length(category) BETWEEN 1 AND 256));

ALTER TABLE title_external_ids
    DROP CONSTRAINT title_external_ids_namespace_check,
    ADD CONSTRAINT title_external_ids_namespace_check
        CHECK (namespace IN ('movie', 'series', 'season', 'episode', 'tv'));

CREATE INDEX titles_source_addon_id_idx
    ON titles (source_addon_id)
    WHERE source_addon_id IS NOT NULL;

CREATE OR REPLACE FUNCTION validate_profile_media_state_title() RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    stored_media_type text;
BEGIN
    SELECT media_type INTO stored_media_type FROM titles WHERE id = NEW.title_id;
    IF TG_TABLE_NAME = 'profile_library' AND stored_media_type NOT IN ('movie', 'series', 'tv') THEN
        RAISE EXCEPTION 'library title must be a movie, series, or TV channel' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'profile_progress' AND stored_media_type NOT IN ('movie', 'episode') THEN
        RAISE EXCEPTION 'progress title must be a movie or episode' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

COMMIT;
