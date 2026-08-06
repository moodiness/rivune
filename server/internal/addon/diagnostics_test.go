package addon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestDiagnosticStoreStateTransitionsUseCompletionOrder(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := newDiagnosticStore(func() time.Time { return now }, 4)
	installed := InstalledAddon{ID: "addon-a", parsedManifest: Manifest{
		Resources: []ManifestResource{{Name: "catalog", Short: true}},
	}}

	observedSince, observations := store.snapshot()
	initial := diagnosticsFor([]InstalledAddon{installed}, observedSince, observations)
	if !observedSince.Equal(now) || len(initial.Diagnostics) != 1 || initial.Diagnostics[0].State != DiagnosticStateUnknown {
		t.Fatalf("initial diagnostics = %+v since %s", initial, observedSince)
	}

	first := store.start(installed.ID)
	now = now.Add(4 * time.Millisecond)
	store.complete(installed.ID, first, fmt.Errorf("private host and path: %w", ErrProviderUnavailable))
	entry := diagnosticEntryForTest(t, store, installed)
	if entry.State != DiagnosticStateUnavailable || entry.LastError == nil || entry.LastError.Code != DiagnosticErrorUnavailable {
		t.Fatalf("first failure diagnostics = %+v", entry)
	}
	firstErrorAt := entry.LastError.At

	slowSuccess := store.start(installed.ID)
	now = now.Add(time.Nanosecond)
	fastFailure := store.start(installed.ID)
	now = now.Add(time.Nanosecond)
	store.complete(installed.ID, fastFailure, context.DeadlineExceeded)
	now = now.Add(time.Nanosecond)
	store.complete(installed.ID, slowSuccess, nil)
	entry = diagnosticEntryForTest(t, store, installed)
	if entry.State != DiagnosticStateAvailable || entry.LastSuccessAt == nil || entry.ApproximateLatencyMS == nil || *entry.ApproximateLatencyMS != 1 {
		t.Fatalf("latest completion success diagnostics = %+v", entry)
	}
	if entry.LastError == nil || entry.LastError.Code != DiagnosticErrorTimeout || !entry.LastError.At.After(firstErrorAt) {
		t.Fatalf("successful recovery did not retain safe historical error: %+v", entry.LastError)
	}

	failure := store.start(installed.ID)
	now = now.Add(time.Millisecond)
	store.complete(installed.ID, failure, fmt.Errorf("provider body secret: %w", ErrInvalidResponse))
	entry = diagnosticEntryForTest(t, store, installed)
	if entry.State != DiagnosticStateDegraded || entry.LastError == nil || entry.LastError.Code != DiagnosticErrorInvalidResponse {
		t.Fatalf("failure after success diagnostics = %+v", entry)
	}

	recovery := store.start(installed.ID)
	now = now.Add(3 * time.Millisecond)
	store.complete(installed.ID, recovery, nil)
	entry = diagnosticEntryForTest(t, store, installed)
	if entry.State != DiagnosticStateAvailable || entry.LastError == nil || entry.LastError.Code != DiagnosticErrorInvalidResponse {
		t.Fatalf("recovered diagnostics = %+v", entry)
	}
}

func TestDiagnosticStoreClassifiesSafelyAndIgnoresClientCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want DiagnosticErrorCode
	}{
		{name: "timeout", err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), want: DiagnosticErrorTimeout},
		{name: "invalid response", err: fmt.Errorf("private response: %w", ErrInvalidResponse), want: DiagnosticErrorInvalidResponse},
		{name: "unavailable", err: fmt.Errorf("private endpoint: %w", ErrProviderUnavailable), want: DiagnosticErrorUnavailable},
		{name: "request failed", err: errors.New("dial private-host.invalid: connection refused"), want: DiagnosticErrorRequestFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyDiagnosticError(test.err); got != test.want {
				t.Fatalf("classifyDiagnosticError() = %q, want %q", got, test.want)
			}
			store := newDiagnosticStore(time.Now, 1)
			store.complete("addon", store.start("addon"), test.err)
			observedSince, observations := store.snapshot()
			response := diagnosticsFor([]InstalledAddon{{ID: "addon"}}, observedSince, observations)
			serialized, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("marshal diagnostics: %v", err)
			}
			for _, private := range []string{"private", "endpoint", "host.invalid", "connection refused", "dial "} {
				if strings.Contains(strings.ToLower(string(serialized)), private) {
					t.Fatalf("serialized diagnostics exposed %q: %s", private, serialized)
				}
			}
		})
	}

	store := newDiagnosticStore(time.Now, 1)
	store.complete("addon", store.start("addon"), fmt.Errorf("client stopped: %w", context.Canceled))
	_, observations := store.snapshot()
	if len(observations) != 0 {
		t.Fatalf("client cancellation created diagnostics: %+v", observations)
	}
}

func TestDiagnosticStoreBoundsEvictsAndRemovesObservations(t *testing.T) {
	clamped := newDiagnosticStore(time.Now, maximumDiagnosticObservations+1)
	if clamped.maximum != maximumDiagnosticObservations {
		t.Fatalf("diagnostic store maximum = %d, want %d", clamped.maximum, maximumDiagnosticObservations)
	}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	store := newDiagnosticStore(func() time.Time { return now }, 2)
	complete := func(addonID string) {
		t.Helper()
		attempt := store.start(addonID)
		now = now.Add(time.Millisecond)
		store.complete(addonID, attempt, nil)
	}
	complete("a")
	complete("b")
	complete("a")
	complete("c")
	_, observations := store.snapshot()
	if store.size() != 2 || !observations["a"].latestSucceeded || !observations["c"].latestSucceeded {
		t.Fatalf("bounded observations = %+v", observations)
	}
	if _, exists := observations["b"]; exists {
		t.Fatal("least recently completed observation was not evicted")
	}

	inFlight := store.start("a")
	store.remove("a")
	store.complete("a", inFlight, ErrProviderUnavailable)
	_, observations = store.snapshot()
	if _, exists := observations["a"]; exists {
		t.Fatalf("removed add-on was resurrected by an older completion: %+v", observations["a"])
	}
	if store.size() > store.maximum || store.maximum > maximumDiagnosticObservations {
		t.Fatalf("store exceeded bound: %d entries, maximum %d", store.size(), store.maximum)
	}
}

func TestDiagnosticStoreConcurrentSnapshotsRemainCoherentAndBounded(t *testing.T) {
	store := newDiagnosticStore(time.Now, 32)
	var wait sync.WaitGroup
	for worker := range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for operation := range 100 {
				addonID := fmt.Sprintf("addon-%d", (worker+operation)%64)
				attempt := store.start(addonID)
				if operation%3 == 0 {
					store.complete(addonID, attempt, ErrProviderUnavailable)
				} else {
					store.complete(addonID, attempt, nil)
				}
				observedSince, observations := store.snapshot()
				if observedSince.IsZero() || len(observations) > 32 {
					t.Errorf("incoherent snapshot: since=%s entries=%d", observedSince, len(observations))
					return
				}
				for id, observation := range observations {
					if id == "" || observation.sequence == 0 {
						t.Errorf("invalid observation %q: %+v", id, observation)
						return
					}
				}
			}
		}()
	}
	wait.Wait()
	if store.size() > 32 {
		t.Fatalf("concurrent store grew to %d entries", store.size())
	}
}

func TestDiagnosticsCapabilitiesComeOnlyFromInstalledManifest(t *testing.T) {
	manifest := Manifest{
		Resources: []ManifestResource{
			{Name: "catalog", Short: true},
			{Name: "meta", Short: true},
			{Name: "catalog", Short: true},
		},
		Catalogs: []ManifestCatalog{
			{Type: "movie", ID: "search", Extra: []ExtraProp{{Name: "search"}, {Name: "skip"}}},
			{Type: "series", ID: "page", ExtraSupported: []string{"skip"}},
		},
		AddonCatalogs: []ManifestCatalog{{Type: "all", ID: "community", ExtraRequired: []string{"skip"}}},
	}
	got := capabilitiesFor(manifest)
	if strings.Join(got.Resources, ",") != "catalog,meta" || !got.Search || !got.Pagination || !got.SearchPagination {
		t.Fatalf("capabilitiesFor() = %+v", got)
	}
}

func TestExecuteRecordsOnlyFinalRetriedOutcome(t *testing.T) {
	calls := 0
	transport := functionTransport{resource: func(context.Context, string, ResourcePath) (json.RawMessage, CachePolicy, error) {
		calls++
		if calls == 1 {
			return nil, CachePolicy{}, unavailable("private request", errors.New("private network failure"), true)
		}
		return json.RawMessage(`{"metas":[]}`), CachePolicy{}, nil
	}}
	service := Service{
		transport:      transport,
		logger:         discardLogger(),
		requestTimeout: time.Second,
		retryDelay:     time.Millisecond,
		diagnostics:    newDiagnosticStore(time.Now, 4),
	}
	request := executeTestRequest("retry")
	batch, err := service.execute(context.Background(), []plannedRequest{request})
	if err != nil || len(batch.Results) != 1 || calls != 2 {
		t.Fatalf("retried execution = calls %d, batch %+v, error %v", calls, batch, err)
	}
	_, observations := service.diagnostics.snapshot()
	observation, exists := observations[request.addon.ID]
	if !exists || !observation.latestSucceeded || !observation.hasSuccess || observation.lastError != nil {
		t.Fatalf("retry recorded intermediate outcome: %+v", observation)
	}
}

func TestDiagnosticsRejectsNonGlobalPrincipalBeforeDatabaseAccess(t *testing.T) {
	profileID := "11111111-1111-4111-8111-111111111111"
	categoryID := "22222222-2222-4222-8222-222222222222"
	service := NewService(nil, nil, discardLogger())
	for _, principal := range []auth.Principal{
		{Role: "member", ActiveProfileID: &profileID},
		{Role: "admin", AuthorizationScope: auth.AuthorizationScopeCategory, CategoryID: &categoryID, ActiveProfileID: &profileID},
	} {
		if _, err := service.Diagnostics(context.Background(), principal); !errors.Is(err, ErrForbidden) {
			t.Fatalf("Diagnostics() error = %v, want %v", err, ErrForbidden)
		}
	}
	_, observations := service.diagnostics.snapshot()
	if len(observations) != 0 {
		t.Fatalf("authorization failures created diagnostics: %+v", observations)
	}
}

func diagnosticEntryForTest(t *testing.T, store *diagnosticStore, installed InstalledAddon) DiagnosticEntry {
	t.Helper()
	observedSince, observations := store.snapshot()
	diagnostics := diagnosticsFor([]InstalledAddon{installed}, observedSince, observations)
	if len(diagnostics.Diagnostics) != 1 {
		t.Fatalf("diagnostics count = %d", len(diagnostics.Diagnostics))
	}
	return diagnostics.Diagnostics[0]
}
