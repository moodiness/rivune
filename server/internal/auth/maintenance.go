package auth

import (
	"context"
	"errors"
	"fmt"
)

const (
	cleanupExpiredNotificationsSQL = `
		DELETE FROM auth_session_notifications
		WHERE expires_at <= now()
	`
	scrubExpiredNotificationBroadcastsSQL = `
		UPDATE auth_notification_broadcasts
		SET message = NULL
		WHERE expires_at <= now()
		  AND message IS NOT NULL
	`
	cleanupExpiredSessionsSQL = `
		DELETE FROM auth_sessions
		WHERE revoked_at IS NOT NULL OR refresh_expires_at <= now()
	`
	cleanupStaleDeviceAuthorizationsSQL = `
		DELETE FROM device_authorizations
		WHERE expires_at < now() - interval '1 hour'
		   OR consumed_at < now() - interval '1 hour'
	`
)

// Cleanup removes authentication records that can no longer become active and
// scrubs expired broadcast text while retaining its idempotency tombstone.
func (s *Service) Cleanup(ctx context.Context) error {
	var cleanupErrors []error
	if _, err := s.pool.Exec(ctx, cleanupExpiredNotificationsSQL); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete expired session notifications: %w", err))
	}
	if _, err := s.pool.Exec(ctx, scrubExpiredNotificationBroadcastsSQL); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("scrub expired notification broadcasts: %w", err))
	}
	if _, err := s.pool.Exec(ctx, cleanupExpiredSessionsSQL); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete inactive authentication sessions: %w", err))
	}
	if _, err := s.pool.Exec(ctx, cleanupStaleDeviceAuthorizationsSQL); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete stale device authorizations: %w", err))
	}
	return errors.Join(cleanupErrors...)
}
