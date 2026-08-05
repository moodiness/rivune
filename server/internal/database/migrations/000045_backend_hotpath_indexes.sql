CREATE INDEX IF NOT EXISTS profile_tracking_outbox_predecessor_idx
    ON profile_tracking_outbox (profile_id, provider, title_id, enqueue_sequence);

CREATE INDEX IF NOT EXISTS auth_sessions_revoked_cleanup_idx
    ON auth_sessions (revoked_at, id)
    WHERE revoked_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS auth_sessions_refresh_expiry_cleanup_idx
    ON auth_sessions (refresh_expires_at, id)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS auth_session_notifications_expiry_cleanup_idx
    ON auth_session_notifications (expires_at, id);

CREATE INDEX IF NOT EXISTS introdb_segment_cache_expiry_cleanup_idx
    ON introdb_segment_cache (expires_at, imdb_id, season_number, episode_number);

CREATE INDEX IF NOT EXISTS fanart_response_cache_expiry_cleanup_idx
    ON fanart_response_cache (expires_at, resource_type, external_id, language);

CREATE INDEX IF NOT EXISTS fanart_response_cache_capacity_cleanup_idx
    ON fanart_response_cache (updated_at DESC, resource_type, external_id, language);

DROP INDEX IF EXISTS auth_session_notifications_expires_at_idx;
DROP INDEX IF EXISTS introdb_segment_cache_expiry_idx;
DROP INDEX IF EXISTS fanart_response_cache_expires_at_idx;
