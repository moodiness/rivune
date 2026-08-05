package mdblist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/collection"
)

func TestResolveCollectionSourceSendsMDBListContractAndNormalizesItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lists/123/items" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("apikey") != "api-key" || query.Get("append_to_response") != "poster,description" ||
			query.Get("limit") != "100" || query.Get("offset") != "100" || query.Get("mediatype") != "show" ||
			query.Get("sort") != "score" || query.Get("order") != "desc" {
			t.Fatalf("unexpected query: %v", query)
		}
		w.Header().Set("X-Has-More", "true")
		_, _ = w.Write([]byte(`{
			"movies":[],
			"shows":[{
				"id":1396,"title":"Breaking Bad","imdb_id":"tt0903747","tvdb_id":81189,
				"ids":{"mdblist":"s1396","imdb":"tt0903747","tmdb":1396,"tvdb":81189},
				"mediatype":"show","release_year":2008,"poster":"https://images.example/breaking-bad.jpg",
				"description":"A chemistry teacher builds an empire."
			}],
			"pagination":{"has_more":false}
		}`))
	}))
	defer server.Close()

	client := newWithBaseURL("api-key", server.URL, server.Client())
	page, err := client.ResolveCollectionSource(context.Background(), collection.MDBListSource{
		ListID: 123, MediaType: collection.MediaTypeSeries, Sort: "score", Order: "desc",
	}, 2)
	if err != nil {
		t.Fatalf("resolve MDBList list: %v", err)
	}
	if page.Page != 2 || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	item := page.Items[0]
	if item.ID != "tmdb:1396" || item.MediaType != collection.MediaTypeSeries || item.Title != "Breaking Bad" ||
		item.PosterURL != "https://images.example/breaking-bad.jpg" || item.Description == "" || item.ReleaseInfo != "2008" ||
		item.ExternalIDs["mdblist"] != "s1396" || item.ExternalIDs["imdb"] != "tt0903747" || item.ExternalIDs["tvdb"] != "81189" {
		t.Fatalf("unexpected normalized item: %+v", item)
	}
}

func TestResolveCollectionSourceMapsMDBListProviderErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{name: "invalid API key", status: http.StatusUnauthorized, want: collection.ErrProviderUnavailable},
		{name: "missing list", status: http.StatusNotFound, want: collection.ErrNotFound},
		{name: "rate limited", status: http.StatusTooManyRequests, want: collection.ErrProviderUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			client := newWithBaseURL("api-key", server.URL, server.Client())
			_, err := client.ResolveCollectionSource(context.Background(), collection.MDBListSource{
				ListID: 1, MediaType: collection.MediaTypeMovie, Sort: "rank", Order: "asc",
			}, 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestResolveCollectionSourceConsumesSharedItemBudgetBeforeNormalization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"movies":[
			{"id":1,"title":"One"},
			{"id":2,"title":"Two"}
		],"shows":[]}`))
	}))
	defer server.Close()
	ctx, budget := addon.WithPayloadBudget(context.Background(), 1024, 1)
	defer budget.Cancel()

	client := newWithBaseURL("api-key", server.URL, server.Client())
	_, err := client.ResolveCollectionSource(addon.WithPayloadBudgetSource(ctx), collection.MDBListSource{
		ListID: 123, MediaType: collection.MediaTypeMovie, Sort: "score", Order: "desc",
	}, 1)
	if err == nil || !budget.Exceeded() {
		t.Fatalf("MDBList item budget error = %v, exceeded=%t", err, budget.Exceeded())
	}
}
