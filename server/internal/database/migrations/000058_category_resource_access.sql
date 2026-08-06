ALTER TABLE profile_addons
    DROP CONSTRAINT profile_addons_profile_id_fkey,
    ALTER COLUMN profile_id DROP NOT NULL,
    ADD CONSTRAINT profile_addons_profile_id_fkey
        FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE SET NULL;

ALTER TABLE profile_addons
    DROP CONSTRAINT IF EXISTS profile_addons_profile_id_transport_url_key;

ALTER TABLE profile_collections
    DROP CONSTRAINT profile_collections_profile_id_fkey,
    ALTER COLUMN profile_id DROP NOT NULL,
    ADD CONSTRAINT profile_collections_profile_id_fkey
        FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS addon_category_access (
    addon_id uuid NOT NULL REFERENCES profile_addons(id) ON DELETE CASCADE,
    category_id uuid NOT NULL REFERENCES access_categories(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (addon_id, category_id),
    UNIQUE (category_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS addon_category_access_category_order_idx
    ON addon_category_access (category_id, position, addon_id);

CREATE TABLE IF NOT EXISTS addon_profile_order (
    addon_id uuid NOT NULL REFERENCES profile_addons(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (addon_id, profile_id),
    UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO addon_profile_order (addon_id, profile_id, position)
SELECT addon_id, profile_id, position
FROM addon_profile_access
ON CONFLICT (addon_id, profile_id) DO NOTHING;

CREATE INDEX IF NOT EXISTS addon_profile_order_profile_order_idx
    ON addon_profile_order (profile_id, position, addon_id);

CREATE TABLE IF NOT EXISTS collection_category_access (
    collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE,
    category_id uuid NOT NULL REFERENCES access_categories(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (collection_id, category_id),
    UNIQUE (category_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS collection_category_access_category_order_idx
    ON collection_category_access (category_id, position, collection_id);

CREATE TABLE IF NOT EXISTS collection_profile_order (
    collection_id uuid NOT NULL REFERENCES profile_collections(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (collection_id, profile_id),
    UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

INSERT INTO collection_profile_order (collection_id, profile_id, position)
SELECT collection_id, profile_id, position
FROM collection_profile_access
ON CONFLICT (collection_id, profile_id) DO NOTHING;

CREATE INDEX IF NOT EXISTS collection_profile_order_profile_order_idx
    ON collection_profile_order (profile_id, position, collection_id);
