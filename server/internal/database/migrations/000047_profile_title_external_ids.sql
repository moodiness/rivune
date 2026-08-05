CREATE TABLE profile_title_external_ids (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (
        char_length(provider) BETWEEN 1 AND 32
        AND provider = lower(provider)
    ),
    namespace text NOT NULL CHECK (
        namespace IN ('movie', 'series')
    ),
    external_id text NOT NULL CHECK (
        char_length(external_id) BETWEEN 1 AND 512
        AND external_id = btrim(external_id)
    ),
    PRIMARY KEY (profile_id, provider, namespace, external_id),
    UNIQUE (title_id)
);
