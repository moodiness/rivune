ALTER TABLE playback_room_members
    ADD COLUMN member_id uuid NOT NULL DEFAULT gen_random_uuid();

CREATE UNIQUE INDEX playback_room_members_opaque_id_idx
    ON playback_room_members (room_id, member_id);
