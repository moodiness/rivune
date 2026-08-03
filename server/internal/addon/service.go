package addon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	pool      *pgxpool.Pool
	transport Transport
}

func NewService(pool *pgxpool.Pool, transport Transport) *Service {
	if transport == nil {
		transport = NewHTTPTransport(nil)
	}
	return &Service{pool: pool, transport: transport}
}

func (service *Service) Install(ctx context.Context, principal auth.Principal, input InstallInput) (InstalledAddon, error) {
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
	manifest, rawManifest, err := service.transport.Manifest(ctx, transportURL)
	if err != nil {
		return InstalledAddon{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("begin addon installation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
	installed, err := queryAddon(tx.QueryRow(ctx, addonForProfileQuery, addonID, profileID))
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query installed addon: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstalledAddon{}, fmt.Errorf("commit addon installation: %w", err)
	}
	return installed, nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]InstalledAddon, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return nil, err
	}
	return service.listForProfile(ctx, profileID)
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
	if err := lockProfile(ctx, tx, profileID); err != nil {
		return err
	}
	authorized, err := auth.CanManageProfiles(ctx, tx, principal, []string{profileID})
	if err != nil {
		return err
	}
	if !authorized {
		return ErrForbidden
	}
	command, err := tx.Exec(ctx, `
		DELETE FROM profile_addons pa
		WHERE pa.id = $1::uuid
		  AND EXISTS (
		      SELECT 1 FROM addon_profile_access access
		      WHERE access.addon_id = pa.id AND access.profile_id = $2::uuid
		  )
	`, addonID, profileID)
	if err != nil {
		return fmt.Errorf("remove addon: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit addon removal: %w", err)
	}
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
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit addon reorder: %w", err)
	}
	return service.listForProfile(ctx, profileID)
}

func (service *Service) Refresh(ctx context.Context, principal auth.Principal, addonID string) (InstalledAddon, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return InstalledAddon{}, err
	}
	installed, err := service.addonForProfile(ctx, profileID, addonID)
	if err != nil {
		return InstalledAddon{}, err
	}
	manifest, rawManifest, err := service.transport.Manifest(ctx, installed.TransportURL)
	if err != nil {
		return InstalledAddon{}, err
	}
	command, err := service.pool.Exec(ctx, `
		UPDATE profile_addons pa
		SET manifest = $3::jsonb, manifest_id = $4, manifest_version = $5, updated_at = now()
		WHERE pa.id = $2::uuid
		  AND EXISTS (
		      SELECT 1 FROM addon_profile_access access
		      WHERE access.addon_id = pa.id AND access.profile_id = $1::uuid
		  )
	`, profileID, addonID, rawManifest, manifest.ID, manifest.Version)
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("refresh addon manifest: %w", err)
	}
	if command.RowsAffected() == 0 {
		return InstalledAddon{}, ErrNotFound
	}
	return service.addonForProfile(ctx, profileID, addonID)
}

func (service *Service) Catalogs(ctx context.Context, principal auth.Principal) ([]CatalogDescriptor, error) {
	addons, err := service.List(ctx, principal)
	if err != nil {
		return nil, err
	}
	catalogs := make([]CatalogDescriptor, 0)
	for _, installed := range addons {
		for _, catalog := range installed.parsedManifest.Catalogs {
			catalogs = append(catalogs, CatalogDescriptor{
				AddonID: installed.ID, ManifestID: installed.parsedManifest.ID, Position: installed.Position, Catalog: catalog,
			})
		}
		for _, catalog := range installed.parsedManifest.AddonCatalogs {
			catalogs = append(catalogs, CatalogDescriptor{
				AddonID: installed.ID, ManifestID: installed.parsedManifest.ID, Position: installed.Position, Catalog: catalog, AddonCatalog: true,
			})
		}
	}
	return catalogs, nil
}

func (service *Service) Fetch(ctx context.Context, principal auth.Principal, addonID string, path ResourcePath) (ResourceResult, error) {
	profileID, err := activeProfileID(principal)
	if err != nil {
		return ResourceResult{}, err
	}
	installed, err := service.addonForProfile(ctx, profileID, addonID)
	if err != nil {
		return ResourceResult{}, err
	}
	path = installed.parsedManifest.ApplyCatalogDefaults(path)
	if !installed.parsedManifest.Supports(path) {
		return ResourceResult{}, ErrUnsupportedResource
	}
	payload, cache, err := service.transport.Resource(ctx, installed.TransportURL, path)
	if err != nil {
		return ResourceResult{}, err
	}
	return resultFor(installed, path, payload, cache), nil
}

func (service *Service) FetchAll(ctx context.Context, principal auth.Principal, path ResourcePath) (ResourceBatch, error) {
	addons, err := service.List(ctx, principal)
	if err != nil {
		return ResourceBatch{}, err
	}
	requests := make([]plannedRequest, 0, len(addons))
	for _, installed := range addons {
		requestPath := installed.parsedManifest.ApplyCatalogDefaults(path)
		if installed.parsedManifest.Supports(requestPath) {
			requests = append(requests, plannedRequest{addon: installed, path: requestPath})
		}
	}
	return service.execute(ctx, requests), nil
}

func (service *Service) FetchCatalogs(ctx context.Context, principal auth.Principal, contentType string, extra []ExtraValue, addonCatalogs bool) (ResourceBatch, error) {
	addons, err := service.List(ctx, principal)
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
			if installed.parsedManifest.Supports(path) {
				requests = append(requests, plannedRequest{addon: installed, path: path})
			}
		}
	}
	return service.execute(ctx, requests), nil
}

func (service *Service) SearchCatalogs(ctx context.Context, principal auth.Principal, contentType string, input CatalogSearchInput) (ResourceBatch, error) {
	if input.Skip < 0 {
		return ResourceBatch{}, fmt.Errorf("%w: skip must be greater than or equal to 0", ErrInvalidInput)
	}
	if input.Limit < 1 || input.Limit > 100 {
		return ResourceBatch{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidInput)
	}
	addons, err := service.List(ctx, principal)
	if err != nil {
		return ResourceBatch{}, err
	}
	return service.execute(ctx, planCatalogSearch(addons, contentType, input)), nil
}

func planCatalogSearch(addons []InstalledAddon, contentType string, input CatalogSearchInput) []plannedRequest {
	requests := make([]plannedRequest, 0)
	seen := make(map[string]struct{})
	for _, installed := range addons {
		manifest := installed.parsedManifest
		if contentType == "tv" && !manifest.SupportsTV() {
			continue
		}
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
			request := plannedRequest{addon: installed, path: path}
			key := plannedRequestKey(request)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			requests = append(requests, request)
		}
	}
	return requests
}

func plannedRequestKey(request plannedRequest) string {
	var key strings.Builder
	appendKeyPart := func(value string) {
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
	}
	appendKeyPart(request.addon.TransportURL)
	appendKeyPart(request.path.Resource)
	appendKeyPart(request.path.Type)
	appendKeyPart(request.path.ID)
	for _, extra := range request.path.Extra {
		appendKeyPart(extra.Name)
		appendKeyPart(extra.Value)
	}
	return key.String()
}

func (service *Service) execute(ctx context.Context, requests []plannedRequest) ResourceBatch {
	outcomes := make([]resourceOutcome, len(requests))
	semaphore := make(chan struct{}, 8)
	var wait sync.WaitGroup
	for index, request := range requests {
		index, request := index, request
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				outcomes[index] = resourceOutcome{request: request, err: ctx.Err()}
				return
			}
			payload, cache, err := service.transport.Resource(ctx, request.addon.TransportURL, request.path)
			outcomes[index] = resourceOutcome{request: request, payload: payload, cache: cache, err: err}
		}()
	}
	wait.Wait()
	batch := ResourceBatch{Results: make([]ResourceResult, 0, len(outcomes)), Errors: make([]ResourceFailure, 0)}
	for _, outcome := range outcomes {
		if outcome.err != nil {
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
	return batch
}

func (service *Service) listForProfile(ctx context.Context, profileID string) ([]InstalledAddon, error) {
	rows, err := service.pool.Query(ctx, `
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

func (service *Service) addonForProfile(ctx context.Context, profileID, addonID string) (InstalledAddon, error) {
	if !validUUID(addonID) {
		return InstalledAddon{}, ErrInvalidInput
	}
	installed, err := queryAddon(service.pool.QueryRow(ctx, addonForProfileQuery, addonID, profileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return InstalledAddon{}, ErrNotFound
	}
	if err != nil {
		return InstalledAddon{}, fmt.Errorf("query addon: %w", err)
	}
	return installed, nil
}

func activeProfileID(principal auth.Principal) (string, error) {
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(time.Now().UTC()) {
		return "", ErrActiveProfileRequired
	}
	return *principal.ActiveProfileID, nil
}

func lockProfile(ctx context.Context, tx pgx.Tx, profileID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID); err != nil {
		return fmt.Errorf("lock profile addons: %w", err)
	}
	return nil
}

func resultFor(installed InstalledAddon, path ResourcePath, payload json.RawMessage, cache CachePolicy) ResourceResult {
	return ResourceResult{
		AddonID: installed.ID, ManifestID: installed.parsedManifest.ID, TransportURL: installed.TransportURL,
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
