CREATE TABLE profile_favorites (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, title_id)
);

CREATE INDEX profile_favorites_profile_updated_at_idx
    ON profile_favorites (profile_id, updated_at DESC, title_id);

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
    IF TG_TABLE_NAME = 'profile_favorites' AND stored_media_type NOT IN ('movie', 'series', 'season', 'episode', 'tv') THEN
        RAISE EXCEPTION 'favorite title must be a projected video type' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

CREATE TRIGGER profile_favorites_title_type_check
    BEFORE INSERT OR UPDATE OF title_id ON profile_favorites
    FOR EACH ROW EXECUTE FUNCTION validate_profile_media_state_title();
