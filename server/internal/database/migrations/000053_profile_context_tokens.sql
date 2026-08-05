ALTER TABLE auth_sessions
    ADD COLUMN profile_context_hash bytea;

ALTER TABLE auth_sessions
    ADD CONSTRAINT auth_sessions_profile_context_hash_length
    CHECK (profile_context_hash IS NULL OR octet_length(profile_context_hash) = 32);
