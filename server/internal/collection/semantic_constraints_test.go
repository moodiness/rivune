package collection

import (
	"slices"
	"testing"
	"time"
)

var semanticConstraintTestNow = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.FixedZone("test", 2*60*60))

func constrainedSemanticQuery(query, language string, excluded map[string]struct{}) parsedSemanticQuery {
	parsed := parseSemanticQuery(query, "", excluded)
	applySemanticConstraints(query, language, semanticConstraintTestNow, &parsed, excluded)
	return parsed
}

func TestSemanticConstraintsRecognizeEnglishAndFrenchReleaseDates(t *testing.T) {
	tests := []struct {
		name, query, language, intent, title, from, to string
	}{
		{"English year", "Alien released in 1979", "en-US", "release_year:1979", "Alien", "1979-01-01", "1979-12-31"},
		{"French year", "Alien sorti en 1979", "fr-FR", "release_year:1979", "Alien", "1979-01-01", "1979-12-31"},
		{"English decade", "movies from the 1990s", "en", "release_decade:1990s", "", "1990-01-01", "1999-12-31"},
		{"French decade", "films des années 80", "fr", "release_decade:1980s", "", "1980-01-01", "1989-12-31"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed := constrainedSemanticQuery(test.query, test.language, nil)
			if !hasSemanticIntent(parsed.intents, test.intent) || parsed.titleQuery != test.title || parsed.constraints.releaseDateFrom != test.from || parsed.constraints.releaseDateTo != test.to {
				t.Fatalf("parsed constraint = intents %+v title %q dates %q..%q", parsed.intents, parsed.titleQuery, parsed.constraints.releaseDateFrom, parsed.constraints.releaseDateTo)
			}
		})
	}
}

func TestSemanticConstraintsRecentUsesUTCNowBoundary(t *testing.T) {
	now := time.Date(2027, time.January, 1, 0, 30, 0, 0, time.FixedZone("east", 2*60*60))
	parsed := parseSemanticQuery("latest movies", "", nil)
	applySemanticConstraints("latest movies", "en", now, &parsed, nil)
	if parsed.constraints.releaseDateFrom != "2024-01-01" || parsed.constraints.releaseDateTo != "2026-12-31" || parsed.constraints.sort != "release_date.desc" {
		t.Fatalf("UTC recent range = %+v", parsed.constraints)
	}
	current := parseSemanticQuery("released in 2026", "", nil)
	applySemanticConstraints("released in 2026", "en", now, &current, nil)
	if current.constraints.releaseDateTo != "2026-12-31" {
		t.Fatalf("current UTC year ended outside current day: %+v", current.constraints)
	}
	future := constrainedSemanticQuery("released in 2027", "en", nil)
	if future.constraints.releaseDateFrom != "" || future.titleQuery != "released in 2027" {
		t.Fatalf("future local year became a release constraint: %+v", future)
	}
}

func TestSemanticConstraintsRecognizeRecentAndNewVariants(t *testing.T) {
	for _, test := range []struct{ query, language string }{
		{"recent movies", "en"}, {"latest releases", "en"}, {"new movies", "en"},
		{"films récents", "fr"}, {"nouveautés", "fr"}, {"nouveaux films", "fr"},
	} {
		parsed := constrainedSemanticQuery(test.query, test.language, nil)
		if !hasSemanticIntent(parsed.intents, "release_recency:recent") || parsed.constraints.sort != "release_date.desc" || parsed.needsExtension {
			t.Errorf("%q did not become a complete recent constraint: %+v", test.query, parsed)
		}
	}
}

func TestSemanticConstraintsRatingsAndRuntime(t *testing.T) {
	tests := []struct {
		query, language, intent string
		rating                  float64
		runtimeMin, runtimeMax  int
		voteCount               int
		sort                    string
	}{
		{query: "movies rated 7 or higher", language: "en", intent: "rating_min:7", rating: 7},
		{query: "films note au moins 8", language: "fr", intent: "rating_min:8", rating: 8},
		{query: "movies rated 7.5 or higher", language: "en", intent: "rating_min:7.5", rating: 7.5},
		{query: "films note au moins 7,5", language: "fr", intent: "rating_min:7.5", rating: 7.5},
		{query: "movies rating at least 7 out of 10", language: "en", intent: "rating_min:7", rating: 7},
		{query: "films note au moins 7 sur 10", language: "fr", intent: "rating_min:7", rating: 7},
		{query: "best rated movies", language: "en", intent: "rating_quality:high", rating: 7.5, voteCount: 100, sort: "vote_average.desc"},
		{query: "films les mieux notes", language: "fr", intent: "rating_quality:high", rating: 7.5, voteCount: 100, sort: "vote_average.desc"},
		{query: "movies under 2 hours", language: "en", intent: "runtime_max:120", runtimeMax: 120},
		{query: "films de moins de 90 minutes", language: "fr", intent: "runtime_max:90", runtimeMax: 90},
		{query: "movies over 100 minutes", language: "en", intent: "runtime_min:100", runtimeMin: 100},
		{query: "films de plus de 2 heures", language: "fr", intent: "runtime_min:120", runtimeMin: 120},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			parsed := constrainedSemanticQuery(test.query, test.language, nil)
			if !hasSemanticIntent(parsed.intents, test.intent) || parsed.constraints.sort != test.sort {
				t.Fatalf("intent/sort = %+v/%q", parsed.intents, parsed.constraints.sort)
			}
			if test.rating != 0 && (parsed.constraints.voteAverageMin == nil || *parsed.constraints.voteAverageMin != test.rating) {
				t.Fatalf("rating = %v, want %v", parsed.constraints.voteAverageMin, test.rating)
			}
			if test.voteCount != 0 && (parsed.constraints.voteCountMin == nil || *parsed.constraints.voteCountMin != test.voteCount) {
				t.Fatalf("vote count = %v, want %d", parsed.constraints.voteCountMin, test.voteCount)
			}
			if test.runtimeMin != 0 && (parsed.constraints.runtimeMin == nil || *parsed.constraints.runtimeMin != test.runtimeMin) {
				t.Fatalf("runtime min = %v, want %d", parsed.constraints.runtimeMin, test.runtimeMin)
			}
			if test.runtimeMax != 0 && (parsed.constraints.runtimeMax == nil || *parsed.constraints.runtimeMax != test.runtimeMax) {
				t.Fatalf("runtime max = %v, want %d", parsed.constraints.runtimeMax, test.runtimeMax)
			}
		})
	}
}

func TestSemanticConstraintsPreserveLikelyNumericTitles(t *testing.T) {
	for _, title := range []string{"Blade Runner 2049", "1917"} {
		parsed := constrainedSemanticQuery(title, "en", nil)
		if parsed.titleQuery != title || parsed.constraints.releaseDateFrom != "" || hasSemanticConstraintIntent(parsed.intents) == true {
			t.Errorf("numeric title %q was constrained: %+v", title, parsed)
		}
	}
}

func TestSemanticConstraintsCombineExactTitleAndFilter(t *testing.T) {
	parsed := constrainedSemanticQuery("Blade Runner 2049 rated 8 or higher", "en", nil)
	if parsed.titleQuery != "Blade Runner 2049" || !parsed.needsExtension || parsed.constraints.voteAverageMin == nil || *parsed.constraints.voteAverageMin != 8 {
		t.Fatalf("exact title plus filter = %+v", parsed)
	}
}

func TestSemanticConstraintsExcludeKnownGenres(t *testing.T) {
	for _, test := range []struct{ query, language, genre string }{
		{"movies without horror", "en", "horror"}, {"no war films", "en", "war"},
		{"films sans horreur", "fr", "horror"}, {"films pas de guerre", "fr", "war"},
	} {
		parsed := constrainedSemanticQuery(test.query, test.language, nil)
		if !hasSemanticIntent(parsed.intents, "exclude_genre:"+test.genre) || !slices.Equal(parsed.constraints.excludedGenres, []string{test.genre}) || slices.Contains(parsed.genres, test.genre) || hasSemanticIntent(parsed.intents, "genre:"+test.genre) {
			t.Errorf("negative genre %q = %+v", test.query, parsed)
		}
	}
}

func TestSemanticConstraintsHonorExcludedChipsAsLiteralText(t *testing.T) {
	tests := []struct{ query, id, title string }{
		{"Alien released in 1979", "release_year:1979", "Alien released in 1979"},
		{"movies without horror", "exclude_genre:horror", "without horror"},
		{"movies under 2 hours", "runtime_max:120", "under 2 hours"},
	}
	for _, test := range tests {
		excluded := map[string]struct{}{test.id: {}}
		parsed := constrainedSemanticQuery(test.query, "en", excluded)
		if hasSemanticIntent(parsed.intents, test.id) || parsed.titleQuery != test.title || !parsed.needsExtension {
			t.Errorf("excluded %s = intents %+v title %q extension=%t", test.id, parsed.intents, parsed.titleQuery, parsed.needsExtension)
		}
	}
}

func TestSemanticConstraintsRejectInvalidValuesAndRanges(t *testing.T) {
	for _, query := range []string{"released in 1700", "released in 2027", "minimum rating 11", "under 0 minutes", "over 601 minutes"} {
		parsed := constrainedSemanticQuery(query, "en", nil)
		if hasSemanticConstraintIntent(parsed.intents) || parsed.titleQuery != query {
			t.Errorf("invalid constraint %q was consumed: %+v", query, parsed)
		}
	}
	parsed := constrainedSemanticQuery("over 3 hours under 2 hours", "en", nil)
	if parsed.constraints.runtimeMin == nil || *parsed.constraints.runtimeMin != 180 || parsed.constraints.runtimeMax != nil || !slices.Contains(semanticIntentIDs(parsed.intents), "runtime_min:180") {
		t.Fatalf("contradictory runtime range was accepted: %+v", parsed)
	}
}

func TestSemanticConstraintsSafeEmptyAndKnownIDs(t *testing.T) {
	parsed := constrainedSemanticQuery("", "fr", nil)
	if parsed.titleQuery != "" || parsed.needsExtension || parsed.constraints.excludedGenres == nil || len(parsed.intents) != 0 {
		t.Fatalf("empty constraints unsafe: %+v", parsed)
	}
	valid := []string{"release_year:1999", "release_decade:1990s", "release_recency:recent", "rating_min:7", "rating_quality:high", "runtime_min:90", "runtime_max:120", "exclude_genre:horror"}
	for _, id := range valid {
		if !knownSemanticConstraintIntentID(id) {
			t.Errorf("known constraint rejected: %s", id)
		}
	}
	invalid := []string{"release_year:99", "release_decade:1995s", "release_recency:new", "rating_min:11", "rating_min:07", "runtime_min:0", "runtime_max:601", "exclude_genre:not_real"}
	for _, id := range invalid {
		if knownSemanticConstraintIntentID(id) {
			t.Errorf("invalid constraint accepted: %s", id)
		}
	}
}

func hasSemanticIntent(intents []SemanticSearchIntent, id string) bool {
	return slices.Contains(semanticIntentIDs(intents), id)
}

func hasSemanticConstraintIntent(intents []SemanticSearchIntent) bool {
	for _, intent := range intents {
		if knownSemanticConstraintIntentID(intent.ID) {
			return true
		}
	}
	return false
}

func semanticIntentIDs(intents []SemanticSearchIntent) []string {
	ids := make([]string, len(intents))
	for index, intent := range intents {
		ids[index] = intent.ID
	}
	return ids
}
