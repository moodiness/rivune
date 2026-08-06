package collection

import (
	"errors"
	"fmt"
	"testing"
)

func TestNormalizeNewCollectionAssignmentsDefaultsOnlyWhenBothDimensionsAreOmitted(t *testing.T) {
	const activeProfileID = "11111111-1111-4111-8111-111111111111"
	categoryID := "22222222-2222-4222-8222-222222222222"

	defaults, err := normalizeNewCollectionAssignments(nil, nil, activeProfileID)
	if err != nil {
		t.Fatalf("normalize omitted assignments: %v", err)
	}
	if len(defaults.profileIDs) != 1 || defaults.profileIDs[0] != activeProfileID || len(defaults.categoryIDs) != 0 {
		t.Fatalf("omitted assignments = %+v", defaults)
	}

	categoryOnly, err := normalizeNewCollectionAssignments([]string{}, []string{categoryID}, activeProfileID)
	if err != nil {
		t.Fatalf("normalize category-only assignments: %v", err)
	}
	if len(categoryOnly.profileIDs) != 0 || len(categoryOnly.categoryIDs) != 1 || categoryOnly.categoryIDs[0] != categoryID {
		t.Fatalf("category-only assignments = %+v", categoryOnly)
	}

	for _, input := range []struct {
		profiles   []string
		categories []string
	}{
		{profiles: []string{}, categories: nil},
		{profiles: nil, categories: []string{}},
		{profiles: []string{}, categories: []string{}},
	} {
		if _, err := normalizeNewCollectionAssignments(input.profiles, input.categories, activeProfileID); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("empty assignment union (%v, %v) error = %v", input.profiles, input.categories, err)
		}
	}
}

func TestNormalizeCollectionAssignmentsEnforcesIndependentLimitsAndUniqueness(t *testing.T) {
	ids := make([]string, 101)
	for index := range ids {
		ids[index] = fmt.Sprintf("%08x-0000-4000-8000-%012x", index+1, index+1)
	}
	if _, err := normalizeNewCollectionAssignments(ids, nil, ids[0]); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("101 profile IDs error = %v", err)
	}
	if _, err := normalizeNewCollectionAssignments(nil, ids, ids[0]); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("101 category IDs error = %v", err)
	}
	duplicate := []string{ids[0], ids[0]}
	if _, err := normalizeCollectionAssignmentUpdate(duplicate, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate profile IDs error = %v", err)
	}
	if _, err := normalizeCollectionAssignmentUpdate(nil, duplicate); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate category IDs error = %v", err)
	}
	caseVariant := []string{"abcdefab-cdef-4abc-8def-abcdefabcdef", "ABCDEFAB-CDEF-4ABC-8DEF-ABCDEFABCDEF"}
	if _, err := normalizeCollectionAssignmentUpdate(caseVariant, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("case-variant duplicate profile IDs error = %v", err)
	}
	if _, err := normalizeCollectionAssignmentUpdate(nil, caseVariant); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("case-variant duplicate category IDs error = %v", err)
	}
}

func TestNormalizeCollectionAssignmentUpdatePreservesOmissionAndExplicitClear(t *testing.T) {
	omitted, err := normalizeCollectionAssignmentUpdate(nil, nil)
	if err != nil {
		t.Fatalf("normalize omitted update: %v", err)
	}
	if omitted.profileIDs != nil || omitted.categoryIDs != nil {
		t.Fatalf("omitted update lost nil markers: %+v", omitted)
	}
	cleared, err := normalizeCollectionAssignmentUpdate([]string{}, []string{})
	if err != nil {
		t.Fatalf("normalize explicit clears: %v", err)
	}
	if cleared.profileIDs == nil || cleared.categoryIDs == nil || len(cleared.profileIDs) != 0 || len(cleared.categoryIDs) != 0 {
		t.Fatalf("explicit clears lost non-nil markers: %+v", cleared)
	}
}
