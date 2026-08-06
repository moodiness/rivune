ALTER TABLE profile_addons
    ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;
