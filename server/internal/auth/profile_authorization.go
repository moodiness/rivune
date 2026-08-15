package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type profileAuthorizationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ReloadAndLockPrincipal reloads the mutable authorization state for a
// captured native access-token session. Locks follow the mutation order used by
// account and profile updates: user, device, profile, profile grant, then
// session. Protected work must commit in the caller-owned transaction while
// these locks remain held.
func ReloadAndLockPrincipal(
	ctx context.Context,
	tx pgx.Tx,
	captured Principal,
	_ time.Time,
	configuredLocation *time.Location,
) (Principal, bool, error) {
	if configuredLocation == nil {
		return Principal{}, false, fmt.Errorf("configured timezone is required")
	}
	principal, authorized, _, err := reloadAndLockPrincipal(ctx, tx, captured, configuredLocation, false)
	return principal, authorized, err
}

// ReloadAndLockLinkedPrincipal revalidates a captured protocol-linked session
// in the caller-owned mutation transaction. Its authority is deliberately
// bounded by refresh expiry, while ReloadAndLockPrincipal retains native
// access-token expiry semantics.
func ReloadAndLockLinkedPrincipal(
	ctx context.Context,
	tx pgx.Tx,
	captured Principal,
	_ time.Time,
	configuredLocation *time.Location,
) (Principal, bool, error) {
	if configuredLocation == nil {
		return Principal{}, false, fmt.Errorf("configured timezone is required")
	}
	principal, authorized, _, err := reloadAndLockPrincipal(ctx, tx, captured, configuredLocation, true)
	return principal, authorized, err
}

func reloadAndLockPrincipal(
	ctx context.Context,
	tx pgx.Tx,
	captured Principal,
	configuredLocation *time.Location,
	linked bool,
) (Principal, bool, time.Time, error) {
	if configuredLocation == nil {
		return Principal{}, false, time.Time{}, fmt.Errorf("configured timezone is required")
	}
	if captured.ActiveProfileID == nil || len(captured.ProfileContextHash) == 0 {
		return Principal{}, false, time.Time{}, nil
	}

	principal := Principal{UserID: captured.UserID, DeviceID: captured.DeviceID}
	if err := tx.QueryRow(ctx, `
		SELECT username, role
		FROM users
		WHERE id::text = $1
		FOR SHARE
	`, captured.UserID).Scan(&principal.Username, &principal.Role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, false, time.Time{}, nil
		}
		return Principal{}, false, time.Time{}, fmt.Errorf("lock deferred authorization user: %w", err)
	}

	var deviceCategoryID *string
	if err := tx.QueryRow(ctx, `
		SELECT category_id::text, platform
		FROM devices
		WHERE id::text = $1 AND user_id::text = $2
		FOR SHARE
	`, captured.DeviceID, captured.UserID).Scan(&deviceCategoryID, &principal.Platform); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, false, time.Time{}, nil
		}
		return Principal{}, false, time.Time{}, fmt.Errorf("lock deferred authorization device: %w", err)
	}

	var activeProfileCategoryID *string
	var access ProfileAccess
	if err := tx.QueryRow(ctx, `
		SELECT id::text, category_id::text, enabled,
		       available_from::text, available_until::text,
		       to_char(access_start_time, 'HH24:MI'),
		       to_char(access_end_time, 'HH24:MI')
		FROM profiles
		WHERE id::text = $1
		FOR SHARE
	`, *captured.ActiveProfileID).Scan(
		&principal.ActiveProfileID, &activeProfileCategoryID,
		&access.Enabled, &access.AvailableFrom, &access.AvailableUntil,
		&access.AccessStartTime, &access.AccessEndTime,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, false, time.Time{}, nil
		}
		return Principal{}, false, time.Time{}, fmt.Errorf("lock deferred authorization profile: %w", err)
	}

	hasProfileAccess := true
	var grantedCanManage bool
	if err := tx.QueryRow(ctx, `
		SELECT can_manage
		FROM user_profile_access
		WHERE user_id::text = $1 AND profile_id::text = $2
		FOR SHARE
	`, captured.UserID, *captured.ActiveProfileID).Scan(&grantedCanManage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			hasProfileAccess = false
		} else {
			return Principal{}, false, time.Time{}, fmt.Errorf("lock deferred profile grant: %w", err)
		}
	}

	var accessExpiresAt, refreshExpiresAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id::text, authorization_scope, category_id::text,
		       profile_grant_expires_at, profile_context_hash,
		       access_expires_at, refresh_expires_at
		FROM auth_sessions
		WHERE id::text = $1
		  AND user_id::text = $2
		  AND device_id::text = $3
		  AND active_profile_id::text = $4
		  AND revoked_at IS NULL
		FOR SHARE
	`, captured.SessionID, captured.UserID, captured.DeviceID, *captured.ActiveProfileID).Scan(
		&principal.SessionID, &principal.AuthorizationScope,
		&principal.CategoryID, &principal.ProfileGrantExpiresAt,
		&principal.ProfileContextHash, &accessExpiresAt, &refreshExpiresAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, false, time.Time{}, nil
		}
		return Principal{}, false, time.Time{}, fmt.Errorf("lock deferred authorization session: %w", err)
	}
	var validatedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&validatedAt); err != nil {
		return Principal{}, false, time.Time{}, fmt.Errorf("read deferred authorization time: %w", err)
	}
	authorityExpiresAt := accessExpiresAt
	if linked {
		authorityExpiresAt = refreshExpiresAt
	}
	if !authorityExpiresAt.After(validatedAt) {
		return Principal{}, false, validatedAt, nil
	}
	if subtle.ConstantTimeCompare(principal.ProfileContextHash, captured.ProfileContextHash) != 1 {
		return Principal{}, false, validatedAt, nil
	}

	scopeValid := validSessionScope(
		principal.Role,
		principal.AuthorizationScope,
		principal.CategoryID,
		deviceCategoryID,
		activeProfileCategoryID,
	)
	if linked && principal.AuthorizationScope == AuthorizationScopeGlobalAdministrator {
		scopeValid = scopeValid && deviceCategoryID != nil && activeProfileCategoryID != nil && *deviceCategoryID == *activeProfileCategoryID
	}
	if !scopeValid || (principal.AuthorizationScope == AuthorizationScopeCategory && !hasProfileAccess) {
		return Principal{}, false, validatedAt, nil
	}
	principal.ActiveProfileCanManage = principal.IsGlobalAdministrator() || grantedCanManage
	access.AccessTimezone = configuredLocation.String()
	if principal.ProfileGrantExpiresAt == nil ||
		!principal.ProfileGrantExpiresAt.After(validatedAt) ||
		!ProfileAccessibleAt(access, validatedAt) {
		return Principal{}, false, validatedAt, nil
	}
	return principal, true, validatedAt, nil
}

// LockActiveProfileSelection validates the captured active-profile capability
// against its authoritative native session and holds a shared row lock through
// the caller-owned transaction. Selection and clear update the same row, so
// whichever transaction acquires it first establishes the authorization order.
func LockActiveProfileSelection(ctx context.Context, tx pgx.Tx, captured Principal) (bool, error) {
	if strings.TrimSpace(captured.SessionID) == "" ||
		strings.TrimSpace(captured.UserID) == "" ||
		strings.TrimSpace(captured.DeviceID) == "" ||
		captured.ActiveProfileID == nil ||
		strings.TrimSpace(*captured.ActiveProfileID) == "" ||
		len(captured.ProfileContextHash) == 0 {
		return false, nil
	}

	var authoritativeHash []byte
	var accessExpiresAt time.Time
	var profileGrantExpiresAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT profile_context_hash, access_expires_at, profile_grant_expires_at
		FROM auth_sessions
		WHERE id::text = $1
		  AND user_id::text = $2
		  AND device_id::text = $3
		  AND active_profile_id::text = $4
		  AND revoked_at IS NULL
		FOR SHARE
	`, captured.SessionID, captured.UserID, captured.DeviceID, *captured.ActiveProfileID).Scan(
		&authoritativeHash, &accessExpiresAt, &profileGrantExpiresAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "40001" {
			return false, nil
		}
		return false, fmt.Errorf("lock active profile selection: %w", err)
	}
	var validatedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&validatedAt); err != nil {
		return false, fmt.Errorf("read active profile selection time: %w", err)
	}
	return accessExpiresAt.After(validatedAt) &&
		profileGrantExpiresAt != nil && profileGrantExpiresAt.After(validatedAt) &&
		subtle.ConstantTimeCompare(authoritativeHash, captured.ProfileContextHash) == 1, nil
}

// CanAccessProfiles reports whether the principal may access every requested profile.
func CanAccessProfiles(ctx context.Context, querier profileAuthorizationQuerier, principal Principal, profileIDs []string) (bool, error) {
	return authorizeProfiles(ctx, querier, principal, profileIDs, false)
}

// CanManageProfiles reports whether the principal may manage every requested profile.
func CanManageProfiles(ctx context.Context, querier profileAuthorizationQuerier, principal Principal, profileIDs []string) (bool, error) {
	return authorizeProfiles(ctx, querier, principal, profileIDs, true)
}

// AuthorizeAndLockProfiles evaluates profile access while holding shared locks
// on the authoritative profile and grant rows for the caller-owned transaction.
// Protected reads and writes must use the same transaction before it commits.
func AuthorizeAndLockProfiles(
	ctx context.Context,
	tx pgx.Tx,
	principal Principal,
	profileIDs []string,
	requireManagement bool,
) (bool, error) {
	if !validProfileIDs(profileIDs) {
		return false, nil
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `
		WITH locked_profiles AS MATERIALIZED (
			SELECT p.id, p.category_id
			FROM profiles p
			WHERE p.id = ANY($1::uuid[])
			ORDER BY p.id
			FOR SHARE
		), locked_access AS MATERIALIZED (
			SELECT upa.profile_id, upa.can_manage
			FROM user_profile_access upa
			JOIN locked_profiles p ON p.id = upa.profile_id
			WHERE upa.user_id::text = $2
			ORDER BY upa.profile_id
			FOR SHARE OF upa
		)
		SELECT count(*) = cardinality($1::uuid[])
		FROM locked_profiles p
		LEFT JOIN locked_access upa ON upa.profile_id = p.id
		WHERE
		  $3
		  OR (
		    $4 = 'category'
		    AND p.category_id::text = $5
		    AND upa.profile_id IS NOT NULL
		    AND (NOT $6 OR upa.can_manage)
		  )
	`, profileIDs, principal.UserID, principal.IsGlobalAdministrator(), principal.AuthorizationScope,
		principalCategoryID(principal), requireManagement).Scan(&authorized); err != nil {
		return false, fmt.Errorf("authorize and lock profile access: %w", err)
	}
	return authorized, nil
}

func authorizeProfiles(ctx context.Context, querier profileAuthorizationQuerier, principal Principal, profileIDs []string, requireManagement bool) (bool, error) {
	if !validProfileIDs(profileIDs) {
		return false, nil
	}
	var authorized bool
	if err := querier.QueryRow(ctx, `
		SELECT count(*) = cardinality($1::uuid[])
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id::text = $2
		WHERE p.id = ANY($1::uuid[])
		  AND (
		    $3
		    OR (
		      $4 = 'category'
		      AND p.category_id::text = $5
		      AND upa.user_id IS NOT NULL
		      AND (NOT $6 OR upa.can_manage)
		    )
		  )
	`, profileIDs, principal.UserID, principal.IsGlobalAdministrator(), principal.AuthorizationScope,
		principalCategoryID(principal), requireManagement).Scan(&authorized); err != nil {
		return false, fmt.Errorf("authorize profile access: %w", err)
	}
	return authorized, nil
}

func validProfileIDs(profileIDs []string) bool {
	if len(profileIDs) == 0 {
		return false
	}
	for _, profileID := range profileIDs {
		var parsed pgtype.UUID
		if err := parsed.Scan(profileID); err != nil || !parsed.Valid {
			return false
		}
	}
	return true
}

func principalCategoryID(principal Principal) string {
	if principal.CategoryID == nil {
		return ""
	}
	return *principal.CategoryID
}
