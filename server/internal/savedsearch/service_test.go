package savedsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/watchstate"
)

type syntheticSmartCatalog struct {
	calls int
	query watchstate.SmartCatalogQuery
}

func (catalog *syntheticSmartCatalog) ListSmartCatalogItems(_ context.Context, _ auth.Principal, query watchstate.SmartCatalogQuery) (watchstate.SmartCatalogPage, error) {
	catalog.calls++
	catalog.query = query
	items := make([]watchstate.CatalogTitle, query.Limit)
	for index := range items { items[index].ID = string(rune(index + 1)) }
	return watchstate.SmartCatalogPage{Items: items, Total: 100_000}, nil
}

func TestEvaluateSmartRuleUsesOneBoundedCatalogCallForHundredThousandCandidates(t *testing.T) {
	catalog := &syntheticSmartCatalog{}
	service := NewService(nil, catalog)
	page, err := service.evaluateSmartRule(context.Background(), auth.Principal{}, SmartCollection{
		Sort: "rating", Rules: Rule{Type: "genre", Operator: "equals", Value: "drama"},
	}, 250, 24)
	if err != nil { t.Fatalf("evaluate synthetic catalog: %v", err) }
	if catalog.calls != 1 || catalog.query.Offset != 5976 || catalog.query.Limit != 24 {
		t.Fatalf("catalog calls=%d query=%+v", catalog.calls, catalog.query)
	}
	if page.Total != 100_000 || page.TotalPages != 4167 || len(page.Items) != 24 {
		t.Fatalf("synthetic page=%+v", page)
	}
}

func TestNormalizeSmartCollectionAcceptsClosedNestedAST(t *testing.T) {
	input, err := normalizeSmartCollection(SmartCollectionInput{
		Name: "  Great dramas  ", Sort: "RATING",
		Rules: Rule{Type: "all", Rules: []Rule{
			{Type: "media_type", Operator: "one_of", Values: []string{"movie", "series"}},
			{Type: "genre", Operator: "equals", Value: " Drama "},
			{Type: "any", Rules: []Rule{
				{Type: "year", Operator: "gte", Number: new(float64(2020))},
				{Type: "rating", Operator: "gte", Number: new(float64(8))},
			}},
		}},
	}, false)
	if err != nil { t.Fatalf("normalize smart collection: %v", err) }
	if input.Name != "Great dramas" || input.Sort != "rating" || input.Rules.Rules[1].Value != "drama" {
		t.Fatalf("input was not normalized: %+v", input)
	}
}

func TestNormalizeSmartCollectionRejectsOpenOrAmbiguousRules(t *testing.T) {
	tests := []Rule{
		{Type: "sql", Operator: "equals", Value: "true"},
		{Type: "genre", Operator: "contains", Value: "drama"},
		{Type: "rating", Operator: "gte", Number: new(float64(11))},
		{Type: "status", Operator: "equals", Value: "arbitrary-provider-status"},
		{Type: "media_type", Operator: "one_of", Values: []string{"movie", "movie"}},
		{Type: "all", Rules: nil},
		{Type: "all", Value: "unexpected", Rules: []Rule{{Type: "genre", Operator: "equals", Value: "drama"}}},
	}
	for _, rule := range tests {
		_, err := normalizeSmartCollection(SmartCollectionInput{Name: "Invalid", Sort: "title", Rules: rule}, false)
		if !errors.Is(err, ErrInvalidInput) { t.Errorf("rule %+v error = %v, want ErrInvalidInput", rule, err) }
	}
}


func TestSavedSearchNormalizationRequiresBoundedNamesAndOptimisticRevision(t *testing.T) {
	if _, err := normalizeSavedSearch(SavedSearchInput{Name: "Films", Query: "space", Sort: "relevance", ExpectedRevision: 0}, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("update without revision error = %v", err)
	}
	tooLong := make([]rune, maximumNameRunes+1)
	for index := range tooLong { tooLong[index] = 'a' }
	if _, err := normalizeSavedSearch(SavedSearchInput{Name: string(tooLong), Query: "space", Sort: "relevance"}, false); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overlong name error = %v", err)
	}
}
