
DELETE FROM title_metadata AS metadata
USING titles AS title
WHERE metadata.title_id = title.id
  AND title.media_type IN ('movie', 'series')
  AND jsonb_typeof(metadata.payload -> 'cast') = 'array'
  AND jsonb_array_length(metadata.payload -> 'cast') = 12;
