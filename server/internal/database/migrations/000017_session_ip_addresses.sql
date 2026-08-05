
ALTER TABLE auth_sessions
    ADD COLUMN IF NOT EXISTS last_ip inet;
