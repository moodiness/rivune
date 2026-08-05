package collection

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

func normalizeCollectionProfileIDs(requested []string, activeProfileID string) ([]string, error) {
	if requested == nil {
		return []string{activeProfileID}, nil
	}
	if len(requested) == 0 || len(requested) > 100 {
		return nil, invalid("profileIds must contain 1 to 100 profiles")
	}
	seen := make(map[string]struct{}, len(requested))
	profileIDs := make([]string, 0, len(requested))
	for _, profileID := range requested {
		if !validUUID(profileID) {
			return nil, invalid("profileIds must contain valid profile identifiers")
		}
		if _, duplicate := seen[profileID]; duplicate {
			return nil, invalid("profileIds must not contain duplicates")
		}
		seen[profileID] = struct{}{}
		profileIDs = append(profileIDs, profileID)
	}
	return profileIDs, nil
}

func authorizeCollectionProfiles(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID string, profileIDs []string) error {
	lockIDs := append([]string(nil), profileIDs...)
	included := false
	for _, profileID := range profileIDs {
		if profileID == activeProfileID {
			included = true
			break
		}
	}
	if !included {
		lockIDs = append(lockIDs, activeProfileID)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, lockIDs, true)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrForbidden
	}
	sort.Strings(lockIDs)
	for _, profileID := range lockIDs {
		if err := lockProfileCollections(ctx, tx, profileID); err != nil {
			return err
		}
	}
	return nil
}

func writeCollectionProfiles(ctx context.Context, tx pgx.Tx, collectionID string, profileIDs []string) error {
	for _, profileID := range profileIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO collection_profile_access (collection_id, profile_id, position)
			VALUES (
				$1::uuid, $2::uuid,
				(SELECT COALESCE(max(position) + 1, 0) FROM collection_profile_access WHERE profile_id = $2::uuid)
			)
			ON CONFLICT (collection_id, profile_id) DO NOTHING
		`, collectionID, profileID); err != nil {
			return fmt.Errorf("grant collection profile access: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM collection_profile_access
		WHERE collection_id = $1::uuid AND NOT (profile_id = ANY($2::uuid[]))
	`, collectionID, profileIDs); err != nil {
		return fmt.Errorf("revoke collection profile access: %w", err)
	}
	return nil
}

func lockCollectionProfileIDs(ctx context.Context, tx pgx.Tx, activeProfileID, collectionID string) ([]string, error) {
	var lockedID string
	if err := tx.QueryRow(ctx, `
		SELECT pc.id::text
		FROM profile_collections pc
		WHERE pc.id = $1::uuid
		  AND EXISTS (
		      SELECT 1 FROM collection_profile_access access
		      WHERE access.collection_id = pc.id AND access.profile_id = $2::uuid
		  )
		FOR UPDATE
	`, collectionID, activeProfileID).Scan(&lockedID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock collection profile access: %w", err)
	}
	var profileIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT ARRAY(
			SELECT profile_id::text
			FROM collection_profile_access
			WHERE collection_id = $1::uuid
			ORDER BY profile_id
		)
	`, collectionID).Scan(&profileIDs); err != nil {
		return nil, fmt.Errorf("query collection profile access: %w", err)
	}
	if len(profileIDs) == 0 {
		return nil, ErrNotFound
	}
	return profileIDs, nil
}

func authorizeExistingCollectionProfiles(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, collectionID string) error {
	profileIDs, err := lockCollectionProfileIDs(ctx, tx, activeProfileID, collectionID)
	if err != nil {
		return err
	}
	return authorizeCollectionProfiles(ctx, tx, principal, activeProfileID, profileIDs)
}

func applyCollectionProfiles(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, collectionID string, profileIDs []string) error {
	currentProfileIDs, err := lockCollectionProfileIDs(ctx, tx, activeProfileID, collectionID)
	if err != nil {
		return err
	}
	accessible := false
	managedProfileIDs := append([]string(nil), profileIDs...)
	managed := make(map[string]struct{}, len(profileIDs)+len(currentProfileIDs))
	for _, profileID := range profileIDs {
		managed[profileID] = struct{}{}
	}
	for _, profileID := range currentProfileIDs {
		if profileID == activeProfileID {
			accessible = true
		}
		if _, exists := managed[profileID]; !exists {
			managedProfileIDs = append(managedProfileIDs, profileID)
			managed[profileID] = struct{}{}
		}
	}
	if !accessible {
		return ErrNotFound
	}
	if err := authorizeCollectionProfiles(ctx, tx, principal, activeProfileID, managedProfileIDs); err != nil {
		return err
	}
	var profileAtLimit bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM unnest($1::uuid[]) target(profile_id)
			WHERE NOT EXISTS (
				SELECT 1 FROM collection_profile_access current_access
				WHERE current_access.collection_id = $2::uuid
				  AND current_access.profile_id = target.profile_id
			)
			  AND (
				SELECT count(*) FROM collection_profile_access profile_access
				WHERE profile_access.profile_id = target.profile_id
			  ) >= $3
		)
	`, profileIDs, collectionID, maximumCollections).Scan(&profileAtLimit); err != nil {
		return fmt.Errorf("count assigned profile collections: %w", err)
	}
	if profileAtLimit {
		return invalid("an assigned profile cannot contain more than 100 collections")
	}
	return writeCollectionProfiles(ctx, tx, collectionID, profileIDs)
}
