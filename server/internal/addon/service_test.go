package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type catalogSearchTransport struct {
	mu    sync.Mutex
	paths []ResourcePath
}

func (transport *catalogSearchTransport) Manifest(context.Context, string) (Manifest, json.RawMessage, error) {
	return Manifest{}, nil, errors.New("unexpected manifest request")
}

func (transport *catalogSearchTransport) Resource(_ context.Context, _ string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
	transport.mu.Lock()
	transport.paths = append(transport.paths, path)
	transport.mu.Unlock()
	if path.ID == "broken" {
		return nil, CachePolicy{}, ErrProviderUnavailable
	}
	return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
}

type resourceTransportFunc func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error)

type functionTransport struct {
	resource resourceTransportFunc
}

func (transport functionTransport) Manifest(context.Context, string) (Manifest, json.RawMessage, error) {
	return Manifest{}, nil, errors.New("unexpected manifest request")
}

func (transport functionTransport) Resource(ctx context.Context, transportURL string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
	return transport.resource(ctx, transportURL, path)
}

type countingRoundTripper struct {
	base  http.RoundTripper
	bytes *atomic.Int64
}

func (transport countingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err == nil {
		response.Body = &countingReadCloser{ReadCloser: response.Body, bytes: transport.bytes}
	}
	return response, err
}

type countingReadCloser struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (reader *countingReadCloser) Read(destination []byte) (int, error) {
	read, err := reader.ReadCloser.Read(destination)
	reader.bytes.Add(int64(read))
	return read, err
}

func writeChunkedResponse(writer http.ResponseWriter, request *http.Request, size int64) {
	flusher, _ := writer.(http.Flusher)
	chunk := bytes.Repeat([]byte{'x'}, 32<<10)
	for remaining := size; remaining > 0; {
		select {
		case <-request.Context().Done():
			return
		default:
		}
		length := min(int64(len(chunk)), remaining)
		written, err := writer.Write(chunk[:length])
		if err != nil {
			return
		}
		remaining -= int64(written)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

type installManifestTransport struct {
	calls         int
	resourceCalls int
	transportURLs []string
	manifest      func(context.Context, string) (Manifest, json.RawMessage, error)
	resource      resourceTransportFunc
}

func (transport *installManifestTransport) Manifest(ctx context.Context, transportURL string) (Manifest, json.RawMessage, error) {
	transport.calls++
	transport.transportURLs = append(transport.transportURLs, transportURL)
	return transport.manifest(ctx, transportURL)
}

func (transport *installManifestTransport) Resource(ctx context.Context, transportURL string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
	transport.resourceCalls++
	if transport.resource == nil {
		return nil, CachePolicy{}, errors.New("unexpected resource request")
	}
	return transport.resource(ctx, transportURL, path)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func executeTestRequest(id string) plannedRequest {
	return plannedRequest{
		addon: InstalledAddon{
			ID:           "addon-" + id,
			transportURL: "https://" + id + ".example/manifest.json",
			parsedManifest: Manifest{
				ID: "manifest-" + id,
			},
		},
		path: ResourcePath{Resource: "catalog", Type: "movie", ID: id},
	}
}

func TestGenericResourceFetchRejectsPlaybackResourcesBeforeProviderAccess(t *testing.T) {
	providerCalls := 0
	transport := functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		providerCalls++
		return json.RawMessage(`{"streams":[{"url":"https://provider.example/private?token=secret","behaviorHints":{"proxyHeaders":{"request":{"Authorization":"Bearer secret"}}}}]}`), CachePolicy{}, nil
	}}
	service := NewService(nil, transport, discardLogger())

	for _, resource := range []string{"stream", "subtitles"} {
		t.Run(resource, func(t *testing.T) {
			result, err := service.Fetch(context.Background(), auth.Principal{}, "not-loaded", ResourcePath{Resource: resource, Type: "movie", ID: "provider-id"})
			if !errors.Is(err, ErrUnsupportedResource) {
				t.Fatalf("Fetch error = %v, want %v", err, ErrUnsupportedResource)
			}
			batch, err := service.FetchAll(context.Background(), auth.Principal{}, ResourcePath{Resource: resource, Type: "movie", ID: "provider-id"})
			if !errors.Is(err, ErrUnsupportedResource) {
				t.Fatalf("FetchAll error = %v, want %v", err, ErrUnsupportedResource)
			}
			serialized, marshalErr := json.Marshal(struct {
				Result ResourceResult `json:"result"`
				Batch  ResourceBatch  `json:"batch"`
			}{Result: result, Batch: batch})
			if marshalErr != nil {
				t.Fatalf("marshal rejected results: %v", marshalErr)
			}
			if strings.Contains(string(serialized), "provider.example") || strings.Contains(string(serialized), "secret") {
				t.Fatalf("rejected resource exposed provider data: %s", serialized)
			}
		})
	}
	for _, resource := range []string{"catalog", "addon_catalog", "meta"} {
		if _, err := service.Fetch(context.Background(), auth.Principal{}, "not-loaded", ResourcePath{Resource: resource, Type: "movie", ID: "provider-id"}); !errors.Is(err, ErrActiveProfileRequired) {
			t.Fatalf("allowed Fetch resource %q error = %v, want profile authorization", resource, err)
		}
		if _, err := service.FetchAll(context.Background(), auth.Principal{}, ResourcePath{Resource: resource, Type: "movie", ID: "provider-id"}); !errors.Is(err, ErrActiveProfileRequired) {
			t.Fatalf("allowed FetchAll resource %q error = %v, want profile authorization", resource, err)
		}
	}
	if providerCalls != 0 {
		t.Fatalf("provider resource calls = %d, want 0", providerCalls)
	}
}

func TestInstallRequiresGlobalAdministratorBeforeManifestFetch(t *testing.T) {
	activeProfileID := "2a000000-0000-4000-8000-000000000004"
	categoryID := "2a000000-0000-4000-8000-000000000002"
	expiresAt := time.Now().UTC().Add(time.Hour)
	transport := &installManifestTransport{manifest: func(context.Context, string) (Manifest, json.RawMessage, error) {
		return Manifest{}, nil, errors.New("unexpected manifest request")
	}}
	service := NewService(nil, transport, discardLogger())

	_, err := service.Install(context.Background(), auth.Principal{
		UserID: "2a000000-0000-4000-8000-000000000001", Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
	}, InstallInput{TransportURL: "http://192.168.1.10/manifest.json"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated manager install error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("manifest transport calls after delegated manager denial = %d, want 0", transport.calls)
	}
}

func TestPreviewRequiresGlobalAdministratorBeforeDatabaseOrManifestFetch(t *testing.T) {
	activeProfileID := "2a000000-0000-4000-8000-000000000004"
	categoryID := "2a000000-0000-4000-8000-000000000002"
	expiresAt := time.Now().UTC().Add(time.Hour)
	transport := &installManifestTransport{manifest: func(context.Context, string) (Manifest, json.RawMessage, error) {
		return Manifest{}, nil, errors.New("unexpected manifest request")
	}}
	service := NewService(nil, transport, discardLogger())

	_, err := service.Preview(context.Background(), auth.Principal{
		UserID: "2a000000-0000-4000-8000-000000000001", Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
	}, InstallInput{TransportURL: "https://addon.example/manifest.json"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated manager preview error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("manifest transport calls after delegated preview denial = %d, want 0", transport.calls)
	}
}

func TestManagementRequiresGlobalAdministratorBeforeDatabaseAccess(t *testing.T) {
	activeProfileID := "2a000000-0000-4000-8000-000000000004"
	categoryID := "2a000000-0000-4000-8000-000000000002"
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := NewService(nil, nil, discardLogger())
	_, err := service.Management(context.Background(), auth.Principal{
		UserID: "2a000000-0000-4000-8000-000000000001", Role: "admin",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID,
		ActiveProfileID: &activeProfileID, ProfileGrantExpiresAt: &expiresAt,
	}, "2a000000-0000-4000-8000-000000000006")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated management lookup error = %v, want %v", err, ErrForbidden)
	}
}

func TestInstallAuthorizesBeforeManifestAndReauthorizesBeforePersistence(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run addon installation authorization tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		userID               = "2a000000-0000-4000-8000-000000000001"
		categoryAID          = "2a000000-0000-4000-8000-000000000002"
		categoryBID          = "2a000000-0000-4000-8000-000000000003"
		profileAID           = "2a000000-0000-4000-8000-000000000004"
		profileBID           = "2a000000-0000-4000-8000-000000000005"
		transportURL         = "https://authorization-boundary.example/config/private/manifest.json?credential=stored"
		profileScopedAddonID = "2a000000-0000-4000-8000-000000000007"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM profile_addons
			WHERE id = $1::uuid
			   OR profile_id = ANY($2::uuid[])
			   OR transport_url = ANY($3::text[])
		`, profileScopedAddonID, []string{profileAID, profileBID}, []string{
			transportURL,
			"https://profile-scope.invalid/manifest.json",
		})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileAID, profileBID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(ctx, `
		WITH inserted_categories AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Addon authorization A', 'addon authorization a', 910001),
			       ($2::uuid, 'Addon authorization B', 'addon authorization b', 910002)
			RETURNING id
		), inserted_user AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($3::uuid, 'addon-authorization-user', 'unused-test-hash', 'admin')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES ($4::uuid, $1::uuid, 'Addon authorization profile A'),
			       ($5::uuid, $1::uuid, 'Addon authorization profile B')
			RETURNING id
		)
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($3::uuid, $4::uuid, true)
	`, categoryAID, categoryBID, userID, profileAID, profileBID); err != nil {
		t.Fatalf("seed addon authorization boundary: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	categoryA := categoryAID
	principal := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &categoryA, ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	rawManifest := json.RawMessage(`{"id":"org.rivune.authorization-boundary","version":"1.0.0","name":"Authorization Boundary","description":"Validated preview","types":["movie","series"],"resources":["catalog","stream","catalog"],"catalogs":[{"type":"movie","id":"searchable","extra":[{"name":"search"},{"name":"skip"}]}],"behaviorHints":{"adult":true,"p2p":true,"configurationRequired":true}}`)
	manifest, validatedRawManifest, err := ParseManifest(rawManifest)
	if err != nil {
		t.Fatalf("validate preview manifest fixture: %v", err)
	}
	rawManifest = validatedRawManifest
	transport := &installManifestTransport{manifest: func(context.Context, string) (Manifest, json.RawMessage, error) {
		return manifest, rawManifest, nil
	}}
	service := NewService(pool, transport, discardLogger())
	input := InstallInput{TransportURL: transportURL, ProfileIDs: []string{profileAID, profileBID}}

	if _, err := service.Install(ctx, principal, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized install error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("manifest transport calls after denied preflight = %d, want 0", transport.calls)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileBID); err != nil {
		t.Fatalf("grant second profile management: %v", err)
	}
	if _, err := service.Install(ctx, principal, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated manager install error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("manifest transport calls after delegated manager denial = %d, want 0", transport.calls)
	}
	globalPrincipal := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	if _, err := service.Preview(ctx, globalPrincipal, InstallInput{TransportURL: transportURL, ProfileIDs: []string{categoryBID}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("preview with unauthorized profile assignment error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("profile-denied preview reached manifest transport %d times", transport.calls)
	}
	previewInput := InstallInput{
		TransportURL: "stremio://authorization-boundary.example/config/private",
		ProfileIDs:   []string{profileAID, profileBID},
	}
	var previewWritesBefore int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM profile_addons
		WHERE profile_id = $1::uuid AND transport_url = $2
	`, profileAID, "https://authorization-boundary.example/config/private/manifest.json").Scan(&previewWritesBefore); err != nil {
		t.Fatalf("count baseline preview persistence: %v", err)
	}
	preview, err := service.Preview(ctx, globalPrincipal, previewInput)
	if err != nil {
		t.Fatalf("authorized preview: %v", err)
	}
	if preview.Manifest.ID != manifest.ID || preview.Manifest.Description != manifest.Description || !preview.Manifest.BehaviorHints.Adult || !preview.Manifest.BehaviorHints.P2P || !preview.Manifest.BehaviorHints.ConfigurationRequired {
		t.Fatalf("preview manifest = %+v", preview.Manifest)
	}
	wantCapabilities := AddonCapabilities{Resources: []string{"catalog", "stream"}, Search: true, Pagination: true, SearchPagination: true}
	if !reflect.DeepEqual(preview.Capabilities, wantCapabilities) {
		t.Fatalf("preview capabilities = %+v, want %+v", preview.Capabilities, wantCapabilities)
	}
	if transport.calls != 1 || len(transport.transportURLs) != 1 || transport.transportURLs[0] != "https://authorization-boundary.example/config/private/manifest.json" {
		t.Fatalf("preview manifest transports = %v with %d calls", transport.transportURLs, transport.calls)
	}
	var previewWritesAfter int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM profile_addons
		WHERE profile_id = $1::uuid AND transport_url = $2
	`, profileAID, "https://authorization-boundary.example/config/private/manifest.json").Scan(&previewWritesAfter); err != nil {
		t.Fatalf("count preview persistence: %v", err)
	}
	if previewWritesAfter != previewWritesBefore {
		t.Fatalf("preview changed persisted add-ons from %d to %d", previewWritesBefore, previewWritesAfter)
	}
	_, previewObservations := service.diagnostics.snapshot()
	if len(previewObservations) != 0 {
		t.Fatalf("preview created health observations: %+v", previewObservations)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("revoke persisted global authority before preview: %v", err)
	}
	if _, err := service.Preview(ctx, globalPrincipal, previewInput); !errors.Is(err, ErrForbidden) {
		t.Fatalf("preview after persisted global authority revocation error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 1 {
		t.Fatalf("persisted-role-denied preview reached manifest transport %d times", transport.calls)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("restore global authority after preview: %v", err)
	}
	transport.manifest = func(ctx context.Context, _ string) (Manifest, json.RawMessage, error) {
		if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, userID); err != nil {
			return Manifest{}, nil, fmt.Errorf("revoke global authority during manifest request: %w", err)
		}
		return manifest, rawManifest, nil
	}
	if _, err := service.Install(ctx, globalPrincipal, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("install after post-manifest global authority revocation error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 2 {
		t.Fatalf("manifest transport calls after post-network reauthorization = %d, want 2", transport.calls)
	}
	var installedCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM profile_addons WHERE transport_url = $1
	`, transportURL).Scan(&installedCount); err != nil {
		t.Fatalf("count protected addon writes: %v", err)
	}
	if installedCount != 0 {
		t.Fatalf("protected addon writes after failed reauthorization = %d, want 0", installedCount)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("restore global authority after installation reauthorization: %v", err)
	}
	transport.manifest = func(context.Context, string) (Manifest, json.RawMessage, error) {
		return manifest, rawManifest, nil
	}
	installed, err := service.Install(ctx, globalPrincipal, input)
	if err != nil {
		t.Fatalf("authorized install: %v", err)
	}
	if transport.calls != 3 {
		t.Fatalf("manifest transport calls after authorized install = %d, want 3", transport.calls)
	}
	if len(installed.ProfileIDs) != 2 {
		t.Fatalf("installed profile assignments = %v, want both profiles", installed.ProfileIDs)
	}
	if installed.TransportURL != transportURL {
		t.Fatal("install response did not preserve the stored transport URL")
	}
	if !installed.Enabled {
		t.Fatal("new installation was not enabled by default")
	}
	var persistedEnabled bool
	if err := pool.QueryRow(ctx, `SELECT enabled FROM profile_addons WHERE id = $1::uuid`, installed.ID).Scan(&persistedEnabled); err != nil {
		t.Fatalf("query installed addon availability: %v", err)
	}
	if !persistedEnabled {
		t.Fatal("database default did not enable the new installation")
	}
	if err := service.ValidatePlaybackAccess(ctx, globalPrincipal, installed.ID); err != nil {
		t.Fatalf("validate enabled playback access: %v", err)
	}
	if transport.resourceCalls != 0 {
		t.Fatalf("playback access validation made %d provider calls", transport.resourceCalls)
	}
	disabled := false
	callsBeforeToggle := transport.calls
	if _, err := service.Update(ctx, principal, installed.ID, UpdateAddonInput{
		Enabled: &disabled, ProfileIDs: []string{profileAID, profileBID},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated availability toggle error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != callsBeforeToggle {
		t.Fatalf("denied availability toggle fetched manifest %d times", transport.calls-callsBeforeToggle)
	}
	disabledAddon, err := service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{
		Enabled: &disabled, ProfileIDs: []string{profileAID, profileBID},
	})
	if err != nil {
		t.Fatalf("disable addon: %v", err)
	}
	if disabledAddon.Enabled || transport.calls != callsBeforeToggle {
		t.Fatalf("disable result = enabled %t with %d manifest calls, want false and %d", disabledAddon.Enabled, transport.calls, callsBeforeToggle)
	}
	if err := service.ValidatePlaybackAccess(ctx, globalPrincipal, installed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled playback access validation error = %v, want %v", err, ErrNotFound)
	}
	if transport.resourceCalls != 0 {
		t.Fatalf("disabled playback access validation made %d provider calls", transport.resourceCalls)
	}
	manageable, err := service.List(ctx, globalPrincipal)
	if err != nil || len(manageable) != 1 || manageable[0].ID != installed.ID || manageable[0].Enabled {
		t.Fatalf("disabled management list = %+v, error %v", manageable, err)
	}
	managedDisabled, err := service.Management(ctx, globalPrincipal, installed.ID)
	if err != nil || managedDisabled.Enabled {
		t.Fatalf("disabled management lookup = %+v, error %v", managedDisabled, err)
	}
	reorderedDisabled, err := service.Reorder(ctx, globalPrincipal, ReorderInput{AddonIDs: []string{installed.ID}})
	if err != nil || len(reorderedDisabled) != 1 || reorderedDisabled[0].Enabled {
		t.Fatalf("disabled addon reorder result = %+v, error %v", reorderedDisabled, err)
	}
	disabledDiagnostics, err := service.Diagnostics(ctx, globalPrincipal)
	if err != nil || len(disabledDiagnostics.Diagnostics) != 1 || disabledDiagnostics.Diagnostics[0].AddonID != installed.ID {
		t.Fatalf("disabled addon diagnostics = %+v, error %v", disabledDiagnostics, err)
	}
	if catalogs, err := service.Catalogs(ctx, globalPrincipal); err != nil || len(catalogs) != 0 {
		t.Fatalf("disabled catalog discovery = %+v, error %v", catalogs, err)
	}
	if _, err := service.Fetch(ctx, globalPrincipal, installed.ID, ResourcePath{Resource: "catalog", Type: "movie", ID: "searchable"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled exact catalog fetch error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.FetchPlaybackResource(ctx, globalPrincipal, installed.ID, ResourcePath{Resource: "stream", Type: "movie", ID: "tt0000001"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled exact playback fetch error = %v, want %v", err, ErrNotFound)
	}
	if batch, err := service.FetchAll(ctx, globalPrincipal, ResourcePath{Resource: "catalog", Type: "movie", ID: "searchable"}); err != nil || len(batch.Results) != 0 || len(batch.Errors) != 0 {
		t.Fatalf("disabled generic fanout = %+v, error %v", batch, err)
	}
	if batch, err := service.FetchAllPlaybackResources(ctx, globalPrincipal, ResourcePath{Resource: "stream", Type: "movie", ID: "tt0000001"}); err != nil || len(batch.Results) != 0 || len(batch.Errors) != 0 {
		t.Fatalf("disabled playback fanout = %+v, error %v", batch, err)
	}
	for _, addonCatalogs := range []bool{false, true} {
		if batch, err := service.FetchCatalogs(ctx, globalPrincipal, "", nil, addonCatalogs); err != nil || len(batch.Results) != 0 || len(batch.Errors) != 0 {
			t.Fatalf("disabled catalog fanout (%t) = %+v, error %v", addonCatalogs, batch, err)
		}
	}
	if batch, err := service.SearchCatalogs(ctx, globalPrincipal, "movie", CatalogSearchInput{Search: "disabled", Limit: 10}); err != nil || len(batch.Results) != 0 || len(batch.Errors) != 0 {
		t.Fatalf("disabled catalog search = %+v, error %v", batch, err)
	}
	if transport.resourceCalls != 0 {
		t.Fatalf("disabled runtime paths made %d provider calls", transport.resourceCalls)
	}
	refreshedDisabled, err := service.Refresh(ctx, globalPrincipal, installed.ID)
	if err != nil || refreshedDisabled.Enabled {
		t.Fatalf("refresh disabled addon = %+v, error %v", refreshedDisabled, err)
	}
	if transport.calls != callsBeforeToggle+1 {
		t.Fatalf("disabled refresh manifest calls = %d, want %d", transport.calls, callsBeforeToggle+1)
	}
	enabled := true
	installed, err = service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{
		Enabled: &enabled, ProfileIDs: []string{profileAID, profileBID},
	})
	if err != nil || !installed.Enabled {
		t.Fatalf("re-enable addon = %+v, error %v", installed, err)
	}
	if transport.calls != callsBeforeToggle+1 {
		t.Fatal("re-enable unexpectedly fetched the manifest")
	}
	diagnostics, err := service.Diagnostics(ctx, globalPrincipal)
	if err != nil || len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].AddonID != installed.ID || diagnostics.Diagnostics[0].State != DiagnosticStateAvailable {
		t.Fatalf("post-commit installation diagnostics = %+v, error %v", diagnostics, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("revoke persisted global role for diagnostics: %v", err)
	}
	if _, err := service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{
		Enabled: &disabled, ProfileIDs: []string{profileAID, profileBID},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("availability toggle after persisted role revocation error = %v, want %v", err, ErrForbidden)
	}
	if err := pool.QueryRow(ctx, `SELECT enabled FROM profile_addons WHERE id = $1::uuid`, installed.ID).Scan(&persistedEnabled); err != nil || !persistedEnabled {
		t.Fatalf("denied persisted-role toggle changed availability to %t, error %v", persistedEnabled, err)
	}
	if _, err := service.Diagnostics(ctx, globalPrincipal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("diagnostics after persisted role revocation error = %v, want %v", err, ErrForbidden)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("restore persisted global role after diagnostics: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted_addon AS (
			INSERT INTO profile_addons (
				id, profile_id, transport_url, manifest, manifest_id, manifest_version, position
			) VALUES ($1::uuid, $2::uuid, 'https://profile-scope.invalid/manifest.json', $3::jsonb, $4, $5, 1)
			RETURNING id
		)
		INSERT INTO addon_profile_access (addon_id, profile_id, position)
		SELECT id, $2::uuid, 1 FROM inserted_addon
	`, profileScopedAddonID, profileBID, rawManifest, manifest.ID, manifest.Version); err != nil {
		t.Fatalf("seed profile-scoped diagnostics addon: %v", err)
	}
	diagnostics, err = service.Diagnostics(ctx, globalPrincipal)
	if err != nil || len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].AddonID != installed.ID {
		t.Fatalf("active profile A diagnostics = %+v, error %v", diagnostics, err)
	}
	globalPrincipal.ActiveProfileID = new(profileBID)
	diagnostics, err = service.Diagnostics(ctx, globalPrincipal)
	if err != nil || len(diagnostics.Diagnostics) != 2 || diagnostics.Diagnostics[0].AddonID != installed.ID || diagnostics.Diagnostics[1].AddonID != profileScopedAddonID || diagnostics.Diagnostics[1].State != DiagnosticStateUnknown {
		t.Fatalf("active profile B diagnostics = %+v, error %v", diagnostics, err)
	}
	globalPrincipal.ActiveProfileID = new(profileAID)
	managed, err := service.Management(ctx, globalPrincipal, installed.ID)
	if err != nil {
		t.Fatalf("authorized addon management lookup: %v", err)
	}
	if managed.TransportURL != transportURL {
		t.Fatal("addon management lookup did not preserve the stored transport URL")
	}
	if _, err := service.Management(ctx, principal, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated addon management lookup error = %v, want %v", err, ErrForbidden)
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileBID); err != nil {
		t.Fatalf("revoke second profile management: %v", err)
	}
	callsBeforeDeniedUpdate := transport.calls
	if _, err := service.Update(ctx, principal, installed.ID, UpdateAddonInput{
		TransportURL: new(transportURL),
		ProfileIDs:   []string{profileAID, profileBID},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized update error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != callsBeforeDeniedUpdate {
		t.Fatalf("manifest transport calls after denied update = %d, want %d", transport.calls, callsBeforeDeniedUpdate)
	}

	if err := service.Remove(ctx, principal, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("shared addon removal error = %v, want %v", err, ErrForbidden)
	}
	var addonCount, assignmentCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), (SELECT count(*) FROM addon_profile_access WHERE addon_id = $1::uuid)
		FROM profile_addons
		WHERE id = $1::uuid
	`, installed.ID).Scan(&addonCount, &assignmentCount); err != nil {
		t.Fatalf("query denied shared removal state: %v", err)
	}
	if addonCount != 1 || assignmentCount != 2 {
		t.Fatalf("shared addon state after denied removal = addon %d assignments %d, want 1 and 2", addonCount, assignmentCount)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1::uuid, $2::uuid, true)
	`, userID, profileBID); err != nil {
		t.Fatalf("restore second profile access: %v", err)
	}
	callsBeforeAssignmentUpdate := transport.calls
	delegatedUpdate, err := service.Update(ctx, principal, installed.ID, UpdateAddonInput{
		ProfileIDs: []string{profileAID, profileBID},
	})
	if err != nil {
		t.Fatalf("delegated assignment-only update: %v", err)
	}
	if delegatedUpdate.TransportURL != "" {
		t.Fatal("delegated assignment-only update exposed the transport URL")
	}
	if transport.calls != callsBeforeAssignmentUpdate {
		t.Fatalf("manifest transport calls after assignment-only update = %d, want %d", transport.calls, callsBeforeAssignmentUpdate)
	}

	const changedTransportURL = "https://changed-authorization-boundary.example/config/private/manifest.json?credential=replacement"
	if _, err := service.Update(ctx, principal, installed.ID, UpdateAddonInput{
		TransportURL: new(changedTransportURL),
		ProfileIDs:   []string{profileAID, profileBID},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated origin update error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != callsBeforeAssignmentUpdate {
		t.Fatalf("manifest transport calls after delegated origin denial = %d, want %d", transport.calls, callsBeforeAssignmentUpdate)
	}
	var persistedTransportURL string
	if err := pool.QueryRow(ctx, `
		SELECT transport_url FROM profile_addons WHERE id = $1::uuid
	`, installed.ID).Scan(&persistedTransportURL); err != nil {
		t.Fatalf("query addon after delegated origin denial: %v", err)
	}
	if persistedTransportURL != transportURL {
		t.Fatalf("transport URL after delegated origin denial = %q, want %q", persistedTransportURL, transportURL)
	}

	const concurrentTransportURL = "https://concurrent-authorization-boundary.example/manifest.json"
	transport.manifest = func(ctx context.Context, _ string) (Manifest, json.RawMessage, error) {
		if _, err := pool.Exec(ctx, `
			UPDATE profile_addons SET transport_url = $2 WHERE id = $1::uuid
		`, installed.ID, concurrentTransportURL); err != nil {
			return Manifest{}, nil, fmt.Errorf("change addon during update manifest: %w", err)
		}
		return manifest, rawManifest, nil
	}
	updatePriorFailure := service.diagnostics.start(installed.ID)
	service.diagnostics.complete(installed.ID, updatePriorFailure, ErrInvalidResponse)
	if _, err := service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{
		TransportURL: new(changedTransportURL),
		ProfileIDs:   []string{profileAID, profileBID},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("global update after concurrent origin change error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != callsBeforeAssignmentUpdate+1 {
		t.Fatalf("manifest transport calls after concurrent origin change = %d, want %d", transport.calls, callsBeforeAssignmentUpdate+1)
	}
	_, failedUpdateObservations := service.diagnostics.snapshot()
	failedUpdateObservation := failedUpdateObservations[installed.ID]
	if failedUpdateObservation.latestSucceeded || failedUpdateObservation.lastError == nil || failedUpdateObservation.lastError.Code != DiagnosticErrorInvalidResponse {
		t.Fatalf("database-rejected update altered diagnostics: %+v", failedUpdateObservation)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE profile_addons SET transport_url = $2 WHERE id = $1::uuid
	`, installed.ID, transportURL); err != nil {
		t.Fatalf("restore addon after concurrent origin change: %v", err)
	}
	transport.manifest = func(context.Context, string) (Manifest, json.RawMessage, error) {
		return manifest, rawManifest, nil
	}
	installed, err = service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{
		TransportURL: new(changedTransportURL),
		ProfileIDs:   []string{profileAID, profileBID},
	})
	if err != nil {
		t.Fatalf("global origin update: %v", err)
	}
	if installed.TransportURL != changedTransportURL {
		t.Fatal("update response did not preserve the replacement transport URL")
	}
	if transport.calls != callsBeforeAssignmentUpdate+2 {
		t.Fatalf("manifest transport calls after global origin update = %d, want %d", transport.calls, callsBeforeAssignmentUpdate+2)
	}
	_, successfulUpdateObservations := service.diagnostics.snapshot()
	successfulUpdateObservation := successfulUpdateObservations[installed.ID]
	if !successfulUpdateObservation.latestSucceeded || successfulUpdateObservation.lastError == nil || successfulUpdateObservation.lastError.Code != DiagnosticErrorInvalidResponse {
		t.Fatalf("committed replacement update diagnostics = %+v", successfulUpdateObservation)
	}
	resourceCallsBeforeGuardedRetry := transport.resourceCalls
	firstTemporaryAttempt := make(chan struct{})
	guardedRetryCalls := 0
	transport.resource = func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		guardedRetryCalls++
		if guardedRetryCalls == 1 {
			close(firstTemporaryAttempt)
		}
		return nil, CachePolicy{}, unavailable("guarded retry temporary provider failure", errors.New("temporary provider failure"), true)
	}
	service.retryDelay = time.Second
	guardedRetryDone := make(chan error, 1)
	go func() {
		_, retryErr := service.FetchAll(ctx, principal, ResourcePath{Resource: "catalog", Type: "movie", ID: "searchable"})
		guardedRetryDone <- retryErr
	}()
	select {
	case <-firstTemporaryAttempt:
	case <-ctx.Done():
		t.Fatalf("wait for temporary provider attempt: %v", ctx.Err())
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = false, updated_at = now() WHERE id = $1::uuid`, installed.ID); err != nil {
		t.Fatalf("disable addon during retry delay: %v", err)
	}
	select {
	case retryErr := <-guardedRetryDone:
		if !errors.Is(retryErr, ErrNotFound) {
			t.Fatalf("retry after persisted disable error = %v, want %v", retryErr, ErrNotFound)
		}
	case <-ctx.Done():
		t.Fatalf("wait for guarded retry result: %v", ctx.Err())
	}
	if guardedRetryCalls != 1 || transport.resourceCalls != resourceCallsBeforeGuardedRetry+1 {
		t.Fatalf("provider calls across persisted disable = guarded %d, total delta %d; want 1", guardedRetryCalls, transport.resourceCalls-resourceCallsBeforeGuardedRetry)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = true, updated_at = now() WHERE id = $1::uuid`, installed.ID); err != nil {
		t.Fatalf("restore addon after guarded retry: %v", err)
	}
	service.diagnostics.remove(installed.ID)
	successfulGuardedRetryCalls := 0
	transport.resource = func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		successfulGuardedRetryCalls++
		if successfulGuardedRetryCalls == 1 {
			return nil, CachePolicy{}, unavailable("successful guarded retry temporary failure", errors.New("temporary provider failure"), true)
		}
		return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
	}
	service.retryDelay = time.Millisecond
	successfulRetryBatch, retryErr := service.FetchAll(ctx, principal, ResourcePath{Resource: "catalog", Type: "movie", ID: "searchable"})
	if retryErr != nil || len(successfulRetryBatch.Results) != 1 || len(successfulRetryBatch.Errors) != 0 || successfulGuardedRetryCalls != 2 {
		t.Fatalf("unchanged guarded retry = calls %d, batch %+v, error %v", successfulGuardedRetryCalls, successfulRetryBatch, retryErr)
	}
	_, guardedRetryObservations := service.diagnostics.snapshot()
	guardedRetryObservation := guardedRetryObservations[installed.ID]
	if !guardedRetryObservation.latestSucceeded || !guardedRetryObservation.hasSuccess || guardedRetryObservation.lastError != nil {
		t.Fatalf("successful guarded retry recorded an intermediate outcome: %+v", guardedRetryObservation)
	}
	service.retryDelay = 0
	const privateTransportURL = "http://192.168.1.10/manifest.json"
	if _, err := pool.Exec(ctx, `
		UPDATE profile_addons SET transport_url = $2 WHERE id = $1::uuid
	`, installed.ID, privateTransportURL); err != nil {
		t.Fatalf("seed legacy private addon transport: %v", err)
	}
	if !isPrivateNetworkTransportURL(privateTransportURL) {
		t.Fatal("private literal addon transport was not recognized")
	}
	transport.resource = func(ctx context.Context, _ string, _ ResourcePath) (json.RawMessage, CachePolicy, error) {
		if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = false, updated_at = now() WHERE id = $1::uuid`, installed.ID); err != nil {
			return nil, CachePolicy{}, fmt.Errorf("disable addon during provider request: %w", err)
		}
		return json.RawMessage(`{"streams":[]}`), CachePolicy{}, nil
	}
	if _, err := service.FetchPlaybackResource(ctx, principal, installed.ID, ResourcePath{
		Resource: "stream", Type: "movie", ID: "disable-during-provider-io",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("concurrently disabled provider result error = %v, want %v", err, ErrNotFound)
	}
	if _, err := pool.Exec(ctx, `UPDATE profile_addons SET enabled = true, updated_at = now() WHERE id = $1::uuid`, installed.ID); err != nil {
		t.Fatalf("restore enabled addon after concurrent provider request: %v", err)
	}
	transport.resource = func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		return json.RawMessage(`{"streams":[]}`), CachePolicy{}, nil
	}
	priorFailure := service.diagnostics.start(installed.ID)
	service.diagnostics.complete(installed.ID, priorFailure, ErrProviderUnavailable)
	if _, err := service.FetchPlaybackResource(ctx, principal, installed.ID, ResourcePath{
		Resource: "stream", Type: "movie", ID: "private-service-probe",
	}); err != nil {
		t.Fatalf("assigned profile private addon fetch: %v", err)
	}
	if transport.resourceCalls != resourceCallsBeforeGuardedRetry+5 {
		t.Fatalf("assigned profile private addon resource calls = %d, want %d", transport.resourceCalls, resourceCallsBeforeGuardedRetry+5)
	}
	_, diagnosticObservations := service.diagnostics.snapshot()
	directObservation := diagnosticObservations[installed.ID]
	if !directObservation.latestSucceeded || !directObservation.hasSuccess || directObservation.lastError == nil {
		t.Fatalf("direct resource fetch diagnostics = %+v", directObservation)
	}
	if _, err := service.Refresh(ctx, principal, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated manager private addon refresh error = %v, want %v", err, ErrForbidden)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE profile_addons SET transport_url = $2 WHERE id = $1::uuid
	`, installed.ID, changedTransportURL); err != nil {
		t.Fatalf("restore public addon transport: %v", err)
	}
	principal.ActiveProfileID = new(profileBID)
	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = false
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileBID); err != nil {
		t.Fatalf("make addon assignee read-only: %v", err)
	}
	if _, err := service.List(ctx, principal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only addon list error = %v, want %v", err, ErrForbidden)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileBID); err != nil {
		t.Fatalf("restore addon assignee management: %v", err)
	}
	resourceCallsBeforeRevocation := transport.resourceCalls
	if _, err := pool.Exec(ctx, `
		DELETE FROM addon_profile_access
		WHERE addon_id = $1::uuid AND profile_id = $2::uuid
	`, installed.ID, profileBID); err != nil {
		t.Fatalf("revoke addon assignment: %v", err)
	}
	principal.ActiveProfileID = new(profileBID)
	if err := service.ValidatePlaybackAccess(ctx, principal, installed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked playback access validation error = %v, want %v", err, ErrNotFound)
	}
	if _, err := service.FetchPlaybackResource(ctx, principal, installed.ID, ResourcePath{
		Resource: "stream", Type: "movie", ID: "retained-addon-id",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked addon lookup error = %v, want %v", err, ErrNotFound)
	}
	if transport.resourceCalls != resourceCallsBeforeRevocation {
		t.Fatalf("resource transport calls after assignment revocation = %d, want %d", transport.resourceCalls, resourceCallsBeforeRevocation)
	}
	globalPrincipal.ActiveProfileID = new(profileAID)
	disabled = false
	if _, err := service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{
		Enabled: &disabled, ProfileIDs: []string{profileAID},
	}); err != nil {
		t.Fatalf("disable addon before removal: %v", err)
	}
	if err := service.Remove(ctx, globalPrincipal, installed.ID); err != nil {
		t.Fatalf("remove addon after diagnostics: %v", err)
	}
	_, removedObservations := service.diagnostics.snapshot()
	if _, exists := removedObservations[installed.ID]; exists {
		t.Fatalf("removed addon retained diagnostics: %+v", removedObservations[installed.ID])
	}
}

func TestRefreshAuthorizesEveryAssignmentBeforeFetchAndCommit(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run addon refresh authorization tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		userID       = "2b000000-0000-4000-8000-000000000001"
		categoryID   = "2b000000-0000-4000-8000-000000000002"
		profileAID   = "2b000000-0000-4000-8000-000000000003"
		profileBID   = "2b000000-0000-4000-8000-000000000004"
		transportURL = "https://refresh-authorization.example/manifest.json"
		changedURL   = "https://refresh-concurrent.example/manifest.json"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile_addons WHERE transport_url = ANY($1::text[])`, []string{transportURL, changedURL})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileAID, profileBID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(ctx, `
		WITH inserted_category AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Addon refresh authorization', 'addon refresh authorization', 910003)
			RETURNING id
		), inserted_user AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($2::uuid, 'addon-refresh-authorization-user', 'unused-test-hash', 'admin')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES ($3::uuid, $1::uuid, 'Addon refresh profile A'),
			       ($4::uuid, $1::uuid, 'Addon refresh profile B')
			RETURNING id
		)
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($2::uuid, $3::uuid, false),
		       ($2::uuid, $4::uuid, false)
	`, categoryID, userID, profileAID, profileBID); err != nil {
		t.Fatalf("seed addon refresh authorization boundary: %v", err)
	}

	oldRawManifest := json.RawMessage(`{"id":"org.rivune.refresh-authorization","version":"1.0.0","name":"Refresh Authorization","types":["movie"],"resources":["catalog"],"catalogs":[]}`)
	oldManifest := Manifest{
		ID: "org.rivune.refresh-authorization", Version: "1.0.0", Name: "Refresh Authorization",
		Types: []string{"movie"}, Resources: []ManifestResource{{Name: "catalog", Short: true}},
	}
	refreshedRawManifest := json.RawMessage(`{"id":"org.rivune.refresh-authorization","version":"2.0.0","name":"Refresh Authorization","types":["movie"],"resources":["catalog"],"catalogs":[]}`)
	refreshedManifest := oldManifest
	refreshedManifest.Version = "2.0.0"
	transport := &installManifestTransport{manifest: func(context.Context, string) (Manifest, json.RawMessage, error) {
		return oldManifest, oldRawManifest, nil
	}}
	service := NewService(pool, transport, discardLogger())
	expiresAt := time.Now().UTC().Add(time.Hour)
	globalPrincipal := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	installed, err := service.Install(ctx, globalPrincipal, InstallInput{
		TransportURL: transportURL,
		ProfileIDs:   []string{profileAID, profileBID},
	})
	if err != nil {
		t.Fatalf("install shared addon for refresh: %v", err)
	}
	transport.calls = 0
	category := categoryID
	viewer := auth.Principal{
		UserID: userID, Role: "member", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &category, ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	manager := viewer
	manager.Role = "admin"

	if _, err := service.Refresh(ctx, viewer, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer refresh error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("manifest fetches after viewer denial = %d, want 0", transport.calls)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileAID); err != nil {
		t.Fatalf("grant partial addon management: %v", err)
	}
	if _, err := service.Refresh(ctx, manager, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("partial manager refresh error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != 0 {
		t.Fatalf("manifest fetches after partial manager denial = %d, want 0", transport.calls)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileBID); err != nil {
		t.Fatalf("grant complete addon management: %v", err)
	}
	transport.manifest = func(context.Context, string) (Manifest, json.RawMessage, error) {
		return refreshedManifest, refreshedRawManifest, nil
	}
	refreshPriorFailure := service.diagnostics.start(installed.ID)
	service.diagnostics.complete(installed.ID, refreshPriorFailure, ErrInvalidResponse)
	refreshed, err := service.Refresh(ctx, manager, installed.ID)
	if err != nil {
		t.Fatalf("complete manager refresh: %v", err)
	}
	if refreshed.parsedManifest.Version != refreshedManifest.Version || transport.calls != 1 {
		t.Fatalf("complete manager refresh = version %q with %d fetches", refreshed.parsedManifest.Version, transport.calls)
	}
	_, refreshObservations := service.diagnostics.snapshot()
	refreshObservation := refreshObservations[installed.ID]
	if !refreshObservation.latestSucceeded || refreshObservation.lastError == nil || refreshObservation.lastError.Code != DiagnosticErrorInvalidResponse {
		t.Fatalf("manifest refresh diagnostics = %+v", refreshObservation)
	}

	resetManifest := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			UPDATE profile_addons
			SET transport_url = $2, manifest = $3::jsonb, manifest_id = $4,
			    manifest_version = $5, updated_at = now()
			WHERE id = $1::uuid
		`, installed.ID, transportURL, oldRawManifest, oldManifest.ID, oldManifest.Version); err != nil {
			t.Fatalf("reset addon manifest: %v", err)
		}
	}
	assertOldManifest := func(label string) {
		t.Helper()
		var version string
		if err := pool.QueryRow(ctx, `
			SELECT manifest_version FROM profile_addons WHERE id = $1::uuid
		`, installed.ID).Scan(&version); err != nil {
			t.Fatalf("%s: query persisted manifest: %v", label, err)
		}
		if version != oldManifest.Version {
			t.Fatalf("%s: persisted manifest version = %q, want %q", label, version, oldManifest.Version)
		}
	}

	resetManifest()
	transport.manifest = func(fetchCtx context.Context, _ string) (Manifest, json.RawMessage, error) {
		if _, err := pool.Exec(fetchCtx, `
			DELETE FROM addon_profile_access
			WHERE addon_id = $1::uuid AND profile_id = $2::uuid
		`, installed.ID, profileBID); err != nil {
			return Manifest{}, nil, fmt.Errorf("change addon assignments during refresh: %w", err)
		}
		return refreshedManifest, refreshedRawManifest, nil
	}
	if _, err := service.Refresh(ctx, manager, installed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refresh after concurrent assignment change error = %v, want %v", err, ErrNotFound)
	}
	assertOldManifest("concurrent assignment change")
	if _, err := pool.Exec(ctx, `
		INSERT INTO addon_profile_access (addon_id, profile_id, position)
		VALUES ($1::uuid, $2::uuid, 1)
	`, installed.ID, profileBID); err != nil {
		t.Fatalf("restore addon assignment: %v", err)
	}

	resetManifest()
	transport.manifest = func(fetchCtx context.Context, _ string) (Manifest, json.RawMessage, error) {
		if _, err := pool.Exec(fetchCtx, `
			UPDATE profile_addons SET transport_url = $2 WHERE id = $1::uuid
		`, installed.ID, changedURL); err != nil {
			return Manifest{}, nil, fmt.Errorf("change addon transport during refresh: %w", err)
		}
		return refreshedManifest, refreshedRawManifest, nil
	}
	if _, err := service.Refresh(ctx, manager, installed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refresh after concurrent transport change error = %v, want %v", err, ErrNotFound)
	}
	assertOldManifest("concurrent transport change")

	resetManifest()
	transport.manifest = func(fetchCtx context.Context, _ string) (Manifest, json.RawMessage, error) {
		if _, err := pool.Exec(fetchCtx, `
			UPDATE user_profile_access SET can_manage = false
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
		`, userID, profileBID); err != nil {
			return Manifest{}, nil, fmt.Errorf("revoke addon management during refresh: %w", err)
		}
		return refreshedManifest, refreshedRawManifest, nil
	}
	if _, err := service.Refresh(ctx, manager, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("refresh after concurrent permission change error = %v, want %v", err, ErrForbidden)
	}
	assertOldManifest("concurrent permission change")
}

func TestCatalogDescriptorsIncludeAddonNamesAndPreserveServiceOrder(t *testing.T) {
	regularCatalog := ManifestCatalog{Type: "movie", ID: "featured", Name: "Featured", ExtraSupported: []string{"search"}}
	addonCatalog := ManifestCatalog{Type: "all", ID: "community", Name: "Community"}
	secondCatalog := ManifestCatalog{Type: "series", ID: "recent", Name: "Recent"}
	addons := []InstalledAddon{
		{
			ID:       "11111111-1111-4111-8111-111111111111",
			Position: 0,
			parsedManifest: Manifest{
				ID: "org.example.first", Name: "First Add-on", Logo: "https://provider.invalid/first-logo.png",
				Catalogs: []ManifestCatalog{regularCatalog}, AddonCatalogs: []ManifestCatalog{addonCatalog},
			},
		},
		{
			ID:       "22222222-2222-4222-8222-222222222222",
			Position: 1,
			parsedManifest: Manifest{
				ID: "org.example.second", Name: "Second Add-on", Logo: "https://provider.invalid/second-logo.png", Catalogs: []ManifestCatalog{secondCatalog},
			},
		},
	}

	got := catalogDescriptors(addons)
	want := []CatalogDescriptor{
		{
			AddonID: "11111111-1111-4111-8111-111111111111", AddonName: "First Add-on", AddonLogoURL: "https://provider.invalid/first-logo.png", ManifestID: "org.example.first",
			Position: 0, Catalog: regularCatalog, Searchable: true,
		},
		{
			AddonID: "11111111-1111-4111-8111-111111111111", AddonName: "First Add-on", AddonLogoURL: "https://provider.invalid/first-logo.png", ManifestID: "org.example.first",
			Position: 0, Catalog: addonCatalog, AddonCatalog: true,
		},
		{
			AddonID: "22222222-2222-4222-8222-222222222222", AddonName: "Second Add-on", AddonLogoURL: "https://provider.invalid/second-logo.png", ManifestID: "org.example.second",
			Position: 1, Catalog: secondCatalog,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog descriptors = %#v, want %#v", got, want)
	}
}

func TestPlanCatalogSearchAdaptsEachTVCatalog(t *testing.T) {
	manifest := Manifest{
		ID:        "org.example.tv",
		Types:     []string{"movie", "tv"},
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
		Catalogs: []ManifestCatalog{
			{
				Type: "tv",
				ID:   "modern",
				Extra: []ExtraProp{
					{Name: "search", IsRequired: true},
					{Name: "skip", IsRequired: true, Default: "0"},
					{Name: "limit"},
					{Name: "country", IsRequired: true, Default: "US"},
				},
			},
			{
				Type:           "tv",
				ID:             "legacy",
				ExtraRequired:  []string{"search"},
				ExtraSupported: []string{"search", "limit"},
			},
			{Type: "tv", ID: "broken", ExtraSupported: []string{"search", "skip"}},
			{Type: "tv", ID: "not-searchable", Extra: []ExtraProp{{Name: "skip"}}},
			{
				Type: "tv",
				ID:   "missing-required",
				Extra: []ExtraProp{
					{Name: "search"},
					{Name: "token", IsRequired: true},
				},
			},
		},
	}
	partialTVManifest := Manifest{
		ID:        "org.example.partial-tv",
		Types:     []string{"movie"},
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
		Catalogs:  []ManifestCatalog{{Type: "tv", ID: "partial", ExtraSupported: []string{"search", "skip"}}},
	}
	addons := []InstalledAddon{
		{ID: "addon-tv", transportURL: "https://tv.example/manifest.json", parsedManifest: manifest},
		{ID: "addon-partial", transportURL: "https://partial.example/manifest.json", parsedManifest: partialTVManifest},
	}

	firstPage, err := planCatalogSearch(addons, "tv", CatalogSearchInput{Search: "news", Limit: 24})
	if err != nil {
		t.Fatalf("plan first-page catalogs: %v", err)
	}
	if got, want := requestIDs(firstPage), []string{"modern", "legacy", "broken"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first-page catalog order = %#v, want %#v", got, want)
	}
	if got, want := firstPage[0].path.Extra, []ExtraValue{
		{Name: "search", Value: "news"},
		{Name: "limit", Value: "24"},
		{Name: "country", Value: "US"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("modern extras = %#v, want %#v", got, want)
	}
	if got, want := firstPage[1].path.Extra, []ExtraValue{
		{Name: "search", Value: "news"},
		{Name: "limit", Value: "24"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy extras = %#v, want %#v", got, want)
	}

	nextPage, err := planCatalogSearch(addons, "tv", CatalogSearchInput{Search: "news", Skip: 24, Limit: 24})
	if err != nil {
		t.Fatalf("plan next-page catalogs: %v", err)
	}
	if got, want := requestIDs(nextPage), []string{"modern", "broken"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("next-page catalog order = %#v, want %#v", got, want)
	}
	if got := nextPage[0].path.Extra[1]; got != (ExtraValue{Name: "skip", Value: "24"}) {
		t.Fatalf("next-page skip = %#v", got)
	}
}

func TestCatalogSearchDeduplicatesRequestsAndKeepsPartialResults(t *testing.T) {
	manifest := Manifest{
		ID:        "org.example.tv",
		Types:     []string{"tv"},
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
		Catalogs: []ManifestCatalog{
			{Type: "tv", ID: "working", ExtraSupported: []string{"search", "skip", "limit"}},
			{Type: "tv", ID: "broken", ExtraSupported: []string{"search", "skip", "limit"}},
		},
	}
	addons := []InstalledAddon{
		{ID: "first", transportURL: "https://same.example/manifest.json", parsedManifest: manifest},
		{ID: "duplicate", transportURL: "https://same.example/manifest.json", parsedManifest: manifest},
	}
	requests, err := planCatalogSearch(addons, "tv", CatalogSearchInput{Search: "sports", Skip: 0, Limit: 24})
	if err != nil {
		t.Fatalf("plan catalogs: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("planned %d requests, want 2 unique requests", len(requests))
	}

	transport := &catalogSearchTransport{}
	service := Service{transport: transport, logger: discardLogger()}
	batch, err := service.execute(context.Background(), requests)
	if err != nil {
		t.Fatalf("execute catalogs: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].ID != "working" {
		t.Fatalf("unexpected successful results: %#v", batch.Results)
	}
	if len(batch.Errors) != 1 || batch.Errors[0].AddonID != "first" || batch.Errors[0].Code != "addon_unavailable" {
		t.Fatalf("unexpected isolated failures: %#v", batch.Errors)
	}
	if got, want := batch.Results[0].Extra, requests[0].path.Extra; !reflect.DeepEqual(got, want) {
		t.Fatalf("result extras = %#v, want sent extras %#v", got, want)
	}
	transport.mu.Lock()
	callCount := len(transport.paths)
	transport.mu.Unlock()
	if callCount != 2 {
		t.Fatalf("transport received %d calls, want 2", callCount)
	}
}
func TestPlanCatalogSearchRequiresDeclaredTypeAndCatalogResource(t *testing.T) {
	searchable := ManifestCatalog{Type: "movie", ID: "search", ExtraSupported: []string{"search"}}
	wrongResourceTypes := []string{"series"}
	addons := []InstalledAddon{
		{
			ID: "missing-type",
			parsedManifest: Manifest{
				Types:     []string{"series"},
				Resources: []ManifestResource{{Name: "catalog", Short: true}},
				Catalogs:  []ManifestCatalog{searchable},
			},
		},
		{
			ID: "missing-resource",
			parsedManifest: Manifest{
				Types:     []string{"movie"},
				Resources: []ManifestResource{{Name: "meta", Short: true}},
				Catalogs:  []ManifestCatalog{searchable},
			},
		},
		{
			ID: "resource-excludes-type",
			parsedManifest: Manifest{
				Types:     []string{"movie"},
				Resources: []ManifestResource{{Name: "catalog", Types: &wrongResourceTypes}},
				Catalogs:  []ManifestCatalog{searchable},
			},
		},
	}

	requests, err := planCatalogSearch(addons, "movie", CatalogSearchInput{Search: "query", Limit: 24})
	if err != nil {
		t.Fatalf("plan undeclared capabilities: %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("planned requests for undeclared capabilities: %#v", requests)
	}
}

func TestExecuteRetriesOnlyTemporaryFailures(t *testing.T) {
	tests := []struct {
		name       string
		firstErr   error
		wantCalls  int
		wantResult bool
	}{
		{
			name:       "temporary network failure",
			firstErr:   unavailable("request failed", errors.New("connection reset"), true),
			wantCalls:  2,
			wantResult: true,
		},
		{
			name:      "permanent HTTP failure",
			firstErr:  unavailable("request failed", errorsText("HTTP 404"), false),
			wantCalls: 1,
		},
		{
			name:      "invalid response",
			firstErr:  fmt.Errorf("%w: catalog response requires \"metas\" array", ErrInvalidResponse),
			wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			transport := functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
				calls++
				if calls == 1 {
					return nil, CachePolicy{}, test.firstErr
				}
				return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
			}}
			service := Service{
				transport:      transport,
				logger:         discardLogger(),
				requestTimeout: time.Second,
				retryDelay:     time.Millisecond,
			}

			batch, err := service.execute(context.Background(), []plannedRequest{executeTestRequest("search")})
			if err != nil {
				t.Fatalf("execute request: %v", err)
			}
			if calls != test.wantCalls {
				t.Fatalf("Resource calls = %d, want %d", calls, test.wantCalls)
			}
			if got := len(batch.Results) == 1; got != test.wantResult {
				t.Fatalf("successful result = %v, want %v; batch = %#v", got, test.wantResult, batch)
			}
		})
	}
}

func TestExecuteUsesIndependentRequestTimeouts(t *testing.T) {
	var mu sync.Mutex
	deadlines := 0
	transport := functionTransport{resource: func(ctx context.Context, _ string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
		if _, ok := ctx.Deadline(); ok {
			mu.Lock()
			deadlines++
			mu.Unlock()
		}
		if path.ID == "slow" {
			<-ctx.Done()
			return nil, CachePolicy{}, ctx.Err()
		}
		return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
	}}
	service := Service{
		transport:      transport,
		logger:         discardLogger(),
		requestTimeout: 20 * time.Millisecond,
		retryDelay:     time.Millisecond,
	}

	batch, err := service.execute(context.Background(), []plannedRequest{
		executeTestRequest("slow"),
		executeTestRequest("fast"),
	})
	if err != nil {
		t.Fatalf("execute timeout requests: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].ID != "fast" {
		t.Fatalf("fast result did not survive independent timeout: %#v", batch.Results)
	}
	if len(batch.Errors) != 1 || batch.Errors[0].Code != "addon_request_timeout" {
		t.Fatalf("unexpected timeout failure: %#v", batch.Errors)
	}
	mu.Lock()
	defer mu.Unlock()
	if deadlines != 2 {
		t.Fatalf("requests with a deadline = %d, want 2", deadlines)
	}
}

func TestExecuteDoesNotExposeTransportCredentialsInResultsOrLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	secretPayload := json.RawMessage(`{"secretPayload":"must-not-be-logged"}`)
	networkCause := errors.New("connection refused")
	secretURL := "https://addon.example/private/catalog/movie/featured.json?token=network-secret"
	cause := unavailable("request failed", &url.Error{Op: "Get", URL: secretURL, Err: networkCause}, true)
	if !errors.Is(cause, networkCause) {
		t.Fatalf("sanitized provider error lost network cause: %v", cause)
	}
	if strings.Contains(cause.Error(), secretURL) || strings.Contains(cause.Error(), "network-secret") {
		t.Fatalf("provider error exposed request URL: %v", cause)
	}
	transport := functionTransport{resource: func(_ context.Context, _ string, path ResourcePath) (json.RawMessage, CachePolicy, error) {
		if path.ID == "healthy" {
			return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
		}
		return secretPayload, CachePolicy{}, cause
	}}
	service := Service{transport: transport, logger: logger, requestTimeout: time.Second, retryDelay: time.Millisecond}
	request := executeTestRequest("featured")
	request.addon.transportURL = "https://addon.example/private/manifest.json?token=must-not-leak"

	batch, err := service.execute(context.Background(), []plannedRequest{request, executeTestRequest("healthy")})
	if err != nil {
		t.Fatalf("execute logged requests: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].ID != "healthy" || len(batch.Errors) != 1 {
		t.Fatalf("unexpected batch: %#v", batch)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("decode log: %v; log = %q", err, logs.String())
	}
	want := map[string]string{
		"addonId":    request.addon.ID,
		"manifestId": request.addon.parsedManifest.ID,
		"resource":   request.path.Resource,
		"type":       request.path.Type,
		"resourceId": request.path.ID,
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("log %s = %#v, want %q", key, record[key], value)
		}
	}
	if loggedCause, _ := record["error"].(string); loggedCause != "addon provider unavailable: request failed" {
		t.Fatalf("sanitized log cause = %#v", record["error"])
	}
	serializedBatch, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal resource batch: %v", err)
	}
	for _, serialized := range []string{logs.String(), string(serializedBatch)} {
		if strings.Contains(serialized, "must-not-be-logged") || strings.Contains(serialized, "must-not-leak") || strings.Contains(serialized, "network-secret") || strings.Contains(serialized, "transportUrl") {
			t.Fatalf("resource output leaked provider data: %s", serialized)
		}
	}
}

func TestPlanAndExecuteBoundFanout(t *testing.T) {
	addons := make([]InstalledAddon, maxPlannedRequests+1)
	for index := range addons {
		catalogID := fmt.Sprintf("catalog-%d", index)
		addons[index] = InstalledAddon{
			ID:           fmt.Sprintf("addon-%d", index),
			transportURL: fmt.Sprintf("https://addon-%d.example/manifest.json", index),
			parsedManifest: Manifest{
				Types:     []string{"movie"},
				Resources: []ManifestResource{{Name: "catalog", Short: true}},
				Catalogs: []ManifestCatalog{{
					Type: "movie", ID: catalogID, ExtraSupported: []string{"search"},
				}},
			},
		}
	}
	if requests, err := planCatalogSearch(addons, "movie", CatalogSearchInput{Search: "bounded", Limit: 24}); !errors.Is(err, ErrInvalidInput) || requests != nil {
		t.Fatalf("oversized plan = (%d requests, %v), want deterministic rejection", len(requests), err)
	}

	transportCalls := 0
	rejectingService := Service{transport: functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		transportCalls++
		return json.RawMessage(`{}`), CachePolicy{}, nil
	}}}
	oversized := make([]plannedRequest, maxPlannedRequests+1)
	if _, err := rejectingService.execute(context.Background(), oversized); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized execute error = %v, want %v", err, ErrInvalidInput)
	}
	if transportCalls != 0 {
		t.Fatalf("transport calls for rejected execute = %d, want 0", transportCalls)
	}

	started := make(chan struct{}, maxConcurrentRequests+1)
	release := make(chan struct{})
	boundedService := Service{
		logger: discardLogger(),
		transport: functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return json.RawMessage(`{}`), CachePolicy{}, nil
		}},
	}
	requests := make([]plannedRequest, maxPlannedRequests)
	for index := range requests {
		requests[index] = executeTestRequest(fmt.Sprintf("%d", index))
	}
	type executionResult struct {
		batch ResourceBatch
		err   error
	}
	completed := make(chan executionResult, 1)
	go func() {
		batch, err := boundedService.execute(context.Background(), requests)
		completed <- executionResult{batch: batch, err: err}
	}()
	for range maxConcurrentRequests {
		<-started
	}
	select {
	case <-started:
		t.Fatalf("more than %d resource workers started concurrently", maxConcurrentRequests)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	result := <-completed
	if result.err != nil || len(result.batch.Results) != len(requests) {
		t.Fatalf("bounded execution = (%d results, %v), want %d results", len(result.batch.Results), result.err, len(requests))
	}
}

func TestExecuteRejectsAggregateResponseOverflow(t *testing.T) {
	payload := json.RawMessage(bytes.Repeat([]byte{'x'}, maxAggregateResponseBytes/2+1))
	service := Service{
		logger: discardLogger(),
		transport: functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
			return payload, CachePolicy{}, nil
		}},
	}
	batch, err := service.execute(context.Background(), []plannedRequest{
		executeTestRequest("first"), executeTestRequest("second"),
	})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("aggregate overflow error = %v, want %v", err, ErrInvalidResponse)
	}
	if len(batch.Results) != 0 || len(batch.Errors) != 0 {
		t.Fatalf("aggregate overflow returned partial data: %#v", batch)
	}
}

func TestAggregateResourceBudgetAcceptsExactLimitAndRejectsNextByte(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		size := int64(maximumResourceBytes)
		if strings.Contains(request.URL.Path, "plus-one") {
			size++
		}
		writeChunkedResponse(writer, request, size)
	}))
	t.Cleanup(server.Close)

	var downloaded atomic.Int64
	client := &http.Client{Transport: countingRoundTripper{base: server.Client().Transport, bytes: &downloaded}}
	transport := NewHTTPTransport(client)

	exactCtx, cancelExact := context.WithCancel(context.Background())
	exactBudget := newAggregateResourceBudget(maxAggregateResponseBytes, cancelExact)
	first, _, firstErr := transport.getWithBudget(exactCtx, server.URL+"/exact-first", maximumResourceBytes, exactBudget)
	second, _, secondErr := transport.getWithBudget(exactCtx, server.URL+"/exact-second", maximumResourceBytes, exactBudget)
	cancelExact()
	if firstErr != nil || secondErr != nil {
		t.Fatalf("exact aggregate reads failed: first=%v second=%v", firstErr, secondErr)
	}
	if len(first)+len(second) != maxAggregateResponseBytes {
		t.Fatalf("exact aggregate bytes = %d, want %d", len(first)+len(second), maxAggregateResponseBytes)
	}
	if exactBudget.wasExceeded() {
		t.Fatal("exact aggregate limit was marked exceeded")
	}

	downloaded.Store(0)
	overflowCtx, cancelOverflow := context.WithCancel(context.Background())
	defer cancelOverflow()
	overflowBudget := newAggregateResourceBudget(maxAggregateResponseBytes, cancelOverflow)
	if _, _, err := transport.getWithBudget(overflowCtx, server.URL+"/overflow-first", maximumResourceBytes, overflowBudget); err != nil {
		t.Fatalf("overflow setup read failed: %v", err)
	}
	_, _, err := transport.getWithBudget(overflowCtx, server.URL+"/plus-one", maximumResourceBytes, overflowBudget)
	if !errors.Is(err, ErrInvalidResponse) || err.Error() != aggregateResourceLimitError().Error() {
		t.Fatalf("aggregate N+1 error = %v, want %q", err, aggregateResourceLimitError())
	}
	if got := downloaded.Load(); got != int64(maxAggregateResponseBytes)+1 {
		t.Fatalf("aggregate N+1 downloaded bytes = %d, want %d", got, int64(maxAggregateResponseBytes)+1)
	}
}

func TestExecuteCancelsChunkedFanoutAtAggregateLimit(t *testing.T) {
	var started atomic.Int64
	var active atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started.Add(1)
		active.Add(1)
		defer active.Add(-1)
		writeChunkedResponse(writer, request, maximumResourceBytes+1)
	}))
	t.Cleanup(server.Close)

	var downloaded atomic.Int64
	client := &http.Client{Transport: countingRoundTripper{base: server.Client().Transport, bytes: &downloaded}}
	service := Service{
		logger:         discardLogger(),
		transport:      NewHTTPTransport(client),
		requestTimeout: 30 * time.Second,
	}
	requests := make([]plannedRequest, maxPlannedRequests)
	for index := range requests {
		requests[index] = executeTestRequest(strconv.Itoa(index))
		requests[index].addon.transportURL = server.URL + "/manifest.json"
	}

	batch, err := service.execute(context.Background(), requests)
	if !errors.Is(err, ErrInvalidResponse) || err.Error() != aggregateResourceLimitError().Error() {
		t.Fatalf("chunked fanout error = %v, want %q", err, aggregateResourceLimitError())
	}
	if len(batch.Results) != 0 || len(batch.Errors) != 0 {
		t.Fatalf("chunked aggregate overflow returned partial data: %#v", batch)
	}
	if got := downloaded.Load(); got > int64(maxAggregateResponseBytes+maxConcurrentRequests) {
		t.Fatalf("chunked fanout downloaded %d bytes, limit with worker sentinels is %d", got, maxAggregateResponseBytes+maxConcurrentRequests)
	}
	if got := started.Load(); got >= int64(maxPlannedRequests) {
		t.Fatalf("aggregate overflow drained all %d planned requests", got)
	}

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for active.Load() != 0 {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("%d chunked handlers remained active after aggregate cancellation", active.Load())
		}
	}
}

func TestSameInstalledAddonDetectsReturnedRevisionChanges(t *testing.T) {
	now := time.Now().UTC()
	expected := InstalledAddon{
		ID:           "00000000-0000-4000-8000-000000000001",
		transportURL: "https://example.com/manifest.json",
		Manifest:     json.RawMessage(`{"id":"example","version":"1.0.0"}`),
		Position:     2,
		ProfileIDs:   []string{"00000000-0000-4000-8000-000000000010"},
		CategoryIDs:  []string{"00000000-0000-4000-8000-000000000020"},
		InstalledAt:  now.Add(-time.Hour),
		UpdatedAt:    now,
	}
	current := expected
	current.Manifest = append(json.RawMessage(nil), expected.Manifest...)
	current.ProfileIDs = append([]string(nil), expected.ProfileIDs...)
	current.CategoryIDs = append([]string(nil), expected.CategoryIDs...)
	if !sameInstalledAddon(current, expected) {
		t.Fatal("identical addon revisions did not match")
	}

	current.Position++
	if sameInstalledAddon(current, expected) {
		t.Fatal("position change was not detected")
	}
	current = expected
	current.Manifest = json.RawMessage(`{"id":"example","version":"2.0.0"}`)
	if sameInstalledAddon(current, expected) {
		t.Fatal("manifest change was not detected")
	}
	current = expected
	current.Enabled = !expected.Enabled
	if sameInstalledAddon(current, expected) {
		t.Fatal("availability change was not detected")
	}
	current = expected
	current.ProfileIDs = append(append([]string(nil), expected.ProfileIDs...), "00000000-0000-4000-8000-000000000011")
	if sameInstalledAddon(current, expected) {
		t.Fatal("profile assignment change was not detected")
	}
	current = expected
	current.CategoryIDs = append(append([]string(nil), expected.CategoryIDs...), "00000000-0000-4000-8000-000000000021")
	if sameInstalledAddon(current, expected) {
		t.Fatal("category assignment change was not detected")
	}
}

func requestIDs(requests []plannedRequest) []string {
	ids := make([]string, len(requests))
	for index, request := range requests {
		ids[index] = request.path.ID
	}
	return ids
}

func TestReorderRequiresManagementAndSerializesGrantRevocation(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run addon reorder authorization tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		userID           = "2d000000-0000-4000-8000-000000000001"
		categoryID       = "2d000000-0000-4000-8000-000000000002"
		profileID        = "2d000000-0000-4000-8000-000000000003"
		addonAID         = "2d000000-0000-4000-8000-000000000004"
		addonBID         = "2d000000-0000-4000-8000-000000000005"
		foreignProfileID = "2d000000-0000-4000-8000-000000000006"
		foreignAddonID   = "2d000000-0000-4000-8000-000000000007"
		addonAManifest   = `{"id":"org.rivune.reorder-a","version":"1.0.0","name":"Reorder A","types":["movie"],"resources":["stream"],"catalogs":[]}`
		addonBManifest   = `{"id":"org.rivune.reorder-b","version":"1.0.0","name":"Reorder B","types":["movie"],"resources":["stream"],"catalogs":[]}`
		foreignManifest  = `{"id":"org.rivune.reorder-foreign","version":"1.0.0","name":"Reorder Foreign","types":["movie"],"resources":["stream"],"catalogs":[]}`
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM profile_addons WHERE id = ANY($1::uuid[])
		`, []string{addonAID, addonBID, foreignAddonID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileID, foreignProfileID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(ctx, `
		WITH inserted_category AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Addon reorder authorization', 'addon reorder authorization', 920001)
			RETURNING id
		), inserted_user AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($2::uuid, 'addon-reorder-authorization-user', 'unused-test-hash', 'member')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES
				($3::uuid, $1::uuid, 'Addon reorder profile'),
				($6::uuid, $1::uuid, 'Foreign addon reorder profile')
			RETURNING id
		), inserted_access AS (
			INSERT INTO user_profile_access (user_id, profile_id, can_manage)
			VALUES
				($2::uuid, $3::uuid, false),
				($2::uuid, $6::uuid, false)
			RETURNING profile_id
		), inserted_addons AS (
			INSERT INTO profile_addons (
				id, profile_id, transport_url, manifest, manifest_id, manifest_version, position
			) VALUES
				($4::uuid, $3::uuid, 'https://reorder-a.example/manifest.json', $8::jsonb, 'org.rivune.reorder-a', '1.0.0', 0),
				($5::uuid, $3::uuid, 'https://reorder-b.example/manifest.json', $9::jsonb, 'org.rivune.reorder-b', '1.0.0', 1),
				($7::uuid, $6::uuid, 'https://reorder-foreign.example/manifest.json', $10::jsonb, 'org.rivune.reorder-foreign', '1.0.0', 0)
			RETURNING id
		)
		INSERT INTO addon_profile_access (addon_id, profile_id, position)
		VALUES
			($4::uuid, $3::uuid, 0),
			($5::uuid, $3::uuid, 1),
			($7::uuid, $6::uuid, 0)
	`, categoryID, userID, profileID, addonAID, addonBID, foreignProfileID, foreignAddonID, addonAManifest, addonBManifest, foreignManifest); err != nil {
		t.Fatalf("seed addon reorder authorization boundary: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	category := categoryID
	activeProfile := profileID
	principal := auth.Principal{
		UserID: userID, Role: "member",
		AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &category,
		ActiveProfileID: &activeProfile, ProfileGrantExpiresAt: &expiresAt,
	}
	service := NewService(pool, nil, discardLogger())
	reversed := ReorderInput{AddonIDs: []string{addonBID, addonAID}}
	if _, err := service.Reorder(ctx, principal, reversed); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer reorder error = %v, want %v", err, ErrForbidden)
	}
	assertPositions := func(wantA, wantB int) {
		t.Helper()
		var positionA, positionB int
		if err := pool.QueryRow(ctx, `
			SELECT
				max(COALESCE(profile_order.position, access.position)) FILTER (WHERE access.addon_id = $2::uuid),
				max(COALESCE(profile_order.position, access.position)) FILTER (WHERE access.addon_id = $3::uuid)
			FROM addon_profile_access access
			LEFT JOIN addon_profile_order profile_order
			  ON profile_order.addon_id = access.addon_id AND profile_order.profile_id = access.profile_id
			WHERE access.profile_id = $1::uuid
		`, profileID, addonAID, addonBID).Scan(&positionA, &positionB); err != nil {
			t.Fatalf("query addon reorder positions: %v", err)
		}
		if positionA != wantA || positionB != wantB {
			t.Fatalf("addon positions = A:%d B:%d, want A:%d B:%d", positionA, positionB, wantA, wantB)
		}
	}
	assertPositions(0, 1)

	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID); err != nil {
		t.Fatalf("grant addon reorder management: %v", err)
	}
	if _, err := service.Reorder(ctx, principal, ReorderInput{AddonIDs: []string{addonBID, foreignAddonID}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("mixed addon reorder error = %v, want %v", err, ErrInvalidInput)
	}
	assertPositions(0, 1)
	reordered, err := service.Reorder(ctx, principal, reversed)
	if err != nil {
		t.Fatalf("manager addon reorder: %v", err)
	}
	if len(reordered) != 2 || reordered[0].ID != addonBID || reordered[1].ID != addonAID {
		t.Fatalf("manager addon reorder result = %+v", reordered)
	}
	assertPositions(1, 0)

	if _, err := pool.Exec(ctx, `
		WITH changed_grant AS (
			UPDATE user_profile_access SET can_manage = false
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
			RETURNING profile_id
		)
		UPDATE addon_profile_order SET position = CASE addon_id
			WHEN $3::uuid THEN 0
			WHEN $4::uuid THEN 1
			END
		WHERE profile_id = (SELECT profile_id FROM changed_grant)
	`, userID, profileID, addonAID, addonBID); err != nil {
		t.Fatalf("prepare global addon reorder: %v", err)
	}
	globalPrincipal := principal
	globalPrincipal.Role = "admin"
	globalPrincipal.AuthorizationScope = auth.AuthorizationScopeGlobalAdministrator
	if _, err := service.Reorder(ctx, globalPrincipal, reversed); err != nil {
		t.Fatalf("global administrator addon reorder: %v", err)
	}
	assertPositions(1, 0)

	if _, err := pool.Exec(ctx, `
		WITH changed_grant AS (
			UPDATE user_profile_access SET can_manage = true
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
			RETURNING profile_id
		)
		UPDATE addon_profile_order SET position = CASE addon_id
			WHEN $3::uuid THEN 0
			WHEN $4::uuid THEN 1
			END
		WHERE profile_id = (SELECT profile_id FROM changed_grant)
	`, userID, profileID, addonAID, addonBID); err != nil {
		t.Fatalf("prepare concurrent addon reorder: %v", err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin addon reorder blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `
		SELECT addon_id
		FROM addon_profile_access
		WHERE profile_id = $1::uuid
		ORDER BY addon_id
		FOR UPDATE
	`, profileID); err != nil {
		t.Fatalf("lock addon assignments: %v", err)
	}
	reorderDone := make(chan error, 1)
	go func() {
		_, reorderErr := service.Reorder(ctx, principal, reversed)
		reorderDone <- reorderErr
	}()
	waitForBlockedQuery := func(fragment string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var blocked bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM pg_stat_activity
					WHERE pid <> pg_backend_pid()
					  AND wait_event_type = 'Lock'
					  AND query LIKE '%' || $1 || '%'
				)
			`, fragment).Scan(&blocked); err != nil {
				t.Fatalf("inspect blocked addon reorder query: %v", err)
			}
			if blocked {
				return
			}
			select {
			case reorderErr := <-reorderDone:
				t.Fatalf("addon reorder returned before assignment lock release: %v", reorderErr)
			default:
			}
			if time.Now().After(deadline) {
				rows, queryErr := pool.Query(ctx, `
					SELECT query FROM pg_stat_activity
					WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock'
				`)
				if queryErr != nil {
					t.Fatalf("query containing %q did not block; inspect locks: %v", fragment, queryErr)
				}
				var waiting []string
				for rows.Next() {
					var query string
					if scanErr := rows.Scan(&query); scanErr != nil {
						rows.Close()
						t.Fatalf("query containing %q did not block; scan locks: %v", fragment, scanErr)
					}
					waiting = append(waiting, query)
				}
				rows.Close()
				t.Fatalf("query containing %q did not block; waiting queries: %q", fragment, waiting)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForBlockedQuery("SELECT addon_id::text FROM addon_profile_access")
	revokeDone := make(chan error, 1)
	go func() {
		_, revokeErr := pool.Exec(ctx, `
			UPDATE user_profile_access SET can_manage = false
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
		`, userID, profileID)
		revokeDone <- revokeErr
	}()
	waitForBlockedQuery("UPDATE user_profile_access SET can_manage = false")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release addon assignment lock: %v", err)
	}
	if err := <-reorderDone; err != nil {
		t.Fatalf("addon reorder lost to concurrent revocation: %v", err)
	}
	if err := <-revokeDone; err != nil {
		t.Fatalf("concurrent addon management revocation: %v", err)
	}
	assertPositions(1, 0)
	var canManage bool
	if err := pool.QueryRow(ctx, `
		SELECT can_manage FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID).Scan(&canManage); err != nil {
		t.Fatalf("query revoked addon management grant: %v", err)
	}
	if canManage {
		t.Fatal("concurrent addon management revocation did not commit")
	}
}

func TestCategoryAssignmentsAreDurableEffectiveAndSeparate(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run addon category assignment tests")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	const (
		userID          = "2e000000-0000-4000-8000-000000000001"
		categoryAID     = "2e000000-0000-4000-8000-000000000002"
		categoryBID     = "2e000000-0000-4000-8000-000000000003"
		profileAID      = "2e000000-0000-4000-8000-000000000004"
		profileBID      = "2e000000-0000-4000-8000-000000000005"
		futureID        = "2e000000-0000-4000-8000-000000000006"
		transportURL    = "https://category-assignment.example/manifest.json"
		explicitAddonID = "2e000000-0000-4000-8000-000000000007"
	)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM profile_addons
			WHERE id = $1::uuid OR profile_id = ANY($2::uuid[]) OR transport_url = $3
		`, explicitAddonID, []string{profileAID, profileBID, futureID}, transportURL)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profiles WHERE id = ANY($1::uuid[])`, []string{profileAID, profileBID, futureID})
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM access_categories WHERE id = ANY($1::uuid[])`, []string{categoryAID, categoryBID})
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := pool.Exec(ctx, `
		WITH inserted_categories AS (
			INSERT INTO access_categories (id, name, normalized_name, position)
			VALUES ($1::uuid, 'Addon category assignment A', 'addon category assignment a', 930001),
			       ($2::uuid, 'Addon category assignment B', 'addon category assignment b', 930002)
			RETURNING id
		), inserted_user AS (
			INSERT INTO users (id, username, password_hash, role)
			VALUES ($3::uuid, 'addon-category-assignment-user', 'unused-test-hash', 'admin')
			RETURNING id
		), inserted_profiles AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES ($4::uuid, $1::uuid, 'Addon category active'),
			       ($5::uuid, $1::uuid, 'Addon category explicit')
			RETURNING id
		)
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($3::uuid, $4::uuid, true), ($3::uuid, $5::uuid, true)
	`, categoryAID, categoryBID, userID, profileAID, profileBID); err != nil {
		t.Fatalf("seed addon category assignments: %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	globalPrincipal := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
		ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	rawManifest := json.RawMessage(`{"id":"org.rivune.category-assignment","version":"1.0.0","name":"Category Assignment","types":["movie"],"resources":["catalog","stream"],"catalogs":[{"type":"movie","id":"category"}]}`)
	manifest, rawManifest, err := ParseManifest(rawManifest)
	if err != nil {
		t.Fatalf("parse category assignment manifest: %v", err)
	}
	transport := &installManifestTransport{manifest: func(context.Context, string) (Manifest, json.RawMessage, error) {
		return manifest, rawManifest, nil
	}}
	service := NewService(pool, transport, discardLogger())
	categoryOnly := InstallInput{TransportURL: transportURL, ProfileIDs: []string{}, CategoryIDs: []string{categoryAID}}
	preview, err := service.Preview(ctx, globalPrincipal, categoryOnly)
	if err != nil || len(preview.ProfileIDs) != 0 || !reflect.DeepEqual(preview.CategoryIDs, []string{categoryAID}) {
		t.Fatalf("category-only preview = %+v, error %v", preview, err)
	}
	installed, err := service.Install(ctx, globalPrincipal, categoryOnly)
	if err != nil {
		t.Fatalf("install category-only addon: %v", err)
	}
	disjoint, err := service.Install(ctx, globalPrincipal, InstallInput{
		TransportURL: transportURL, ProfileIDs: []string{}, CategoryIDs: []string{categoryBID},
	})
	if err != nil {
		t.Fatalf("install same-owner same-transport add-on for disjoint category: %v", err)
	}
	if disjoint.ID == installed.ID {
		t.Fatal("disjoint category install reused the existing add-on row")
	}
	if _, err := service.Install(ctx, globalPrincipal, InstallInput{
		TransportURL: transportURL, ProfileIDs: []string{}, CategoryIDs: []string{categoryBID},
	}); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("overlapping disjoint-category transport policy error = %v, want %v", err, ErrAlreadyInstalled)
	}
	var sameOwnerRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM profile_addons
		WHERE id = ANY($1::uuid[]) AND profile_id = $2::uuid AND transport_url = $3
	`, []string{installed.ID, disjoint.ID}, profileAID, transportURL).Scan(&sameOwnerRows); err != nil {
		t.Fatalf("count same-owner same-transport add-ons: %v", err)
	}
	if sameOwnerRows != 2 {
		t.Fatalf("same-owner same-transport add-on rows = %d, want 2", sameOwnerRows)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM profile_addons WHERE id = $1::uuid`, disjoint.ID); err != nil {
		t.Fatalf("remove disjoint category fixture add-on: %v", err)
	}
	categoryA := categoryAID
	categoryDelegated := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &categoryA, ActiveProfileID: new(profileAID), ProfileGrantExpiresAt: &expiresAt,
	}
	manifestCalls := transport.calls
	if _, err := service.Refresh(ctx, categoryDelegated, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated category refresh error = %v, want %v", err, ErrForbidden)
	}
	if transport.calls != manifestCalls {
		t.Fatalf("delegated category refresh made %d manifest calls", transport.calls-manifestCalls)
	}
	if _, err := service.Refresh(ctx, globalPrincipal, installed.ID); err != nil {
		t.Fatalf("global category refresh: %v", err)
	}
	if len(installed.ProfileIDs) != 0 || !reflect.DeepEqual(installed.CategoryIDs, []string{categoryAID}) {
		t.Fatalf("category-only assignments = profiles %v categories %v", installed.ProfileIDs, installed.CategoryIDs)
	}
	list, err := service.List(ctx, globalPrincipal)
	if err != nil || len(list) != 1 || list[0].ID != installed.ID {
		t.Fatalf("category effective list = %+v, error %v", list, err)
	}
	if err := service.ValidatePlaybackAccess(ctx, globalPrincipal, installed.ID); err != nil {
		t.Fatalf("category effective playback access: %v", err)
	}
	if err := service.ValidatePlaybackAccesses(ctx, globalPrincipal, []string{installed.ID, installed.ID}); err != nil {
		t.Fatalf("deduplicated category playback access batch: %v", err)
	}
	if err := service.ValidatePlaybackAccesses(ctx, globalPrincipal, []string{"not-an-addon-id"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid playback provider batch error = %v, want %v", err, ErrInvalidInput)
	}
	if _, err := pool.Exec(ctx, `
		WITH inserted_addon AS (
			INSERT INTO profile_addons (
				id, profile_id, transport_url, manifest, manifest_id, manifest_version, position
			) VALUES (
				$1::uuid, $2::uuid, 'https://explicit-management.example/manifest.json',
				$3::jsonb, 'org.rivune.explicit-management', '1.0.0', 1
			)
			RETURNING id
		), inserted_access AS (
			INSERT INTO addon_profile_access (addon_id, profile_id, position)
			SELECT id, $2::uuid, 0 FROM inserted_addon
			RETURNING addon_id
		)
		INSERT INTO addon_profile_order (addon_id, profile_id, position)
		SELECT addon_id, $2::uuid, 0 FROM inserted_access
	`, explicitAddonID, profileAID, rawManifest); err != nil {
		t.Fatalf("seed explicit-only management addon: %v", err)
	}
	delegatedManaged, err := service.List(ctx, categoryDelegated)
	if err != nil || len(delegatedManaged) != 1 || delegatedManaged[0].ID != explicitAddonID || len(delegatedManaged[0].CategoryIDs) != 0 {
		t.Fatalf("delegated explicit-only management list = %+v, error %v", delegatedManaged, err)
	}
	if err := service.ValidatePlaybackAccesses(ctx, globalPrincipal, []string{installed.ID, explicitAddonID, installed.ID}); err != nil {
		t.Fatalf("multi-provider playback access batch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM addon_profile_access WHERE addon_id = $1::uuid AND profile_id = $2::uuid
	`, explicitAddonID, profileAID); err != nil {
		t.Fatalf("revoke explicit playback provider access: %v", err)
	}
	if err := service.ValidatePlaybackAccesses(ctx, globalPrincipal, []string{installed.ID, explicitAddonID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked provider batch error = %v, want %v", err, ErrNotFound)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM profile_addons WHERE id = $1::uuid`, explicitAddonID); err != nil {
		t.Fatalf("remove explicit-only management fixture: %v", err)
	}
	if delegatedManaged, err := service.List(ctx, categoryDelegated); err != nil || len(delegatedManaged) != 0 {
		t.Fatalf("delegated category policy leaked through management list = %+v, error %v", delegatedManaged, err)
	}

	if _, err := pool.Exec(ctx, `
		WITH inserted_profile AS (
			INSERT INTO profiles (id, category_id, name)
			VALUES ($1::uuid, $2::uuid, 'Addon category future')
			RETURNING id
		)
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		SELECT $3::uuid, id, true FROM inserted_profile
	`, futureID, categoryAID, userID); err != nil {
		t.Fatalf("create future category profile: %v", err)
	}
	futurePrincipal := globalPrincipal
	futurePrincipal.ActiveProfileID = new(futureID)
	if list, err := service.List(ctx, futurePrincipal); err != nil || len(list) != 1 || list[0].ID != installed.ID {
		t.Fatalf("future profile inherited addon = %+v, error %v", list, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid`, futureID, categoryBID); err != nil {
		t.Fatalf("move future profile category: %v", err)
	}
	if list, err := service.List(ctx, futurePrincipal); err != nil || len(list) != 0 {
		t.Fatalf("moved profile retained category addon = %+v, error %v", list, err)
	}

	installed, err = service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{ProfileIDs: []string{profileBID}})
	if err != nil || !reflect.DeepEqual(installed.ProfileIDs, []string{profileBID}) || !reflect.DeepEqual(installed.CategoryIDs, []string{categoryAID}) {
		t.Fatalf("profile update preserving category = %+v, error %v", installed, err)
	}
	profileBGlobal := globalPrincipal
	profileBGlobal.ActiveProfileID = new(profileBID)
	categoryB := categoryBID
	profileBDelegated := auth.Principal{
		UserID: userID, Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID: &categoryB, ActiveProfileID: new(profileBID), ProfileGrantExpiresAt: &expiresAt,
	}
	if list, err := service.List(ctx, profileBGlobal); err != nil || len(list) != 1 {
		t.Fatalf("overlapping explicit/category access was not deduplicated = %+v, error %v", list, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE profiles SET category_id = $2::uuid WHERE id = $1::uuid`, profileBID, categoryBID); err != nil {
		t.Fatalf("move explicit profile category: %v", err)
	}
	if list, err := service.List(ctx, profileBGlobal); err != nil || len(list) != 1 {
		t.Fatalf("explicit access did not survive category move = %+v, error %v", list, err)
	}
	_, err = service.Update(ctx, profileBDelegated, installed.ID, UpdateAddonInput{CategoryIDs: []string{}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated category policy clear error = %v, want %v", err, ErrForbidden)
	}
	installed, err = service.Update(ctx, profileBGlobal, installed.ID, UpdateAddonInput{CategoryIDs: []string{}})
	if err != nil || !reflect.DeepEqual(installed.ProfileIDs, []string{profileBID}) || len(installed.CategoryIDs) != 0 {
		t.Fatalf("category clear preserving explicit assignment = %+v, error %v", installed, err)
	}
	if _, err := service.Update(ctx, profileBGlobal, installed.ID, UpdateAddonInput{ProfileIDs: []string{}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty resulting assignment union error = %v, want %v", err, ErrInvalidInput)
	}

	_, err = service.Update(ctx, profileBDelegated, installed.ID, UpdateAddonInput{
		ProfileIDs: []string{}, CategoryIDs: []string{categoryAID},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated category assignment error = %v, want %v", err, ErrForbidden)
	}
	installed, err = service.Update(ctx, profileBGlobal, installed.ID, UpdateAddonInput{
		ProfileIDs: []string{}, CategoryIDs: []string{categoryAID},
	})
	if err != nil {
		t.Fatalf("restore category-only assignment: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'member' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("revoke persisted global role: %v", err)
	}
	if _, err := service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{CategoryIDs: []string{categoryBID}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("category update after persisted role revocation error = %v, want %v", err, ErrForbidden)
	}
	if _, err := service.List(ctx, globalPrincipal); !errors.Is(err, ErrForbidden) {
		t.Fatalf("management list after persisted role revocation error = %v, want %v", err, ErrForbidden)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("restore persisted global role: %v", err)
	}

	reordered, err := service.Reorder(ctx, globalPrincipal, ReorderInput{AddonIDs: []string{installed.ID}})
	if err != nil || len(reordered) != 1 {
		t.Fatalf("reorder category-effective addon = %+v, error %v", reordered, err)
	}
	var explicitAccessCount, profileOrderCount int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM addon_profile_access WHERE addon_id = $1::uuid AND profile_id = $2::uuid),
			(SELECT count(*) FROM addon_profile_order WHERE addon_id = $1::uuid AND profile_id = $2::uuid)
	`, installed.ID, profileAID).Scan(&explicitAccessCount, &profileOrderCount); err != nil {
		t.Fatalf("query effective reorder persistence: %v", err)
	}
	if explicitAccessCount != 0 || profileOrderCount != 1 {
		t.Fatalf("effective reorder materialized access=%d order=%d", explicitAccessCount, profileOrderCount)
	}

	if _, err := service.Install(ctx, globalPrincipal, InstallInput{
		TransportURL: transportURL, ProfileIDs: []string{profileAID}, CategoryIDs: []string{},
	}); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("overlapping transport policy error = %v, want %v", err, ErrAlreadyInstalled)
	}
	disabled := false
	installed, err = service.Update(ctx, globalPrincipal, installed.ID, UpdateAddonInput{Enabled: &disabled})
	if err != nil || installed.Enabled {
		t.Fatalf("disable category addon = %+v, error %v", installed, err)
	}
	if err := service.ValidatePlaybackAccesses(ctx, globalPrincipal, []string{installed.ID, installed.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled category playback batch error = %v, want %v", err, ErrNotFound)
	}
	diagnostics, err := service.Diagnostics(ctx, globalPrincipal)
	if err != nil || len(diagnostics.Diagnostics) != 1 || diagnostics.Diagnostics[0].AddonID != installed.ID {
		t.Fatalf("disabled category diagnostics = %+v, error %v", diagnostics, err)
	}
	if err := service.Remove(ctx, categoryDelegated, installed.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delegated category removal error = %v, want %v", err, ErrForbidden)
	}
	if err := service.Remove(ctx, globalPrincipal, installed.ID); err != nil {
		t.Fatalf("global category removal: %v", err)
	}
}
