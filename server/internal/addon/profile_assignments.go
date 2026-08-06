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

func lockAddonTransportURL(ctx context.Context, tx pgx.Tx, addonID string) (string, error) {
	var transportURL string
	if err := tx.QueryRow(ctx, `
		SELECT transport_url
		FROM profile_addons
		WHERE id = $1::uuid
		FOR UPDATE
	`, addonID).Scan(&transportURL); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("lock addon transport: %w", err)
	}
	return transportURL, nil
}

func authorizeAddonAssignmentChange(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, addonID string, requestedProfileIDs []string) error {
	var lockedID string
	if err := tx.QueryRow(ctx, `
		SELECT pa.id::text
		FROM profile_addons pa
		WHERE pa.id = $1::uuid
		  AND EXISTS (
		      SELECT 1 FROM addon_profile_access access
		      WHERE access.addon_id = pa.id AND access.profile_id = $2::uuid
		  )
		FOR UPDATE
	`, addonID, activeProfileID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock addon assignment change: %w", err)
	}
	currentProfileIDs := make([]string, 0)
	rows, err := tx.Query(ctx, `
		SELECT profile_id::text
		FROM addon_profile_access
		WHERE addon_id = $1::uuid
		ORDER BY profile_id
		FOR UPDATE
	`, addonID)
	if err != nil {
		return fmt.Errorf("lock addon profile access: %w", err)
	}
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			rows.Close()
			return fmt.Errorf("scan addon profile access: %w", err)
		}
		currentProfileIDs = append(currentProfileIDs, profileID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate addon profile access: %w", err)
	}
	rows.Close()
	if len(currentProfileIDs) == 0 {
		return ErrNotFound
	}
	if _, accessible := profileIDSet(currentProfileIDs)[activeProfileID]; !accessible {
		return ErrNotFound
	}
	return authorizeProfileAssignments(
		ctx, tx, principal, activeProfileID,
		mergeProfileIDs(requestedProfileIDs, currentProfileIDs),
	)
}

func authorizeAndLoadAddonRefresh(ctx context.Context, tx pgx.Tx, principal auth.Principal, activeProfileID, addonID string) (InstalledAddon, error) {
	if principal.IsGlobalAdministrator() {
		if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
			return InstalledAddon{}, err
		}
	}
	if err := authorizeAddonAssignmentChange(ctx, tx, principal, activeProfileID, addonID, nil); err != nil {
		return InstalledAddon{}, err
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, activeProfileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query addon for refresh: %w", err)
	}
	if isPrivateNetworkTransportURL(installed.transportURL) && !principal.IsGlobalAdministrator() {
		return InstalledAddon{}, ErrForbidden
	}
	return installed, nil
}

func applyAddonUpdate(ctx context.Context, tx pgx.Tx, addonID, transportURL string, profileIDs []string, manifest Manifest, rawManifest json.RawMessage, enabled *bool) error {
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
		    manifest_version = $5, enabled = COALESCE($6::boolean, enabled), updated_at = now()
		WHERE id = $1::uuid
	`, addonID, transportURL, rawManifest, manifest.ID, manifest.Version, enabled); err != nil {
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
	if input.ProfileIDs == nil {
		return InstalledAddon{}, fmt.Errorf("%w: profileIds is required", ErrInvalidInput)
	}
	profileIDs, err := normalizeProfileIDs(input.ProfileIDs, activeProfileID)
	if err != nil {
		return InstalledAddon{}, err
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
	if err := authorizeActiveProfile(ctx, authorizationTx, principal, activeProfileID); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizeAddonAssignmentChange(ctx, authorizationTx, principal, activeProfileID, addonID, profileIDs); err != nil {
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
		assigned, err := addonTransportAssignedToProfiles(ctx, authorizationTx, transportURL, profileIDs, &addonID)
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
		if err := writeProfileAssignments(ctx, authorizationTx, addonID, profileIDs); err != nil {
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
	if err := authorizationTx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon update authorization: %w", err)
	}

	diagnosticAttempt := service.diagnostics.start(addonID)
	manifest, rawManifest, err := service.transport.Manifest(ctx, transportURL)
	if err != nil {
		service.diagnostics.complete(addonID, diagnosticAttempt, err)
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
	if err := authorizeActiveProfile(ctx, tx, principal, activeProfileID); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizeAddonAssignmentChange(ctx, tx, principal, activeProfileID, addonID, profileIDs); err != nil {
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
	if err := applyAddonUpdate(ctx, tx, addonID, transportURL, profileIDs, manifest, rawManifest, input.Enabled); err != nil {
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
	return installed, nil
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, addonID string, input UpdateAddonInput) (ManagedAddon, error) {
	installed, err := service.update(ctx, principal, addonID, input)
	if err != nil {
		return ManagedAddon{}, err
	}
	return managedAddon(installed, principal.IsGlobalAdministrator()), nil
}
