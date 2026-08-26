CREATE TABLE tmdb_semantic_catalog (
    language text PRIMARY KEY CHECK (language ~ '^[a-z]{2,3}(-[A-Z]{2})?$'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tmdb_semantic_catalog_expiry_idx
    ON tmdb_semantic_catalog (expires_at, language);

CREATE INDEX tmdb_semantic_catalog_capacity_idx
    ON tmdb_semantic_catalog (updated_at DESC, language);
