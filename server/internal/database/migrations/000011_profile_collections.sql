BEGIN;

CREATE TABLE profile_collections (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
 title text NOT NULL CHECK (title = btrim(title) AND length(title) BETWEEN 1 AND 120),
 backdrop_image_url text,
 pin_to_top boolean NOT NULL DEFAULT false,
 focus_glow_enabled boolean NOT NULL DEFAULT true,
 view_mode text NOT NULL DEFAULT 'tabbed_grid'
  CHECK (view_mode IN ('tabbed_grid', 'rows', 'follow_layout')),
 show_all_tab boolean NOT NULL DEFAULT true,
 folders jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(folders) = 'array'),
 position integer NOT NULL CHECK (position >= 0),
 version integer NOT NULL DEFAULT 1 CHECK (version >= 1),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (profile_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX profile_collections_profile_order_idx
 ON profile_collections (profile_id, pin_to_top DESC, position, id);

COMMIT;
