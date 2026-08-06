package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type Service struct {
	pool           *pgxpool.Pool
	transport      Transport
	logger         *slog.Logger
	requestTimeout time.Duration
	retryDelay     time.Duration
	diagnostics    *diagnosticStore
}

func NewService(pool *pgxpool.Pool, transport Transport, logger *slog.Logger) *Service {
	if transport == nil {
		transport = NewHTTPTransport(nil)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		pool:           pool,
		transport:      transport,
		logger:         logger,
		requestTimeout: 10 * time.Second,
		retryDelay:     100 * time.Millisecond,
		diagnostics:    newDiagnosticStore(nil, maximumDiagnosticObservations),
	}
}

func (service *Service) install(ctx context.Context, principal auth.Principal, input InstallInput) (InstalledAddon, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return InstalledAddon{}, err
	}
	profileIDs, err := normalizeProfileIDs(input.ProfileIDs, profileID)
	if err != nil {
		return InstalledAddon{}, err
	}
	transportURL, err := NormalizeTransportURL(input.TransportURL)
	if err != nil {
		return InstalledAddon{}, err
	}
	if !principal.IsGlobalAdministrator() {
		return InstalledAddon{}, ErrForbidden
	}
	authorizationTx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon installation authorization: %w", err)
	}
	defer func() { _ = authorizationTx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, authorizationTx, principal); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizeActiveProfile(ctx, authorizationTx, principal, profileID); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizeProfileAssignments(ctx, authorizationTx, principal, profileID, profileIDs); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizationTx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon installation authorization: %w", err)
	}
	diagnosticAttempt := service.diagnostics.start("")
	manifest, rawManifest, err := service.transport.Manifest(ctx, transportURL)
	if err != nil {
		return InstalledAddon{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon installation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizeProfileAssignments(ctx, tx, principal, profileID, profileIDs); err != nil {
		return InstalledAddon{}, err
	}
	assigned, err := addonTransportAssignedToProfiles(ctx, tx, transportURL, profileIDs, nil)
	if err != nil {
		return InstalledAddon{}, err
	}
	if assigned {
		return InstalledAddon{}, ErrAlreadyInstalled
	}
	var addonID string
	err = tx.QueryRow(ctx, `
		INSERT INTO profile_addons (
			profile_id, transport_url, manifest, manifest_id, manifest_version, position
		)
		VALUES (
			$1::uuid, $2, $3::jsonb, $4, $5,
			(SELECT COALESCE(max(position) + 1, 0) FROM profile_addons WHERE profile_id = $1::uuid)
		)
		RETURNING id::text
	`, profileID, transportURL, rawManifest, manifest.ID, manifest.Version).Scan(&addonID)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return InstalledAddon{}, ErrAlreadyInstalled
		}
		return InstalledAddon{}, fmt.Errorf("install addon: %w", err)
	}
	if err := writeProfileAssignments(ctx, tx, addonID, profileIDs); err != nil {
		return InstalledAddon{}, err
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, profileID))
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query installed addon: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon installation: %w", err)
	}
	service.diagnostics.complete(installed.ID, diagnosticAttempt, nil)
	return installed, nil
}

func (service *Service) Install(ctx context.Context, principal auth.Principal, input InstallInput) (ManagedAddon, error) {
	installed, err := service.install(ctx, principal, input)
	if err != nil {
		return ManagedAddon{}, err
	}
	return managedAddon(installed, principal.IsGlobalAdministrator()), nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]InstalledAddon, error) {
	return service.loadAddonList(ctx, principal, true)
}

func (service *Service) Management(ctx context.Context, principal auth.Principal, addonID string) (ManagedAddon, error) {
	if !principal.IsGlobalAdministrator() {
		return ManagedAddon{}, ErrForbidden
	}
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ManagedAddon{}, err
	}
	if !validUUID(addonID) {
		return ManagedAddon{}, ErrInvalidInput
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ManagedAddon{}, fmt.Errorf("begin addon management lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return ManagedAddon{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return ManagedAddon{}, err
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForProfileQuery, addonID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedAddon{}, ErrNotFound
	}
	if err != nil {
		return ManagedAddon{}, fmt.Errorf("query addon management details: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedAddon{}, fmt.Errorf("commit addon management lookup: %w", err)
	}
	return managedAddon(installed, true), nil
}

func (service *Service) loadAddonList(ctx context.Context, principal auth.Principal, requireManagement bool) ([]InstalledAddon, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return nil, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin addon list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return nil, err
	}
	if requireManagement {
		authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
		if err != nil {
			return nil, fmt.Errorf("authorize addon list management: %w", err)
		}
		if !authorized {
			return nil, ErrForbidden
		}
	}
	addons, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit addon list: %w", err)
	}
	return addons, nil
}

func (service *Service) Remove(ctx context.Context, principal auth.Principal, addonID string) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	if !validUUID(addonID) {
		return ErrInvalidInput
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin addon removal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	if err := authorizeAddonAssignmentChange(ctx, tx, principal, profileID, addonID, nil); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		DELETE FROM profile_addons
		WHERE id = $1::uuid
	`, addonID)
	if err != nil {
		return fmt.Errorf("remove addon: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit addon removal: %w", err)
	}
	service.diagnostics.remove(addonID)
	return nil
}

func (service *Service) Reorder(ctx context.Context, principal auth.Principal, input ReorderInput) ([]InstalledAddon, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(input.AddonIDs))
	for _, addonID := range input.AddonIDs {
		if !validUUID(addonID) {
			return nil, ErrInvalidInput
		}
		if _, duplicate := seen[addonID]; duplicate {
			return nil, ErrInvalidInput
		}
		seen[addonID] = struct{}{}
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin addon reorder: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return nil, err
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return nil, fmt.Errorf("authorize addon reorder management: %w", err)
	}
	if !authorized {
		return nil, ErrForbidden
	}
	if err := lockProfile(ctx, tx, profileID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT addon_id::text FROM addon_profile_access WHERE profile_id = $1::uuid FOR UPDATE`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query addons for reorder: %w", err)
	}
	current := make(map[string]struct{})
	for rows.Next() {
		var addonID string
		if err := rows.Scan(&addonID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan addon for reorder: %w", err)
		}
		current[addonID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate addons for reorder: %w", err)
	}
	rows.Close()
	if len(current) != len(input.AddonIDs) {
		return nil, ErrInvalidInput
	}
	for addonID := range current {
		if _, included := seen[addonID]; !included {
			return nil, ErrInvalidInput
		}
	}
	for position, addonID := range input.AddonIDs {
		if _, err := tx.Exec(ctx, `
			UPDATE addon_profile_access SET position = $3
			WHERE profile_id = $1::uuid AND addon_id = $2::uuid
		`, profileID, addonID, position); err != nil {
			return nil, fmt.Errorf("update addon position: %w", err)
		}
	}
	addons, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit addon reorder: %w", err)
	}
	return addons, nil
}

func (service *Service) Refresh(ctx context.Context, principal auth.Principal, addonID string) (InstalledAddon, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return InstalledAddon{}, err
	}
	if !validUUID(addonID) {
		return InstalledAddon{}, ErrInvalidInput
	}
	authorizationTx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon refresh authorization: %w", err)
	}
	defer func() { _ = authorizationTx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, authorizationTx, principal, profileID); err != nil {
		return InstalledAddon{}, err
	}
	current, err := authorizeAndLoadAddonRefresh(ctx, authorizationTx, principal, profileID, addonID)
	if err != nil {
		return InstalledAddon{}, err
	}
	if err := authorizationTx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon refresh authorization: %w", err)
	}

	diagnosticAttempt := service.diagnostics.start(addonID)
	manifest, rawManifest, err := service.transport.Manifest(ctx, current.transportURL)
	if err != nil {
		service.diagnostics.complete(addonID, diagnosticAttempt, err)
		return InstalledAddon{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return InstalledAddon{}, err
	}
	locked, err := authorizeAndLoadAddonRefresh(ctx, tx, principal, profileID, addonID)
	if err != nil {
		return InstalledAddon{}, err
	}
	if !sameInstalledAddon(locked, current) {
		return InstalledAddon{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profile_addons
		SET manifest = $2::jsonb, manifest_id = $3, manifest_version = $4, updated_at = now()
		WHERE id = $1::uuid
	`, addonID, rawManifest, manifest.ID, manifest.Version); err != nil {
		return InstalledAddon{}, fmt.Errorf("refresh addon manifest: %w", err)
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForManagementQuery, addonID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query refreshed addon: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon refresh: %w", err)
	}
	service.diagnostics.complete(addonID, diagnosticAttempt, nil)
	return installed, nil
}

func (service *Service) Catalogs(ctx context.Context, principal auth.Principal) ([]CatalogDescriptor, error) {
	addons, err := service.loadAddonList(ctx, principal, false)
	if err != nil {
		return nil, err
	}
	catalogs := catalogDescriptors(addons)
	if err := service.revalidateAddonList(ctx, principal, addons); err != nil {
		return nil, err
	}
	return catalogs, nil
}

func catalogDescriptors(addons []InstalledAddon) []CatalogDescriptor {
	catalogs := make([]CatalogDescriptor, 0)
	for _, installed := range addons {
		for _, catalog := range installed.parsedManifest.Catalogs {
			catalogs = append(catalogs, CatalogDescriptor{
				AddonID: installed.ID, AddonName: installed.parsedManifest.Name, AddonLogoURL: installed.parsedManifest.Logo, ManifestID: installed.parsedManifest.ID, Position: installed.Position, Catalog: catalog, Searchable: catalog.SupportsSearch(),
			})
		}
		for _, catalog := range installed.parsedManifest.AddonCatalogs {
			catalogs = append(catalogs, CatalogDescriptor{
				AddonID: installed.ID, AddonName: installed.parsedManifest.Name, AddonLogoURL: installed.parsedManifest.Logo, ManifestID: installed.parsedManifest.ID, Position: installed.Position, Catalog: catalog, AddonCatalog: true,
			})
		}
	}
	return catalogs
}

func (service *Service) Fetch(ctx context.Context, principal auth.Principal, addonID string, path ResourcePath) (ResourceResult, error) {
	if !IsExposableResource(path.Resource) {
		return ResourceResult{}, ErrUnsupportedResource
	}
	return service.fetch(ctx, principal, addonID, path)
}

func (service *Service) FetchPlaybackResource(ctx context.Context, principal auth.Principal, addonID string, path ResourcePath) (ResourceResult, error) {
	if !isPlaybackResource(path.Resource) {
		return ResourceResult{}, ErrUnsupportedResource
	}
	return service.fetch(ctx, principal, addonID, path)
}

func (service *Service) fetch(ctx context.Context, principal auth.Principal, addonID string, path ResourcePath) (ResourceResult, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ResourceResult{}, err
	}
	installed, err := service.loadAddonForProfile(ctx, principal, profileID, addonID)
	if err != nil {
		return ResourceResult{}, err
	}
	path = installed.parsedManifest.ApplyCatalogDefaults(path)
	if !installed.parsedManifest.Supports(path) {
		return ResourceResult{}, ErrUnsupportedResource
	}
	diagnosticAttempt := service.diagnostics.start(addonID)
	payload, cache, providerErr := service.transport.Resource(ctx, installed.transportURL, path)
	if providerErr != nil {
		if revalidationErr := service.revalidateAddon(ctx, principal, installed); revalidationErr != nil {
			return ResourceResult{}, revalidationErr
		}
		service.diagnostics.complete(addonID, diagnosticAttempt, providerErr)
		return ResourceResult{}, providerErr
	}
	result := resultFor(installed, path, payload, cache)
	if err := service.revalidateAddon(ctx, principal, installed); err != nil {
		return ResourceResult{}, err
	}
	service.diagnostics.complete(addonID, diagnosticAttempt, nil)
	return result, nil
}

func (service *Service) FetchAll(ctx context.Context, principal auth.Principal, path ResourcePath) (ResourceBatch, error) {
	if !IsExposableResource(path.Resource) {
		return ResourceBatch{}, ErrUnsupportedResource
	}
	return service.fetchAll(ctx, principal, path)
}

func (service *Service) FetchAllPlaybackResources(ctx context.Context, principal auth.Principal, path ResourcePath) (ResourceBatch, error) {
	if !isPlaybackResource(path.Resource) {
		return ResourceBatch{}, ErrUnsupportedResource
	}
	return service.fetchAll(ctx, principal, path)
}

func (service *Service) fetchAll(ctx context.Context, principal auth.Principal, path ResourcePath) (ResourceBatch, error) {
	addons, err := service.loadAddonList(ctx, principal, false)
	if err != nil {
		return ResourceBatch{}, err
	}
	requests := make([]plannedRequest, 0, min(len(addons), maxPlannedRequests))
	for _, installed := range addons {
		requestPath := installed.parsedManifest.ApplyCatalogDefaults(path)
		if !installed.parsedManifest.Supports(requestPath) {
			continue
		}
		if len(requests) == maxPlannedRequests {
			return ResourceBatch{}, requestPlanLimitError()
		}
		requests = append(requests, plannedRequest{addon: installed, path: requestPath})
	}
	batch, executeErr := service.execute(ctx, requests)
	if err := service.revalidateAddonList(ctx, principal, addons); err != nil {
		return ResourceBatch{}, err
	}
	if executeErr != nil {
		return ResourceBatch{}, executeErr
	}
	return batch, nil
}

func (service *Service) FetchCatalogs(ctx context.Context, principal auth.Principal, contentType string, extra []ExtraValue, addonCatalogs bool) (ResourceBatch, error) {
	addons, err := service.loadAddonList(ctx, principal, false)
	if err != nil {
		return ResourceBatch{}, err
	}
	resource := "catalog"
	if addonCatalogs {
		resource = "addon_catalog"
	}
	requests := make([]plannedRequest, 0)
	for _, installed := range addons {
		catalogs := installed.parsedManifest.Catalogs
		if addonCatalogs {
			catalogs = installed.parsedManifest.AddonCatalogs
		}
		for _, catalog := range catalogs {
			if contentType != "" && catalog.Type != contentType {
				continue
			}
			path := ResourcePath{Resource: resource, Type: catalog.Type, ID: catalog.ID, Extra: extra}
			path = installed.parsedManifest.ApplyCatalogDefaults(path)
			if !installed.parsedManifest.Supports(path) {
				continue
			}
			if len(requests) == maxPlannedRequests {
				return ResourceBatch{}, requestPlanLimitError()
			}
			requests = append(requests, plannedRequest{addon: installed, path: path})
		}
	}
	batch, executeErr := service.execute(ctx, requests)
	if err := service.revalidateAddonList(ctx, principal, addons); err != nil {
		return ResourceBatch{}, err
	}
	if executeErr != nil {
		return ResourceBatch{}, executeErr
	}
	return batch, nil
}

func (service *Service) SearchCatalogs(ctx context.Context, principal auth.Principal, contentType string, input CatalogSearchInput) (ResourceBatch, error) {
	if input.Skip < 0 {
		return ResourceBatch{}, fmt.Errorf("%w: skip must be greater than or equal to 0", ErrInvalidInput)
	}
	if input.Limit < 1 || input.Limit > 100 {
		return ResourceBatch{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidInput)
	}
	addons, err := service.loadAddonList(ctx, principal, false)
	if err != nil {
		return ResourceBatch{}, err
	}
	requests, err := planCatalogSearch(addons, contentType, input)
	if err != nil {
		return ResourceBatch{}, err
	}
	batch, executeErr := service.execute(ctx, requests)
	if err := service.revalidateAddonList(ctx, principal, addons); err != nil {
		return ResourceBatch{}, err
	}
	if executeErr != nil {
		return ResourceBatch{}, executeErr
	}
	return batch, nil
}

func planCatalogSearch(addons []InstalledAddon, contentType string, input CatalogSearchInput) ([]plannedRequest, error) {
	requests := make([]plannedRequest, 0)
	seen := make(map[string]struct{})
	for _, installed := range addons {
		manifest := installed.parsedManifest
		for _, catalog := range manifest.Catalogs {
			if catalog.Type != contentType || !catalog.SupportsSearch() {
				continue
			}
			if input.Skip > 0 && !catalog.DeclaresExtra("skip") {
				continue
			}
			extra := make([]ExtraValue, 1, 1+len(input.Extra)+2)
			extra[0] = ExtraValue{Name: "search", Value: input.Search}
			for _, value := range input.Extra {
				if value.Name != "search" && value.Name != "skip" && value.Name != "limit" {
					extra = append(extra, value)
				}
			}
			if catalog.DeclaresExtra("skip") {
				extra = append(extra, ExtraValue{Name: "skip", Value: strconv.Itoa(input.Skip)})
			}
			if input.Limit > 0 && catalog.DeclaresExtra("limit") {
				extra = append(extra, ExtraValue{Name: "limit", Value: strconv.Itoa(input.Limit)})
			}
			path := manifest.ApplyCatalogDefaults(ResourcePath{
				Resource: "catalog",
				Type:     catalog.Type,
				ID:       catalog.ID,
				Extra:    extra,
			})
			if !manifest.Supports(path) {
				continue
			}
			if input.Skip == 0 {
				path.Extra = slices.DeleteFunc(path.Extra, func(value ExtraValue) bool { return value.Name == "skip" })
			}
			request := plannedRequest{addon: installed, path: path}
			key := plannedRequestKey(request)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			if len(requests) == maxPlannedRequests {
				return nil, requestPlanLimitError()
			}
			requests = append(requests, request)
		}
	}
	return requests, nil
}

func requestPlanLimitError() error {
	return fmt.Errorf("%w: addon request plan exceeds limit of %d", ErrInvalidInput, maxPlannedRequests)
}

func plannedRequestKey(request plannedRequest) string {
	var key strings.Builder
	appendKeyPart := func(value string) {
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
	}
	appendKeyPart(request.addon.transportURL)
	appendKeyPart(request.path.Resource)
	appendKeyPart(request.path.Type)
	appendKeyPart(request.path.ID)
	for _, extra := range request.path.Extra {
		appendKeyPart(extra.Name)
		appendKeyPart(extra.Value)
	}
	return key.String()
}

func (service *Service) execute(ctx context.Context, requests []plannedRequest) (ResourceBatch, error) {
	if len(requests) > maxPlannedRequests {
		return ResourceBatch{}, requestPlanLimitError()
	}
	outcomes := make([]resourceOutcome, len(requests))
	if len(requests) == 0 {
		return ResourceBatch{Results: []ResourceResult{}, Errors: []ResourceFailure{}}, nil
	}

	batchCtx, cancelBatch := context.WithCancel(ctx)
	defer cancelBatch()
	budget := newAggregateResourceBudget(maxAggregateResponseBytes, cancelBatch)
	workerCount := min(len(requests), maxConcurrentRequests)
	jobs := make(chan int)
	var wait sync.WaitGroup
	wait.Add(workerCount)
	for range workerCount {
		go func() {
			defer wait.Done()
			for {
				select {
				case <-budget.done:
					return
				case index, open := <-jobs:
					if !open {
						return
					}
					select {
					case <-budget.done:
						return
					default:
					}
					request := requests[index]
					requestCtx, cancel := context.WithTimeout(batchCtx, service.effectiveRequestTimeout())
					payload, cache, err := service.executeRequest(requestCtx, request, budget)
					cancel()
					if budget.wasExceeded() {
						payload = nil
					}
					outcomes[index] = resourceOutcome{request: request, payload: payload, cache: cache, err: err}
				}
			}
		}()
	}

enqueue:
	for index := range requests {
		select {
		case <-budget.done:
			break enqueue
		case jobs <- index:
		}
	}
	close(jobs)
	wait.Wait()
	if budget.wasExceeded() {
		return ResourceBatch{}, aggregateResourceLimitError()
	}

	batch := ResourceBatch{Results: make([]ResourceResult, 0, len(outcomes)), Errors: make([]ResourceFailure, 0)}
	for _, outcome := range outcomes {
		if outcome.err != nil {
			service.effectiveLogger().WarnContext(ctx, "addon resource request failed",
				"addonId", outcome.request.addon.ID,
				"manifestId", outcome.request.addon.parsedManifest.ID,
				"resource", outcome.request.path.Resource,
				"type", outcome.request.path.Type,
				"resourceId", outcome.request.path.ID,
				"error", outcome.err,
			)
			batch.Errors = append(batch.Errors, ResourceFailure{
				AddonID:    outcome.request.addon.ID,
				ManifestID: outcome.request.addon.parsedManifest.ID,
				Code:       resourceErrorCode(outcome.err),
				Message:    "The addon resource request failed",
			})
			continue
		}
		batch.Results = append(batch.Results, resultFor(outcome.request.addon, outcome.request.path, outcome.payload, outcome.cache))
	}
	return batch, nil
}

func aggregateResourceLimitError() error {
	return fmt.Errorf(
		"%w: aggregate addon response exceeds limit of %d bytes",
		ErrInvalidResponse, maxAggregateResponseBytes,
	)
}

func (service *Service) executeRequest(ctx context.Context, request plannedRequest, budget *aggregateResourceBudget) (payload json.RawMessage, cache CachePolicy, err error) {
	diagnosticAttempt := service.diagnostics.start(request.addon.ID)
	defer func() {
		service.diagnostics.complete(request.addon.ID, diagnosticAttempt, err)
	}()
	payload, cache, err = service.executeTransportRequest(ctx, request, budget)
	if err == nil || ctx.Err() != nil || !isTemporaryProviderError(err) {
		return payload, cache, err
	}
	timer := time.NewTimer(service.effectiveRetryDelay())
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil, CachePolicy{}, ctx.Err()
	}
	return service.executeTransportRequest(ctx, request, budget)
}

func (service *Service) executeTransportRequest(ctx context.Context, request plannedRequest, budget *aggregateResourceBudget) (json.RawMessage, CachePolicy, error) {
	if transport, ok := service.transport.(aggregateBudgetTransport); ok {
		return transport.resourceWithBudget(ctx, request.addon.transportURL, request.path, budget)
	}
	payload, cache, err := service.transport.Resource(ctx, request.addon.transportURL, request.path)
	if err == nil {
		if budgetErr := budget.consumeMaterialized(len(payload)); budgetErr != nil {
			return nil, CachePolicy{}, budgetErr
		}
	}
	return payload, cache, err
}

func (service *Service) effectiveRequestTimeout() time.Duration {
	if service.requestTimeout > 0 {
		return service.requestTimeout
	}
	return 10 * time.Second
}

func (service *Service) effectiveRetryDelay() time.Duration {
	if service.retryDelay > 0 {
		return service.retryDelay
	}
	return 100 * time.Millisecond
}

func (service *Service) effectiveLogger() *slog.Logger {
	if service.logger != nil {
		return service.logger
	}
	return slog.Default()
}

func listForProfile(ctx context.Context, tx pgx.Tx, profileID string) ([]InstalledAddon, error) {
	rows, err := tx.Query(ctx, `
		SELECT pa.id::text, pa.transport_url, pa.manifest::text, access.position,
		       ARRAY(
		           SELECT assignment.profile_id::text
		           FROM addon_profile_access assignment
		           JOIN profiles p ON p.id = assignment.profile_id
		           WHERE assignment.addon_id = pa.id
		           ORDER BY lower(p.name), p.id
		       ),
		       pa.installed_at, pa.updated_at
		FROM addon_profile_access access
		JOIN profile_addons pa ON pa.id = access.addon_id
		WHERE access.profile_id = $1::uuid
		ORDER BY access.position, pa.id
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query profile addons: %w", err)
	}
	defer rows.Close()
	addons := make([]InstalledAddon, 0)
	for rows.Next() {
		installed, err := queryAddon(rows)
		if err != nil {
			return nil, fmt.Errorf("scan profile addon: %w", err)
		}
		addons = append(addons, installed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profile addons: %w", err)
	}
	return addons, nil
}

func addonForProfile(ctx context.Context, tx pgx.Tx, profileID, addonID string) (InstalledAddon, error) {
	if !validUUID(addonID) {
		return InstalledAddon{}, ErrInvalidInput
	}
	installed, err := queryAddon(tx.QueryRow(ctx, addonForProfileQuery, addonID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query addon: %w", err)
	}
	return installed, nil
}

func (service *Service) loadAddonForProfile(ctx context.Context, principal auth.Principal, profileID, addonID string) (InstalledAddon, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return InstalledAddon{}, err
	}
	installed, err := addonForProfile(ctx, tx, profileID, addonID)
	if err != nil {
		return InstalledAddon{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon query: %w", err)
	}
	return installed, nil
}

func (service *Service) revalidateAddon(ctx context.Context, principal auth.Principal, expected InstalledAddon) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin addon revalidation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	current, err := addonForProfile(ctx, tx, profileID, expected.ID)
	if err != nil {
		return err
	}
	if !sameInstalledAddon(current, expected) {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit addon revalidation: %w", err)
	}
	return nil
}

func (service *Service) revalidateAddonList(ctx context.Context, principal auth.Principal, expected []InstalledAddon) error {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin addon list revalidation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	current, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return err
	}
	if len(current) != len(expected) {
		return ErrNotFound
	}
	for index := range current {
		if !sameInstalledAddon(current[index], expected[index]) {
			return ErrNotFound
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit addon list revalidation: %w", err)
	}
	return nil
}

func sameInstalledAddon(left, right InstalledAddon) bool {
	return left.ID == right.ID &&
		left.transportURL == right.transportURL &&
		left.Position == right.Position &&
		left.InstalledAt.Equal(right.InstalledAt) &&
		left.UpdatedAt.Equal(right.UpdatedAt) &&
		bytes.Equal(left.Manifest, right.Manifest) &&
		slices.Equal(left.ProfileIDs, right.ProfileIDs)
}

func activeProfileID(principal auth.Principal) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return "", ErrActiveProfileRequired
	}
	return *principal.ActiveProfileID, nil
}

func authorizeGlobalAddonOrigin(ctx context.Context, tx pgx.Tx, principal auth.Principal) error {
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
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		return fmt.Errorf("authorize global addon origin: %w", err)
	}
	if !administrator {
		return ErrForbidden
	}
	return nil
}

func authorizeActiveProfile(ctx context.Context, tx pgx.Tx, principal auth.Principal, profileID string) error {
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return fmt.Errorf("authorize active addon profile: %w", err)
	}
	if !authorized {
		return ErrActiveProfileRequired
	}
	return nil
}

func lockProfile(ctx context.Context, tx pgx.Tx, profileID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID); err != nil {
		return fmt.Errorf("lock profile addons: %w", err)
	}
	return nil
}

func resultFor(installed InstalledAddon, path ResourcePath, payload json.RawMessage, cache CachePolicy) ResourceResult {
	return ResourceResult{
		AddonID: installed.ID, ManifestID: installed.parsedManifest.ID,
		Resource: path.Resource, Type: path.Type, ID: path.ID, Payload: payload, Cache: cache, Extra: path.Extra,
	}
}

func resourceErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidResponse):
		return "addon_invalid_response"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "addon_request_timeout"
	default:
		return "addon_unavailable"
	}
}

type plannedRequest struct {
	addon InstalledAddon
	path  ResourcePath
}

type resourceOutcome struct {
	request plannedRequest
	payload json.RawMessage
	cache   CachePolicy
	err     error
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}
