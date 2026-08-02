BEGIN;

CREATE TABLE IF NOT EXISTS fanart_response_cache (
    resource_type text NOT NULL CHECK (resource_type IN ('movie', 'tv')),
    external_id text NOT NULL CHECK (external_id ~ '^[1-9][0-9]*$'),
    language text NOT NULL CHECK (language ~ '^[a-z0-9]{1,16}$'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    available boolean NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_type, external_id, language)
);

CREATE INDEX fanart_response_cache_expires_at_idx ON fanart_response_cache (expires_at);

COMMIT;
