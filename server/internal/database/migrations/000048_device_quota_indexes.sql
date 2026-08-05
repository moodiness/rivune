DO $migration$
BEGIN
    CREATE INDEX IF NOT EXISTS devices_user_id_idx
        ON devices (user_id)
        WHERE user_id IS NOT NULL;
    CREATE INDEX IF NOT EXISTS devices_orphan_cleanup_idx
        ON devices (created_at, id)
        WHERE user_id IS NULL;
    CREATE INDEX IF NOT EXISTS auth_sessions_device_id_idx
        ON auth_sessions (device_id);
END
$migration$;
