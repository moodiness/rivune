
ALTER TABLE profile_collections
    ADD COLUMN IF NOT EXISTS hero_enabled boolean NOT NULL DEFAULT false;

UPDATE profile_collections
SET hero_enabled = true
WHERE id IN (
    SELECT DISTINCT ON (access.profile_id) access.collection_id
    FROM collection_profile_access access
    JOIN profile_collections collection ON collection.id = access.collection_id
    ORDER BY access.profile_id, collection.pin_to_top DESC, access.position, collection.id
);
