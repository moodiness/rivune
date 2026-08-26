DROP INDEX playback_source_failovers_expiry_idx;
CREATE INDEX playback_source_failovers_expiry_idx
    ON playback_source_failovers (expires_at, id);
CREATE INDEX playback_source_failovers_terminal_cleanup_idx
    ON playback_source_failovers (updated_at, id)
    WHERE status IN ('exhausted', 'cancelled');
CREATE INDEX playback_source_failovers_active_created_idx
    ON playback_source_failovers (created_at, id)
    WHERE status = 'active';
