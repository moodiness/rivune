package artwork

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestPresentAddonResourcesLocalizesNestedVideoThumbnailsBeforeSerialization(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	upstreamThumbnail := fixture.URL + "/episode-thumbnail.webp"
	upstreamThumbnailURL := fixture.URL + "/episode-thumbnail-url.webp"
	results := []addon.ResourceResult{{
		Resource: "meta", Type: "series", ID: "opaque-series-id",
		Payload: json.RawMessage(`{"meta":{"id":"opaque-series-id","type":"series","videos":[{"id":"opaque-video-id","season":1,"episode":2,"thumbnail":"` + upstreamThumbnail + `","thumbnailUrl":"` + upstreamThumbnailURL + `"}]}}`),
	}}

	service.PresentAddonResources(context.Background(), results)
	serialized, err := json.Marshal(results[0])
	if err != nil {
		t.Fatalf("marshal presented meta resource: %v", err)
	}
	if strings.Contains(string(serialized), fixture.URL) {
		t.Fatalf("presented meta resource leaked upstream video artwork: %s", serialized)
	}

	var resource struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(serialized, &resource); err != nil {
		t.Fatalf("decode serialized resource result: %v", err)
	}
	var envelope struct {
		Meta struct {
			Videos []map[string]any `json:"videos"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(resource.Payload, &envelope); err != nil || len(envelope.Meta.Videos) != 1 {
		t.Fatalf("decode presented meta videos: %v payload=%s", err, resource.Payload)
	}
	video := envelope.Meta.Videos[0]
	for field, upstream := range map[string]string{
		"thumbnail":    upstreamThumbnail,
		"thumbnailUrl": upstreamThumbnailURL,
	} {
		normalized, err := normalizeURL(upstream, false)
		if err != nil {
			t.Fatalf("normalize expected %s: %v", field, err)
		}
		if got, want := video[field], publicPrefix+artworkKey(normalized); got != want {
			t.Fatalf("video %s = %#v, want %q", field, got, want)
		}
	}
	if video["id"] != "opaque-video-id" {
		t.Fatalf("video playback ID changed: %#v", video)
	}

	var registered int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM artwork_cache
		WHERE source_url = ANY($1::text[])
	`, []string{upstreamThumbnail, upstreamThumbnailURL}).Scan(&registered); err != nil {
		t.Fatalf("query video artwork registrations: %v", err)
	}
	if registered != 2 {
		t.Fatalf("registered %d nested video artwork URLs, want 2", registered)
	}
}

func TestPresentAddonResourcesBoundsArtworkRegistrationsPerResponse(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	metas := make([]map[string]any, maximumAddonArtworkURLsPerResponse+32)
	for index := range metas {
		metas[index] = map[string]any{
			"id":     "malicious:" + strconv.Itoa(index),
			"type":   "movie",
			"poster": fixture.URL + "/poster-" + strconv.Itoa(index) + ".png",
		}
	}
	payload, err := json.Marshal(map[string]any{"metas": metas})
	if err != nil {
		t.Fatalf("encode addon response: %v", err)
	}
	results := []addon.ResourceResult{{Resource: "catalog", Type: "movie", Payload: payload}}
	service.PresentAddonResources(context.Background(), results)

	var presented struct {
		Metas []map[string]any `json:"metas"`
	}
	if err := json.Unmarshal(results[0].Payload, &presented); err != nil {
		t.Fatalf("decode presented addon response: %v", err)
	}
	nonEmpty := 0
	for _, meta := range presented.Metas {
		poster, _ := meta["poster"].(string)
		if poster == "" {
			continue
		}
		nonEmpty++
		if !strings.HasPrefix(poster, publicPrefix) {
			t.Fatalf("provider artwork escaped same-origin presentation: %q", poster)
		}
	}
	if nonEmpty != maximumAddonArtworkURLsPerResponse {
		t.Fatalf("presented %d artwork URLs, want bounded %d", nonEmpty, maximumAddonArtworkURLsPerResponse)
	}

	var registered int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM artwork_cache
		WHERE source_url LIKE $1
	`, fixture.URL+"/%").Scan(&registered); err != nil {
		t.Fatalf("count bounded artwork registrations: %v", err)
	}
	if registered != maximumAddonArtworkURLsPerResponse {
		t.Fatalf("registered %d artwork URLs, want bounded %d", registered, maximumAddonArtworkURLsPerResponse)
	}
	if queued := len(service.warmupQueue); queued != maximumAddonArtworkURLsPerResponse {
		t.Fatalf("queued %d artwork warmups, want bounded %d", queued, maximumAddonArtworkURLsPerResponse)
	}
}

func TestPresentResolvedFolderLocalizesFallbackArtworkAndRawPayload(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	poster := fixture.URL + "/fallback-poster.png"
	sourcePoster := fixture.URL + "/source-poster.png"
	cover := fixture.URL + "/folder-cover.png"
	resolved := collection.ResolvedFolder{
		Folder:           collection.Folder{CoverImageURL: cover},
		SourcePosterURLs: map[string]string{"collection-source": sourcePoster},
		Items: []collection.Item{{
			ID: "unknown:1", MediaType: "movie", Title: "Fallback", PosterURL: poster,
			ExternalIDs: map[string]string{}, Raw: json.RawMessage(`{"poster":"` + poster + `","name":"Fallback"}`),
		}},
	}
	service.PresentResolvedFolder(context.Background(), &resolved)

	if !strings.HasPrefix(resolved.Items[0].PosterURL, publicPrefix) ||
		!strings.HasPrefix(resolved.Folder.CoverImageURL, publicPrefix) ||
		!strings.HasPrefix(resolved.SourcePosterURLs["collection-source"], publicPrefix) {
		t.Fatalf("fallback artwork was not localized: %#v", resolved)
	}
	if strings.Contains(string(resolved.Items[0].Raw), fixture.URL) || !strings.Contains(string(resolved.Items[0].Raw), publicPrefix) {
		t.Fatalf("raw provider artwork was not hidden: %s", resolved.Items[0].Raw)
	}
	if resolved.Items[0].Title != "Fallback" {
		t.Fatalf("non-artwork item field changed: %#v", resolved.Items[0])
	}
}

func TestPresentResolvedFolderFallsBackWhenCanonicalPosterCannotBeLocalized(t *testing.T) {
	pool := openArtworkTestPool(t)
	fixture := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fixture.Close()
	service := newArtworkTestService(t, pool, fixture.Client(), 1<<20)

	const titleID = "77777777-7777-4777-8777-777777777777"
	const imdbID = "tt41111628"
	if _, err := pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID); err != nil {
		t.Fatalf("clear canonical fallback fixture: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM titles WHERE id = $1::uuid`, titleID) })
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO titles (id, media_type, display_title, poster_url)
		VALUES ($1::uuid, 'movie', 'Leur vérité', 'file:///unusable-canonical-poster.jpg')
	`, titleID); err != nil {
		t.Fatalf("insert canonical title fallback fixture: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO title_external_ids (title_id, provider, external_id)
		VALUES ($1::uuid, 'imdb', $2)
	`, titleID, imdbID); err != nil {
		t.Fatalf("insert canonical identity fallback fixture: %v", err)
	}

	providerPoster := fixture.URL + "/provider-poster.jpg"
	resolved := collection.ResolvedFolder{Items: []collection.Item{{
		ID: imdbID, MediaType: "movie", Title: "Leur vérité", PosterURL: providerPoster,
		ExternalIDs: map[string]string{"imdb": imdbID}, Raw: json.RawMessage(`{"id":"` + imdbID + `","poster":"` + providerPoster + `"}`),
	}}}
	service.PresentResolvedFolder(context.Background(), &resolved)

	normalized, err := normalizeURL(providerPoster, false)
	if err != nil {
		t.Fatalf("normalize provider poster: %v", err)
	}
	want := publicPrefix + artworkKey(normalized)
	if resolved.Items[0].PosterURL != want {
		t.Fatalf("poster = %q, want localized provider fallback %q", resolved.Items[0].PosterURL, want)
	}
	if strings.Contains(string(resolved.Items[0].Raw), providerPoster) || !strings.Contains(string(resolved.Items[0].Raw), want) {
		t.Fatalf("raw payload did not use hidden provider fallback: %s", resolved.Items[0].Raw)
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
	titleLogo := fixture.URL + "/folder-title-logo.png"
	heroBackdrop := fixture.URL + "/folder-hero-backdrop.png"
	collections := []collection.Collection{{
		Title:            "Collection",
		BackdropImageURL: collectionBackground,
		Folders:          []collection.Folder{{Title: "Folder", CoverImageURL: cover, TitleLogoURL: titleLogo, HeroBackdropURL: heroBackdrop}},
	}}
	service.PresentCollections(context.Background(), collections)
	if !strings.HasPrefix(collections[0].BackdropImageURL, publicPrefix) ||
		!strings.HasPrefix(collections[0].Folders[0].CoverImageURL, publicPrefix) ||
		!strings.HasPrefix(collections[0].Folders[0].TitleLogoURL, publicPrefix) ||
		!strings.HasPrefix(collections[0].Folders[0].HeroBackdropURL, publicPrefix) {
		t.Fatalf("collection artwork was not localized: %#v", collections[0])
	}
	input := collection.SaveInput{
		Title:            collections[0].Title,
		BackdropImageURL: collections[0].BackdropImageURL,
		Folders:          collections[0].Folders,
	}
	service.RestoreCollectionSaveInput(context.Background(), &input)
	if input.BackdropImageURL != collectionBackground || input.Folders[0].CoverImageURL != cover ||
		input.Folders[0].TitleLogoURL != titleLogo || input.Folders[0].HeroBackdropURL != heroBackdrop {
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
	seriesSeasonBackdrop := fixture.URL + "/series-season-backdrop.png"
	seriesPageSeasonBackdrop := fixture.URL + "/series-page-season-backdrop.png"
	seasonBackdrop := fixture.URL + "/season-backdrop.png"
	episodeBackdrop := fixture.URL + "/episode-backdrop.png"
	movie := metadata.Movie{
		PosterURL: fixture.URL + "/movie-poster.png",
		Cast:      []metadata.CastMember{{ID: "1", Name: "Movie Actor", ProfileURL: movieProfile}},
	}
	series := metadata.Series{
		BackdropURL: fixture.URL + "/series-backdrop.png",
		Cast:        []metadata.CastMember{{ID: "2", Name: "Series Actor", ProfileURL: seriesProfile}},
		Seasons:     []metadata.SeasonSummary{{SeasonNumber: 1, BackdropURL: seriesSeasonBackdrop}},
	}
	moviePage := metadata.MoviePage{Items: []metadata.Movie{{
		Cast: []metadata.CastMember{{ID: "3", Name: "Movie Page Actor", ProfileURL: moviePageProfile}},
	}}}
	seriesPage := metadata.SeriesPage{Items: []metadata.Series{{
		Cast:    []metadata.CastMember{{ID: "4", Name: "Series Page Actor", ProfileURL: seriesPageProfile}},
		Seasons: []metadata.SeasonSummary{{SeasonNumber: 1, PosterURL: seriesPageSeasonPoster, BackdropURL: seriesPageSeasonBackdrop}},
	}}}
	season := metadata.Season{
		SeasonNumber: 1,
		BackdropURL:  seasonBackdrop,
		Episodes:     []metadata.Episode{{BackdropURL: episodeBackdrop}},
	}

	service.LocalizeMovie(context.Background(), &movie)
	service.LocalizeSeries(context.Background(), &series)
	service.LocalizeMoviePage(context.Background(), &moviePage)
	service.LocalizeSeriesPage(context.Background(), &seriesPage)
	service.LocalizeSeason(context.Background(), &season)

	for label, value := range map[string]string{
		"movie profile":               movie.Cast[0].ProfileURL,
		"series profile":              series.Cast[0].ProfileURL,
		"movie page profile":          moviePage.Items[0].Cast[0].ProfileURL,
		"series page profile":         seriesPage.Items[0].Cast[0].ProfileURL,
		"series page season":          seriesPage.Items[0].Seasons[0].PosterURL,
		"series season backdrop":      series.Seasons[0].BackdropURL,
		"series page season backdrop": seriesPage.Items[0].Seasons[0].BackdropURL,
		"season backdrop":             season.BackdropURL,
		"episode backdrop":            season.Episodes[0].BackdropURL,
	} {
		if !strings.HasPrefix(value, publicPrefix) {
			t.Fatalf("%s was not localized through %q: %q", label, publicPrefix, value)
		}
		if strings.Contains(value, fixture.URL) {
			t.Fatalf("%s leaked upstream URL %q", label, value)
		}
	}
	for upstream, localized := range map[string]string{
		movieProfile:             movie.Cast[0].ProfileURL,
		seriesProfile:            series.Cast[0].ProfileURL,
		moviePageProfile:         moviePage.Items[0].Cast[0].ProfileURL,
		seriesPageProfile:        seriesPage.Items[0].Cast[0].ProfileURL,
		seriesPageSeasonPoster:   seriesPage.Items[0].Seasons[0].PosterURL,
		seriesSeasonBackdrop:     series.Seasons[0].BackdropURL,
		seriesPageSeasonBackdrop: seriesPage.Items[0].Seasons[0].BackdropURL,
		seasonBackdrop:           season.BackdropURL,
		episodeBackdrop:          season.Episodes[0].BackdropURL,
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
