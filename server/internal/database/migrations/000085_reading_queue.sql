CREATE TABLE profile_reading_queue_states (
    profile_id uuid PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE profile_reading_queue_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    media_type text NOT NULL CHECK (media_type IN ('movie', 'series', 'episode', 'tv')),
    resource_id text NOT NULL CHECK (resource_id = btrim(resource_id) AND length(resource_id) BETWEEN 1 AND 512),
    source_addon_id uuid,
    title_id uuid,
    display_title text NOT NULL CHECK (display_title = btrim(display_title) AND length(display_title) BETWEEN 1 AND 240),
    poster_url text CHECK (poster_url IS NULL OR (poster_url = btrim(poster_url) AND length(poster_url) BETWEEN 1 AND 2048)),
    position integer NOT NULL CHECK (position >= 0 AND position < 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX profile_reading_queue_identity_key
    ON profile_reading_queue_items (profile_id, media_type, resource_id, COALESCE(source_addon_id, '00000000-0000-0000-0000-000000000000'::uuid));
CREATE INDEX profile_reading_queue_order_idx
    ON profile_reading_queue_items (profile_id, position, id);

CREATE TABLE profile_reading_queue_operations (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('add', 'update', 'remove', 'reorder', 'consume')),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    result_revision bigint NOT NULL CHECK (result_revision > 0),
    result_item_id uuid,
    result_deduplicated boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, operation_id)
);
