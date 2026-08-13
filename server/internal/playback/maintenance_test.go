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

func TestProxyAssetAtomicallyRejectsIdleSessionBeforeExtendingIt(t *testing.T) {
	want := "UPDATE playback_sessions playback SET last_seen_at = now(), expires_at = GREATEST(playback.expires_at, now() + $4::interval) FROM auth_sessions session WHERE playback.id::text = $1 AND playback.token_hash = $2 AND playback.expires_at > now() AND playback.last_seen_at > now() - $3::interval AND session.id = playback.auth_session_id AND session.revoked_at IS NULL AND session.refresh_expires_at > now() AND session.active_profile_id = playback.profile_id AND session.profile_grant_expires_at > now() RETURNING playback.assets"
	if got := normalizedMaintenanceSQL(proxyAssetSessionSQL); got != want {
		t.Fatalf("unexpected atomic playback serve predicate: %s", got)
	}
}

func normalizedMaintenanceSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
