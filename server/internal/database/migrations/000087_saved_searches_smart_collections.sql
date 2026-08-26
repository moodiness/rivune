CREATE TABLE profile_saved_searches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    query text NOT NULL CHECK (query = btrim(query) AND char_length(query) BETWEEN 1 AND 256),
    media_type text CHECK (media_type IS NULL OR media_type IN ('movie', 'series', 'season', 'episode', 'video', 'tv')),
    sort text NOT NULL DEFAULT 'relevance' CHECK (sort IN ('relevance', 'title', 'year', 'rating', 'added')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_id, name)
);

CREATE INDEX profile_saved_searches_profile_order_idx
    ON profile_saved_searches (profile_id, lower(name) COLLATE "C", id);

CREATE TABLE profile_smart_collections (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name = btrim(name) AND char_length(name) BETWEEN 1 AND 120),
    rules jsonb NOT NULL CHECK (jsonb_typeof(rules) = 'object'),
    sort text NOT NULL DEFAULT 'title' CHECK (sort IN ('title', 'year', 'rating', 'added')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_id, name)
);

CREATE INDEX profile_smart_collections_profile_order_idx
    ON profile_smart_collections (profile_id, lower(name) COLLATE "C", id);
