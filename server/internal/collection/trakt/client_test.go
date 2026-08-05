package trakt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/collection"
)

func TestResolveCollectionSourceSendsTraktContractAndNormalizesItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lists/123/items/movies" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("trakt-api-key") != "client-id" || r.Header.Get("trakt-api-version") != "2" {
			t.Fatalf("unexpected Trakt headers: %v", r.Header)
		}
		query := r.URL.Query()
		if query.Get("extended") != "full" || query.Get("page") != "2" || query.Get("limit") != "100" || query.Get("sort_by") != "votes" || query.Get("sort_how") != "desc" {
			t.Fatalf("unexpected query: %v", query)
		}
		w.Header().Set("X-Pagination-Page-Count", "3")
		_, _ = w.Write([]byte(`[{
			"rank":1,"listed_at":"2026-01-02T03:04:05Z","type":"movie",
			"movie":{"title":"Blade Runner","year":1982,"overview":"A replicant hunter.","released":"1982-06-25","rating":8.1,"votes":42000,
			"ids":{"trakt":1,"slug":"blade-runner-1982","imdb":"tt0083658","tmdb":78}}
		}]`))
	}))
	defer server.Close()

	client := newWithBaseURL("client-id", server.URL, server.Client())
	page, err := client.ResolveCollectionSource(context.Background(), collection.TraktSource{
		ListID: 123, MediaType: collection.MediaTypeMovie, SortBy: "votes", SortHow: "desc",
	}, 2)
	if err != nil {
		t.Fatalf("resolve Trakt list: %v", err)
	}
	if page.Page != 2 || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("unexpected page: %+v", page)
	}
	item := page.Items[0]
	if item.ID != "tmdb:78" || item.Title != "Blade Runner" || item.ExternalIDs["imdb"] != "tt0083658" || item.ExternalIDs["trakt"] != "1" {
		t.Fatalf("unexpected normalized item: %+v", item)
	}
}

func TestResolveCollectionSourceConsumesSharedItemBudgetBeforeNormalization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"movie":{"title":"One","ids":{"trakt":1}}},
			{"movie":{"title":"Two","ids":{"trakt":2}}}
		]`))
	}))
	defer server.Close()
	ctx, budget := addon.WithPayloadBudget(context.Background(), 1024, 1)
	defer budget.Cancel()

	client := newWithBaseURL("client-id", server.URL, server.Client())
	_, err := client.ResolveCollectionSource(addon.WithPayloadBudgetSource(ctx), collection.TraktSource{
		ListID: 123, MediaType: collection.MediaTypeMovie, SortBy: "rank", SortHow: "asc",
	}, 1)
	if err == nil || !budget.Exceeded() {
		t.Fatalf("Trakt item budget error = %v, exceeded=%t", err, budget.Exceeded())
	}
}
