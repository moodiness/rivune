package jellyfin

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestParseItemQueryAcceptsCaseInsensitiveNames(t *testing.T) {
	values := url.Values{
		"searchterm":       {"  Signal  "},
		"PARENTID":         {"A0B1C2D3-E4F5-4678-89AB-0123456789AB"},
		"startINDEX":       {"12"},
		"LIMIT":            {"25"},
		"recursive":        {"TRUE"},
		"includeitemtypes": {"Movie, Episode"},
		"FIELDS":           {"Overview", "ProviderIds,Path"},
		"sortby":           {"SortName"},
		"sortorder":        {"descending"},
		"enableuserdata":   {"false"},
		"IDS":              {"12345678-1234-4234-8234-123456789abc"},
	}
	query, err := ParseItemQuery(values)
	if err != nil {
		t.Fatalf("ParseItemQuery: %v", err)
	}
	if query.SearchTerm != "Signal" || query.ParentId != "a0b1c2d3-e4f5-4678-89ab-0123456789ab" ||
		query.StartIndex != 12 || query.Limit != 25 || !query.Recursive || query.EnableUserData ||
		query.SortOrder != "Descending" || len(query.IncludeItemTypes) != 2 || len(query.Fields) != 3 || len(query.Ids) != 1 {
		t.Fatalf("unexpected parsed query: %#v", query)
	}
}

func TestParseItemQueryCanonicalizesCompactIDs(t *testing.T) {
	tests := []struct {
		name   string
		values url.Values
	}{
		{
			name: "lowerCamel",
			values: url.Values{
				"parentId": {"A0B1C2D3E4F5467889AB0123456789AB"},
				"ids":      {"12345678123442348234123456789ABC"},
			},
		},
		{
			name: "PascalCase",
			values: url.Values{
				"ParentId": {"A0B1C2D3E4F5467889AB0123456789AB"},
				"Ids":      {"12345678123442348234123456789ABC"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, err := ParseItemQuery(test.values)
			if err != nil {
				t.Fatalf("ParseItemQuery: %v", err)
			}
			if got, want := query.ParentId, "a0b1c2d3-e4f5-4678-89ab-0123456789ab"; got != want {
				t.Fatalf("ParentId = %q, want %q", got, want)
			}
			if len(query.Ids) != 1 || query.Ids[0] != "12345678-1234-4234-8234-123456789abc" {
				t.Fatalf("Ids = %#v, want one canonical UUID", query.Ids)
			}
		})
	}
}

func TestParseItemQueryDefaults(t *testing.T) {
	query, err := ParseItemQuery(nil)
	if err != nil {
		t.Fatal(err)
	}
	if query.StartIndex != 0 || query.Limit != DefaultQueryLimit || !query.EnableUserData || query.SortOrder != "Ascending" {
		t.Fatalf("unexpected defaults: %#v", query)
	}
}

func TestParseItemQueryAcceptsCountOnlyLimit(t *testing.T) {
	query, err := ParseItemQuery(url.Values{"Limit": {"0"}})
	if err != nil {
		t.Fatalf("ParseItemQuery: %v", err)
	}
	if query.Limit != 0 {
		t.Fatalf("Limit = %d, want count-only zero", query.Limit)
	}
}

func TestParseItemQueryRejectsAmbiguityAndBounds(t *testing.T) {
	tests := []url.Values{
		{"Limit": {strconv.Itoa(MaximumQueryLimit + 1)}},
		{"StartIndex": {strconv.Itoa(MaximumStartIndex + 1)}},
		{"SearchTerm": {strings.Repeat("x", MaximumSearchTermBytes+1)}},
		{"Recursive": {"sometimes"}},
		{"SortOrder": {"sideways"}},
		{"Ids": {"not-a-uuid"}},
		{"Limit": {"10"}, "limit": {"20"}},
	}
	for _, values := range tests {
		if _, err := ParseItemQuery(values); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("ParseItemQuery(%v) error = %v, want ErrInvalidQuery", values, err)
		}
	}
}

func TestParseItemQueryHasWholeRequestBudget(t *testing.T) {
	values := make(url.Values)
	for index := 0; index <= MaximumQueryParameters; index++ {
		values["ignored"+strconv.Itoa(index)] = []string{"value"}
	}
	if _, err := ParseItemQuery(values); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("oversized parameter set error = %v, want ErrInvalidQuery", err)
	}
}
