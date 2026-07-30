ALTER TABLE profile_collections
 ADD COLUMN folder_cover_shape text NOT NULL DEFAULT 'poster'
 CHECK (folder_cover_shape IN ('poster', 'landscape', 'square'));
