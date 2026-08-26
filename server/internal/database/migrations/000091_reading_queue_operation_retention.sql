ALTER TABLE profile_reading_queue_operations
    ADD COLUMN expires_at timestamptz;

UPDATE profile_reading_queue_operations
SET expires_at = created_at + interval '24 hours'
WHERE expires_at IS NULL;

ALTER TABLE profile_reading_queue_operations
    ALTER COLUMN expires_at SET NOT NULL,
    ALTER COLUMN expires_at SET DEFAULT (clock_timestamp() + interval '24 hours'),
    ADD CONSTRAINT profile_reading_queue_operations_expiry_check CHECK (expires_at > created_at);

CREATE INDEX profile_reading_queue_operations_expiry_idx
    ON profile_reading_queue_operations (expires_at, profile_id, operation_id);
