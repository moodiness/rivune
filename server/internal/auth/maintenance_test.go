package auth

import (
	"strings"
	"testing"
)

func TestAuthenticationCleanupPredicatesPreserveRefreshActiveSessions(t *testing.T) {
	query := normalizedSQL(cleanupExpiredSessionsSQL)
	for _, predicate := range []string{
		"WHERE revoked_at IS NOT NULL ORDER BY revoked_at, id LIMIT $1",
		"WHERE revoked_at IS NULL AND refresh_expires_at <= now() ORDER BY refresh_expires_at, id LIMIT $1",
		"LIMIT $1 FOR UPDATE OF session SKIP LOCKED",
		"DELETE FROM auth_sessions session USING locked WHERE session.id = locked.id",
	} {
		if !strings.Contains(query, predicate) {
			t.Fatalf("authentication session cleanup lacks %q: %s", predicate, query)
		}
	}
	if strings.Contains(query, "access_expires_at") {
		t.Fatal("access-token expiry must not purge a refresh-active session")
	}
	if authenticationCleanupBatch != 500 {
		t.Fatalf("authentication cleanup batch = %d, want 500", authenticationCleanupBatch)
	}
}

func TestAuthenticationCleanupBoundsConsumedRefreshHistory(t *testing.T) {
	query := normalizedSQL(cleanupConsumedRefreshTokensSQL)
	for _, predicate := range []string{
		"WHERE consumed_at <= now() - interval '30 days'",
		"ORDER BY consumed_at, token_hash LIMIT $1 FOR UPDATE SKIP LOCKED",
		"DELETE FROM auth_refresh_tokens token USING expired WHERE token.token_hash = expired.token_hash",
	} {
		if !strings.Contains(query, predicate) {
			t.Fatalf("consumed refresh token cleanup lacks %q: %s", predicate, query)
		}
	}
}

func TestAuthenticationCleanupDeletesAreBoundedAndSkipLocked(t *testing.T) {
	for name, query := range map[string]string{
		"notifications":         cleanupExpiredNotificationsSQL,
		"refresh token history": cleanupConsumedRefreshTokensSQL,
		"sessions":              cleanupExpiredSessionsSQL,
		"orphan devices":        cleanupOrphanDevicesSQL,
	} {
		normalized := normalizedSQL(query)
		if !strings.Contains(normalized, "LIMIT $1") || !strings.Contains(normalized, "FOR UPDATE") || !strings.Contains(normalized, "SKIP LOCKED") {
			t.Fatalf("%s cleanup is not bounded and non-blocking: %s", name, normalized)
		}
	}
	if got := normalizedSQL(cleanupExpiredNotificationsSQL); !strings.Contains(got, "WHERE expires_at <= now() ORDER BY expires_at, id LIMIT $1 FOR UPDATE SKIP LOCKED") {
		t.Fatalf("unexpected notification cleanup predicate: %s", got)
	}
	if got := normalizedSQL(scrubExpiredNotificationBroadcastsSQL); got != "UPDATE auth_notification_broadcasts SET message = NULL WHERE expires_at <= now() AND message IS NOT NULL" {
		t.Fatalf("unexpected broadcast scrub predicate: %s", got)
	}
	if got := normalizedSQL(cleanupStaleDeviceAuthorizationsSQL); got != "DELETE FROM device_authorizations WHERE id IN ( SELECT id FROM device_authorizations WHERE expires_at <= now() OR consumed_at IS NOT NULL ORDER BY expires_at, id LIMIT $1 )" {
		t.Fatalf("unexpected device authorization cleanup predicate: %s", got)
	}
	if deviceAuthorizationCleanupBatch != 500 {
		t.Fatalf("device authorization cleanup batch = %d, want 500", deviceAuthorizationCleanupBatch)
	}
	orphanCleanup := normalizedSQL(cleanupOrphanDevicesSQL)
	for _, predicate := range []string{
		"WHERE device.user_id IS NULL",
		"NOT EXISTS ( SELECT 1 FROM auth_sessions session WHERE session.device_id = device.id )",
		"LIMIT $1 FOR UPDATE OF device SKIP LOCKED",
	} {
		if !strings.Contains(orphanCleanup, predicate) {
			t.Fatalf("orphan device cleanup lacks %q: %s", predicate, orphanCleanup)
		}
	}
}

func normalizedSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
