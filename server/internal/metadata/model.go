package metadata

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidInput         = errors.New("invalid metadata input")
	ErrNotFound             = errors.New("title not found")
	ErrProfileRequired      = errors.New("active profile required")
	ErrProviderUnavailable  = errors.New("metadata provider unavailable")
	ErrProviderUnauthorized = errors.New("metadata provider authentication failed")
	ErrProviderNotFound     = errors.New("provider title not found")
	ErrProviderRateLimited  = errors.New("metadata provider rate limited")
	ErrProviderFailure      = errors.New("metadata provider request failed")
)

const (
	MediaTypeMovie   = "movie"
	MediaTypeSeries  = "series"
	MediaTypeSeason  = "season"
	MediaTypeEpisode = "episode"
)

type QueryOptions struct {
	Page     int
	Language string
	Region   string
}

type SeriesDetailsOptions struct {
	Language        string
	MappingProvider string
	EpisodeOrderID  string
}

type RefreshMissingOptions struct {
	Language  string
	BatchSize int
}

type RefreshResult struct {
	Candidates int `json:"candidates"`
	Refreshed  int `json:"refreshed"`
	Failed     int `json:"failed"`
}

type SearchOptions struct {
	QueryOptions
	Query string
}

type Alias struct {
	Language string `json:"language"`
	Name     string `json:"name"`
}

type EpisodeOrder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsDefault bool   `json:"isDefault"`
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Movie struct {
	ID               string            `json:"id"`
	MediaType        string            `json:"mediaType"`
	Title            string            `json:"title"`
	OriginalTitle    string            `json:"originalTitle"`
	OriginalLanguage string            `json:"originalLanguage"`
	Overview         string            `json:"overview"`
	ReleaseDate      string            `json:"releaseDate,omitempty"`
	PosterURL        string            `json:"posterUrl,omitempty"`
	BackdropURL      string            `json:"backdropUrl,omitempty"`
	LogoURL          string            `json:"logoUrl,omitempty"`
	Tagline          string            `json:"tagline,omitempty"`
	RuntimeMinutes   int               `json:"runtimeMinutes,omitempty"`
	Genres           []Genre           `json:"genres"`
	VoteAverage      float64           `json:"voteAverage"`
	VoteCount        int               `json:"voteCount"`
	ExternalIDs      map[string]string `json:"externalIds"`
}

type MoviePage struct {
	Items        []Movie `json:"items"`
	Page         int     `json:"page"`
	TotalPages   int     `json:"totalPages"`
	TotalResults int     `json:"totalResults"`
}

type Trailer struct {
	YouTubeID         string `json:"youtubeId"`
	Name              string `json:"name"`
	Language          string `json:"language"`
	IsFallback        bool   `json:"isFallback"`
	CaptionPreference string `json:"captionPreference,omitempty"`
}

type TrailerList struct {
	Trailers []Trailer `json:"trailers"`
}

type ProviderTrailer struct {
	YouTubeID   string
	Name        string
	Language    string
	Site        string
	Type        string
	Official    bool
	PublishedAt time.Time
}

type ProviderMovie struct {
	ExternalID       string
	Title            string
	OriginalTitle    string
	OriginalLanguage string
	Overview         string
	ReleaseDate      string
	PosterURL        string
	BackdropURL      string
	LogoURL          string
	Tagline          string
	RuntimeMinutes   int
	Genres           []Genre
	VoteAverage      float64
	VoteCount        int
	AdditionalIDs    map[string]string
}

type ProviderMoviePage struct {
	Items        []ProviderMovie
	Page         int
	TotalPages   int
	TotalResults int
}

type Series struct {
	ID                     string            `json:"id"`
	MediaType              string            `json:"mediaType"`
	Name                   string            `json:"name"`
	OriginalName           string            `json:"originalName"`
	OriginalLanguage       string            `json:"originalLanguage"`
	Overview               string            `json:"overview"`
	FirstAirDate           string            `json:"firstAirDate,omitempty"`
	LastAirDate            string            `json:"lastAirDate,omitempty"`
	PosterURL              string            `json:"posterUrl,omitempty"`
	BackdropURL            string            `json:"backdropUrl,omitempty"`
	LogoURL                string            `json:"logoUrl,omitempty"`
	Tagline                string            `json:"tagline,omitempty"`
	Status                 string            `json:"status,omitempty"`
	NumberOfSeasons        int               `json:"numberOfSeasons,omitempty"`
	NumberOfEpisodes       int               `json:"numberOfEpisodes,omitempty"`
	Genres                 []Genre           `json:"genres"`
	VoteAverage            float64           `json:"voteAverage"`
	VoteCount              int               `json:"voteCount"`
	Seasons                []SeasonSummary   `json:"seasons"`
	Aliases                []Alias           `json:"aliases"`
	EpisodeOrders          []EpisodeOrder    `json:"episodeOrders"`
	SelectedEpisodeOrderID string            `json:"selectedEpisodeOrderId,omitempty"`
	MappingProvider        string            `json:"mappingProvider"`
	ExternalIDs            map[string]string `json:"externalIds"`
}

type SeriesPage struct {
	Items        []Series `json:"items"`
	Page         int      `json:"page"`
	TotalPages   int      `json:"totalPages"`
	TotalResults int      `json:"totalResults"`
}

type SeasonSummary struct {
	ID           string            `json:"id"`
	MediaType    string            `json:"mediaType"`
	SeriesID     string            `json:"seriesId"`
	Name         string            `json:"name"`
	Overview     string            `json:"overview"`
	SeasonNumber int               `json:"seasonNumber"`
	EpisodeCount int               `json:"episodeCount"`
	AirDate      string            `json:"airDate,omitempty"`
	PosterURL    string            `json:"posterUrl,omitempty"`
	VoteAverage  float64           `json:"voteAverage"`
	ExternalIDs  map[string]string `json:"externalIds"`
}

type Season struct {
	ID           string            `json:"id"`
	MediaType    string            `json:"mediaType"`
	SeriesID     string            `json:"seriesId"`
	Name         string            `json:"name"`
	Overview     string            `json:"overview"`
	SeasonNumber int               `json:"seasonNumber"`
	AirDate      string            `json:"airDate,omitempty"`
	PosterURL    string            `json:"posterUrl,omitempty"`
	VoteAverage  float64           `json:"voteAverage"`
	Episodes     []Episode         `json:"episodes"`
	ExternalIDs  map[string]string `json:"externalIds"`
}

type Episode struct {
	ID             string            `json:"id"`
	MediaType      string            `json:"mediaType"`
	SeasonID       string            `json:"seasonId"`
	Name           string            `json:"name"`
	Overview       string            `json:"overview"`
	SeasonNumber   int               `json:"seasonNumber"`
	EpisodeNumber  int               `json:"episodeNumber"`
	AirDate        string            `json:"airDate,omitempty"`
	StillURL       string            `json:"stillUrl,omitempty"`
	RuntimeMinutes int               `json:"runtimeMinutes,omitempty"`
	VoteAverage    float64           `json:"voteAverage"`
	VoteCount      int               `json:"voteCount"`
	ExternalIDs    map[string]string `json:"externalIds"`
}

type ProviderSeries struct {
	ExternalID       string
	Name             string
	OriginalName     string
	OriginalLanguage string
	Overview         string
	FirstAirDate     string
	LastAirDate      string
	PosterURL        string
	BackdropURL      string
	LogoURL          string
	Tagline          string
	Status           string
	NumberOfSeasons  int
	NumberOfEpisodes int
	Genres           []Genre
	VoteAverage      float64
	VoteCount        int
	Seasons          []ProviderSeasonSummary
	Aliases          []Alias
	EpisodeOrders    []EpisodeOrder
	AdditionalIDs    map[string]string
}

type ProviderSeriesPage struct {
	Items        []ProviderSeries
	Page         int
	TotalPages   int
	TotalResults int
}

type ProviderSeasonSummary struct {
	ExternalID   string
	Name         string
	Overview     string
	SeasonNumber int
	EpisodeCount int
	AirDate      string
	PosterURL    string
	VoteAverage  float64
}

type ProviderSeason struct {
	ExternalID   string
	Name         string
	Overview     string
	SeasonNumber int
	AirDate      string
	PosterURL    string
	VoteAverage  float64
	Episodes     []ProviderEpisode
}

type ProviderEpisode struct {
	ExternalID     string
	Name           string
	Overview       string
	SeasonNumber   int
	EpisodeNumber  int
	AirDate        string
	StillURL       string
	RuntimeMinutes int
	VoteAverage    float64
	VoteCount      int
	AdditionalIDs  map[string]string
}

type Provider interface {
	DiscoverMovies(context.Context, QueryOptions) (ProviderMoviePage, error)
	SearchMovies(context.Context, SearchOptions) (ProviderMoviePage, error)
	MovieDetails(context.Context, string, string) (ProviderMovie, error)
	DiscoverSeries(context.Context, QueryOptions) (ProviderSeriesPage, error)
	SearchSeries(context.Context, SearchOptions) (ProviderSeriesPage, error)
	SeriesDetails(context.Context, string, string) (ProviderSeries, error)
	SeasonDetails(context.Context, string, int, string) (ProviderSeason, error)
}

type TrailerProvider interface {
	Trailers(context.Context, string, string, string, *int) ([]ProviderTrailer, error)
}

type ExternalIDResolver interface {
	ResolveExternalID(context.Context, string, string, string) (string, error)
}

type TelevisionEnricher interface {
	EnrichSeries(context.Context, ProviderSeries) (ProviderSeries, error)
	EnrichSeason(context.Context, string, ProviderSeason) (ProviderSeason, error)
}

type ArtworkEnricher interface {
	EnrichMovie(context.Context, ProviderMovie, string) (ProviderMovie, error)
	EnrichSeries(context.Context, ProviderSeries, string) (ProviderSeries, error)
	EnrichSeason(context.Context, string, ProviderSeason, string) (ProviderSeason, error)
}

type TelevisionMapper interface {
	SeriesSeasons(context.Context, string, string) ([]ProviderSeasonSummary, error)
	SeriesSeason(context.Context, string, string) (ProviderSeason, error)
}
