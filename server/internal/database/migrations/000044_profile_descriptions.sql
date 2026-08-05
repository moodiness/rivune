
ALTER TABLE profiles
    ADD COLUMN IF NOT EXISTS description text;

DO $migration$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'profiles'::regclass
          AND conname = 'profiles_description_check'
    ) THEN
        ALTER TABLE profiles
            ADD CONSTRAINT profiles_description_check CHECK (
                description IS NULL OR (
                    description = btrim(description)
                    AND char_length(description) <= 500
                )
            );
    END IF;
END;
$migration$;
