package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReloadLinkedPrincipal rehydrates authorization from a trusted native session
// and profile link. Unlike access-token authentication, validity is bounded by
// the native refresh expiry so a protocol adapter can issue its own audience-
// specific credential without accepting a native token.
func (s *Service) ReloadLinkedPrincipal(ctx context.Context, sessionID, profileID string) (Principal, error) {
	sessionID = strings.TrimSpace(sessionID)
	profileID = strings.TrimSpace(profileID)
	if sessionID == "" || profileID == "" {
		return Principal{}, ErrInvalidToken
	}
	timezone := s.runtimeTimezone(ctx)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Principal{}, fmt.Errorf("begin linked principal reload: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var linkedUserID, linkedDeviceID string
	if err := tx.QueryRow(ctx, `
		SELECT user_id::text, device_id::text
		FROM auth_sessions
		WHERE id::text = $1 AND active_profile_id::text = $2
	`, sessionID, profileID).Scan(&linkedUserID, &linkedDeviceID); errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	} else if err != nil {
		return Principal{}, fmt.Errorf("resolve linked session ownership: %w", err)
	}

	principal := Principal{UserID: linkedUserID, DeviceID: linkedDeviceID}
	if err := tx.QueryRow(ctx, `
		SELECT username, role
		FROM users
		WHERE id::text = $1
		FOR SHARE
	`, linkedUserID).Scan(&principal.Username, &principal.Role); errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	} else if err != nil {
		return Principal{}, fmt.Errorf("lock linked session user: %w", err)
	}

	var deviceCategoryID *string
	if err := tx.QueryRow(ctx, `
		SELECT platform, category_id::text
		FROM devices
		WHERE id::text = $1 AND user_id::text = $2
		FOR SHARE
	`, linkedDeviceID, linkedUserID).Scan(&principal.Platform, &deviceCategoryID); errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	} else if err != nil {
		return Principal{}, fmt.Errorf("lock linked session device: %w", err)
	}

	var lockedProfileID string
	var profileCategoryID *string
	var access ProfileAccess
	if err := tx.QueryRow(ctx, `
		SELECT id::text, category_id::text, enabled,
		       available_from::text, available_until::text,
		       to_char(access_start_time, 'HH24:MI'),
		       to_char(access_end_time, 'HH24:MI'),
		       COALESCE(access_timezone, 'UTC')
		FROM profiles
		WHERE id::text = $1
		FOR SHARE
	`, profileID).Scan(
		&lockedProfileID, &profileCategoryID, &access.Enabled,
		&access.AvailableFrom, &access.AvailableUntil,
		&access.AccessStartTime, &access.AccessEndTime, &access.AccessTimezone,
	); errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	} else if err != nil {
		return Principal{}, fmt.Errorf("lock linked session profile: %w", err)
	}
	principal.ActiveProfileID = &lockedProfileID

	hasProfileAccess := true
	var grantedCanManage bool
	if err := tx.QueryRow(ctx, `
		SELECT can_manage
		FROM user_profile_access
		WHERE user_id::text = $1 AND profile_id::text = $2
		FOR SHARE
	`, linkedUserID, profileID).Scan(&grantedCanManage); errors.Is(err, pgx.ErrNoRows) {
		hasProfileAccess = false
	} else if err != nil {
		return Principal{}, fmt.Errorf("lock linked profile access: %w", err)
	}

	var refreshExpiresAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id::text, authorization_scope, category_id::text,
		       refresh_expires_at, profile_grant_expires_at, profile_context_hash
		FROM auth_sessions
		WHERE id::text = $1
		  AND user_id::text = $2
		  AND device_id::text = $3
		  AND active_profile_id::text = $4
		  AND revoked_at IS NULL
		  AND profile_context_hash IS NOT NULL
		FOR SHARE
	`, sessionID, linkedUserID, linkedDeviceID, profileID).Scan(
		&principal.SessionID, &principal.AuthorizationScope,
		&principal.CategoryID, &refreshExpiresAt, &principal.ProfileGrantExpiresAt,
		&principal.ProfileContextHash,
	); errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	} else if err != nil {
		return Principal{}, fmt.Errorf("lock linked authentication session: %w", err)
	}
	var validatedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&validatedAt); err != nil {
		return Principal{}, fmt.Errorf("read linked authentication validation time: %w", err)
	}
	if !refreshExpiresAt.After(validatedAt) || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(validatedAt) {
		return Principal{}, ErrInvalidToken
	}

	scopeValid := validSessionScope(
		principal.Role,
		principal.AuthorizationScope,
		principal.CategoryID,
		deviceCategoryID,
		profileCategoryID,
	)
	if principal.AuthorizationScope == AuthorizationScopeGlobalAdministrator {
		scopeValid = scopeValid && deviceCategoryID != nil && profileCategoryID != nil && *deviceCategoryID == *profileCategoryID
	}
	if principal.AuthorizationScope == AuthorizationScopeCategory && !hasProfileAccess {
		scopeValid = false
	}
	if !scopeValid {
		revokedIDs, lockErr := lockActiveCompatibilitySessionIDs(ctx, tx, principal.SessionID)
		if lockErr != nil {
			return Principal{}, fmt.Errorf("lock mismatched linked compatibility sessions: %w", lockErr)
		}
		if _, revokeErr := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = COALESCE(revoked_at, now()),
			    revoked_reason = COALESCE(revoked_reason, 'authorization_category_mismatch')
			WHERE id = $1::uuid
		`, principal.SessionID); revokeErr != nil {
			return Principal{}, fmt.Errorf("revoke mismatched linked session: %w", revokeErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return Principal{}, fmt.Errorf("commit linked session revocation: %w", commitErr)
		}
		notifyCompatibilitySessionRevocations(s.compatibilitySessionRevocationNotifier(), revokedIDs)
		return Principal{}, ErrInvalidToken
	}

	access.AccessTimezone = timezone
	if !ProfileAccessibleAt(access, validatedAt) {
		return Principal{}, ErrInvalidToken
	}
	principal.ActiveProfileCanManage = principal.IsGlobalAdministrator() || grantedCanManage
	if principal.CategoryID != nil {
		principal.Category, err = loadCategoryRef(ctx, tx, *principal.CategoryID)
		if err != nil {
			return Principal{}, err
		}
	}

	command, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET last_seen_at = clock_timestamp()
		WHERE id = $1::uuid
		  AND active_profile_id = $2::uuid
		  AND revoked_at IS NULL
		  AND refresh_expires_at > clock_timestamp()
		  AND profile_grant_expires_at > clock_timestamp()
	`, principal.SessionID, profileID)
	if err != nil {
		return Principal{}, fmt.Errorf("touch linked authentication session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return Principal{}, ErrInvalidToken
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, fmt.Errorf("commit linked principal reload: %w", err)
	}
	return principal, nil
}

// RevokeUnfinishedLinkedSession closes a native session created for a
// compatibility login that failed before its linked credential was issued.
func (s *Service) RevokeUnfinishedLinkedSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrInvalidToken
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = COALESCE(revoked_reason, 'compatibility_login_failed')
		WHERE id = $1::uuid
	`, sessionID)
	if err != nil {
		return fmt.Errorf("revoke unfinished linked session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrInvalidToken
	}
	return nil
}

// LogoutLinkedSession revokes a native session only when the supplied
// compatibility session is authoritatively linked to it and the same profile.
// PostgreSQL executes the update and the compatibility revocation trigger in
// one statement transaction, so a trigger failure leaves both credentials
// active and retryable rather than committing a native-only revocation.
func (s *Service) LogoutLinkedSession(ctx context.Context, principal Principal, compatSessionID string) error {
	sessionID := strings.TrimSpace(principal.SessionID)
	userID := strings.TrimSpace(principal.UserID)
	compatSessionID = strings.TrimSpace(compatSessionID)
	if sessionID == "" || userID == "" || compatSessionID == "" || principal.ActiveProfileID == nil {
		return ErrInvalidToken
	}
	profileID := strings.TrimSpace(*principal.ActiveProfileID)
	if profileID == "" {
		return ErrInvalidToken
	}

	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions AS native
		SET revoked_at = COALESCE(native.revoked_at, now()),
		    revoked_reason = COALESCE(native.revoked_reason, 'logout')
		WHERE native.id::text = $1
		  AND native.user_id::text = $2
		  AND native.active_profile_id::text = $3
		  AND EXISTS (
		    SELECT 1
		    FROM jellyfin_compat_sessions AS compat
		    WHERE compat.id::text = $4
		      AND compat.auth_session_id = native.id
		      AND compat.profile_id = native.active_profile_id
		  )
	`, sessionID, userID, profileID, compatSessionID)
	if err != nil {
		return fmt.Errorf("revoke linked compatibility session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrInvalidToken
	}
	return nil
}
