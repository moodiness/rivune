package tmdb

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/moodiness/rivune/server/internal/collection"
)

type collectionMediaResponse struct {
	ID               int64   `json:"id"`
	MediaType        string  `json:"media_type"`
	Title            string  `json:"title"`
	Name             string  `json:"name"`
	Overview         string  `json:"overview"`
	ReleaseDate      string  `json:"release_date"`
	FirstAirDate     string  `json:"first_air_date"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	Popularity       float64 `json:"popularity"`
	OriginalLanguage string  `json:"original_language"`
	Job              string  `json:"job"`
}

type collectionMediaPage struct {
	Page       int                       `json:"page"`
	Results    []collectionMediaResponse `json:"results"`
	Items      []collectionMediaResponse `json:"items"`
	TotalPages int                       `json:"total_pages"`
}

type tmdbCollectionResponse struct {
	ID           int64                     `json:"id"`
	Name         string                    `json:"name"`
	PosterPath   string                    `json:"poster_path"`
	BackdropPath string                    `json:"backdrop_path"`
	Parts        []collectionMediaResponse `json:"parts"`
}

type personCreditsResponse struct {
	Cast []collectionMediaResponse `json:"cast"`
	Crew []collectionMediaResponse `json:"crew"`
}

type lookupPage struct {
	Results []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		LogoPath     string `json:"logo_path"`
		PosterPath   string `json:"poster_path"`
		BackdropPath string `json:"backdrop_path"`
		ProfilePath  string `json:"profile_path"`
	} `json:"results"`
}

type genresResponse struct {
	Genres []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"genres"`
}

func (client *Client) ResolveCollectionSource(ctx context.Context, source collection.TMDBSource, page int, language, region string) (collection.SourcePage, error) {
	switch source.SourceType {
	case "list":
		return client.resolveTMDBList(ctx, source, page, language)
	case "collection":
		return client.resolveTMDBCollection(ctx, source, language)
	case "person", "director":
		return client.resolveTMDBPerson(ctx, source, language)
	case "company", "network", "discover":
		return client.resolveTMDBDiscover(ctx, source, page, language, region)
	default:
		return collection.SourcePage{}, collection.ErrInvalidInput
	}
}

func (client *Client) LookupCollectionSource(ctx context.Context, kind, query, language string, page int) ([]collection.LookupResult, error) {
	endpoint := map[string]string{
		"company": "/search/company", "collection": "/search/collection",
		"person": "/search/person", "keyword": "/search/keyword",
	}[kind]
	if endpoint == "" {
		return nil, collection.ErrInvalidInput
	}
	values := url.Values{"query": {query}, "page": {strconv.Itoa(page)}}
	if kind != "keyword" && kind != "company" {
		values.Set("language", language)
	}
	var response lookupPage
	if err := client.get(ctx, endpoint, values, &response); err != nil {
		return nil, err
	}
	results := make([]collection.LookupResult, 0, len(response.Results))
	for _, value := range response.Results {
		if value.ID < 1 || strings.TrimSpace(value.Name) == "" {
			continue
		}
		path := value.LogoPath
		if path == "" {
			path = value.PosterPath
		}
		if path == "" {
			path = value.ProfilePath
		}
		if path == "" {
			path = value.BackdropPath
		}
		results = append(results, collection.LookupResult{ID: value.ID, Name: value.Name, ImageURL: collectionImageURL(path, "w500")})
	}
	return results, nil
}

func (client *Client) CollectionGenres(ctx context.Context, mediaType, language string) ([]collection.Genre, error) {
	endpoint := "/genre/movie/list"
	if mediaType == collection.MediaTypeSeries {
		endpoint = "/genre/tv/list"
	}
	var response genresResponse
	if err := client.get(ctx, endpoint, url.Values{"language": {language}}, &response); err != nil {
		return nil, err
	}
	genres := make([]collection.Genre, 0, len(response.Genres))
	for _, value := range response.Genres {
		genres = append(genres, collection.Genre{ID: value.ID, Name: value.Name})
	}
	return genres, nil
}

func (client *Client) resolveTMDBList(ctx context.Context, source collection.TMDBSource, page int, language string) (collection.SourcePage, error) {
	if source.TMDBID == nil {
		return collection.SourcePage{}, collection.ErrInvalidInput
	}
	var response collectionMediaPage
	if err := client.get(ctx, "/list/"+strconv.FormatInt(*source.TMDBID, 10), url.Values{
		"language": {language}, "page": {strconv.Itoa(page)},
	}, &response); err != nil {
		return collection.SourcePage{}, err
	}
	values := response.Items
	if values == nil {
		values = response.Results
	}
	items := collectionItems(values, selectableMediaType(source.MediaType))
	sortCollectionItems(items, source.Sort)
	totalPages := response.TotalPages
	if totalPages < 1 {
		totalPages = page
	}
	return collection.SourcePage{Items: items, Page: page, HasMore: page < totalPages}, nil
}

func (client *Client) resolveTMDBCollection(ctx context.Context, source collection.TMDBSource, language string) (collection.SourcePage, error) {
	if source.TMDBID == nil {
		return collection.SourcePage{}, collection.ErrInvalidInput
	}
	var response tmdbCollectionResponse
	if err := client.get(ctx, "/collection/"+strconv.FormatInt(*source.TMDBID, 10), url.Values{"language": {language}}, &response); err != nil {
		return collection.SourcePage{}, err
	}
	items := collectionItems(response.Parts, collection.MediaTypeMovie)
	sortCollectionItems(items, source.Sort)
	return collection.SourcePage{
		Items: items, Page: 1, HasMore: false,
		CoverImageURL:   collectionImageURL(response.PosterPath, "w500"),
		HeroBackdropURL: collectionImageURL(response.BackdropPath, "w1280"),
	}, nil
}

func (client *Client) resolveTMDBPerson(ctx context.Context, source collection.TMDBSource, language string) (collection.SourcePage, error) {
	if source.TMDBID == nil {
		return collection.SourcePage{}, collection.ErrInvalidInput
	}
	var response personCreditsResponse
	if err := client.get(ctx, "/person/"+strconv.FormatInt(*source.TMDBID, 10)+"/combined_credits", url.Values{"language": {language}}, &response); err != nil {
		return collection.SourcePage{}, err
	}
	values := response.Cast
	if source.SourceType == "director" {
		values = values[:0]
		for _, value := range response.Crew {
			if strings.EqualFold(value.Job, "Director") {
				values = append(values, value)
			}
		}
	}
	items := collectionItems(values, selectableMediaType(source.MediaType))
	sortCollectionItems(items, source.Sort)
	return collection.SourcePage{Items: deduplicateCollectionItems(items), Page: 1, HasMore: false}, nil
}

func (client *Client) resolveTMDBDiscover(ctx context.Context, source collection.TMDBSource, page int, language, region string) (collection.SourcePage, error) {
	if source.MediaType != collection.MediaTypeBoth {
		return client.resolveTMDBDiscoverMedia(ctx, source, page, language, region)
	}
	mediaTypes := []string{collection.MediaTypeMovie, collection.MediaTypeSeries}
	if source.SourceType == "network" {
		mediaTypes = []string{collection.MediaTypeSeries}
	}
	type outcome struct {
		page collection.SourcePage
		err  error
	}
	outcomes := make(chan outcome, len(mediaTypes))
	for _, mediaType := range mediaTypes {
		mediaSource := source
		mediaSource.MediaType = mediaType
		go func() {
			resolved, err := client.resolveTMDBDiscoverMedia(ctx, mediaSource, page, language, region)
			outcomes <- outcome{page: resolved, err: err}
		}()
	}
	resolved := collection.SourcePage{Page: page}
	for range mediaTypes {
		result := <-outcomes
		if result.err != nil {
			return collection.SourcePage{}, result.err
		}
		resolved.Items = append(resolved.Items, result.page.Items...)
		resolved.HasMore = resolved.HasMore || result.page.HasMore
	}
	resolved.Items = deduplicateCollectionItems(resolved.Items)
	sortCollectionItems(resolved.Items, source.Sort)
	return resolved, nil
}

func (client *Client) resolveTMDBDiscoverMedia(ctx context.Context, source collection.TMDBSource, page int, language, region string) (collection.SourcePage, error) {
	filters := source.Filters
	query := url.Values{
		"include_adult": {"false"}, "language": {language}, "page": {strconv.Itoa(page)},
		"sort_by": {collectionSort(source.Sort, source.MediaType)},
	}
	if region != "" {
		query.Set("region", region)
	}
	setIDs(query, "with_genres", filters.Genres)
	setIDs(query, "with_keywords", filters.Keywords)
	setIDs(query, "with_companies", filters.Companies)
	if source.SourceType == "company" && source.TMDBID != nil {
		query.Set("with_companies", strconv.FormatInt(*source.TMDBID, 10))
	}
	if filters.VoteAverageMin != nil {
		query.Set("vote_average.gte", strconv.FormatFloat(*filters.VoteAverageMin, 'f', -1, 64))
	}
	if filters.VoteAverageMax != nil {
		query.Set("vote_average.lte", strconv.FormatFloat(*filters.VoteAverageMax, 'f', -1, 64))
	}
	if filters.VoteCountMin != nil {
		query.Set("vote_count.gte", strconv.Itoa(*filters.VoteCountMin))
	}
	if filters.OriginalLanguage != "" {
		query.Set("with_original_language", filters.OriginalLanguage)
	}
	if filters.OriginCountry != "" {
		query.Set("with_origin_country", filters.OriginCountry)
	}
	if len(filters.WatchProviders) > 0 {
		setIDs(query, "with_watch_providers", filters.WatchProviders)
		watchRegion := filters.WatchRegion
		if watchRegion == "" {
			watchRegion = region
		}
		query.Set("watch_region", watchRegion)
		query.Set("with_watch_monetization_types", "flatrate|free|ads|rent|buy")
	}
	var response collectionMediaPage
	if source.MediaType == collection.MediaTypeSeries {
		query.Set("include_null_first_air_dates", "false")
		if filters.ReleaseDateFrom != "" {
			query.Set("first_air_date.gte", filters.ReleaseDateFrom)
		}
		if filters.ReleaseDateTo != "" {
			query.Set("first_air_date.lte", filters.ReleaseDateTo)
		}
		if filters.Year != nil {
			query.Set("first_air_date_year", strconv.Itoa(*filters.Year))
		}
		setIDs(query, "with_networks", filters.Networks)
		if source.SourceType == "network" && source.TMDBID != nil {
			query.Set("with_networks", strconv.FormatInt(*source.TMDBID, 10))
		}
		if err := client.get(ctx, "/discover/tv", query, &response); err != nil {
			return collection.SourcePage{}, err
		}
	} else {
		query.Set("include_video", "false")
		if filters.ReleaseDateFrom != "" {
			query.Set("primary_release_date.gte", filters.ReleaseDateFrom)
		}
		if filters.ReleaseDateTo != "" {
			query.Set("primary_release_date.lte", filters.ReleaseDateTo)
		}
		if filters.Year != nil {
			query.Set("year", strconv.Itoa(*filters.Year))
		}
		if err := client.get(ctx, "/discover/movie", query, &response); err != nil {
			return collection.SourcePage{}, err
		}
	}
	return collection.SourcePage{
		Items: collectionItems(response.Results, source.MediaType), Page: response.Page,
		HasMore: response.Page < response.TotalPages,
	}, nil
}

func collectionItems(values []collectionMediaResponse, requiredMediaType string) []collection.Item {
	items := make([]collection.Item, 0, len(values))
	for _, value := range values {
		mediaType := value.MediaType
		if mediaType == "tv" {
			mediaType = collection.MediaTypeSeries
		}
		if mediaType == "" {
			mediaType = requiredMediaType
			if mediaType == "" {
				if strings.TrimSpace(value.Title) != "" {
					mediaType = collection.MediaTypeMovie
				} else {
					mediaType = collection.MediaTypeSeries
				}
			}
		}
		if requiredMediaType != "" && mediaType != requiredMediaType {
			continue
		}
		title := strings.TrimSpace(value.Title)
		if title == "" {
			title = strings.TrimSpace(value.Name)
		}
		if value.ID < 1 || title == "" {
			continue
		}
		released := value.ReleaseDate
		if released == "" {
			released = value.FirstAirDate
		}
		voteAverage := value.VoteAverage
		voteCount := value.VoteCount
		popularity := value.Popularity
		raw, _ := json.Marshal(value)
		items = append(items, collection.Item{
			ID: "tmdb:" + strconv.FormatInt(value.ID, 10), MediaType: mediaType, Title: title,
			PosterURL: collectionImageURL(value.PosterPath, "w500"), BackgroundURL: collectionImageURL(value.BackdropPath, "w1280"),
			Description: value.Overview, ReleaseInfo: yearFromDate(released), Released: released,
			VoteAverage: &voteAverage, VoteCount: &voteCount, Popularity: &popularity,
			ExternalIDs: map[string]string{"tmdb": strconv.FormatInt(value.ID, 10)}, Raw: raw,
		})
	}
	return items
}

func sortCollectionItems(items []collection.Item, sortBy string) {
	if sortBy == "original" {

		return
	}
	sort.SliceStable(items, func(left, right int) bool {
		switch sortBy {
		case "vote_average.desc":
			return number(items[left].VoteAverage) > number(items[right].VoteAverage)
		case "vote_count.desc":
			return integer(items[left].VoteCount) > integer(items[right].VoteCount)
		case "release_date.desc", "first_air_date.desc":
			return items[left].Released > items[right].Released
		case "popularity.desc":
			return number(items[left].Popularity) > number(items[right].Popularity)
		default:
			return number(items[left].Popularity) > number(items[right].Popularity)
		}
	})
}

func deduplicateCollectionItems(items []collection.Item) []collection.Item {
	seen := make(map[string]struct{}, len(items))
	result := make([]collection.Item, 0, len(items))
	for _, item := range items {
		key := item.MediaType + ":" + item.ID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}

func selectableMediaType(mediaType string) string {
	if mediaType == collection.MediaTypeBoth {
		return ""
	}
	return mediaType
}

func collectionSort(value, mediaType string) string {
	if mediaType == collection.MediaTypeSeries {
		if value == "release_date.desc" {
			return "first_air_date.desc"
		}
		return value
	}
	if value == "first_air_date.desc" {
		return "primary_release_date.desc"
	}
	if value == "release_date.desc" {
		return "primary_release_date.desc"
	}
	return value
}

func setIDs(query url.Values, key string, values []int64) {
	if len(values) == 0 {
		return
	}
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.FormatInt(value, 10)
	}
	query.Set(key, strings.Join(parts, "|"))
}

func collectionImageURL(path, size string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return imageBaseURL + "/" + size + path
}

func yearFromDate(value string) string {
	if len(value) >= 4 {
		return value[:4]
	}
	return ""
}

func number(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func integer(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

var _ collection.TMDBProvider = (*Client)(nil)
