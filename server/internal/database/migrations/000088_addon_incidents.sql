CREATE TABLE addon_incidents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id uuid NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
    addon_id uuid NOT NULL REFERENCES profile_addons(id) ON DELETE CASCADE,
    addon_name text NOT NULL CHECK (length(addon_name) BETWEEN 1 AND 200),
    code text NOT NULL CHECK (code IN ('timeout', 'unavailable', 'invalid_response', 'unhealthy')),
    state text NOT NULL CHECK (state IN ('open', 'recovering', 'resolved')),
    occurrence_count integer NOT NULL DEFAULT 1 CHECK (occurrence_count > 0),
    consecutive_successes smallint NOT NULL DEFAULT 0 CHECK (consecutive_successes BETWEEN 0 AND 2),
    first_occurred_at timestamptz NOT NULL DEFAULT now(),
    last_occurred_at timestamptz NOT NULL DEFAULT now(),
    last_success_at timestamptz,
    recovery_started_at timestamptz,
    resolved_at timestamptz,
    acknowledged_at timestamptz,
    acknowledged_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (last_occurred_at >= first_occurred_at),
    CHECK (last_success_at IS NULL OR last_success_at >= first_occurred_at),
    CHECK ((state = 'open' AND consecutive_successes = 0 AND resolved_at IS NULL)
        OR (state = 'recovering' AND consecutive_successes = 1 AND recovery_started_at IS NOT NULL AND resolved_at IS NULL)
        OR (state = 'resolved' AND consecutive_successes = 2 AND recovery_started_at IS NOT NULL AND resolved_at IS NOT NULL))
);

CREATE UNIQUE INDEX addon_incidents_active_dedup_idx
    ON addon_incidents (profile_id, addon_id, code)
    WHERE state IN ('open', 'recovering');
CREATE INDEX addon_incidents_profile_timeline_idx
    ON addon_incidents (profile_id, updated_at DESC, id DESC);
CREATE INDEX addon_incidents_retention_idx
    ON addon_incidents (resolved_at, id)
    WHERE state = 'resolved';

CREATE TABLE addon_incident_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    incident_id uuid NOT NULL REFERENCES addon_incidents(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('opened', 'occurred', 'recovering', 'resolved', 'acknowledged')),
    code text NOT NULL CHECK (code IN ('timeout', 'unavailable', 'invalid_response', 'unhealthy')),
    occurred_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX addon_incident_events_timeline_idx
    ON addon_incident_events (incident_id, occurred_at DESC, id DESC);
