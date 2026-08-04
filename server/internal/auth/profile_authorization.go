package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type profileAuthorizationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
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
