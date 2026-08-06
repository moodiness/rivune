package addon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const maximumDiagnosticObservations = 4096

type DiagnosticState string

const (
	DiagnosticStateUnknown     DiagnosticState = "unknown"
	DiagnosticStateAvailable   DiagnosticState = "available"
	DiagnosticStateDegraded    DiagnosticState = "degraded"
	DiagnosticStateUnavailable DiagnosticState = "unavailable"
)

type DiagnosticErrorCode string

const (
	DiagnosticErrorTimeout         DiagnosticErrorCode = "timeout"
	DiagnosticErrorInvalidResponse DiagnosticErrorCode = "invalid_response"
	DiagnosticErrorUnavailable     DiagnosticErrorCode = "unavailable"
	DiagnosticErrorRequestFailed   DiagnosticErrorCode = "request_failed"
)

type DiagnosticLastError struct {
	Code DiagnosticErrorCode `json:"code"`
	At   time.Time           `json:"at"`
}

type AddonCapabilities struct {
	Resources        []string `json:"resources"`
	Search           bool     `json:"search"`
	Pagination       bool     `json:"pagination"`
	SearchPagination bool     `json:"searchPagination"`
}

type DiagnosticEntry struct {
	AddonID              string               `json:"addonId"`
	State                DiagnosticState      `json:"state"`
	LastSuccessAt        *time.Time           `json:"lastSuccessAt,omitempty"`
	ApproximateLatencyMS *int64               `json:"approximateLatencyMs,omitempty"`
	LastError            *DiagnosticLastError `json:"lastError,omitempty"`
	Capabilities         AddonCapabilities    `json:"capabilities"`
}

type Diagnostics struct {
	ObservedSince time.Time         `json:"observedSince"`
	Diagnostics   []DiagnosticEntry `json:"diagnostics"`
}

type diagnosticAttempt struct {
	startedAt  time.Time
	generation uint64
}

type diagnosticObservation struct {
	sequence             uint64
	generation           uint64
	latestSucceeded      bool
	hasSuccess           bool
	lastSuccessAt        time.Time
	approximateLatencyMS int64
	lastError            *DiagnosticLastError
}

type diagnosticRemoval struct {
	sequence   uint64
	generation uint64
}

type diagnosticStore struct {
	mu            sync.Mutex
	clock         func() time.Time
	observedSince time.Time
	maximum       int
	sequence      atomic.Uint64
	observations  map[string]diagnosticObservation
	removals      map[string]diagnosticRemoval
}

func newDiagnosticStore(clock func() time.Time, maximum int) *diagnosticStore {
	if clock == nil {
		clock = time.Now
	}
	if maximum <= 0 || maximum > maximumDiagnosticObservations {
		maximum = maximumDiagnosticObservations
	}
	return &diagnosticStore{
		clock:         clock,
		observedSince: clock().UTC(),
		maximum:       maximum,
		observations:  make(map[string]diagnosticObservation),
		removals:      make(map[string]diagnosticRemoval),
	}
}

func (store *diagnosticStore) start(addonID string) diagnosticAttempt {
	if store == nil {
		return diagnosticAttempt{}
	}
	store.mu.Lock()
	attempt := diagnosticAttempt{startedAt: store.clock()}
	if observation, exists := store.observations[addonID]; exists {
		attempt.generation = observation.generation
	} else if removal, exists := store.removals[addonID]; exists {
		attempt.generation = removal.generation
	}
	store.mu.Unlock()
	return attempt
}

func (store *diagnosticStore) complete(addonID string, attempt diagnosticAttempt, err error) {
	if store == nil || addonID == "" || isClientCancellation(err) {
		return
	}
	sequence := store.sequence.Add(1)
	store.mu.Lock()
	defer store.mu.Unlock()
	completedAt := store.clock().UTC()

	observation, observed := store.observations[addonID]
	removal, removed := store.removals[addonID]
	if (observed && observation.generation != attempt.generation) || (removed && removal.generation != attempt.generation) {
		return
	}
	if (observed && observation.sequence > sequence) || (removed && removal.sequence > sequence) {
		return
	}
	if !observed && !removed && store.size() >= store.maximum {
		store.evictOldest()
	}
	delete(store.removals, addonID)
	observation.sequence = sequence
	observation.generation = attempt.generation
	observation.latestSucceeded = err == nil
	if err == nil {
		latency := completedAt.Sub(attempt.startedAt).Milliseconds()
		if latency < 1 {
			latency = 1
		}
		observation.hasSuccess = true
		observation.lastSuccessAt = completedAt
		observation.approximateLatencyMS = latency
	} else {
		observation.lastError = &DiagnosticLastError{Code: classifyDiagnosticError(err), At: completedAt}
	}
	store.observations[addonID] = observation
}

func (store *diagnosticStore) remove(addonID string) {
	if store == nil || addonID == "" {
		return
	}
	sequence := store.sequence.Add(1)
	store.mu.Lock()
	observation, observed := store.observations[addonID]
	removal, removed := store.removals[addonID]
	if !observed && !removed && store.size() >= store.maximum {
		store.evictOldest()
	}
	generation := observation.generation
	if removed {
		generation = removal.generation
	}
	delete(store.observations, addonID)
	store.removals[addonID] = diagnosticRemoval{sequence: sequence, generation: generation + 1}
	store.mu.Unlock()
}

func (store *diagnosticStore) size() int {
	return len(store.observations) + len(store.removals)
}

func (store *diagnosticStore) evictOldest() {
	var oldestID string
	var oldestSequence uint64
	oldestWasRemoval := false
	for candidateID, candidate := range store.observations {
		if oldestID == "" || candidate.sequence < oldestSequence {
			oldestID = candidateID
			oldestSequence = candidate.sequence
			oldestWasRemoval = false
		}
	}
	for candidateID, candidate := range store.removals {
		if oldestID == "" || candidate.sequence < oldestSequence {
			oldestID = candidateID
			oldestSequence = candidate.sequence
			oldestWasRemoval = true
		}
	}
	if oldestWasRemoval {
		delete(store.removals, oldestID)
	} else {
		delete(store.observations, oldestID)
	}
}

func (store *diagnosticStore) snapshot() (time.Time, map[string]diagnosticObservation) {
	if store == nil {
		return time.Time{}, map[string]diagnosticObservation{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	observations := make(map[string]diagnosticObservation, len(store.observations))
	for addonID, observation := range store.observations {
		if observation.lastError != nil {
			lastError := *observation.lastError
			observation.lastError = &lastError
		}
		observations[addonID] = observation
	}
	return store.observedSince, observations
}

func isClientCancellation(err error) bool {
	return err != nil && errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func classifyDiagnosticError(err error) DiagnosticErrorCode {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return DiagnosticErrorTimeout
	case errors.Is(err, ErrInvalidResponse):
		return DiagnosticErrorInvalidResponse
	case errors.Is(err, ErrProviderUnavailable):
		return DiagnosticErrorUnavailable
	default:
		return DiagnosticErrorRequestFailed
	}
}

func capabilitiesFor(manifest Manifest) AddonCapabilities {
	capabilities := AddonCapabilities{Resources: make([]string, 0, len(manifest.Resources))}
	seenResources := make(map[string]struct{}, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		if _, exists := seenResources[resource.Name]; exists {
			continue
		}
		seenResources[resource.Name] = struct{}{}
		capabilities.Resources = append(capabilities.Resources, resource.Name)
	}
	for _, catalog := range manifest.Catalogs {
		search := catalog.DeclaresExtra("search")
		pagination := catalog.DeclaresExtra("skip")
		capabilities.Search = capabilities.Search || search
		capabilities.Pagination = capabilities.Pagination || pagination
		capabilities.SearchPagination = capabilities.SearchPagination || search && pagination
	}
	for _, catalog := range manifest.AddonCatalogs {
		capabilities.Pagination = capabilities.Pagination || catalog.DeclaresExtra("skip")
	}
	return capabilities
}

func diagnosticsFor(addons []InstalledAddon, observedSince time.Time, observations map[string]diagnosticObservation) Diagnostics {
	entries := make([]DiagnosticEntry, 0, len(addons))
	for _, installed := range addons {
		entry := DiagnosticEntry{
			AddonID:      installed.ID,
			State:        DiagnosticStateUnknown,
			Capabilities: capabilitiesFor(installed.parsedManifest),
		}
		if observation, exists := observations[installed.ID]; exists {
			switch {
			case observation.latestSucceeded:
				entry.State = DiagnosticStateAvailable
			case observation.hasSuccess:
				entry.State = DiagnosticStateDegraded
			default:
				entry.State = DiagnosticStateUnavailable
			}
			if observation.hasSuccess {
				lastSuccessAt := observation.lastSuccessAt
				latency := observation.approximateLatencyMS
				entry.LastSuccessAt = &lastSuccessAt
				entry.ApproximateLatencyMS = &latency
			}
			entry.LastError = observation.lastError
		}
		entries = append(entries, entry)
	}
	return Diagnostics{ObservedSince: observedSince, Diagnostics: entries}
}

func (service *Service) Diagnostics(ctx context.Context, principal auth.Principal) (Diagnostics, error) {
	if !principal.IsGlobalAdministrator() {
		return Diagnostics{}, ErrForbidden
	}
	profileID, err := activeProfileID(principal)
	if err != nil {
		return Diagnostics{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Diagnostics{}, fmt.Errorf("begin addon diagnostics: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := authorizeGlobalAddonOrigin(ctx, tx, principal); err != nil {
		return Diagnostics{}, err
	}
	if err := authorizeActiveProfile(ctx, tx, principal, profileID); err != nil {
		return Diagnostics{}, err
	}
	addons, err := listForProfile(ctx, tx, profileID)
	if err != nil {
		return Diagnostics{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Diagnostics{}, fmt.Errorf("commit addon diagnostics: %w", err)
	}
	observedSince, observations := service.diagnostics.snapshot()
	return diagnosticsFor(addons, observedSince, observations), nil
}
