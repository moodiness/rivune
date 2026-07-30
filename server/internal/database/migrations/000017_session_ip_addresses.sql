BEGIN;

ALTER TABLE auth_sessions
    ADD COLUMN last_ip inet;

COMMIT;
