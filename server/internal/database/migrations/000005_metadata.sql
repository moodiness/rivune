DO $migration$
BEGIN
    CREATE TABLE titles (
        id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
        media_type text NOT NULL CHECK (media_type IN ('movie')),
        created_at timestamptz NOT NULL DEFAULT now(),
        updated_at timestamptz NOT NULL DEFAULT now()
    );

    CREATE TABLE title_external_ids (
        title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
        provider text NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 32 AND provider = lower(provider)),
        external_id text NOT NULL CHECK (char_length(external_id) BETWEEN 1 AND 128 AND external_id = btrim(external_id)),
        PRIMARY KEY (provider, external_id),
        UNIQUE (title_id, provider)
    );

    CREATE TABLE title_metadata (
        title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
        provider text NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 32 AND provider = lower(provider)),
        language text NOT NULL CHECK (char_length(language) BETWEEN 2 AND 16 AND language = btrim(language)),
        payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
        expires_at timestamptz NOT NULL,
        updated_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (title_id, provider, language),
        FOREIGN KEY (title_id, provider) REFERENCES title_external_ids(title_id, provider) ON DELETE CASCADE
    );

    CREATE INDEX title_metadata_expires_at_idx ON title_metadata (expires_at);
END
$migration$;
