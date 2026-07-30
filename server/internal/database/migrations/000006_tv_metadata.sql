DO $migration$
BEGIN
    ALTER TABLE titles
        DROP CONSTRAINT titles_media_type_check,
        ADD COLUMN parent_id uuid REFERENCES titles(id) ON DELETE CASCADE,
        ADD COLUMN ordinal integer;

    ALTER TABLE titles
        ADD CONSTRAINT titles_media_type_check
            CHECK (media_type IN ('movie', 'series', 'season', 'episode')),
        ADD CONSTRAINT titles_hierarchy_check
            CHECK (
                (media_type IN ('movie', 'series') AND parent_id IS NULL AND ordinal IS NULL)
                OR (media_type IN ('season', 'episode') AND parent_id IS NOT NULL AND ordinal >= 0)
            ),
        ADD CONSTRAINT titles_parent_ordinal_unique
            UNIQUE (parent_id, media_type, ordinal);

    ALTER TABLE title_external_ids
        ADD COLUMN namespace text NOT NULL DEFAULT 'movie'
            CHECK (namespace IN ('movie', 'series', 'season', 'episode'));

    ALTER TABLE title_external_ids
        DROP CONSTRAINT title_external_ids_pkey,
        ADD PRIMARY KEY (provider, namespace, external_id);
END
$migration$;
