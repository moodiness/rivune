ALTER TABLE titles
    ADD COLUMN hierarchy_variant text NOT NULL DEFAULT '';

ALTER TABLE titles
    DROP CONSTRAINT titles_hierarchy_check;

ALTER TABLE titles
    ADD CONSTRAINT titles_hierarchy_check CHECK (
        (media_type IN ('movie', 'series', 'tv') AND parent_id IS NULL AND ordinal IS NULL AND hierarchy_variant = '')
        OR
        (media_type IN ('season', 'episode') AND parent_id IS NOT NULL AND ordinal >= 0
            AND (hierarchy_variant = '' OR hierarchy_variant ~ '^tvdb:[1-9][0-9]*$'))
    );

DROP INDEX titles_parent_ordinal_unique;

CREATE UNIQUE INDEX titles_parent_ordinal_unique
    ON titles (parent_id, media_type, hierarchy_variant, ordinal)
    WHERE is_current;

CREATE TABLE title_episode_order_identities (
    title_id uuid PRIMARY KEY REFERENCES titles(id) ON DELETE CASCADE,
    series_title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider = 'tvdb'),
    order_id text NOT NULL CHECK (order_id ~ '^[1-9][0-9]*$' AND char_length(order_id) <= 32),
    namespace text NOT NULL CHECK (namespace IN ('season', 'episode')),
    external_id text NOT NULL CHECK (external_id ~ '^[1-9][0-9]*$' AND char_length(external_id) <= 512),
    CONSTRAINT title_episode_order_identities_unique
        UNIQUE (series_title_id, provider, order_id, namespace, external_id)
);
