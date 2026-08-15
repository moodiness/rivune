ALTER TABLE profile_title_external_ids
    DROP CONSTRAINT profile_title_external_ids_namespace_check,
    ADD CONSTRAINT profile_title_external_ids_namespace_check
        CHECK (namespace IN ('movie', 'series', 'season', 'episode', 'tv'));
