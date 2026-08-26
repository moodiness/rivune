CREATE TABLE semantic_extension_memo (
    key_version integer NOT NULL CHECK (key_version > 0),
    cache_key bytea NOT NULL CHECK (octet_length(cache_key) = 32),
    selection text[] NOT NULL CHECK (
        cardinality(selection) <= 8
        AND array_position(selection, NULL) IS NULL
        AND octet_length(array_to_string(selection, E'\x1f')) <= 4096
    ),
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (key_version, cache_key),
    CHECK (expires_at > updated_at AND expires_at <= updated_at + interval '24 hours')
);

CREATE INDEX semantic_extension_memo_expiry_idx
    ON semantic_extension_memo (expires_at, key_version, cache_key);

CREATE INDEX semantic_extension_memo_capacity_idx
    ON semantic_extension_memo (updated_at DESC, key_version, cache_key);
