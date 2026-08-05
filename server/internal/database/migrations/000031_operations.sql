
CREATE TABLE IF NOT EXISTS operation_schedules (
    task text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT false,
    interval_hours smallint NOT NULL DEFAULT 24,
    language text NOT NULL DEFAULT 'en-US',
    batch_size smallint NOT NULL DEFAULT 25,
    next_run_at timestamptz,
    claim_token text,
    claim_expires_at timestamptz,
    last_started_at timestamptz,
    last_completed_at timestamptz,
    last_status text,
    last_result jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT operation_schedules_task_check CHECK (task = 'metadata-refresh'),
    CONSTRAINT operation_schedules_interval_check CHECK (interval_hours IN (6, 12, 24, 168)),
    CONSTRAINT operation_schedules_language_check CHECK (language ~ '^[A-Za-z]{2,3}(-[A-Za-z]{2})?$'),
    CONSTRAINT operation_schedules_batch_check CHECK (batch_size BETWEEN 1 AND 100),
    CONSTRAINT operation_schedules_status_check CHECK (last_status IS NULL OR last_status IN ('succeeded', 'partial', 'failed')),
    CONSTRAINT operation_schedules_result_check CHECK (last_result IS NULL OR jsonb_typeof(last_result) = 'object'),
    CONSTRAINT operation_schedules_claim_check CHECK ((claim_token IS NULL) = (claim_expires_at IS NULL))
);

INSERT INTO operation_schedules (task)
VALUES ('metadata-refresh')
ON CONFLICT (task) DO NOTHING;
