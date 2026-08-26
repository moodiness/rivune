package addon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moodiness/rivune/server/internal/auth"
)

type addonAssignments struct {
	profileIDs  []string
	categoryIDs []string
}

func normalizeInstallAssignments(requestedProfileIDs, requestedCategoryIDs []string, activeProfileID string) (addonAssignments, error) {
	if requestedProfileIDs == nil && requestedCategoryIDs == nil {
		return addonAssignments{profileIDs: []string{activeProfileID}, categoryIDs: []string{}}, nil
	}
	profileIDs, err := normalizeAssignmentIDs("profileIds", requestedProfileIDs)
	if err != nil {
		return addonAssignments{}, err
	}
	categoryIDs, err := normalizeAssignmentIDs("categoryIds", requestedCategoryIDs)
	if err != nil {
		return addonAssignments{}, err
	}
	if len(profileIDs)+len(categoryIDs) == 0 {
		return addonAssignments{}, fmt.Errorf("%w: at least one profileId or categoryId is required", ErrInvalidInput)
	}
	return addonAssignments{profileIDs: profileIDs, categoryIDs: categoryIDs}, nil
}

func normalizeAssignmentIDs(field string, requested []string) ([]string, error) {
	if len(requested) > 100 {
		return nil, fmt.Errorf("%w: %s must contain no more than 100 identifiers", ErrInvalidInput, field)
	}
	seen := make(map[string]struct{}, len(requested))
	ids := make([]string, 0, len(requested))
	for _, id := range requested {
		if !validUUID(id) {
			return nil, fmt.Errorf("%w: %s must contain valid identifiers", ErrInvalidInput, field)
		}
		canonicalID := strings.ToLower(id)
		if _, duplicate := seen[canonicalID]; duplicate {
			return nil, fmt.Errorf("%w: %s must not contain duplicates", ErrInvalidInput, field)
		}
		seen[canonicalID] = struct{}{}
		ids = append(ids, canonicalID)
	}
	return ids, nil
}

func authorizeProfileAssignments(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID string, profileIDs []string) error {
	lockIDs := append([]string(nil), profileIDs...)
	if _, included := stringSet(profileIDs)[activeProfileID]; !included {
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
		if err := lockProfile(ctx, tx, profileID); err != nil {
			return err
		}
	}
	return nil
}

func authorizeCategoryAssignments(ctx context.Context, tx pgx.Tx, principal auth.Principal, categoryIDs []string) error {
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return err
	}
	return lockCategoryRows(ctx, tx, categoryIDs)
}

func lockCategoryRows(ctx context.Context, tx pgx.Tx, categoryIDs []string) error {
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
		return fmt.Errorf("lock addon assignment categories: %w", err)
	}
	defer rows.Close()
	found := make(map[string]struct{}, len(categoryIDs))
	for rows.Next() {
		var categoryID string
		if err := rows.Scan(&categoryID); err != nil {
			return fmt.Errorf("scan addon assignment category: %w", err)
		}
		found[categoryID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate addon assignment categories: %w", err)
	}
	if len(found) != len(stringSet(categoryIDs)) {
		return ErrForbidden
	}
	return nil
}

func categoriesForProfiles(ctx context.Context, tx pgx.Tx, profileIDs []string) ([]string, error) {
	if len(profileIDs) == 0 {
		return []string{}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT category_id::text
		FROM profiles
		WHERE id = ANY($1::uuid[])
		ORDER BY category_id::text
	`, profileIDs)
	if err != nil {
		return nil, fmt.Errorf("read addon profile categories: %w", err)
	}
	defer rows.Close()
	categoryIDs := make([]string, 0)
	for rows.Next() {
		var categoryID string
		if err := rows.Scan(&categoryID); err != nil {
			return nil, fmt.Errorf("scan addon profile category: %w", err)
		}
		categoryIDs = append(categoryIDs, categoryID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate addon profile categories: %w", err)
	}
	return categoryIDs, nil
}

func authorizeAssignments(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID string, assignments addonAssignments) error {
	profileCategoryIDs, err := categoriesForProfiles(ctx, tx, mergeIDs(assignments.profileIDs, []string{activeProfileID}))
	if err != nil {
		return err
	}
	if len(assignments.categoryIDs) > 0 {
		if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
			return err
		}
	}
	if err := lockCategoryRows(ctx, tx, mergeIDs(assignments.categoryIDs, profileCategoryIDs)); err != nil {
		return err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, activeProfileID); err != nil {
		return err
	}
	if err := authorizeProfileAssignments(ctx, tx, principal, activeProfileID, assignments.profileIDs); err != nil {
		return err
	}
	currentProfileCategoryIDs, err := categoriesForProfiles(ctx, tx, mergeIDs(assignments.profileIDs, []string{activeProfileID}))
	if err != nil {
		return err
	}
	if !equalIDs(profileCategoryIDs, currentProfileCategoryIDs) {
		return ErrForbidden
	}
	return nil
}

func writeAddonAssignments(ctx context.Context, tx pgx.Tx, addonID string, assignments addonAssignments) error {
	for _, profileID := range assignments.profileIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO addon_profile_access (addon_id, profile_id, position)
			VALUES ($1::uuid, $2::uuid,
				(SELECT COALESCE(max(position) + 1, 0) FROM addon_profile_access WHERE profile_id = $2::uuid))
			ON CONFLICT (addon_id, profile_id) DO NOTHING
		`, addonID, profileID); err != nil {
			return fmt.Errorf("grant addon profile access: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO addon_profile_order (addon_id, profile_id, position)
			VALUES ($1::uuid, $2::uuid,
				(SELECT COALESCE(max(position) + 1, 0) FROM addon_profile_order WHERE profile_id = $2::uuid))
			ON CONFLICT (addon_id, profile_id) DO NOTHING
		`, addonID, profileID); err != nil {
			return fmt.Errorf("append addon profile order: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM addon_profile_access
		WHERE addon_id = $1::uuid AND NOT (profile_id = ANY($2::uuid[]))
	`, addonID, assignments.profileIDs); err != nil {
		return fmt.Errorf("revoke addon profile access: %w", err)
	}
	for _, categoryID := range assignments.categoryIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO addon_category_access (addon_id, category_id, position)
			VALUES ($1::uuid, $2::uuid,
				(SELECT COALESCE(max(position) + 1, 0) FROM addon_category_access WHERE category_id = $2::uuid))
			ON CONFLICT (addon_id, category_id) DO NOTHING
		`, addonID, categoryID); err != nil {
			return fmt.Errorf("grant addon category access: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM addon_category_access
		WHERE addon_id = $1::uuid AND NOT (category_id = ANY($2::uuid[]))
	`, addonID, assignments.categoryIDs); err != nil {
		return fmt.Errorf("revoke addon category access: %w", err)
	}
	return nil
}

func addonTransportAssigned(ctx context.Context, tx pgx.Tx, transportURL string, assignments addonAssignments, exceptAddonID *string) (bool, error) {
	var assigned bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profile_addons installed
			WHERE installed.transport_url = $1
			  AND ($4::uuid IS NULL OR installed.id <> $4::uuid)
			  AND (
			      EXISTS (
			          SELECT 1 FROM addon_profile_access explicit_access
			          WHERE explicit_access.addon_id = installed.id
			            AND explicit_access.profile_id = ANY($2::uuid[])
			      )
			      OR EXISTS (
			          SELECT 1 FROM addon_category_access category_access
			          WHERE category_access.addon_id = installed.id
			            AND category_access.category_id = ANY($3::uuid[])
			      )
			      OR EXISTS (
			          SELECT 1
			          FROM addon_category_access category_access
			          JOIN profiles requested_profile ON requested_profile.id = ANY($2::uuid[])
			          WHERE category_access.addon_id = installed.id
			            AND category_access.category_id = requested_profile.category_id
			      )
			      OR EXISTS (
			          SELECT 1
			          FROM addon_profile_access explicit_access
			          JOIN profiles existing_profile ON existing_profile.id = explicit_access.profile_id
			          WHERE explicit_access.addon_id = installed.id
			            AND existing_profile.category_id = ANY($3::uuid[])
			      )
			  )
		)
	`, transportURL, assignments.profileIDs, assignments.categoryIDs, exceptAddonID).Scan(&assigned); err != nil {
		return false, fmt.Errorf("check addon transport assignment overlap: %w", err)
	}
	return assigned, nil
}

func lockAddonTransportURL(ctx context.Context, tx pgx.Tx, addonID string) (string, error) {
	var transportURL string
	if err := tx.QueryRow(ctx, `
		SELECT transport_url FROM profile_addons WHERE id = $1::uuid FOR UPDATE
	`, addonID).Scan(&transportURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("lock addon transport: %w", err)
	}
	return transportURL, nil
}

func readAddonAssignments(ctx context.Context, tx pgx.Tx, addonID string) (addonAssignments, error) {
	assignments := addonAssignments{profileIDs: make([]string, 0), categoryIDs: make([]string, 0)}
	rows, err := tx.Query(ctx, `
		SELECT profile_id::text FROM addon_profile_access
		WHERE addon_id = $1::uuid ORDER BY profile_id
	`, addonID)
	if err != nil {
		return addonAssignments{}, fmt.Errorf("read addon profile access: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return addonAssignments{}, fmt.Errorf("scan addon profile access: %w", err)
		}
		assignments.profileIDs = append(assignments.profileIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return addonAssignments{}, fmt.Errorf("iterate addon profile access: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT category_id::text FROM addon_category_access
		WHERE addon_id = $1::uuid ORDER BY category_id
	`, addonID)
	if err != nil {
		return addonAssignments{}, fmt.Errorf("read addon category access: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return addonAssignments{}, fmt.Errorf("scan addon category access: %w", err)
		}
		assignments.categoryIDs = append(assignments.categoryIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return addonAssignments{}, fmt.Errorf("iterate addon category access: %w", err)
	}
	rows.Close()
	return assignments, nil
}

func loadAddonAssignments(ctx context.Context, tx pgx.Tx, addonID string) (addonAssignments, error) {
	assignments := addonAssignments{profileIDs: make([]string, 0), categoryIDs: make([]string, 0)}
	rows, err := tx.Query(ctx, `
		SELECT profile_id::text FROM addon_profile_access
		WHERE addon_id = $1::uuid ORDER BY profile_id FOR UPDATE
	`, addonID)
	if err != nil {
		return addonAssignments{}, fmt.Errorf("lock addon profile access: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return addonAssignments{}, fmt.Errorf("scan addon profile access: %w", err)
		}
		assignments.profileIDs = append(assignments.profileIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return addonAssignments{}, fmt.Errorf("iterate addon profile access: %w", err)
	}
	rows.Close()
	rows, err = tx.Query(ctx, `
		SELECT category_id::text FROM addon_category_access
		WHERE addon_id = $1::uuid ORDER BY category_id FOR UPDATE
	`, addonID)
	if err != nil {
		return addonAssignments{}, fmt.Errorf("lock addon category access: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return addonAssignments{}, fmt.Errorf("scan addon category access: %w", err)
		}
		assignments.categoryIDs = append(assignments.categoryIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return addonAssignments{}, fmt.Errorf("iterate addon category access: %w", err)
	}
	rows.Close()
	return assignments, nil
}

func authorizeAddonAssignmentChange(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, addonID string, requestedProfileIDs, requestedCategoryIDs []string) (addonAssignments, error) {
	var visibleID string
	if err := tx.QueryRow(ctx, `
		SELECT pa.id::text
		FROM profile_addons pa
		JOIN profiles active_profile ON active_profile.id = $2::uuid
		WHERE pa.id = $1::uuid
		  AND (
		      EXISTS (SELECT 1 FROM addon_profile_access a WHERE a.addon_id = pa.id AND a.profile_id = active_profile.id)
		      OR EXISTS (SELECT 1 FROM addon_category_access a WHERE a.addon_id = pa.id AND a.category_id = active_profile.category_id)
		  )
	`, addonID, activeProfileID).Scan(&visibleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return addonAssignments{}, ErrNotFound
		}
		return addonAssignments{}, fmt.Errorf("read addon assignment change: %w", err)
	}
	snapshot, err := readAddonAssignments(ctx, tx, addonID)
	if err != nil {
		return addonAssignments{}, err
	}
	result := addonAssignments{profileIDs: snapshot.profileIDs, categoryIDs: snapshot.categoryIDs}
	if requestedProfileIDs != nil {
		result.profileIDs = requestedProfileIDs
	}
	if requestedCategoryIDs != nil {
		result.categoryIDs = requestedCategoryIDs
	}
	if len(result.profileIDs)+len(result.categoryIDs) == 0 {
		return addonAssignments{}, fmt.Errorf("%w: at least one profileId or categoryId is required", ErrInvalidInput)
	}
	allCategories := mergeIDs(snapshot.categoryIDs, result.categoryIDs)
	allProfiles := mergeIDs(snapshot.profileIDs, result.profileIDs)
	profileCategoryIDs, err := categoriesForProfiles(ctx, tx, mergeIDs(allProfiles, []string{activeProfileID}))
	if err != nil {
		return addonAssignments{}, err
	}
	if len(allCategories) > 0 {
		if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
			return addonAssignments{}, err
		}
	}
	if err := lockCategoryRows(ctx, tx, mergeIDs(allCategories, profileCategoryIDs)); err != nil {
		return addonAssignments{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, activeProfileID); err != nil {
		return addonAssignments{}, err
	}
	if err := authorizeProfileAssignments(ctx, tx, principal, activeProfileID, allProfiles); err != nil {
		return addonAssignments{}, err
	}
	currentProfileCategoryIDs, err := categoriesForProfiles(ctx, tx, mergeIDs(allProfiles, []string{activeProfileID}))
	if err != nil {
		return addonAssignments{}, err
	}
	if !equalIDs(profileCategoryIDs, currentProfileCategoryIDs) {
		return addonAssignments{}, ErrForbidden
	}
	var lockedID string
	if err := tx.QueryRow(ctx, `
		SELECT pa.id::text
		FROM profile_addons pa
		JOIN profiles active_profile ON active_profile.id = $2::uuid
		WHERE pa.id = $1::uuid
		  AND (
		      EXISTS (SELECT 1 FROM addon_profile_access a WHERE a.addon_id = pa.id AND a.profile_id = active_profile.id)
		      OR EXISTS (SELECT 1 FROM addon_category_access a WHERE a.addon_id = pa.id AND a.category_id = active_profile.category_id)
		  )
		FOR UPDATE OF pa
	`, addonID, activeProfileID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return addonAssignments{}, ErrNotFound
		}
		return addonAssignments{}, fmt.Errorf("lock addon assignment change: %w", err)
	}
	current, err := loadAddonAssignments(ctx, tx, addonID)
	if err != nil {
		return addonAssignments{}, err
	}
	if !equalIDs(snapshot.profileIDs, current.profileIDs) || !equalIDs(snapshot.categoryIDs, current.categoryIDs) {
		return addonAssignments{}, ErrForbidden
	}
	return result, nil
}

func authorizeAndLoadAddonRefresh(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, addonID string) (InstalledAddon, error) {
	assignments, err := authorizeAddonAssignmentChange(ctx, tx, principal, activeProfileID, addonID, nil, nil)
	if err != nil {
		return InstalledAddon{}, err
	}
	if principal.IsGlobalAdministrator() {
		if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
			return InstalledAddon{}, err
		}
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, activeProfileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query addon for refresh: %w", err)
	}
	if (len(assignments.categoryIDs) > 0 || isPrivateNetworkTransportURL(installed.transportURL)) && !principal.IsGlobalAdministrator() {
		return InstalledAddon{}, ErrForbidden
	}
	return installed, nil
}

func applyAddonUpdate(ctx context.Context, tx pgx.Tx, addonID, transportURL string, assignments addonAssignments, manifest Manifest, rawManifest json.RawMessage, enabled *bool) error {
	assigned, err := addonTransportAssigned(ctx, tx, transportURL, assignments, &addonID)
	if err != nil {
		return err
	}
	if assigned {
		return ErrAlreadyInstalled
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profile_addons
		SET transport_url = $2, manifest = $3::jsonb, manifest_id = $4,
		    manifest_version = $5, enabled = COALESCE($6::boolean, enabled), updated_at = now()
		WHERE id = $1::uuid
	`, addonID, transportURL, rawManifest, manifest.ID, manifest.Version, enabled); err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return ErrAlreadyInstalled
		}
		return fmt.Errorf("update addon: %w", err)
	}
	return writeAddonAssignments(ctx, tx, addonID, assignments)
}

func stringSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func mergeIDs(primary, secondary []string) []string {
	merged := append([]string(nil), primary...)
	seen := stringSet(merged)
	for _, id := range secondary {
		if _, exists := seen[id]; exists {
			continue
		}
		merged = append(merged, id)
		seen[id] = struct{}{}
	}
	return merged
}

func equalIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (service *Service) update(ctx context.Context, principal auth.Principal, addonID string, input UpdateAddonInput) (InstalledAddon, error) {
	activeProfileID, err := activeProfileID(principal)
	if err != nil {
		return InstalledAddon{}, err
	}
	if !validUUID(addonID) {
		return InstalledAddon{}, ErrInvalidInput
	}
	if input.Enabled != nil && !principal.IsGlobalAdministrator() {
		return InstalledAddon{}, ErrForbidden
	}
	var profileIDs, categoryIDs []string
	if input.ProfileIDs != nil {
		profileIDs, err = normalizeAssignmentIDs("profileIds", input.ProfileIDs)
		if err != nil {
			return InstalledAddon{}, err
		}
	}
	if input.CategoryIDs != nil {
		categoryIDs, err = normalizeAssignmentIDs("categoryIds", input.CategoryIDs)
		if err != nil {
			return InstalledAddon{}, err
		}
	}
	var requestedTransportURL string
	if input.TransportURL != nil {
		requestedTransportURL, err = NormalizeTransportURL(*input.TransportURL)
		if err != nil {
			return InstalledAddon{}, err
		}
	}
	authorizationTx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon update authorization: %w", err)
	}
	defer func() { _ = authorizationTx.Rollback(ctx) }()
	if principal.IsGlobalAdministrator() {
		if err := authorizeGlobalAddonOrigin(ctx, authorizationTx, principal); err != nil {
			return InstalledAddon{}, err
		}
	}
	assignments, err := authorizeAddonAssignmentChange(ctx, authorizationTx, principal, activeProfileID, addonID, profileIDs, categoryIDs)
	if err != nil {
		return InstalledAddon{}, err
	}
	currentTransportURL, err := lockAddonTransportURL(ctx, authorizationTx, addonID)
	if err != nil {
		return InstalledAddon{}, err
	}
	current, err := queryAddon(authorizationTx.QueryRow(ctx, addonForManagementQuery, addonID, activeProfileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query addon update revision: %w", err)
	}
	transportURL := currentTransportURL
	if input.TransportURL != nil {
		transportURL = requestedTransportURL
	}
	if currentTransportURL == transportURL {
		assigned, err := addonTransportAssigned(ctx, authorizationTx, transportURL, assignments, &addonID)
		if err != nil {
			return InstalledAddon{}, err
		}
		if assigned {
			return InstalledAddon{}, ErrAlreadyInstalled
		}
		if input.Enabled != nil {
			if _, err := authorizationTx.Exec(ctx, `
				UPDATE profile_addons
				SET enabled = $2, updated_at = CASE WHEN enabled IS DISTINCT FROM $2 THEN now() ELSE updated_at END
				WHERE id = $1::uuid
			`, addonID, *input.Enabled); err != nil {
				return InstalledAddon{}, fmt.Errorf("update addon availability: %w", err)
			}
		}
		if err := writeAddonAssignments(ctx, authorizationTx, addonID, assignments); err != nil {
			return InstalledAddon{}, err
		}
		installed, err := queryAddon(authorizationTx.QueryRow(ctx, addonForManagementQuery, addonID, activeProfileID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return InstalledAddon{}, ErrNotFound
			}
			return InstalledAddon{}, fmt.Errorf("query updated addon: %w", err)
		}
		if err := authorizationTx.Commit(ctx); err != nil {
			return InstalledAddon{}, fmt.Errorf("commit addon assignment update: %w", err)
		}
		return installed, nil
	}
	if !principal.IsGlobalAdministrator() {
		return InstalledAddon{}, ErrForbidden
	}
	if err := authorizeGlobalAddonOrigin(ctx, authorizationTx, principal); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizationTx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon update authorization: %w", err)
	}

	diagnosticAttempt := service.diagnostics.start(addonID)
	manifest, rawManifest, err := service.transport.Manifest(ctx, transportURL)
	if err != nil {
		service.diagnostics.complete(addonID, diagnosticAttempt, err)
		service.recordIncident(ctx, principal, addonID, current.parsedManifest.Name, err)
		return InstalledAddon{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return InstalledAddon{}, err
	}
	assignments, err = authorizeAddonAssignmentChange(ctx, tx, principal, activeProfileID, addonID, profileIDs, categoryIDs)
	if err != nil {
		return InstalledAddon{}, err
	}
	if _, err := lockAddonTransportURL(ctx, tx, addonID); err != nil {
		return InstalledAddon{}, err
	}
	locked, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, activeProfileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query locked addon update revision: %w", err)
	}
	if locked.transportURL != current.transportURL {
		return InstalledAddon{}, ErrForbidden
	}
	if !sameInstalledAddon(locked, current) {
		return InstalledAddon{}, ErrNotFound
	}
	if err := applyAddonUpdate(ctx, tx, addonID, transportURL, assignments, manifest, rawManifest, input.Enabled); err != nil {
		return InstalledAddon{}, err
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, activeProfileID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InstalledAddon{}, ErrNotFound
		}
		return InstalledAddon{}, fmt.Errorf("query updated addon: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon update: %w", err)
	}
	service.diagnostics.complete(addonID, diagnosticAttempt, nil)
	service.recordIncident(ctx, principal, addonID, installed.parsedManifest.Name, nil)
	return installed, nil
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, addonID string, input UpdateAddonInput) (ManagedAddon, error) {
	installed, err := service.update(ctx, principal, addonID, input)
	if err != nil {
		return ManagedAddon{}, err
	}
	return managedAddon(installed, principal.IsGlobalAdministrator()), nil
}
