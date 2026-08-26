package watchstate

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompileSmartCatalogRuleBindsEveryUserValue(t *testing.T) {
	rule := SmartCatalogRule{Type: "all", Rules: []SmartCatalogRule{
		{Type: "genre", Operator: "equals", Value: "Drama'); DROP TABLE titles;--"},
		{Type: "any", Rules: []SmartCatalogRule{
			{Type: "media_type", Operator: "one_of", Values: []string{"movie", "series"}},
			{Type: "rating", Operator: "gte", Number: 8.25},
		}},
	}}
	predicate, arguments, err := compileSmartCatalogRule(rule, 2)
	if err != nil { t.Fatalf("compile rule: %v", err) }
	if strings.Contains(predicate, "DROP TABLE") || strings.Contains(predicate, "Drama") {
		t.Fatalf("untrusted value entered SQL: %s", predicate)
	}
	want := []any{"Drama'); DROP TABLE titles;--", []string{"movie", "series"}, 8.25}
	if !reflect.DeepEqual(arguments, want) { t.Fatalf("arguments=%#v want=%#v", arguments, want) }
	for _, placeholder := range []string{"$2", "$3", "$4"} {
		if !strings.Contains(predicate, placeholder) { t.Fatalf("predicate %q missing %s", predicate, placeholder) }
	}
}

func TestSmartCatalogSortIsClosed(t *testing.T) {
	if _, err := smartCatalogSortSQL("title; DROP TABLE titles"); err == nil {
		t.Fatal("arbitrary sort SQL was accepted")
	}
	for _, value := range []string{"title", "year", "rating", "added"} {
		if _, err := smartCatalogSortSQL(value); err != nil { t.Fatalf("sort %q: %v", value, err) }
	}
}
