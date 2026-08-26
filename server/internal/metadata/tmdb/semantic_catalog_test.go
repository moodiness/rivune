package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestSemanticCatalogLoadsPrimaryTranslationsGenresAndCountries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/configuration/primary_translations":
			_, _ = response.Write([]byte(`["fr-fr","en-US","fr-FR","invalid-tag-extra"]`))
		case "/genre/movie/list":
			if request.URL.Query().Get("language") != "fr-FR" {
				t.Fatalf("movie genre language = %q", request.URL.Query().Get("language"))
			}
			_, _ = response.Write([]byte(`{"genres":[{"id":10752,"name":"Guerre"}]}`))
		case "/genre/tv/list":
			if request.URL.Query().Get("language") != "fr-FR" {
				t.Fatalf("series genre language = %q", request.URL.Query().Get("language"))
			}
			_, _ = response.Write([]byte(`{"genres":[{"id":10768,"name":"Guerre et politique"}]}`))
		case "/configuration/countries":
			if request.URL.Query().Get("language") != "fr-FR" {
				t.Fatalf("country language = %q", request.URL.Query().Get("language"))
			}
			_, _ = response.Write([]byte(`[{"iso_3166_1":"FR","english_name":"France","native_name":"France"}]`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	client := newWithBaseURL("token", server.URL, server.Client())

	languages, err := client.SemanticCatalogLanguages(context.Background())
	if err != nil || !slices.Equal(languages, []string{"en-US", "fr-FR"}) {
		t.Fatalf("primary translations = %v, error=%v", languages, err)
	}
	locale, err := client.SemanticCatalogLocale(context.Background(), "fr-fr")
	if err != nil {
		t.Fatal(err)
	}
	if len(locale.MovieGenres) != 1 || locale.MovieGenres[0].Name != "Guerre" || len(locale.SeriesGenres) != 1 || locale.SeriesGenres[0].ID != 10768 || len(locale.Countries) != 1 || locale.Countries[0].Code != "FR" {
		t.Fatalf("semantic locale = %+v", locale)
	}
}
