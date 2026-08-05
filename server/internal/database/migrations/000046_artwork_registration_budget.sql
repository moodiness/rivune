ALTER TABLE artwork_cache
    ADD COLUMN IF NOT EXISTS registered_at timestamptz;

UPDATE artwork_cache
SET registered_at = created_at
WHERE registered_at IS NULL;

ALTER TABLE artwork_cache
    ALTER COLUMN registered_at SET DEFAULT now(),
    ALTER COLUMN registered_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS artwork_cache_registration_idx
    ON artwork_cache (registered_at DESC, key DESC);

CREATE INDEX IF NOT EXISTS artwork_cache_uncached_registration_idx
    ON artwork_cache (registered_at, key)
    WHERE byte_size IS NULL;
