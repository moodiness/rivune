
CREATE UNIQUE INDEX IF NOT EXISTS title_external_ids_title_provider_namespace_idx
    ON title_external_ids (title_id, provider, namespace);
