package addon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

const (
	verificationBudget    = 15 * time.Second
	verificationTTL       = 10 * time.Minute
	verificationRetention = 20
	maximumCatalogProbes  = 3
	maximumProbeWorkers       = 2
	verificationCleanupBatch = 500
)

func (service *Service) VerifyCandidate(ctx context.Context, principal auth.Principal, input VerificationInput) (AddonVerification, error) {
	if !principal.IsGlobalAdministrator() {
		return AddonVerification{}, ErrForbidden
	}
	profileID, err := activeProfileID(principal)
	if err != nil {
		return AddonVerification{}, err
	}
	assignments, err := normalizeInstallAssignments(input.ProfileIDs, input.CategoryIDs, profileID)
	if err != nil {
		return AddonVerification{}, err
	}
	transportURL, err := NormalizeTransportURL(input.TransportURL)
	if err != nil {
		return AddonVerification{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return AddonVerification{}, fmt.Errorf("begin addon verification authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return AddonVerification{}, err
	}
	if err := authorizeAssignments(ctx, tx, principal, profileID, assignments); err != nil {
		return AddonVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AddonVerification{}, fmt.Errorf("commit addon verification authorization: %w", err)
	}
	return service.runVerification(ctx, principal, "", profileID, transportURL, assignments)
}

func (service *Service) VerifyInstalled(ctx context.Context, principal auth.Principal, addonID string) (AddonVerification, error) {
	if !principal.IsGlobalAdministrator() {
		return AddonVerification{}, ErrForbidden
	}
	profileID, err := activeProfileID(principal)
	if err != nil {
		return AddonVerification{}, err
	}
	if !validUUID(addonID) {
		return AddonVerification{}, ErrInvalidInput
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return AddonVerification{}, fmt.Errorf("begin installed addon verification authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return AddonVerification{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return AddonVerification{}, err
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AddonVerification{}, ErrNotFound
	}
	if err != nil {
		return AddonVerification{}, fmt.Errorf("query installed addon verification target: %w", err)
	}
	assignments := addonAssignments{profileIDs: installed.ProfileIDs, categoryIDs: installed.CategoryIDs}
	if err := authorizeAssignments(ctx, tx, principal, profileID, assignments); err != nil {
		return AddonVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AddonVerification{}, fmt.Errorf("commit installed addon verification authorization: %w", err)
	}
	return service.runVerification(ctx, principal, addonID, profileID, installed.transportURL, assignments)
}

func (service *Service) runVerification(parent context.Context, principal auth.Principal, addonID, profileID, transportURL string, assignments addonAssignments) (AddonVerification, error) {
	ctx, cancel := context.WithTimeout(parent, verificationBudget)
	defer cancel()
	checks := []VerificationCheck{{Code: "manifest_fetch", Status: "failed"}, {Code: "manifest_valid", Status: "skipped"}, {Code: "catalog_probe", Status: "skipped"}}
	status, summary := "failed", "manifest_unavailable"
	manifest, rawManifest, err := service.transport.Manifest(ctx, transportURL)
	if err == nil {
		checks[0].Status = "passed"
		checks[1].Status = "passed"
		status, summary = "passed", "ready"
		paths := safeCatalogProbePaths(manifest)
		if len(paths) > 0 {
			if probeErr := service.probeVerificationCatalogs(ctx, transportURL, paths); probeErr != nil {
				checks[2].Status = "failed"
				status, summary = "failed", "catalog_unavailable"
			} else {
				checks[2].Status = "passed"
			}
		}
	} else if errors.Is(err, ErrInvalidManifest) || errors.Is(err, ErrInvalidResponse) {
		checks[0].Status = "passed"
		checks[1].Status = "failed"
		summary = "manifest_invalid"
	}
	verification, persistErr := service.persistVerification(parent, principal, addonID, profileID, transportURL, assignments, status, summary, checks, manifest, rawManifest)
	if persistErr != nil {
		return AddonVerification{}, persistErr
	}
	return verification, nil
}

func safeCatalogProbePaths(manifest Manifest) []ResourcePath {
	paths := make([]ResourcePath, 0, maximumCatalogProbes)
	seen := make(map[string]struct{})
	for _, catalog := range manifest.Catalogs {
		path := manifest.ApplyCatalogDefaults(ResourcePath{Resource: "catalog", Type: catalog.Type, ID: catalog.ID})
		if !manifest.Supports(path) {
			continue
		}
		key := path.Resource + "\x00" + path.Type + "\x00" + path.ID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		paths = append(paths, path)
		if len(paths) == maximumCatalogProbes {
			break
		}
	}
	return paths
}

func (service *Service) probeVerificationCatalogs(ctx context.Context, transportURL string, paths []ResourcePath) error {
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, maximumProbeWorkers)
	errorsByProbe := make(chan error, len(paths))
	var group sync.WaitGroup
	for _, path := range paths {
		path := path
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-probeCtx.Done():
				errorsByProbe <- probeCtx.Err()
				return
			}
			payload, _, err := service.transport.Resource(probeCtx, transportURL, path)
			if err == nil {
				err = validateExposablePayloadComplexity(probeCtx, payload)
			}
			if err != nil {
				cancel()
			}
			errorsByProbe <- err
		}()
	}
	group.Wait()
	close(errorsByProbe)
	for err := range errorsByProbe {
		if err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) persistVerification(ctx context.Context, principal auth.Principal, addonID, profileID, transportURL string, assignments addonAssignments, status, summary string, checks []VerificationCheck, manifest Manifest, rawManifest json.RawMessage) (AddonVerification, error) {
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		return AddonVerification{}, fmt.Errorf("encode addon verification checks: %w", err)
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return AddonVerification{}, fmt.Errorf("begin addon verification persistence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return AddonVerification{}, err
	}
	if err := authorizeAssignments(ctx, tx, principal, profileID, assignments); err != nil {
		return AddonVerification{}, err
	}
	now := time.Now().UTC()
	var addonValue any
	if addonID != "" {
		addonValue = addonID
	}
	var manifestValue any
	var manifestID, manifestVersion any
	if status == "passed" {
		manifestValue, manifestID, manifestVersion = rawManifest, manifest.ID, manifest.Version
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return AddonVerification{}, fmt.Errorf("allocate addon verification: %w", err)
	}
	envelope, err := service.sealVerificationTransport(id, status, transportURL)
	if err != nil {
		return AddonVerification{}, err
	}
	var ciphertext any
	var cipherVersion, keyVersion any
	if envelope != nil {
		ciphertext, cipherVersion, keyVersion = envelope.Ciphertext, envelope.CipherVersion, envelope.KeyVersion
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO addon_verifications (id,requested_by_user_id,requested_by_session_id,addon_id,profile_id,transport_url_ciphertext,transport_url_cipher_version,transport_url_key_version,manifest,manifest_id,manifest_version,profile_ids,category_ids,status,checks,created_at,expires_at)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9::jsonb,$10,$11,$12::uuid[],$13::uuid[],$14,$15::jsonb,$16,$17)
	`, id, principal.UserID, principal.SessionID, addonValue, profileID, ciphertext, cipherVersion, keyVersion, manifestValue, manifestID, manifestVersion,
		assignments.profileIDs, assignments.categoryIDs, status, checksJSON, now, now.Add(verificationTTL)); err != nil {
		return AddonVerification{}, fmt.Errorf("persist addon verification: %w", err)
	}
	_, _ = tx.Exec(ctx, `
		DELETE FROM addon_verifications verification
		WHERE verification.id IN (
			SELECT id FROM addon_verifications
			WHERE requested_by_user_id=$1::uuid AND addon_id IS NOT DISTINCT FROM $2::uuid
			ORDER BY created_at DESC,id DESC OFFSET $3 LIMIT $4)
	`, principal.UserID, addonValue, verificationRetention, verificationCleanupBatch)
	if err := tx.Commit(ctx); err != nil {
		return AddonVerification{}, fmt.Errorf("commit addon verification: %w", err)
	}
	result := AddonVerification{ID: id, Status: status, Summary: summary, Checks: checks, ProfileIDs: assignments.profileIDs, CategoryIDs: assignments.categoryIDs, CreatedAt: now, ExpiresAt: now.Add(verificationTTL)}
	if status == "passed" {
		result.Manifest = &manifest
		capabilities := capabilitiesFor(manifest)
		result.Capabilities = &capabilities
	}
	return result, nil
}

func (service *Service) sealVerificationTransport(id, status, transportURL string) (*secretcrypto.Envelope, error) {
	if status != "passed" {
		return nil, nil
	}
	if service.verificationKeys == nil {
		return nil, errors.New("addon verification encryption is not configured")
	}
	keyVersion := service.verificationKeys.ActiveVersion()
	envelope, err := service.verificationKeys.Encrypt([]byte(transportURL), verificationTransportAAD(id, keyVersion))
	if err != nil {
		return nil, errors.New("addon verification transport could not be encrypted")
	}
	return &envelope, nil
}

func verificationTransportAAD(id string, keyVersion int) []byte {
	return []byte(fmt.Sprintf("addon-verification:%s:transport-url:key-version:%d", id, keyVersion))
}

func (service *Service) RunScheduled(ctx context.Context) error {
	result, err := service.pool.Exec(ctx, `
		WITH expired AS (
			SELECT id FROM addon_verifications
			WHERE expires_at <= now()
			ORDER BY expires_at,id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		DELETE FROM addon_verifications verification
		USING expired
		WHERE verification.id=expired.id
	`, verificationCleanupBatch)
	if err != nil {
		return fmt.Errorf("cleanup expired addon verifications: %w", err)
	}
	service.logger.Debug("scheduled addon verification cleanup completed", "deleted", result.RowsAffected())
	return nil
}
