package artwork

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/addon"
	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/metadata"
)

func TestPresentAddonResourcesOverlaysCanonicalArtworkAndHidesProviderURLs(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	const titleID = "88888888-8888-4888-8888-888888888888"
	const imdbID = "tt0948470"
	if _, err := pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID); err != nil {
		t.Fatalf("clear canonical title fixture: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID) })
	poster := fixture.URL + "/fanart-poster.png"
	background := fixture.URL + "/fanart-background.png"
	logo := fixture.URL + "/fanart-logo.png"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO titles (id, media_type, display_title, poster_url, background_url)
		VALUES ($1::uuid, 'movie', 'Spider-Man', 'https://tmdb.example/poster.jpg', 'https://tmdb.example/background.jpg')
	`, titleID); err != nil {
		t.Fatalf("insert canonical title: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO title_external_ids (title_id, provider, external_id)
		VALUES ($1::uuid, 'imdb', $2), ($1::uuid, 'tmdb', '1930')
	`, titleID, imdbID); err != nil {
		t.Fatalf("insert canonical title identities: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO title_metadata (title_id, provider, language, payload, expires_at)
		VALUES ($1::uuid, 'tmdb', 'en-US', jsonb_build_object(
			'id', $1::text, 'mediaType', 'movie', 'title', 'Spider-Man',
			'posterUrl', $2::text, 'backdropUrl', $3::text, 'logoUrl', $4::text,
			'genres', '[]'::jsonb, 'voteAverage', 0, 'voteCount', 0, 'externalIds', '{}'::jsonb
		), now() + interval '1 day')
	`, titleID, poster, background, logo); err != nil {
		t.Fatalf("insert canonical title metadata: %v", err)
	}

	results := []addon.ResourceResult{
		{
			Resource: "catalog", Type: "movie", ID: "netflix",
			Payload: json.RawMessage(`{"metas":[{"id":"tt0948470","type":"movie","name":"Spider-Man","poster":"` + fixture.URL + `/addon-poster.png","background":"` + fixture.URL + `/addon-background.png","logo":"` + fixture.URL + `/addon-logo.png","customNumber":9007199254740993}]}`),
		},
		{
			Resource: "meta", Type: "tv", ID: imdbID,
			Payload: json.RawMessage(`{"meta":[{"id":"tt0948470","type":"tv","name":"Live channel","poster":"` + fixture.URL + `/live-tv.png"}]}`),
		},
	}
	service.PresentAddonResources(context.Background(), results)

	var envelope struct {
		Metas []map[string]any `json:"metas"`
	}
	if err := json.Unmarshal(results[0].Payload, &envelope); err != nil || len(envelope.Metas) != 1 {
		t.Fatalf("decode presented catalog: %v payload=%s", err, results[0].Payload)
	}
	meta := envelope.Metas[0]
	for field, upstream := range map[string]string{"poster": poster, "background": background, "logo": logo} {
		normalized, err := normalizeURL(upstream, false)
		if err != nil {
			t.Fatalf("normalize expected %s: %v", field, err)
		}
		expected := publicPrefix + artworkKey(normalized)
		if meta[field] != expected {
			t.Fatalf("%s = %#v, want %q", field, meta[field], expected)
		}
	}
	if meta["name"] != "Spider-Man" || !strings.Contains(string(results[0].Payload), `"customNumber":9007199254740993`) {
		t.Fatalf("non-artwork catalog values changed: %s", results[0].Payload)
	}
	if strings.Contains(string(results[0].Payload), fixture.URL) {
		t.Fatalf("presented catalog leaked an upstream artwork URL: %s", results[0].Payload)
	}
	var liveEnvelope struct {
		Meta []map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(results[1].Payload, &liveEnvelope); err != nil || len(liveEnvelope.Meta) != 1 {
		t.Fatalf("decode presented live-TV metadata: %v payload=%s", err, results[1].Payload)
	}
	liveUpstream := fixture.URL + "/live-tv.png"
	normalizedLive, err := normalizeURL(liveUpstream, false)
	if err != nil {
		t.Fatalf("normalize live-TV artwork: %v", err)
	}
	if got, want := liveEnvelope.Meta[0]["poster"], publicPrefix+artworkKey(normalizedLive); got != want {
		t.Fatalf("live-TV poster = %#v, want localized provider art %q", got, want)
	}

	var registered int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM artwork_cache
		WHERE source_url = ANY($1::text[])
	`, []string{fixture.URL + "/addon-poster.png", fixture.URL + "/addon-background.png", fixture.URL + "/addon-logo.png"}).Scan(&registered); err != nil {
		t.Fatalf("query addon artwork registrations: %v", err)
	}
	if registered != 0 {
		t.Fatalf("registered %d superseded addon artwork URLs", registered)
	}
}

func TestPresentResolvedFolderLocalizesFallbackArtworkAndRawPayload(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	poster := fixture.URL + "/fallback-poster.png"
	cover := fixture.URL + "/folder-cover.png"
	resolved := collection.ResolvedFolder{
		Folder: collection.Folder{CoverImageURL: cover},
		Items: []collection.Item{{
			ID: "unknown:1", MediaType: "movie", Title: "Fallback", PosterURL: poster,
			ExternalIDs: map[string]string{}, Raw: json.RawMessage(`{"poster":"` + poster + `","name":"Fallback"}`),
		}},
	}
	service.PresentResolvedFolder(context.Background(), &resolved)

	if !strings.HasPrefix(resolved.Items[0].PosterURL, publicPrefix) || !strings.HasPrefix(resolved.Folder.CoverImageURL, publicPrefix) {
		t.Fatalf("fallback artwork was not localized: %#v", resolved)
	}
	if strings.Contains(string(resolved.Items[0].Raw), fixture.URL) || !strings.Contains(string(resolved.Items[0].Raw), publicPrefix) {
		t.Fatalf("raw provider artwork was not hidden: %s", resolved.Items[0].Raw)
	}
	if resolved.Items[0].Title != "Fallback" {
		t.Fatalf("non-artwork item field changed: %#v", resolved.Items[0])
	}
}

func TestPresentResponseBoundaryArtworkUsesSameOriginReferences(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	logo := fixture.URL + "/addon-logo.png"
	background := fixture.URL + "/addon-background.png"
	addons := []addon.InstalledAddon{{
		Manifest: json.RawMessage(`{"id":"example","logo":"` + logo + `","background":"` + background + `","customNumber":9007199254740993}`),
	}}
	service.PresentInstalledAddons(context.Background(), addons)
	if strings.Contains(string(addons[0].Manifest), fixture.URL) {
		t.Fatalf("presented addon manifest leaked provider artwork: %s", addons[0].Manifest)
	}
	if !strings.Contains(string(addons[0].Manifest), `"customNumber":9007199254740993`) {
		t.Fatalf("presented addon manifest changed an unrelated value: %s", addons[0].Manifest)
	}

	image := fixture.URL + "/lookup.png"
	results := []collection.LookupResult{{ID: 42, Name: "Lookup", ImageURL: image}}
	service.LocalizeCollectionLookupResults(context.Background(), results)
	normalized, err := normalizeURL(image, false)
	if err != nil {
		t.Fatalf("normalize lookup image: %v", err)
	}
	if got, want := results[0].ImageURL, publicPrefix+artworkKey(normalized); got != want {
		t.Fatalf("lookup image = %q, want %q", got, want)
	}
	if results[0].ID != 42 || results[0].Name != "Lookup" {
		t.Fatalf("lookup result changed unrelated values: %#v", results[0])
	}

	collectionBackground := fixture.URL + "/collection-background.png"
	cover := fixture.URL + "/folder-cover.png"
	collections := []collection.Collection{{
		Title:            "Collection",
		BackdropImageURL: collectionBackground,
		Folders:          []collection.Folder{{Title: "Folder", CoverImageURL: cover}},
	}}
	service.PresentCollections(context.Background(), collections)
	if !strings.HasPrefix(collections[0].BackdropImageURL, publicPrefix) ||
		!strings.HasPrefix(collections[0].Folders[0].CoverImageURL, publicPrefix) {
		t.Fatalf("collection artwork was not localized: %#v", collections[0])
	}
	input := collection.SaveInput{
		Title:            collections[0].Title,
		BackdropImageURL: collections[0].BackdropImageURL,
		Folders:          collections[0].Folders,
	}
	service.RestoreCollectionSaveInput(context.Background(), &input)
	if input.BackdropImageURL != collectionBackground || input.Folders[0].CoverImageURL != cover {
		t.Fatalf("collection artwork sources were not restored before persistence: %#v", input)
	}
}

func TestLocalizeMetadataIncludesCastProfilesAndNestedSeriesArtwork(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	movieProfile := fixture.URL + "/movie-cast.png"
	seriesProfile := fixture.URL + "/series-cast.png"
	moviePageProfile := fixture.URL + "/movie-page-cast.png"
	seriesPageProfile := fixture.URL + "/series-page-cast.png"
	seriesPageSeasonPoster := fixture.URL + "/series-page-season.png"
	movie := metadata.Movie{
		PosterURL: fixture.URL + "/movie-poster.png",
		Cast:      []metadata.CastMember{{ID: "1", Name: "Movie Actor", ProfileURL: movieProfile}},
	}
	series := metadata.Series{
		BackdropURL: fixture.URL + "/series-backdrop.png",
		Cast:        []metadata.CastMember{{ID: "2", Name: "Series Actor", ProfileURL: seriesProfile}},
	}
	moviePage := metadata.MoviePage{Items: []metadata.Movie{{
		Cast: []metadata.CastMember{{ID: "3", Name: "Movie Page Actor", ProfileURL: moviePageProfile}},
	}}}
	seriesPage := metadata.SeriesPage{Items: []metadata.Series{{
		Cast:    []metadata.CastMember{{ID: "4", Name: "Series Page Actor", ProfileURL: seriesPageProfile}},
		Seasons: []metadata.SeasonSummary{{SeasonNumber: 1, PosterURL: seriesPageSeasonPoster}},
	}}}

	service.LocalizeMovie(context.Background(), &movie)
	service.LocalizeSeries(context.Background(), &series)
	service.LocalizeMoviePage(context.Background(), &moviePage)
	service.LocalizeSeriesPage(context.Background(), &seriesPage)

	for label, value := range map[string]string{
		"movie profile":       movie.Cast[0].ProfileURL,
		"series profile":      series.Cast[0].ProfileURL,
		"movie page profile":  moviePage.Items[0].Cast[0].ProfileURL,
		"series page profile": seriesPage.Items[0].Cast[0].ProfileURL,
		"series page season":  seriesPage.Items[0].Seasons[0].PosterURL,
	} {
		if !strings.HasPrefix(value, publicPrefix) {
			t.Fatalf("%s was not localized through %q: %q", label, publicPrefix, value)
		}
		if strings.Contains(value, fixture.URL) {
			t.Fatalf("%s leaked upstream URL %q", label, value)
		}
	}
	for upstream, localized := range map[string]string{
		movieProfile:           movie.Cast[0].ProfileURL,
		seriesProfile:          series.Cast[0].ProfileURL,
		moviePageProfile:       moviePage.Items[0].Cast[0].ProfileURL,
		seriesPageProfile:      seriesPage.Items[0].Cast[0].ProfileURL,
		seriesPageSeasonPoster: seriesPage.Items[0].Seasons[0].PosterURL,
	} {
		normalized, err := normalizeURL(upstream, false)
		if err != nil {
			t.Fatalf("normalize artwork URL: %v", err)
		}
		if want := publicPrefix + artworkKey(normalized); localized != want {
			t.Fatalf("localized artwork = %q, want %q", localized, want)
		}
	}
}
