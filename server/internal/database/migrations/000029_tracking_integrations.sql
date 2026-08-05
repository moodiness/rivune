
CREATE TABLE IF NOT EXISTS profile_tracking_accounts (
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('trakt', 'simkl')),
    access_token_encrypted bytea NOT NULL CHECK (octet_length(access_token_encrypted) >= 29),
    refresh_token_encrypted bytea CHECK (refresh_token_encrypted IS NULL OR octet_length(refresh_token_encrypted) >= 29),
    token_expires_at timestamptz,
    sync_watched boolean NOT NULL DEFAULT true,
    sync_progress boolean NOT NULL DEFAULT true,
    sync_library boolean NOT NULL DEFAULT true,
    connected_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_success_at timestamptz,
    last_error text CHECK (last_error IS NULL OR char_length(last_error) <= 500),
    PRIMARY KEY (profile_id, provider)
);

CREATE TABLE IF NOT EXISTS profile_tracking_authorizations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    provider text NOT NULL CHECK (provider IN ('trakt', 'simkl')),
    provider_code_encrypted bytea NOT NULL CHECK (octet_length(provider_code_encrypted) >= 29),
    user_code text NOT NULL CHECK (char_length(user_code) BETWEEN 4 AND 32),
    verification_url text NOT NULL CHECK (char_length(verification_url) BETWEEN 8 AND 2048),
    interval_seconds integer NOT NULL CHECK (interval_seconds BETWEEN 1 AND 300),
    expires_at timestamptz NOT NULL,
    last_polled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS profile_tracking_authorizations_profile_provider_idx
    ON profile_tracking_authorizations (profile_id, provider);
CREATE INDEX IF NOT EXISTS profile_tracking_authorizations_expires_at_idx
    ON profile_tracking_authorizations (expires_at);

CREATE TABLE IF NOT EXISTS profile_tracking_event_heads (
    profile_id uuid NOT NULL,
    provider text NOT NULL,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('watched', 'progress', 'library')),
    idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 300),
    affects_watched boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, provider, title_id, event_type),
    FOREIGN KEY (profile_id, provider)
        REFERENCES profile_tracking_accounts(profile_id, provider) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS profile_tracking_outbox (
    enqueue_sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL,
    provider text NOT NULL,
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('watched', 'progress', 'library')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 300),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    leased_until timestamptz,
    last_error text CHECK (last_error IS NULL OR char_length(last_error) <= 500),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (profile_id, provider)
        REFERENCES profile_tracking_accounts(profile_id, provider) ON DELETE CASCADE,
    UNIQUE (profile_id, provider, idempotency_key)
);

CREATE INDEX IF NOT EXISTS profile_tracking_outbox_due_idx
    ON profile_tracking_outbox (next_attempt_at, enqueue_sequence);
