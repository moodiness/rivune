
CREATE TABLE IF NOT EXISTS artwork_cache (
    key text PRIMARY KEY,
    source_url text NOT NULL UNIQUE,
    content_type text,
    byte_size bigint,
    cached_at timestamptz,
    last_accessed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT artwork_cache_key_check CHECK (key ~ '^[0-9a-f]{64}$'),
    CONSTRAINT artwork_cache_content_type_check CHECK (content_type IS NULL OR content_type IN ('image/jpeg', 'image/png', 'image/webp')),
    CONSTRAINT artwork_cache_byte_size_check CHECK (byte_size IS NULL OR byte_size BETWEEN 1 AND 12582912),
    CONSTRAINT artwork_cache_content_check CHECK (
        (content_type IS NULL AND byte_size IS NULL AND cached_at IS NULL AND last_accessed_at IS NULL)
        OR
        (content_type IS NOT NULL AND byte_size IS NOT NULL AND cached_at IS NOT NULL AND last_accessed_at IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS artwork_cache_lru_idx
    ON artwork_cache (last_accessed_at, key)
    WHERE byte_size IS NOT NULL;
