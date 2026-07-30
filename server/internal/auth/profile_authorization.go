package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type profileAuthorizationQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// CanManageProfiles reports whether the principal may manage every requested profile.
func CanManageProfiles(ctx context.Context, querier profileAuthorizationQuerier, principal Principal, profileIDs []string) (bool, error) {
	if len(profileIDs) == 0 {
		return false, nil
	}
	var authorized bool
	if err := querier.QueryRow(ctx, `
		SELECT count(*) = cardinality($1::uuid[])
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id::text = $2
		WHERE p.id = ANY($1::uuid[])
		  AND ($3 = 'admin' OR COALESCE(upa.can_manage, false))
	`, profileIDs, principal.UserID, principal.Role).Scan(&authorized); err != nil {
		return false, fmt.Errorf("authorize profile management: %w", err)
	}
	return authorized, nil
}
