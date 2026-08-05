package auth

import (
	"context"
	"errors"
	"fmt"
)

const (
	authenticationCleanupBatch = 500

	cleanupExpiredNotificationsSQL = `
		WITH expired AS (
			SELECT id
			FROM auth_session_notifications
			WHERE expires_at <= now()
			ORDER BY expires_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM auth_session_notifications notification
		USING expired
		WHERE notification.id = expired.id
	`
	scrubExpiredNotificationBroadcastsSQL = `
		UPDATE auth_notification_broadcasts
		SET message = NULL
		WHERE expires_at <= now()
		  AND message IS NOT NULL
	`
	cleanupExpiredSessionsSQL = `
		WITH candidates AS (
			SELECT id, inactive_at
			FROM (
				SELECT id, revoked_at AS inactive_at
				FROM auth_sessions
				WHERE revoked_at IS NOT NULL
				ORDER BY revoked_at, id
				LIMIT $1
			) revoked
			UNION ALL
			SELECT id, inactive_at
			FROM (
				SELECT id, refresh_expires_at AS inactive_at
				FROM auth_sessions
				WHERE revoked_at IS NULL
				  AND refresh_expires_at <= now()
				ORDER BY refresh_expires_at, id
				LIMIT $1
			) expired
		),
		locked AS (
			SELECT session.id
			FROM auth_sessions session
			JOIN candidates ON candidates.id = session.id
			ORDER BY candidates.inactive_at, session.id
			LIMIT $1
			FOR UPDATE OF session SKIP LOCKED
		)
		DELETE FROM auth_sessions session
		USING locked
		WHERE session.id = locked.id
	`
	cleanupOrphanDevicesSQL = `
		WITH candidates AS (
			SELECT device.id
			FROM devices device
			WHERE device.user_id IS NULL
			  AND NOT EXISTS (
				SELECT 1
				FROM auth_sessions session
				WHERE session.device_id = device.id
			  )
			ORDER BY device.created_at, device.id
			LIMIT $1
			FOR UPDATE OF device SKIP LOCKED
		)
		DELETE FROM devices device
		USING candidates
		WHERE device.id = candidates.id
	`
	cleanupStaleDeviceAuthorizationsSQL = `
		DELETE FROM device_authorizations
		WHERE id IN (
			SELECT id
			FROM device_authorizations
			WHERE expires_at <= now() OR consumed_at IS NOT NULL
			ORDER BY expires_at, id
			LIMIT $1
		)
	`
)

// Cleanup removes authentication records that can no longer become active and
// scrubs expired broadcast text while retaining its idempotency tombstone.
func (s *Service) Cleanup(ctx context.Context) error {
	var cleanupErrors []error
	if _, err := s.pool.Exec(ctx, cleanupExpiredNotificationsSQL, authenticationCleanupBatch); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete expired session notifications: %w", err))
	}
	if _, err := s.pool.Exec(ctx, scrubExpiredNotificationBroadcastsSQL); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("scrub expired notification broadcasts: %w", err))
	}
	if _, err := s.pool.Exec(ctx, cleanupExpiredSessionsSQL, authenticationCleanupBatch); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete inactive authentication sessions: %w", err))
	}
	if _, err := s.pool.Exec(ctx, cleanupOrphanDevicesSQL, authenticationCleanupBatch); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete orphan devices: %w", err))
	}
	if _, err := s.pool.Exec(ctx, cleanupStaleDeviceAuthorizationsSQL, deviceAuthorizationCleanupBatch); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("delete stale device authorizations: %w", err))
	}
	return errors.Join(cleanupErrors...)
}
