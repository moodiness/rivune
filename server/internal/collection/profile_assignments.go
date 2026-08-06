package collection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
)

type collectionAssignments struct {
	profileIDs  []string
	categoryIDs []string
}

func normalizeCollectionAssignmentIDs(requested []string, field string) ([]string, error) {
	if requested == nil {
		return nil, nil
	}
	if len(requested) > 100 {
		return nil, invalid(field + " cannot contain more than 100 identifiers")
	}
	seen := make(map[string]struct{}, len(requested))
	normalized := make([]string, 0, len(requested))
	for _, id := range requested {
		if !validUUID(id) {
			return nil, invalid(field + " must contain valid identifiers")
		}
		canonicalID := strings.ToLower(id)
		if _, duplicate := seen[canonicalID]; duplicate {
			return nil, invalid(field + " must not contain duplicates")
		}
		seen[canonicalID] = struct{}{}
		normalized = append(normalized, canonicalID)
	}
	return normalized, nil
}

func normalizeNewCollectionAssignments(profileIDs, categoryIDs []string, activeProfileID string) (collectionAssignments, error) {
	profiles, err := normalizeCollectionAssignmentIDs(profileIDs, "profileIds")
	if err != nil {
		return collectionAssignments{}, err
	}
	categories, err := normalizeCollectionAssignmentIDs(categoryIDs, "categoryIds")
	if err != nil {
		return collectionAssignments{}, err
	}
	if profileIDs == nil && categoryIDs == nil {
		profiles = []string{activeProfileID}
	}
	if len(profiles) == 0 && len(categories) == 0 {
		return collectionAssignments{}, invalid("at least one profileId or categoryId is required")
	}
	return collectionAssignments{profileIDs: profiles, categoryIDs: categories}, nil
}

func normalizeCollectionAssignmentUpdate(profileIDs, categoryIDs []string) (collectionAssignments, error) {
	profiles, err := normalizeCollectionAssignmentIDs(profileIDs, "profileIds")
	if err != nil {
		return collectionAssignments{}, err
	}
	categories, err := normalizeCollectionAssignmentIDs(categoryIDs, "categoryIds")
	if err != nil {
		return collectionAssignments{}, err
	}
	return collectionAssignments{profileIDs: profiles, categoryIDs: categories}, nil
}

func authorizeCollectionProfiles(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID string, profileIDs []string) error {
	lockIDs := append([]string(nil), profileIDs...)
	included := false
	for _, profileID := range lockIDs {
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

func authorizeGlobalCollectionPolicy(ctx context.Context, tx pgx.Tx, principal auth.Principal) error {
	if !principal.IsGlobalAdministrator() {
		return ErrForbidden
	}
	var administrator bool
	if err := tx.QueryRow(ctx, `
		SELECT role = 'admin'
		FROM users
		WHERE id = $1::uuid
		FOR SHARE
	`, principal.UserID).Scan(&administrator); err != nil {
		if err == pgx.ErrNoRows {
			return ErrForbidden
		}
		return fmt.Errorf("authorize global collection policy: %w", err)
	}
	if !administrator {
		return ErrForbidden
	}
	return nil
}

func lockCollectionCategories(ctx context.Context, tx pgx.Tx, categoryIDs []string) error {
	if len(categoryIDs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text
		FROM access_categories
		WHERE id = ANY($1::uuid[])
		ORDER BY id
		FOR UPDATE
	`, categoryIDs)
	if err != nil {
		return fmt.Errorf("lock collection categories: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate collection categories: %w", err)
	}
	if count != len(categoryIDs) {
		return ErrForbidden
	}
	return nil
}
func lockCollectionPolicyBeforeProfiles(ctx context.Context, tx pgx.Tx, principal auth.Principal, collectionID string, requestedCategoryIDs []string) error {
	var currentCategoryIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT ARRAY(
			SELECT category_id::text
			FROM collection_category_access
			WHERE collection_id = pc.id
			ORDER BY category_id
		)
		FROM profile_collections pc
		WHERE pc.id = $1::uuid
		FOR UPDATE OF pc
	`, collectionID).Scan(&currentCategoryIDs); err != nil {
		if err == pgx.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("lock collection policy: %w", err)
	}
	categoryIDs := mergeCollectionIDs(currentCategoryIDs, requestedCategoryIDs)
	if len(categoryIDs) == 0 {
		return nil
	}
	if err := authorizeGlobalCollectionPolicy(ctx, tx, principal); err != nil {
		return err
	}
	return lockCollectionCategories(ctx, tx, categoryIDs)
}

func lockCollectionAssignments(ctx context.Context, tx pgx.Tx, activeProfileID, collectionID string) (collectionAssignments, error) {
	var lockedID string
	if err := tx.QueryRow(ctx, `
		SELECT pc.id::text
		FROM profile_collections pc
		JOIN profiles active_profile ON active_profile.id = $2::uuid
		WHERE pc.id = $1::uuid
		  AND (
		      EXISTS (SELECT 1 FROM collection_profile_access access WHERE access.collection_id = pc.id AND access.profile_id = active_profile.id)
		      OR EXISTS (SELECT 1 FROM collection_category_access access WHERE access.collection_id = pc.id AND access.category_id = active_profile.category_id)
		  )
		FOR UPDATE OF pc
	`, collectionID, activeProfileID).Scan(&lockedID); err != nil {
		if err == pgx.ErrNoRows {
			return collectionAssignments{}, ErrNotFound
		}
		return collectionAssignments{}, fmt.Errorf("lock collection assignments: %w", err)
	}
	current := collectionAssignments{}
	if err := tx.QueryRow(ctx, `
		SELECT
			ARRAY(SELECT profile_id::text FROM collection_profile_access WHERE collection_id = $1::uuid ORDER BY profile_id FOR UPDATE),
			ARRAY(SELECT category_id::text FROM collection_category_access WHERE collection_id = $1::uuid ORDER BY category_id FOR UPDATE)
	`, collectionID).Scan(&current.profileIDs, &current.categoryIDs); err != nil {
		return collectionAssignments{}, fmt.Errorf("lock collection access rows: %w", err)
	}
	if len(current.profileIDs) == 0 && len(current.categoryIDs) == 0 {
		return collectionAssignments{}, ErrNotFound
	}
	if err := lockCollectionCategories(ctx, tx, current.categoryIDs); err != nil {
		return collectionAssignments{}, err
	}
	return current, nil
}

func mergeCollectionIDs(primary, secondary []string) []string {
	merged := append([]string(nil), primary...)
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	for _, id := range primary {
		seen[id] = struct{}{}
	}
	for _, id := range secondary {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		merged = append(merged, id)
	}
	return merged
}

func authorizeCollectionAssignmentPolicy(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID string, current, requested collectionAssignments) error {
	allCategories := mergeCollectionIDs(current.categoryIDs, requested.categoryIDs)
	if len(allCategories) != 0 {
		if err := authorizeGlobalCollectionPolicy(ctx, tx, principal); err != nil {
			return err
		}
		if err := lockCollectionCategories(ctx, tx, allCategories); err != nil {
			return err
		}
	}
	return authorizeCollectionProfiles(ctx, tx, principal, activeProfileID, mergeCollectionIDs(current.profileIDs, requested.profileIDs))
}

func collectionTargetProfileIDs(ctx context.Context, tx pgx.Tx, assignments collectionAssignments) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT p.id::text
		FROM profiles p
		WHERE p.id = ANY($1::uuid[]) OR p.category_id = ANY($2::uuid[])
		ORDER BY p.id
		FOR SHARE OF p
	`, assignments.profileIDs, assignments.categoryIDs)
	if err != nil {
		return nil, fmt.Errorf("lock collection target profiles: %w", err)
	}
	defer rows.Close()
	profileIDs := make([]string, 0)
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			return nil, fmt.Errorf("scan collection target profile: %w", err)
		}
		profileIDs = append(profileIDs, profileID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection target profiles: %w", err)
	}
	return profileIDs, nil
}

func lockCollectionTargetProfiles(ctx context.Context, tx pgx.Tx, assignments collectionAssignments) ([]string, error) {
	profileIDs, err := collectionTargetProfileIDs(ctx, tx, assignments)
	if err != nil {
		return nil, err
	}
	for _, profileID := range profileIDs {
		if err := lockProfileCollections(ctx, tx, profileID); err != nil {
			return nil, err
		}
	}
	return profileIDs, nil
}

func writeCollectionAssignments(ctx context.Context, tx pgx.Tx, collectionID string, assignments collectionAssignments) error {
	for _, profileID := range assignments.profileIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO collection_profile_access (collection_id, profile_id, position)
			VALUES ($1::uuid, $2::uuid, (SELECT COALESCE(max(position) + 1, 0) FROM collection_profile_access WHERE profile_id = $2::uuid))
			ON CONFLICT (collection_id, profile_id) DO NOTHING
		`, collectionID, profileID); err != nil {
			return fmt.Errorf("grant collection profile access: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO collection_profile_order (collection_id, profile_id, position)
			VALUES ($1::uuid, $2::uuid, (SELECT COALESCE(max(position) + 1, 0) FROM collection_profile_order WHERE profile_id = $2::uuid))
			ON CONFLICT (collection_id, profile_id) DO NOTHING
		`, collectionID, profileID); err != nil {
			return fmt.Errorf("append collection profile order: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM collection_profile_access WHERE collection_id = $1::uuid AND NOT (profile_id = ANY($2::uuid[]))`, collectionID, assignments.profileIDs); err != nil {
		return fmt.Errorf("revoke collection profile access: %w", err)
	}
	for _, categoryID := range assignments.categoryIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO collection_category_access (collection_id, category_id, position)
			VALUES ($1::uuid, $2::uuid, (SELECT COALESCE(max(position) + 1, 0) FROM collection_category_access WHERE category_id = $2::uuid))
			ON CONFLICT (collection_id, category_id) DO NOTHING
		`, collectionID, categoryID); err != nil {
			return fmt.Errorf("grant collection category access: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM collection_category_access WHERE collection_id = $1::uuid AND NOT (category_id = ANY($2::uuid[]))`, collectionID, assignments.categoryIDs); err != nil {
		return fmt.Errorf("revoke collection category access: %w", err)
	}
	return nil
}

func ensureCollectionProfileLimits(ctx context.Context, tx pgx.Tx, profileIDs []string) error {
	if len(profileIDs) == 0 {
		return nil
	}
	var overLimit bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profiles target
			WHERE target.id = ANY($1::uuid[])
			  AND (
				SELECT count(DISTINCT pc.id)
				FROM profile_collections pc
				LEFT JOIN collection_profile_access explicit_access ON explicit_access.collection_id = pc.id AND explicit_access.profile_id = target.id
				LEFT JOIN collection_category_access category_access ON category_access.collection_id = pc.id AND category_access.category_id = target.category_id
				WHERE explicit_access.collection_id IS NOT NULL OR category_access.collection_id IS NOT NULL
			  ) > $2
		)
	`, profileIDs, maximumCollections).Scan(&overLimit); err != nil {
		return fmt.Errorf("count effective profile collections: %w", err)
	}
	if overLimit {
		return invalid("an assigned profile cannot contain more than 100 collections")
	}
	return nil
}
func ensureCollectionCategoryLimits(ctx context.Context, tx pgx.Tx, categoryIDs []string) error {
	if len(categoryIDs) == 0 {
		return nil
	}
	var overLimit bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM collection_category_access access
			WHERE access.category_id = ANY($1::uuid[])
			GROUP BY access.category_id
			HAVING count(DISTINCT access.collection_id) > $2
		)
	`, categoryIDs, maximumCollections).Scan(&overLimit); err != nil {
		return fmt.Errorf("count category collection policies: %w", err)
	}
	if overLimit {
		return invalid("an assigned category cannot contain more than 100 collections")
	}
	return nil
}

func authorizeExistingCollectionAssignments(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, collectionID string) (collectionAssignments, error) {
	current, err := lockCollectionAssignments(ctx, tx, activeProfileID, collectionID)
	if err != nil {
		return collectionAssignments{}, err
	}
	if err := authorizeCollectionAssignmentPolicy(ctx, tx, principal, activeProfileID, current, current); err != nil {
		return collectionAssignments{}, err
	}
	return current, nil
}

func applyCollectionAssignments(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, collectionID string, requested collectionAssignments, profilesProvided, categoriesProvided bool) (collectionAssignments, error) {
	current, err := lockCollectionAssignments(ctx, tx, activeProfileID, collectionID)
	if err != nil {
		return collectionAssignments{}, err
	}
	result := current
	if profilesProvided {
		result.profileIDs = requested.profileIDs
	}
	if categoriesProvided {
		result.categoryIDs = requested.categoryIDs
	}
	if len(result.profileIDs) == 0 && len(result.categoryIDs) == 0 {
		return collectionAssignments{}, invalid("at least one profileId or categoryId is required")
	}
	if err := authorizeCollectionAssignmentPolicy(ctx, tx, principal, activeProfileID, current, result); err != nil {
		return collectionAssignments{}, err
	}
	targetProfileIDs, err := lockCollectionTargetProfiles(ctx, tx, result)
	if err != nil {
		return collectionAssignments{}, err
	}
	if err := writeCollectionAssignments(ctx, tx, collectionID, result); err != nil {
		return collectionAssignments{}, err
	}
	if err := ensureCollectionProfileLimits(ctx, tx, targetProfileIDs); err != nil {
		return collectionAssignments{}, err
	}
	if err := ensureCollectionCategoryLimits(ctx, tx, result.categoryIDs); err != nil {
		return collectionAssignments{}, err
	}
	return result, nil
}
