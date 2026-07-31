package addon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moodiness/rivune/server/internal/auth"
)

func normalizeProfileIDs(requested []string, activeProfileID string) ([]string, error) {
	if requested == nil {
		return []string{activeProfileID}, nil
	}
	if len(requested) == 0 || len(requested) > 100 {
		return nil, fmt.Errorf("%w: profileIds must contain 1 to 100 profiles", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(requested))
	profileIDs := make([]string, 0, len(requested))
	for _, profileID := range requested {
		if !validUUID(profileID) {
			return nil, fmt.Errorf("%w: profileIds must contain valid profile identifiers", ErrInvalidInput)
		}
		if _, duplicate := seen[profileID]; duplicate {
			return nil, fmt.Errorf("%w: profileIds must not contain duplicates", ErrInvalidInput)
		}
		seen[profileID] = struct{}{}
		profileIDs = append(profileIDs, profileID)
	}
	return profileIDs, nil
}

func authorizeProfileAssignments(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID string, profileIDs []string) error {
	lockIDs := append([]string(nil), profileIDs...)
	if _, included := profileIDSet(profileIDs)[activeProfileID]; !included {
		lockIDs = append(lockIDs, activeProfileID)
	}
	sort.Strings(lockIDs)
	for _, profileID := range lockIDs {
		if err := lockProfile(ctx, tx, profileID); err != nil {
			return err
		}
	}
	authorized, err := auth.CanManageProfiles(ctx, tx, principal, lockIDs)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrForbidden
	}
	return nil
}

func writeProfileAssignments(ctx context.Context, tx pgx.Tx, addonID string, profileIDs []string) error {
	for _, profileID := range profileIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO addon_profile_access (addon_id, profile_id, position)
			VALUES (
				$1::uuid, $2::uuid,
				(SELECT COALESCE(max(position) + 1, 0) FROM addon_profile_access WHERE profile_id = $2::uuid)
			)
			ON CONFLICT (addon_id, profile_id) DO NOTHING
		`, addonID, profileID); err != nil {
			return fmt.Errorf("grant addon profile access: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM addon_profile_access
		WHERE addon_id = $1::uuid AND NOT (profile_id = ANY($2::uuid[]))
	`, addonID, profileIDs); err != nil {
		return fmt.Errorf("revoke addon profile access: %w", err)
	}
	return nil
}

func addonTransportAssignedToProfiles(ctx context.Context, tx pgx.Tx, transportURL string, profileIDs []string, exceptAddonID *string) (bool, error) {
	var assigned bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM addon_profile_access access
			JOIN profile_addons installed ON installed.id = access.addon_id
			WHERE access.profile_id = ANY($2::uuid[])
			  AND installed.transport_url = $1
			  AND ($3::uuid IS NULL OR installed.id <> $3::uuid)
		)
	`, transportURL, profileIDs, exceptAddonID).Scan(&assigned); err != nil {
		return false, fmt.Errorf("check addon transport profile access: %w", err)
	}
	return assigned, nil
}

func applyAddonUpdate(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, addonID, transportURL string, profileIDs []string, manifest Manifest, rawManifest json.RawMessage) error {
	var lockedID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text FROM profile_addons WHERE id = $1::uuid FOR UPDATE
	`, addonID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock addon update: %w", err)
	}
	var currentProfileIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT ARRAY(
			SELECT profile_id::text
			FROM addon_profile_access
			WHERE addon_id = $1::uuid
		)
	`, addonID).Scan(&currentProfileIDs); err != nil {
		return fmt.Errorf("query addon profile access: %w", err)
	}
	if len(currentProfileIDs) == 0 {
		return ErrNotFound
	}
	if _, accessible := profileIDSet(currentProfileIDs)[activeProfileID]; !accessible {
		return ErrNotFound
	}
	managedProfileIDs := mergeProfileIDs(profileIDs, currentProfileIDs)
	if err := authorizeProfileAssignments(ctx, tx, principal, activeProfileID, managedProfileIDs); err != nil {
		return err
	}
	assigned, err := addonTransportAssignedToProfiles(ctx, tx, transportURL, profileIDs, &addonID)
	if err != nil {
		return err
	}
	if assigned {
		return ErrAlreadyInstalled
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profile_addons
		SET transport_url = $2, manifest = $3::jsonb, manifest_id = $4,
		    manifest_version = $5, updated_at = now()
		WHERE id = $1::uuid
	`, addonID, transportURL, rawManifest, manifest.ID, manifest.Version); err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return ErrAlreadyInstalled
		}
		return fmt.Errorf("update addon: %w", err)
	}
	return writeProfileAssignments(ctx, tx, addonID, profileIDs)
}

func profileIDSet(profileIDs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(profileIDs))
	for _, profileID := range profileIDs {
		set[profileID] = struct{}{}
	}
	return set
}

func mergeProfileIDs(primary, secondary []string) []string {
	merged := append([]string(nil), primary...)
	seen := profileIDSet(merged)
	for _, profileID := range secondary {
		if _, exists := seen[profileID]; exists {
			continue
		}
		merged = append(merged, profileID)
		seen[profileID] = struct{}{}
	}
	return merged
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, addonID string, input UpdateAddonInput) (InstalledAddon, error) {
	activeProfileID, err := activeProfileID(principal)
	if err != nil {
		return InstalledAddon{}, err
	}
	if !validUUID(addonID) {
		return InstalledAddon{}, ErrInvalidInput
	}
	if input.ProfileIDs == nil {
		return InstalledAddon{}, fmt.Errorf("%w: profileIds is required", ErrInvalidInput)
	}
	profileIDs, err := normalizeProfileIDs(input.ProfileIDs, activeProfileID)
	if err != nil {
		return InstalledAddon{}, err
	}
	transportURL, err := NormalizeTransportURL(input.TransportURL)
	if err != nil {
		return InstalledAddon{}, err
	}
	current, err := service.addonForProfile(ctx, activeProfileID, addonID)
	if err != nil {
		return InstalledAddon{}, err
	}
	authorized, err := auth.CanManageProfiles(ctx, service.pool, principal, mergeProfileIDs(profileIDs, current.ProfileIDs))
	if err != nil {
		return InstalledAddon{}, err
	}
	if !authorized {
		return InstalledAddon{}, ErrForbidden
	}
	manifest, rawManifest, err := service.transport.Manifest(ctx, transportURL)
	if err != nil {
		return InstalledAddon{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := applyAddonUpdate(ctx, tx, principal, activeProfileID, addonID, transportURL, profileIDs, manifest, rawManifest); err != nil {
		return InstalledAddon{}, err
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForProfileQuery, addonID, activeProfileID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InstalledAddon{}, ErrNotFound
		}
		return InstalledAddon{}, fmt.Errorf("query updated addon: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon update: %w", err)
	}
	return installed, nil
}
