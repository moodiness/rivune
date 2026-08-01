BEGIN;

WITH season_names AS (
    SELECT DISTINCT ON (metadata.title_id)
        metadata.title_id,
        btrim(metadata.payload ->> 'name') AS display_title
    FROM title_metadata AS metadata
    JOIN titles AS season
      ON season.id = metadata.title_id
     AND season.media_type = 'season'
    WHERE jsonb_typeof(metadata.payload) = 'object'
      AND metadata.payload ->> 'mediaType' = 'season'
      AND jsonb_typeof(metadata.payload -> 'name') = 'string'
      AND btrim(metadata.payload ->> 'name') <> ''
    ORDER BY metadata.title_id, metadata.updated_at DESC, metadata.provider, metadata.language
)
UPDATE titles AS season
SET display_title = season_names.display_title,
    updated_at = now()
FROM season_names
WHERE season.id = season_names.title_id
  AND NULLIF(btrim(COALESCE(season.display_title, '')), '') IS NULL;

WITH episode_names AS (
    SELECT DISTINCT ON (episode_title.id)
        episode_title.id AS title_id,
        btrim(episode ->> 'name') AS display_title
    FROM title_metadata AS metadata
    JOIN titles AS season
      ON season.id = metadata.title_id
     AND season.media_type = 'season'
    CROSS JOIN LATERAL jsonb_array_elements(
        CASE
            WHEN jsonb_typeof(metadata.payload -> 'episodes') = 'array'
                THEN metadata.payload -> 'episodes'
            ELSE '[]'::jsonb
        END
    ) AS cached_episode(episode)
    JOIN titles AS episode_title
      ON episode_title.parent_id = season.id
     AND episode_title.media_type = 'episode'
     AND episode_title.id::text = episode ->> 'id'
    WHERE jsonb_typeof(metadata.payload) = 'object'
      AND metadata.payload ->> 'mediaType' = 'season'
      AND jsonb_typeof(episode) = 'object'
      AND episode ->> 'mediaType' = 'episode'
      AND jsonb_typeof(episode -> 'id') = 'string'
      AND jsonb_typeof(episode -> 'name') = 'string'
      AND btrim(episode ->> 'name') <> ''
    ORDER BY episode_title.id, metadata.updated_at DESC, metadata.provider, metadata.language, btrim(episode ->> 'name')
)
UPDATE titles AS episode
SET display_title = episode_names.display_title,
    updated_at = now()
FROM episode_names
WHERE episode.id = episode_names.title_id
  AND NULLIF(btrim(COALESCE(episode.display_title, '')), '') IS NULL;

COMMIT;
