package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	semanticCatalogTTL              = 30 * 24 * time.Hour
	semanticCatalogRefreshInterval  = 24 * time.Hour
	semanticCatalogRetryInterval    = time.Minute
	semanticCatalogMaximumBackoff   = 24 * time.Hour
	semanticCatalogProviderSpacing  = time.Second
	maximumSemanticCatalogLocales   = 256
	maximumSemanticCatalogGenres    = 64
	maximumSemanticCatalogCountries = 300
)

type semanticCatalogLocaleEntry struct {
	locale    SemanticCatalogLocale
	expiresAt time.Time
}

type semanticCatalogRetry struct {
	failures    uint
	nextAttempt time.Time
}

type semanticCatalogFlight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	finished bool
	err      error
}

var errSemanticCatalogBackoff = errors.New("TMDB semantic locale refresh is backed off")

type semanticVocabulary struct {
	genres    []semanticDefinition
	countries []semanticDefinition
	labels    map[string]map[string]string
	known     map[string]struct{}
}

func (vocabulary *semanticVocabulary) label(intent SemanticSearchIntent, language string) SemanticSearchIntent {
	if vocabulary == nil {
		return intent
	}
	language = canonicalSemanticLanguage(language)
	for _, candidate := range []string{language, "en-US", "en"} {
		if labels := vocabulary.labels[candidate]; labels != nil {
			if label := strings.TrimSpace(labels[intent.ID]); label != "" {
				intent.Label = label
				return intent
			}
		}
	}
	return intent
}

func (vocabulary *semanticVocabulary) knows(id string) bool {
	if vocabulary == nil {
		return knownSemanticIntentID(id)
	}
	_, ok := vocabulary.known[id]
	return ok
}

func (vocabulary *semanticVocabulary) intents() []SemanticSearchIntent {
	if vocabulary == nil {
		return nil
	}
	definitions := make([]semanticDefinition, 0, len(semanticTypeDefinitions)+len(vocabulary.genres)+len(semanticThemeDefinitions)+len(vocabulary.countries))
	definitions = append(definitions, semanticTypeDefinitions...)
	definitions = append(definitions, vocabulary.genres...)
	for _, theme := range semanticThemeDefinitions {
		definitions = append(definitions, theme.semanticDefinition)
	}
	definitions = append(definitions, vocabulary.countries...)
	result := make([]SemanticSearchIntent, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if _, duplicate := seen[definition.intent.ID]; duplicate {
			continue
		}
		seen[definition.intent.ID] = struct{}{}
		result = append(result, definition.intent)
	}
	return result
}

type semanticCatalog struct {
	pool    *pgxpool.Pool
	source  ProviderSource
	logger  *slog.Logger
	now     func() time.Time
	mu      sync.Mutex
	loaded  bool
	locales map[string]semanticCatalogLocaleEntry
	retries map[string]semanticCatalogRetry
	flights map[string]*semanticCatalogFlight
	current atomic.Pointer[semanticVocabulary]
	wake    chan struct{}

	cancellations uint64
	evictions     uint64
}

func newSemanticCatalog(pool *pgxpool.Pool, source ProviderSource) *semanticCatalog {
	catalog := &semanticCatalog{
		pool: pool, source: source, logger: slog.Default(), now: time.Now,
		locales: make(map[string]semanticCatalogLocaleEntry), retries: make(map[string]semanticCatalogRetry),
		flights: make(map[string]*semanticCatalogFlight), wake: make(chan struct{}, 1),
	}
	catalog.current.Store(buildSemanticVocabulary(nil))
	return catalog
}

func (catalog *semanticCatalog) setLogger(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	catalog.mu.Lock()
	catalog.logger = logger
	catalog.mu.Unlock()
}

func (catalog *semanticCatalog) vocabulary(ctx context.Context, provider TMDBProvider, language string) (*semanticVocabulary, bool, error) {
	partial := false
	if err := catalog.load(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, false, err
		}
		partial = true
		catalog.warn(ctx, "load TMDB semantic catalog", err)
	}
	language = canonicalSemanticLanguage(language)
	if language == "" {
		language = "en-US"
	}
	if provider == nil {
		return catalog.current.Load(), true, nil
	}
	if !catalog.localeFresh(language) {
		if err := catalog.refreshLocale(ctx, provider, language); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, false, err
			}
			partial = true
			if !errors.Is(err, errSemanticCatalogBackoff) {
				catalog.warn(ctx, "refresh TMDB semantic locale", err, "language", language)
			}
		} else {
			catalog.signal()
		}
	}
	return catalog.current.Load(), partial, nil
}

func (catalog *semanticCatalog) load(ctx context.Context) error {
	catalog.mu.Lock()
	loaded := catalog.loaded
	catalog.mu.Unlock()
	if loaded {
		return nil
	}
	return catalog.doFlight(ctx, "catalog", func(operationContext context.Context) error {
		catalog.mu.Lock()
		if catalog.loaded {
			catalog.mu.Unlock()
			return nil
		}
		catalog.mu.Unlock()
		if catalog.pool == nil {
			catalog.mu.Lock()
			catalog.loaded = true
			catalog.mu.Unlock()
			return nil
		}
		rows, err := catalog.pool.Query(operationContext, `
			SELECT language, payload, expires_at
			FROM tmdb_semantic_catalog
			ORDER BY updated_at DESC, language
			LIMIT $1
		`, maximumSemanticCatalogLocales)
		if err != nil {
			return fmt.Errorf("query TMDB semantic catalog: %w", err)
		}
		defer rows.Close()
		locales := make(map[string]semanticCatalogLocaleEntry)
		for rows.Next() {
			var language string
			var payload []byte
			var expiresAt time.Time
			if err := rows.Scan(&language, &payload, &expiresAt); err != nil {
				return fmt.Errorf("scan TMDB semantic catalog: %w", err)
			}
			var locale SemanticCatalogLocale
			if err := json.Unmarshal(payload, &locale); err != nil {
				catalog.warn(operationContext, "ignore malformed TMDB semantic locale", err, "language", language)
				continue
			}
			normalized, err := normalizeSemanticCatalogLocale(locale)
			if err != nil {
				catalog.warn(operationContext, "ignore invalid TMDB semantic locale", err, "language", language)
				continue
			}
			language = canonicalSemanticLanguage(language)
			if language == "" {
				continue
			}
			locales[language] = semanticCatalogLocaleEntry{locale: normalized, expiresAt: expiresAt}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate TMDB semantic catalog: %w", err)
		}
		catalog.mu.Lock()
		catalog.locales = locales
		catalog.loaded = true
		catalog.current.Store(buildSemanticVocabulary(locales))
		catalog.mu.Unlock()
		return nil
	})
}

func (catalog *semanticCatalog) localeFresh(language string) bool {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	entry, ok := catalog.locales[language]
	return ok && entry.expiresAt.After(catalog.now().UTC())
}

func (catalog *semanticCatalog) refreshLocale(ctx context.Context, provider TMDBProvider, language string) error {
	language = canonicalSemanticLanguage(language)
	if language == "" {
		return ErrInvalidInput
	}
	if !catalog.localeRefreshDue(language) {
		return errSemanticCatalogBackoff
	}
	return catalog.doFlight(ctx, "locale:"+language, func(operationContext context.Context) error {
		if catalog.localeFresh(language) {
			return nil
		}
		if !catalog.localeRefreshDue(language) {
			return errSemanticCatalogBackoff
		}
		locale, err := provider.SemanticCatalogLocale(operationContext, language)
		if err == nil {
			locale, err = normalizeSemanticCatalogLocale(locale)
			if err != nil {
				err = fmt.Errorf("validate TMDB semantic locale %s: %w", language, err)
			}
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				catalog.recordLocaleFailure(language)
			}
			return err
		}
		expiresAt := catalog.now().UTC().Add(semanticCatalogTTL)
		payload, err := json.Marshal(locale)
		if err != nil {
			catalog.recordLocaleFailure(language)
			return fmt.Errorf("encode TMDB semantic locale %s: %w", language, err)
		}
		if catalog.pool != nil {
			if _, err := catalog.pool.Exec(operationContext, `
				WITH stored AS (
					INSERT INTO tmdb_semantic_catalog (language, payload, expires_at)
					VALUES ($1, $2, $3)
					ON CONFLICT (language) DO UPDATE
					SET payload = EXCLUDED.payload,
					    expires_at = EXCLUDED.expires_at,
					    updated_at = now()
					RETURNING language
				), overflow AS (
					SELECT language
					FROM tmdb_semantic_catalog
					WHERE language <> $1
					ORDER BY updated_at DESC, language
					OFFSET $4
				)
				DELETE FROM tmdb_semantic_catalog cached
				USING overflow
				WHERE cached.language = overflow.language
			`, language, payload, expiresAt, maximumSemanticCatalogLocales-1); err != nil {
				catalog.recordLocaleFailure(language)
				return fmt.Errorf("store TMDB semantic locale %s: %w", language, err)
			}
		}
		catalog.mu.Lock()
		catalog.locales[language] = semanticCatalogLocaleEntry{locale: locale, expiresAt: expiresAt}
		delete(catalog.retries, language)
		catalog.evictions += uint64(pruneSemanticCatalogLocales(catalog.locales, maximumSemanticCatalogLocales))
		catalog.current.Store(buildSemanticVocabulary(catalog.locales))
		catalog.mu.Unlock()
		return nil
	})
}

func pruneSemanticCatalogLocales(locales map[string]semanticCatalogLocaleEntry, maximum int) int {
	evicted := 0
	for len(locales) > maximum {
		oldestLanguage := ""
		var oldestExpiry time.Time
		for language, entry := range locales {
			if oldestLanguage == "" || entry.expiresAt.Before(oldestExpiry) || entry.expiresAt.Equal(oldestExpiry) && language > oldestLanguage {
				oldestLanguage = language
				oldestExpiry = entry.expiresAt
			}
		}
		delete(locales, oldestLanguage)
		evicted++
	}
	return evicted
}

func (catalog *semanticCatalog) doFlight(ctx context.Context, key string, run func(context.Context) error) error {
	catalog.mu.Lock()
	flight := catalog.flights[key]
	if flight != nil {
		flight.waiters++
		catalog.mu.Unlock()
		return catalog.waitFlight(ctx, key, flight)
	}
	operationContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	flight = &semanticCatalogFlight{done: make(chan struct{}), cancel: cancel, waiters: 1}
	catalog.flights[key] = flight
	catalog.mu.Unlock()
	go func() {
		err := run(operationContext)
		cancel()
		catalog.mu.Lock()
		if catalog.flights[key] == flight {
			delete(catalog.flights, key)
		}
		flight.err, flight.finished = err, true
		close(flight.done)
		catalog.mu.Unlock()
	}()
	return catalog.waitFlight(ctx, key, flight)
}

func (catalog *semanticCatalog) waitFlight(ctx context.Context, key string, flight *semanticCatalogFlight) error {
	select {
	case <-ctx.Done():
		catalog.mu.Lock()
		if !flight.finished {
			flight.waiters--
			catalog.cancellations++
			if flight.waiters == 0 {
				if catalog.flights[key] == flight {
					delete(catalog.flights, key)
				}
				flight.cancel()
			}
		}
		catalog.mu.Unlock()
		return ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		return flight.err
	}
}

func (catalog *semanticCatalog) localeRefreshDue(language string) bool {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	retry, ok := catalog.retries[language]
	return !ok || !catalog.now().UTC().Before(retry.nextAttempt)
}

func (catalog *semanticCatalog) recordLocaleFailure(language string) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	retry := catalog.retries[language]
	retry.failures++
	delay := semanticCatalogRetryInterval
	for attempt := uint(1); attempt < retry.failures && delay < semanticCatalogMaximumBackoff; attempt++ {
		delay *= 2
		if delay > semanticCatalogMaximumBackoff {
			delay = semanticCatalogMaximumBackoff
		}
	}
	retry.nextAttempt = catalog.now().UTC().Add(delay)
	catalog.retries[language] = retry
}

func (catalog *semanticCatalog) localeRetryDelay(language string) (time.Duration, bool) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	retry, ok := catalog.retries[language]
	if !ok {
		return 0, false
	}
	delay := retry.nextAttempt.Sub(catalog.now().UTC())
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func (catalog *semanticCatalog) Run(ctx context.Context) {
	if err := catalog.load(ctx); err != nil && ctx.Err() == nil {
		catalog.warn(ctx, "load TMDB semantic catalog worker state", err)
	}
	for ctx.Err() == nil {
		delay := catalog.synchronize(ctx)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-catalog.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (catalog *semanticCatalog) signal() {
	select {
	case catalog.wake <- struct{}{}:
	default:
	}
}

func (catalog *semanticCatalog) synchronize(ctx context.Context) time.Duration {
	if catalog.source == nil {
		return semanticCatalogRetryInterval
	}
	provider := catalog.source.CollectionProviders().TMDB
	if provider == nil {
		return semanticCatalogRetryInterval
	}
	languages, err := provider.SemanticCatalogLanguages(ctx)
	if err != nil {
		if ctx.Err() == nil {
			catalog.warn(ctx, "list TMDB semantic catalog languages", err)
		}
		return semanticCatalogRetryInterval
	}
	languages = normalizeSemanticCatalogLanguages(languages)
	if len(languages) == 0 {
		catalog.warn(ctx, "list TMDB semantic catalog languages", errors.New("TMDB returned no primary translations"))
		return semanticCatalogRetryInterval
	}
	nextWake := semanticCatalogRefreshInterval
	for index, language := range languages {
		if ctx.Err() != nil {
			return semanticCatalogRetryInterval
		}
		if catalog.localeFresh(language) {
			continue
		}
		if delay, backedOff := catalog.localeRetryDelay(language); backedOff && delay > 0 {
			if delay < nextWake {
				nextWake = delay
			}
			continue
		}
		provider = catalog.source.CollectionProviders().TMDB
		if provider == nil {
			return semanticCatalogRetryInterval
		}
		if err := catalog.refreshLocale(ctx, provider, language); err != nil {
			if ctx.Err() == nil && !errors.Is(err, errSemanticCatalogBackoff) {
				catalog.warn(ctx, "synchronize TMDB semantic locale", err, "language", language)
			}
			if delay, retrying := catalog.localeRetryDelay(language); retrying && delay < nextWake {
				nextWake = delay
			}
			continue
		}
		if index == len(languages)-1 {
			continue
		}
		timer := time.NewTimer(semanticCatalogProviderSpacing)
		select {
		case <-ctx.Done():
			timer.Stop()
			return semanticCatalogRetryInterval
		case <-timer.C:
		}
	}
	return nextWake
}

func (catalog *semanticCatalog) warn(ctx context.Context, message string, err error, attributes ...any) {
	catalog.mu.Lock()
	logger := catalog.logger
	catalog.mu.Unlock()
	if logger == nil {
		logger = slog.Default()
	}
	values := []any{"error_class", semanticCatalogErrorClass(err)}
	if requestID := requestwork.RequestID(ctx); requestID != "" {
		values = append(values, "request_id", requestID)
	}
	values = append(values, attributes...)
	logger.WarnContext(ctx, message, values...)
}

func semanticCatalogErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, ErrInvalidInput):
		return "invalid_response"
	case errors.Is(err, ErrProviderUnavailable):
		return "provider_unavailable"
	default:
		return "provider_failure"
	}
}

func normalizeSemanticCatalogLanguages(values []string) []string {
	result := make([]string, 0, min(len(values), maximumSemanticCatalogLocales))
	seen := make(map[string]struct{}, min(len(values), maximumSemanticCatalogLocales))
	for _, value := range values {
		language := canonicalSemanticLanguage(value)
		if language == "" {
			continue
		}
		if _, duplicate := seen[language]; duplicate {
			continue
		}
		seen[language] = struct{}{}
		result = append(result, language)
		if len(result) == maximumSemanticCatalogLocales {
			break
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left] == "en-US" {
			return true
		}
		if result[right] == "en-US" {
			return false
		}
		return result[left] < result[right]
	})
	return result
}

func canonicalSemanticLanguage(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) == 1 && len(parts[0]) >= 2 && len(parts[0]) <= 3 && asciiLetters(parts[0]) {
		return strings.ToLower(parts[0])
	}
	if len(parts) == 2 && len(parts[0]) >= 2 && len(parts[0]) <= 3 && len(parts[1]) == 2 && asciiLetters(parts[0]) && asciiLetters(parts[1]) {
		return strings.ToLower(parts[0]) + "-" + strings.ToUpper(parts[1])
	}
	return ""
}

func asciiLetters(value string) bool {
	for index := range len(value) {
		character := value[index]
		if character < 'A' || character > 'Z' && character < 'a' || character > 'z' {
			return false
		}
	}
	return true
}

func normalizeSemanticCatalogLocale(locale SemanticCatalogLocale) (SemanticCatalogLocale, error) {
	movies, err := normalizeSemanticCatalogGenres(locale.MovieGenres)
	if err != nil {
		return SemanticCatalogLocale{}, fmt.Errorf("movie genres: %w", err)
	}
	series, err := normalizeSemanticCatalogGenres(locale.SeriesGenres)
	if err != nil {
		return SemanticCatalogLocale{}, fmt.Errorf("series genres: %w", err)
	}
	if len(locale.Countries) == 0 || len(locale.Countries) > maximumSemanticCatalogCountries {
		return SemanticCatalogLocale{}, errors.New("country count is outside bounds")
	}
	countries := make([]Country, 0, len(locale.Countries))
	seenCountries := make(map[string]struct{}, len(locale.Countries))
	for _, country := range locale.Countries {
		country.Code = strings.ToUpper(strings.TrimSpace(country.Code))
		country.EnglishName = strings.TrimSpace(country.EnglishName)
		country.NativeName = strings.TrimSpace(country.NativeName)
		if len(country.Code) != 2 || !asciiLetters(country.Code) || !validSemanticCatalogName(country.EnglishName) && !validSemanticCatalogName(country.NativeName) {
			continue
		}
		if _, duplicate := seenCountries[country.Code]; duplicate {
			continue
		}
		seenCountries[country.Code] = struct{}{}
		countries = append(countries, country)
	}
	if len(countries) == 0 {
		return SemanticCatalogLocale{}, errors.New("country list has no valid entries")
	}
	return SemanticCatalogLocale{MovieGenres: movies, SeriesGenres: series, Countries: countries}, nil
}

func normalizeSemanticCatalogGenres(values []Genre) ([]Genre, error) {
	if len(values) == 0 || len(values) > maximumSemanticCatalogGenres {
		return nil, errors.New("genre count is outside bounds")
	}
	result := make([]Genre, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, genre := range values {
		genre.Name = strings.TrimSpace(genre.Name)
		if genre.ID < 1 || !validSemanticCatalogName(genre.Name) {
			continue
		}
		if _, duplicate := seen[genre.ID]; duplicate {
			continue
		}
		seen[genre.ID] = struct{}{}
		result = append(result, genre)
	}
	if len(result) == 0 {
		return nil, errors.New("genre list has no valid entries")
	}
	return result, nil
}

func validSemanticCatalogName(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= 2 && utf8.RuneCountInString(value) <= 100
}

var semanticCatalogExtraGenreDefinitions = []semanticDefinition{
	semanticDefinitionValue("genre", "action_adventure", "Action & Adventure", "action and adventure"),
	semanticDefinitionValue("genre", "science_fiction_fantasy", "Sci-Fi & Fantasy", "sci-fi and fantasy", "science fiction and fantasy"),
	semanticDefinitionValue("genre", "tv_movie", "TV Movie", "tv movie"),
	semanticDefinitionValue("genre", "kids", "Kids", "kids"),
	semanticDefinitionValue("genre", "news", "News", "news"),
	semanticDefinitionValue("genre", "reality", "Reality", "reality"),
	semanticDefinitionValue("genre", "soap", "Soap", "soap"),
	semanticDefinitionValue("genre", "talk", "Talk", "talk"),
	semanticDefinitionValue("genre", "war_politics", "War & Politics", "war and politics"),
}

var semanticCatalogGenreConcepts = map[semanticCatalogGenreKey]string{
	{MediaTypeMovie, 28}: "action", {MediaTypeMovie, 12}: "adventure", {MediaTypeMovie, 16}: "animation", {MediaTypeMovie, 35}: "comedy",
	{MediaTypeMovie, 80}: "crime", {MediaTypeMovie, 99}: "documentary", {MediaTypeMovie, 18}: "drama", {MediaTypeMovie, 10751}: "family",
	{MediaTypeMovie, 14}: "fantasy", {MediaTypeMovie, 36}: "history", {MediaTypeMovie, 27}: "horror", {MediaTypeMovie, 10402}: "music",
	{MediaTypeMovie, 9648}: "mystery", {MediaTypeMovie, 10749}: "romance", {MediaTypeMovie, 878}: "science_fiction", {MediaTypeMovie, 10770}: "tv_movie",
	{MediaTypeMovie, 53}: "thriller", {MediaTypeMovie, 10752}: "war", {MediaTypeMovie, 37}: "western",
	{MediaTypeSeries, 10759}: "action_adventure", {MediaTypeSeries, 16}: "animation", {MediaTypeSeries, 35}: "comedy", {MediaTypeSeries, 80}: "crime",
	{MediaTypeSeries, 99}: "documentary", {MediaTypeSeries, 18}: "drama", {MediaTypeSeries, 10751}: "family", {MediaTypeSeries, 10762}: "kids",
	{MediaTypeSeries, 9648}: "mystery", {MediaTypeSeries, 10763}: "news", {MediaTypeSeries, 10764}: "reality", {MediaTypeSeries, 10765}: "science_fiction_fantasy",
	{MediaTypeSeries, 10766}: "soap", {MediaTypeSeries, 10767}: "talk", {MediaTypeSeries, 10768}: "war_politics", {MediaTypeSeries, 37}: "western",
}

type semanticCatalogGenreKey struct {
	mediaType string
	id        int64
}

func buildSemanticVocabulary(locales map[string]semanticCatalogLocaleEntry) *semanticVocabulary {
	genres := cloneSemanticDefinitions(append(append([]semanticDefinition{}, semanticGenreDefinitions...), semanticCatalogExtraGenreDefinitions...))
	countries := cloneSemanticDefinitions(semanticCountryDefinitions)
	genreByID := definitionIndex(genres)
	countryByID := definitionIndex(countries)
	labels := make(map[string]map[string]string)
	genreCandidates := make(map[string]map[string]struct{})
	countryCandidates := make(map[string]map[string]struct{})
	genreOwners := make(map[string]map[string]struct{})
	countryOwners := make(map[string]map[string]struct{})
	languages := make([]string, 0, len(locales))
	for language := range locales {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	for _, language := range languages {
		locale := locales[language].locale
		for _, media := range []struct {
			name   string
			genres []Genre
		}{{MediaTypeMovie, locale.MovieGenres}, {MediaTypeSeries, locale.SeriesGenres}} {
			for _, genre := range media.genres {
				value := semanticCatalogGenreConcepts[semanticCatalogGenreKey{media.name, genre.ID}]
				if value == "" {
					continue
				}
				intentID := "genre:" + value
				addSemanticAliasCandidate(genreCandidates, genreOwners, intentID, genre.Name)
				setSemanticLabel(labels, language, intentID, genre.Name)
			}
		}
		for _, country := range locale.Countries {
			intentID := "country:" + strings.ToLower(country.Code)
			if _, ok := countryByID[intentID]; !ok {
				label := country.NativeName
				if label == "" {
					label = country.EnglishName
				}
				countries = append(countries, semanticDefinitionValue("country", country.Code, label))
				countryByID[intentID] = len(countries) - 1
			}
			addSemanticAliasCandidate(countryCandidates, countryOwners, intentID, country.EnglishName)
			addSemanticAliasCandidate(countryCandidates, countryOwners, intentID, country.NativeName)
			label := country.NativeName
			if label == "" {
				label = country.EnglishName
			}
			setSemanticLabel(labels, language, intentID, label)
		}
	}
	appendUnambiguousSemanticAliases(genres, genreByID, genreCandidates, genreOwners)
	appendUnambiguousSemanticAliases(countries, countryByID, countryCandidates, countryOwners)
	genres = orderSemanticDefinitions(genres)
	countries = orderSemanticDefinitions(countries)
	known := make(map[string]struct{}, len(semanticTypeDefinitions)+len(genres)+len(semanticThemeDefinitions)+len(countries))
	for _, definitions := range [][]semanticDefinition{semanticTypeDefinitions, genres, countries} {
		for _, definition := range definitions {
			known[definition.intent.ID] = struct{}{}
		}
	}
	for _, theme := range semanticThemeDefinitions {
		known[theme.intent.ID] = struct{}{}
	}
	return &semanticVocabulary{genres: genres, countries: countries, labels: labels, known: known}
}

func cloneSemanticDefinitions(values []semanticDefinition) []semanticDefinition {
	result := make([]semanticDefinition, len(values))
	for index, value := range values {
		result[index] = value
		result[index].aliases = make([][]string, len(value.aliases))
		for aliasIndex, alias := range value.aliases {
			result[index].aliases[aliasIndex] = append([]string(nil), alias...)
		}
	}
	return result
}

func definitionIndex(values []semanticDefinition) map[string]int {
	result := make(map[string]int, len(values))
	for index, value := range values {
		result[value.intent.ID] = index
	}
	return result
}

func addSemanticAliasCandidate(candidates map[string]map[string]struct{}, owners map[string]map[string]struct{}, intentID, value string) {
	alias := strings.Join(strings.Fields(normalizeSemanticText(value)), " ")
	if alias == "" {
		return
	}
	if candidates[intentID] == nil {
		candidates[intentID] = make(map[string]struct{})
	}
	candidates[intentID][alias] = struct{}{}
	if owners[alias] == nil {
		owners[alias] = make(map[string]struct{})
	}
	owners[alias][intentID] = struct{}{}
}

func appendUnambiguousSemanticAliases(definitions []semanticDefinition, indexes map[string]int, candidates, owners map[string]map[string]struct{}) {
	for intentID, aliases := range candidates {
		index, ok := indexes[intentID]
		if !ok {
			continue
		}
		seen := make(map[string]struct{}, len(definitions[index].aliases)+len(aliases))
		for _, alias := range definitions[index].aliases {
			seen[strings.Join(alias, " ")] = struct{}{}
		}
		for alias := range aliases {
			if len(owners[alias]) != 1 {
				continue
			}
			if _, duplicate := seen[alias]; duplicate {
				continue
			}
			definitions[index].aliases = append(definitions[index].aliases, strings.Fields(alias))
			seen[alias] = struct{}{}
		}
		sort.SliceStable(definitions[index].aliases, func(left, right int) bool {
			if len(definitions[index].aliases[left]) != len(definitions[index].aliases[right]) {
				return len(definitions[index].aliases[left]) > len(definitions[index].aliases[right])
			}
			return strings.Join(definitions[index].aliases[left], " ") < strings.Join(definitions[index].aliases[right], " ")
		})
	}
}

func setSemanticLabel(labels map[string]map[string]string, language, intentID, label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	if labels[language] == nil {
		labels[language] = make(map[string]string)
	}
	if labels[language][intentID] == "" {
		labels[language][intentID] = label
	}
}

func orderSemanticDefinitions(values []semanticDefinition) []semanticDefinition {
	sort.SliceStable(values, func(left, right int) bool {
		leftLength, rightLength := 0, 0
		if len(values[left].aliases) != 0 {
			leftLength = len(values[left].aliases[0])
		}
		if len(values[right].aliases) != 0 {
			rightLength = len(values[right].aliases[0])
		}
		return leftLength > rightLength
	})
	return values
}
