CREATE TABLE playback_device_presence (
    auth_session_id uuid PRIMARY KEY REFERENCES auth_sessions(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    capabilities text[] NOT NULL DEFAULT ARRAY[]::text[],
    playback_state jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (cardinality(capabilities) <= 16),
    CHECK (jsonb_typeof(playback_state) = 'object')
);

CREATE INDEX playback_device_presence_profile_seen_idx
    ON playback_device_presence (profile_id, last_seen_at DESC);

CREATE TABLE playback_commands (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    sender_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    command text NOT NULL CHECK (command IN ('load', 'play', 'pause', 'seek', 'stop')),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    acknowledged_at timestamptz,
    CHECK (expires_at > created_at),
    CHECK (acknowledged_at IS NULL OR acknowledged_at >= created_at)
);

CREATE INDEX playback_commands_target_pending_idx
    ON playback_commands (target_session_id, id)
    WHERE acknowledged_at IS NULL;
CREATE INDEX playback_commands_expiry_idx ON playback_commands (expires_at);

CREATE TABLE playback_rooms (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    host_profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    join_code_hash bytea NOT NULL UNIQUE CHECK (octet_length(join_code_hash) = 32),
    title_id uuid NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
    playback_item jsonb NOT NULL CHECK (jsonb_typeof(playback_item) = 'object'),
    state text NOT NULL DEFAULT 'paused' CHECK (state IN ('playing', 'paused', 'ended')),
    position_milliseconds bigint NOT NULL DEFAULT 0 CHECK (position_milliseconds BETWEEN 0 AND 604800000),
    duration_milliseconds bigint NOT NULL DEFAULT 0 CHECK (duration_milliseconds BETWEEN 0 AND 604800000),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE INDEX playback_rooms_expiry_idx ON playback_rooms (expires_at);

CREATE TABLE playback_room_members (
    room_id uuid NOT NULL REFERENCES playback_rooms(id) ON DELETE CASCADE,
    auth_session_id uuid NOT NULL REFERENCES auth_sessions(id) ON DELETE CASCADE,
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('host', 'participant')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, auth_session_id)
);

CREATE UNIQUE INDEX playback_room_single_host_idx
    ON playback_room_members (room_id) WHERE role = 'host';
CREATE INDEX playback_room_members_session_idx
    ON playback_room_members (auth_session_id, last_seen_at DESC);
