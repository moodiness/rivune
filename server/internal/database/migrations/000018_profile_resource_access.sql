BEGIN;

CREATE TABLE addon_profile_access (
    addon_id uuid NOT NULL REFERENCES profile_addons(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (addon_id, profile_id),
    UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO addon_profile_access (addon_id, profile_id, position)
SELECT id, profile_id, position
FROM profile_addons;

CREATE INDEX addon_profile_access_profile_order_idx
    ON addon_profile_access (profile_id, position, addon_id);

CREATE TABLE collection_profile_access (
    collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (collection_id, profile_id),
    UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO collection_profile_access (collection_id, profile_id, position)
SELECT id, profile_id, position
FROM profile_collections;

CREATE INDEX collection_profile_access_profile_order_idx
    ON collection_profile_access (profile_id, position, collection_id);

COMMIT;
