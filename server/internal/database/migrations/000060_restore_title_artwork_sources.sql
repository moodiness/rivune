UPDATE titles AS title
SET poster_url = artwork.source_url
FROM artwork_cache AS artwork
WHERE title.media_type = 'tv'
  AND title.poster_url = '/api/v1/artwork/' || artwork.key;

UPDATE titles AS title
SET background_url = artwork.source_url
FROM artwork_cache AS artwork
WHERE title.media_type = 'tv'
  AND title.background_url = '/api/v1/artwork/' || artwork.key;
