
DELETE FROM title_metadata AS metadata
USING titles AS title
WHERE metadata.title_id = title.id
  AND title.media_type = 'series'
  AND metadata.provider = 'tvdb';
