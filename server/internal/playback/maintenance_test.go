package playback

import (
	"strings"
	"testing"
)

func TestPlaybackCleanupPredicatesRemoveOnlyExpiredOrIdleSessions(t *testing.T) {
	if got := normalizedMaintenanceSQL(cleanupInactiveSessionsSQL); got != "DELETE FROM playback_sessions WHERE expires_at <= now() OR last_seen_at <= now() - $1::interval RETURNING id::text" {
		t.Fatalf("unexpected inactive playback predicate: %s", got)
	}
}

func TestOrphanDetectionUsesInverseActiveSessionPredicate(t *testing.T) {
	if got := normalizedMaintenanceSQL(activePlaybackSessionsSQL); got != "SELECT id::text FROM playback_sessions WHERE expires_at > now() AND last_seen_at > now() - $1::interval" {
		t.Fatalf("unexpected active playback predicate: %s", got)
	}
}

func normalizedMaintenanceSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
