package jellyfin

import (
	"errors"
	"net/url"
	"reflect"
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
	if query.StartIndex != 0 || query.Limit != DefaultQueryLimit || query.RequestedLimit != DefaultQueryLimit || !query.EnableUserData || query.SortOrder != "Ascending" {
		t.Fatalf("unexpected defaults: %#v", query)
	}
}

func TestParseItemQueryAcceptsCountOnlyLimit(t *testing.T) {
	query, err := ParseItemQuery(url.Values{"Limit": {"0"}})
	if err != nil {
		t.Fatalf("ParseItemQuery: %v", err)
	}
	if query.Limit != 0 || query.RequestedLimit != 0 {
		t.Fatalf("limits = effective %d requested %d, want count-only zero", query.Limit, query.RequestedLimit)
	}
}

func TestParseItemQueryPreservesBoundedLargeLimitsForChunkedRoutes(t *testing.T) {
	for _, requested := range []int{201, 1000, MaximumLatestQueryLimit} {
		query, err := ParseItemQuery(url.Values{"Limit": {strconv.Itoa(requested)}})
		if err != nil {
			t.Fatalf("ParseItemQuery(Limit=%d): %v", requested, err)
		}
		if query.Limit != MaximumQueryLimit || query.RequestedLimit != requested {
			t.Fatalf("ParseItemQuery(Limit=%d) limits = effective %d requested %d", requested, query.Limit, query.RequestedLimit)
		}
	}
}

func TestParseItemQueryRejectsAmbiguityAndBounds(t *testing.T) {
	tests := []url.Values{
		{"Limit": {"-1"}},
		{"Limit": {strconv.Itoa(MaximumStartIndex + 1)}},
		{"Limit": {strconv.Itoa(MaximumLatestQueryLimit + 1)}},
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

func TestParseItemQueryParsesRepeatedFilterAndImageMatrix(t *testing.T) {
	values := url.Values{
		"MEDIATYPES":             {"Video", "Unknown"},
		"excludeitemtypes":       {"Series,Season"},
		"Filters":                {"IsFavorite,isResumable"},
		"isplayed":               {"false"},
		"ISFAVORITE":             {"true"},
		"isresumable":            {"TRUE"},
		"MinCommunityRating":     {"8.25"},
		"HasSubtitles":           {"true"},
		"Genres":                 {"Drama", "Science Fiction|Comedy"},
		"genreids":               {"genre-one,genre-two"},
		"Years":                  {"2024", "2025,2026"},
		"officialratings":        {"PG-13|R"},
		"Tags":                   {"Featured", "Classic|Archive"},
		"personids":              {"person-one,person-two"},
		"EnableImages":           {"false"},
		"enableimagetypes":       {"Primary,Backdrop"},
		"ImageTypeLimit":         {"0"},
		"EnableTotalRecordCount": {"false"},
	}
	query, err := ParseItemQuery(values)
	if err != nil {
		t.Fatalf("ParseItemQuery: %v", err)
	}
	if !reflect.DeepEqual(query.MediaTypes, []string{"Video", "Unknown"}) ||
		!reflect.DeepEqual(query.ExcludeItemTypes, []string{"Series", "Season"}) ||
		!reflect.DeepEqual(query.Filters, []string{"isfavorite", "isresumable"}) ||
		query.IsPlayed == nil || *query.IsPlayed || query.IsFavorite == nil || !*query.IsFavorite || query.IsResumable == nil || !*query.IsResumable ||
		query.MinCommunityRating == nil || *query.MinCommunityRating != 8.25 || query.HasSubtitles == nil || !*query.HasSubtitles ||
		!reflect.DeepEqual(query.Genres, []string{"Drama", "Science Fiction", "Comedy"}) ||
		!reflect.DeepEqual(query.OfficialRatings, []string{"PG-13", "R"}) ||
		!reflect.DeepEqual(query.Tags, []string{"Featured", "Classic", "Archive"}) ||
		!reflect.DeepEqual(query.Years, []int{2024, 2025, 2026}) || query.EnableImages || query.ImageTypeLimit != 0 || query.EnableTotalRecordCount {
		t.Fatalf("unexpected filter matrix: %#v", query)
	}
}

func TestParseItemQueryRejectsAmbiguousNewScalars(t *testing.T) {
	for _, values := range []url.Values{
		{"IsPlayed": {"true", "false"}},
		{"IsFavorite": {""}},
		{"IsResumable": {"sometimes"}},
		{"EnableImages": {"true"}, "enableimages": {"false"}},
		{"EnableTotalRecordCount": {"true", "true"}},
		{"ImageTypeLimit": {"-1"}},
		{"MinCommunityRating": {"8", "9"}},
		{"MinCommunityRating": {"NaN"}},
		{"MinCommunityRating": {"10.1"}},
		{"HasSubtitles": {"true", "true"}},
		{"HasSubtitles": {"sometimes"}},
		{"Filters": {"HasTrailer"}},
		{"Fields": {"DateCreated"}},
		{"EnableImageTypes": {"Primary,Disc"}},
		{"SearchTerm": {"valid\x00suffix"}},
		{"Genres": {string([]byte{0xff})}},
		{"Genres": {strings.Repeat("x|", MaximumQueryListValues) + "x"}},
		{"Tags": {strings.Repeat("x", MaximumQueryValueBytes+1)}},
	} {
		if _, err := ParseItemQuery(values); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("ParseItemQuery(%v) error = %v, want ErrInvalidQuery", values, err)
		}
	}
}

func TestParseItemQueryIgnoresEmptyRepeatedListSegments(t *testing.T) {
	query, err := ParseItemQuery(url.Values{
		"Genres": {"", "Drama||Comedy", "   "},
		"Years":  {"", "2025,,2026"},
		"Fields": {"", "Overview"},
	})
	if err != nil || !reflect.DeepEqual(query.Genres, []string{"Drama", "Comedy"}) ||
		!reflect.DeepEqual(query.Years, []int{2025, 2026}) || !reflect.DeepEqual(query.Fields, []string{"Overview"}) {
		t.Fatalf("empty list segments query=%#v error=%v", query, err)
	}
}
func TestParseItemQueryUsesPinnedArraySeparators(t *testing.T) {
	query, err := ParseItemQuery(url.Values{
		"Genres":          {"Drama|Science Fiction,Comedy"},
		"OfficialRatings": {"PG-13|TV-MA"},
		"Tags":            {"Featured|Classic"},
		"Studios":         {"Studio One|Studio Two"},
		"PersonIds":       {"person-one,person-two"},
		"FIELDS":          {"Overview"},
		"fields":          {"People"},
	})
	if err != nil {
		t.Fatalf("ParseItemQuery: %v", err)
	}
	if !reflect.DeepEqual(query.Genres, []string{"Drama", "Science Fiction,Comedy"}) ||
		!reflect.DeepEqual(query.OfficialRatings, []string{"PG-13", "TV-MA"}) ||
		!reflect.DeepEqual(query.Tags, []string{"Featured", "Classic"}) ||
		!reflect.DeepEqual(query.Studios, []string{"Studio One", "Studio Two"}) ||
		!reflect.DeepEqual(query.PersonIds, []string{"person-one", "person-two"}) ||
		!reflect.DeepEqual(query.Fields, []string{"Overview", "People"}) {
		t.Fatalf("unexpected separated query: %#v", query)
	}
}

func TestParseItemQueryClampsObservedJellyfinLimits(t *testing.T) {
	for _, requested := range []string{"1000", "1008"} {
		query, err := ParseItemQuery(url.Values{"Limit": {requested}})
		if err != nil || query.Limit != MaximumQueryLimit {
			t.Fatalf("Limit=%s query=%#v error=%v", requested, query, err)
		}
	}
}
