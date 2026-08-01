package auth

import (
	"strings"
	"testing"
)

func TestAuthenticationCleanupPredicatesPreserveRefreshActiveSessions(t *testing.T) {
	predicate := normalizedSQL(cleanupExpiredSessionsSQL)
	if predicate != "DELETE FROM auth_sessions WHERE revoked_at IS NOT NULL OR refresh_expires_at <= now()" {
		t.Fatalf("unexpected authentication session cleanup predicate: %s", predicate)
	}
	if strings.Contains(predicate, "access_expires_at") {
		t.Fatal("access-token expiry must not purge a refresh-active session")
	}
}

func TestAuthenticationCleanupPurgesOnlyExpiredAuxiliaryRecords(t *testing.T) {
	if got := normalizedSQL(cleanupExpiredNotificationsSQL); got != "DELETE FROM auth_session_notifications WHERE expires_at <= now()" {
		t.Fatalf("unexpected notification cleanup predicate: %s", got)
	}
	if got := normalizedSQL(scrubExpiredNotificationBroadcastsSQL); got != "UPDATE auth_notification_broadcasts SET message = NULL WHERE expires_at <= now() AND message IS NOT NULL" {
		t.Fatalf("unexpected broadcast scrub predicate: %s", got)
	}
	if got := normalizedSQL(cleanupStaleDeviceAuthorizationsSQL); got != "DELETE FROM device_authorizations WHERE expires_at < now() - interval '1 hour' OR consumed_at < now() - interval '1 hour'" {
		t.Fatalf("unexpected device authorization cleanup predicate: %s", got)
	}
}

func normalizedSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
