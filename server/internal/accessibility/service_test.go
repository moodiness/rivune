package accessibility

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	profileA = "11111111-1111-4111-8111-111111111111"
	profileB = "22222222-2222-4222-8222-222222222222"
)

type memoryRepository struct{ documents map[string]Document }

func (repository *memoryRepository) Get(_ context.Context, _ auth.Principal, profileID string) (Document, error) {
	if document, ok := repository.documents[profileID]; ok {
		return document, nil
	}
	document := Document{Preferences: Defaults()}
	repository.documents[profileID] = document
	return document, nil
}

func (repository *memoryRepository) Update(_ context.Context, _ auth.Principal, profileID string, input UpdateInput) (Document, error) {
	current, ok := repository.documents[profileID]
	if !ok {
		current = Document{Preferences: Defaults()}
	}
	if current.Revision != input.Revision {
		return Document{}, ErrConflict
	}
	next := Document{Revision: current.Revision + 1, Preferences: input.Preferences}
	repository.documents[profileID] = next
	return next, nil
}

func TestDefaultsAreExplicitAndPersistPerProfile(t *testing.T) {
	repository := &memoryRepository{documents: make(map[string]Document)}
	service := testService(repository)
	document, err := service.Get(context.Background(), principal(profileA), profileA)
	if err != nil {
		t.Fatal(err)
	}
	if document.Revision != 0 || !reflect.DeepEqual(document.Preferences, Defaults()) {
		t.Fatalf("default document = %+v", document)
	}
	if stored, ok := repository.documents[profileA]; !ok || !reflect.DeepEqual(stored, document) {
		t.Fatalf("default document was not persisted: %+v", repository.documents)
	}
}

func TestProfilesRemainIsolated(t *testing.T) {
	repository := &memoryRepository{documents: make(map[string]Document)}
	service := testService(repository)
	updated, err := service.Update(context.Background(), principal(profileA), profileA, UpdateInput{Preferences: Preferences{
		ReducedMotion: "reduce", HighContrast: "more", TextScale: 130, Captions: "on", AudioDescription: true, FocusIndicators: "enhanced",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 1 {
		t.Fatalf("updated revision = %d", updated.Revision)
	}
	other, err := service.Get(context.Background(), principal(profileB), profileB)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(other.Preferences, Defaults()) || repository.documents[profileA] == repository.documents[profileB] {
		t.Fatalf("profile documents leaked: A=%+v B=%+v", repository.documents[profileA], repository.documents[profileB])
	}
	if _, err := service.Get(context.Background(), principal(profileA), profileB); !errors.Is(err, ErrActiveProfileRequired) {
		t.Fatalf("cross-profile read error = %v", err)
	}
}

func TestUpdateAdvancesRevisionAndRejectsStaleWrite(t *testing.T) {
	repository := &memoryRepository{documents: make(map[string]Document)}
	service := testService(repository)
	input := UpdateInput{Preferences: Defaults()}
	input.TextScale = 115
	first, err := service.Update(context.Background(), principal(profileA), profileA, input)
	if err != nil || first.Revision != 1 || first.TextScale != 115 {
		t.Fatalf("first update = %+v, %v", first, err)
	}
	input.HighContrast = "more"
	if _, err := service.Update(context.Background(), principal(profileA), profileA, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	input.Revision = first.Revision
	second, err := service.Update(context.Background(), principal(profileA), profileA, input)
	if err != nil || second.Revision != 2 || second.HighContrast != "more" {
		t.Fatalf("second update = %+v, %v", second, err)
	}
}

func TestUpdateRejectsEveryInvalidClosedValue(t *testing.T) {
	valid := UpdateInput{Preferences: Defaults()}
	tests := map[string]UpdateInput{
		"negative revision": func() UpdateInput { value := valid; value.Revision = -1; return value }(),
		"motion":            func() UpdateInput { value := valid; value.ReducedMotion = "sometimes"; return value }(),
		"contrast":          func() UpdateInput { value := valid; value.HighContrast = "maximum"; return value }(),
		"scale":             func() UpdateInput { value := valid; value.TextScale = 120; return value }(),
		"captions":          func() UpdateInput { value := valid; value.Captions = "auto"; return value }(),
		"focus":             func() UpdateInput { value := valid; value.FocusIndicators = "bold"; return value }(),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			service := testService(&memoryRepository{documents: make(map[string]Document)})
			if _, err := service.Update(context.Background(), principal(profileA), profileA, input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid update error = %v", err)
			}
		})
	}
}

func testService(repository repository) *Service {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	return &Service{repository: repository, now: func() time.Time { return now }}
}

func principal(profileID string) auth.Principal {
	expires := time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC)
	return auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expires}
}
