UPDATE titles AS title
SET poster_url = '/api/v1/artwork/' || artwork.key
FROM artwork_cache AS artwork
WHERE title.media_type = 'tv'
  AND title.poster_url = artwork.source_url;

UPDATE titles AS title
SET background_url = '/api/v1/artwork/' || artwork.key
FROM artwork_cache AS artwork
WHERE title.media_type = 'tv'
  AND title.background_url = artwork.source_url;
