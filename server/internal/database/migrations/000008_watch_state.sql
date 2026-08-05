
CREATE TABLE IF NOT EXISTS profile_library (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    added_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, title_id)
);

CREATE INDEX IF NOT EXISTS profile_library_profile_added_at_idx
    ON profile_library (profile_id, added_at DESC, title_id);

CREATE TABLE IF NOT EXISTS profile_progress (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    position_seconds integer NOT NULL CHECK (position_seconds >= 0),
    duration_seconds integer NOT NULL CHECK (duration_seconds >= 0),
    completed boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    last_watched_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, title_id),
    CHECK (position_seconds <= duration_seconds OR duration_seconds = 0)
);

CREATE INDEX IF NOT EXISTS profile_progress_continue_idx
    ON profile_progress (profile_id, completed, last_watched_at DESC, title_id);

CREATE OR REPLACE FUNCTION validate_profile_media_state_title() RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    stored_media_type text;
BEGIN
    SELECT media_type INTO stored_media_type FROM titles WHERE id = NEW.title_id;
    IF TG_TABLE_NAME = 'profile_library' AND stored_media_type NOT IN ('movie', 'series') THEN
        RAISE EXCEPTION 'library title must be a movie or series' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'profile_progress' AND stored_media_type NOT IN ('movie', 'episode') THEN
        RAISE EXCEPTION 'progress title must be a movie or episode' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

DROP TRIGGER IF EXISTS profile_library_title_type_check ON profile_library;

CREATE TRIGGER profile_library_title_type_check
    BEFORE INSERT OR UPDATE OF title_id ON profile_library
    FOR EACH ROW EXECUTE FUNCTION validate_profile_media_state_title();

DROP TRIGGER IF EXISTS profile_progress_title_type_check ON profile_progress;

CREATE TRIGGER profile_progress_title_type_check
    BEFORE INSERT OR UPDATE OF title_id ON profile_progress
    FOR EACH ROW EXECUTE FUNCTION validate_profile_media_state_title();
