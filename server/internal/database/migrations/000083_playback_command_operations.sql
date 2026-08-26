ALTER TABLE playback_device_presence
    ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0);

ALTER TABLE playback_commands
    ADD COLUMN operation_id uuid,
    ADD COLUMN target_revision bigint,
    ADD COLUMN result_status text,
    ADD COLUMN result_code text,
    ADD COLUMN completed_at timestamptz;

UPDATE playback_commands
SET operation_id = gen_random_uuid()
WHERE operation_id IS NULL;
UPDATE playback_commands
SET payload = jsonb_set(
        CASE WHEN command = 'load' THEN jsonb_set(payload, '{mode}', '"play-copy"'::jsonb, true) ELSE payload END,
        '{operationId}', to_jsonb(operation_id::text), true
    ),
    result_status = CASE WHEN acknowledged_at IS NOT NULL THEN 'applied' ELSE NULL END,
    result_code = CASE WHEN acknowledged_at IS NOT NULL THEN 'applied' ELSE NULL END,
    completed_at = acknowledged_at;

ALTER TABLE playback_commands
    ALTER COLUMN operation_id SET NOT NULL,
    ALTER COLUMN operation_id SET DEFAULT gen_random_uuid(),
    ADD CONSTRAINT playback_commands_target_revision_check CHECK (target_revision IS NULL OR target_revision > 0),
    ADD CONSTRAINT playback_commands_result_status_check CHECK (result_status IS NULL OR result_status IN ('applied', 'failed', 'expired')),
    ADD CONSTRAINT playback_commands_result_code_check CHECK (result_code IS NULL OR result_code IN ('applied', 'unsupported', 'invalid_state', 'stale_target', 'expired', 'execution_failed')),
    ADD CONSTRAINT playback_commands_result_complete_check CHECK ((result_status IS NULL) = (result_code IS NULL) AND (result_status IS NULL) = (completed_at IS NULL)),
    ADD CONSTRAINT playback_commands_completion_time_check CHECK (completed_at IS NULL OR completed_at >= created_at),
    ADD CONSTRAINT playback_commands_load_mode_check CHECK (command <> 'load' OR payload->>'mode' IN ('handoff', 'play-copy'));

CREATE UNIQUE INDEX playback_commands_sender_operation_key
    ON playback_commands (sender_session_id, operation_id);
DROP INDEX playback_commands_target_pending_idx;
CREATE INDEX playback_commands_target_pending_idx
    ON playback_commands (target_session_id, created_at, operation_id)
    WHERE result_status IS NULL;
