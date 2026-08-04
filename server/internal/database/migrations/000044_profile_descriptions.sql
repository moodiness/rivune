BEGIN;

ALTER TABLE profiles
    ADD COLUMN description text,
    ADD CONSTRAINT profiles_description_check CHECK (
        description IS NULL OR (
            description = btrim(description)
            AND char_length(description) <= 500
        )
    );

COMMIT;
