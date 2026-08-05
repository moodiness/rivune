WITH candidate_sources AS (
    SELECT pc.id AS collection_id,
           folder.ordinality AS folder_index,
           source.ordinality AS source_index,
           installed.id,
           installed.manifest_id,
           count(*) OVER (
               PARTITION BY pc.id, folder.ordinality, source.ordinality
           ) AS candidate_count
    FROM profile_collections pc
    CROSS JOIN LATERAL jsonb_array_elements(pc.folders) WITH ORDINALITY AS folder(value, ordinality)
    CROSS JOIN LATERAL jsonb_array_elements(folder.value->'sources') WITH ORDINALITY AS source(value, ordinality)
    JOIN LATERAL (
        SELECT addon.id, addon.manifest_id
        FROM profile_addons addon
        WHERE source.value->>'kind' = 'addon_catalog'
          AND (
              COALESCE(source.value->'addonCatalog'->>'manifestId', '') = ''
              OR addon.manifest_id = source.value->'addonCatalog'->>'manifestId'
          )
          AND EXISTS (
              SELECT 1
              FROM jsonb_array_elements(COALESCE(addon.manifest->'catalogs', '[]'::jsonb)) catalog(value)
              WHERE catalog.value->>'type' = source.value->'addonCatalog'->>'type'
                AND catalog.value->>'id' = source.value->'addonCatalog'->>'catalogId'
          )
          AND EXISTS (
              SELECT 1
              FROM jsonb_array_elements(COALESCE(addon.manifest->'resources', '[]'::jsonb)) resource(value)
              WHERE (
                  jsonb_typeof(resource.value) = 'string'
                  AND resource.value #>> '{}' = 'catalog'
                  AND COALESCE(addon.manifest->'types', '[]'::jsonb) ? (source.value->'addonCatalog'->>'type')
              ) OR (
                  jsonb_typeof(resource.value) = 'object'
                  AND resource.value->>'name' = 'catalog'
                  AND COALESCE(resource.value->'types', '[]'::jsonb) ? (source.value->'addonCatalog'->>'type')
              )
          )
          AND NOT EXISTS (
              SELECT 1
              FROM collection_profile_access collection_access
              WHERE collection_access.collection_id = pc.id
                AND NOT EXISTS (
                    SELECT 1
                    FROM addon_profile_access addon_access
                    WHERE addon_access.addon_id = addon.id
                      AND addon_access.profile_id = collection_access.profile_id
                )
          )
    ) installed ON true
), unique_sources AS (
    SELECT collection_id, folder_index, source_index, id, manifest_id
    FROM candidate_sources
    WHERE candidate_count = 1
), repaired_collections AS (
    SELECT pc.id,
           jsonb_agg(
               jsonb_set(
                   folder.value,
                   '{sources}',
                   COALESCE((
                       SELECT jsonb_agg(
                           CASE
                               WHEN replacement.id IS NULL THEN source.value
                               ELSE jsonb_set(
                                   jsonb_set(source.value, '{addonCatalog,addonId}', to_jsonb(replacement.id::text), false),
                                   '{addonCatalog,manifestId}', to_jsonb(replacement.manifest_id), true
                               )
                           END
                           ORDER BY source.ordinality
                       )
                       FROM jsonb_array_elements(folder.value->'sources') WITH ORDINALITY AS source(value, ordinality)
                       LEFT JOIN unique_sources replacement
                         ON replacement.collection_id = pc.id
                        AND replacement.folder_index = folder.ordinality
                        AND replacement.source_index = source.ordinality
                   ), '[]'::jsonb),
                   false
               )
               ORDER BY folder.ordinality
           ) AS folders
    FROM profile_collections pc
    CROSS JOIN LATERAL jsonb_array_elements(pc.folders) WITH ORDINALITY AS folder(value, ordinality)
    WHERE EXISTS (
        SELECT 1 FROM unique_sources replacement WHERE replacement.collection_id = pc.id
    )
    GROUP BY pc.id
)
UPDATE profile_collections collection
SET folders = repaired.folders,
    version = collection.version + 1,
    updated_at = now()
FROM repaired_collections repaired
WHERE collection.id = repaired.id
  AND collection.folders IS DISTINCT FROM repaired.folders;
