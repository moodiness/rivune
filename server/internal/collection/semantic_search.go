package collection

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/metadata"
	"github.com/moodiness/rivune/server/internal/requestwork"
	"golang.org/x/text/unicode/norm"
)

const (
	MaximumSemanticSearchLimit           = 40
	maximumSemanticSearchExcludedIntents = 16
)

type SemanticSearchInput struct {
	Query             string
	MediaType         string
	Language          string
	Region            string
	Page              int
	Limit             int
	ExcludedIntentIDs []string
}

type SemanticSearchIntent struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
	Label string `json:"label"`
}

type SemanticSearchPage struct {
	Intents    []SemanticSearchIntent `json:"intents"`
	TitleQuery string                 `json:"titleQuery"`
	MediaTypes []string               `json:"mediaTypes"`
	Items      []Item                 `json:"items"`
	Page       int                    `json:"page"`
	HasMore    bool                   `json:"hasMore"`
	Partial    bool                   `json:"partial"`
}

type semanticDefinition struct {
	intent  SemanticSearchIntent
	aliases [][]string
}

type semanticTheme struct {
	semanticDefinition
	query         string
	acceptedNames map[string]struct{}
}

type semanticToken struct {
	original   string
	normalized string
}

type parsedSemanticQuery struct {
	intents        []SemanticSearchIntent
	titleQuery     string
	mediaTypes     []string
	genres         []string
	themes         []string
	countries      []string
	needsExtension bool
	constraints    semanticQueryConstraints
}

var semanticTypeDefinitions = []semanticDefinition{
	semanticDefinitionValue("media_type", "movie", "Movies", "long métrage", "long métrages", "feature film", "feature films", "film", "films", "movie", "movies", "cinéma", "cinema"),
	semanticDefinitionValue("media_type", "series", "Series", "série télévisée", "séries télévisées", "serie televisee", "series televisees", "tv series", "série", "séries", "serie", "series", "show", "shows"),
	semanticDefinitionValue("media_type", "anime", "Anime", "anime", "animes", "animé", "animés"),
	semanticDefinitionValue("media_type", "tv", "Live TV", "télévision en direct", "television en direct", "live television", "live tv", "chaîne tv", "chaine tv", "chaînes tv", "chaines tv"),
}

var semanticGenreDefinitions = []semanticDefinition{
	semanticDefinitionValue("genre", "science_fiction", "Science Fiction", "science-fiction", "science fiction", "sci-fi", "sci fi", "scifi"),
	semanticDefinitionValue("genre", "war", "War", "guerre", "war", "militaire", "military"),
	semanticDefinitionValue("genre", "crime", "Crime", "policière", "policier", "policières", "policiers", "crime", "detective", "détective"),
	semanticDefinitionValue("genre", "action", "Action", "action"),
	semanticDefinitionValue("genre", "adventure", "Adventure", "aventure", "aventures", "adventure"),
	semanticDefinitionValue("genre", "animation", "Animation", "animation", "animated"),
	semanticDefinitionValue("genre", "comedy", "Comedy", "makes me laugh", "qui fait rire", "fait rire", "comédie", "comedie", "comique", "drôle", "drole", "marrant", "marrante", "rigolo", "rigolote", "amusant", "amusante", "funny", "hilarious", "comedy"),
	semanticDefinitionValue("genre", "documentary", "Documentary", "documentaire", "documentaires", "documentary"),
	semanticDefinitionValue("genre", "drama", "Drama", "drame", "dramatique", "drama"),
	semanticDefinitionValue("genre", "family", "Family", "famille", "familial", "family"),
	semanticDefinitionValue("genre", "fantasy", "Fantasy", "fantastique", "fantasy"),
	semanticDefinitionValue("genre", "history", "History", "historique", "histoire", "history"),
	semanticDefinitionValue("genre", "horror", "Horror", "something spooky", "qui fait peur", "fait peur", "horreur", "épouvante", "epouvante", "flippant", "flippante", "effrayant", "effrayante", "terrifiant", "terrifiante", "scary", "spooky", "frightening", "creepy", "horror"),
	semanticDefinitionValue("genre", "music", "Music", "musical", "musique", "music"),
	semanticDefinitionValue("genre", "mystery", "Mystery", "mystère", "mystere", "mystery"),
	semanticDefinitionValue("genre", "romance", "Romance", "romantique", "romance"),
	semanticDefinitionValue("genre", "thriller", "Thriller", "thriller", "suspense"),
	semanticDefinitionValue("genre", "western", "Western", "western"),
}

var semanticThemeDefinitions = []semanticTheme{
	semanticThemeValue("space", "Space", "space", []string{"space", "outer space", "space travel"}, "dans l'espace", "dans espace", "espace", "spatial", "spatiale", "cosmos", "outer space", "space travel", "space"),
	semanticThemeValue("time_travel", "Time travel", "time travel", []string{"time travel"}, "voyage dans le temps", "voyages dans le temps", "time travel"),
	semanticThemeValue("artificial_intelligence", "Artificial intelligence", "artificial intelligence", []string{"artificial intelligence"}, "intelligence artificielle", "artificial intelligence"),
	semanticThemeValue("serial_killer", "Serial killer", "serial killer", []string{"serial killer"}, "tueur en série", "tueur en serie", "serial killer"),
	semanticThemeValue("superhero", "Superhero", "superhero", []string{"superhero"}, "super-héros", "super héros", "super heros", "superhero", "superheroes"),
	semanticThemeValue("dystopia", "Dystopia", "dystopia", []string{"dystopia"}, "dystopie", "dystopia"),
	semanticThemeValue("apocalypse", "Apocalypse", "apocalypse", []string{"apocalypse"}, "post-apocalyptique", "post apocalyptique", "apocalypse"),
	semanticThemeValue("zombie", "Zombies", "zombie", []string{"zombie"}, "zombie", "zombies"),
	semanticThemeValue("vampire", "Vampires", "vampire", []string{"vampire"}, "vampire", "vampires"),
	semanticThemeValue("prison", "Prison", "prison", []string{"prison"}, "prison", "carcéral", "carceral"),
	semanticThemeValue("heist", "Heist", "heist", []string{"heist"}, "cambriolage", "braquage", "heist"),
}

var semanticCountryDefinitions = []semanticDefinition{
	semanticDefinitionValue("country", "GB", "United Kingdom", "royaume-uni", "royaume uni", "britannique", "britanniques", "british"),
	semanticDefinitionValue("country", "FR", "France", "française", "francais", "français", "french"),
	semanticDefinitionValue("country", "US", "United States", "américaine", "americain", "américain", "american"),
	semanticDefinitionValue("country", "KR", "South Korea", "sud-coréenne", "sud coreenne", "coréenne", "coreenne", "korean"),
	semanticDefinitionValue("country", "JP", "Japan", "japonaise", "japonais", "japanese"),
	semanticDefinitionValue("country", "ES", "Spain", "espagnole", "espagnol", "spanish"),
	semanticDefinitionValue("country", "IT", "Italy", "italienne", "italien", "italian"),
	semanticDefinitionValue("country", "DE", "Germany", "allemande", "allemand", "german"),
	semanticDefinitionValue("country", "CA", "Canada", "canadienne", "canadien", "canadian"),
}

var semanticStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "avec": {}, "d": {}, "dans": {}, "de": {}, "des": {}, "du": {}, "en": {}, "et": {},
	"in": {}, "l": {}, "la": {}, "le": {}, "les": {}, "of": {}, "pour": {}, "sur": {}, "the": {}, "un": {}, "une": {}, "with": {},
}

func (service *Service) SemanticSearch(ctx context.Context, principal auth.Principal, input SemanticSearchInput) (SemanticSearchPage, error) {
	ctx = service.pinProviders(ctx)
	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
		return SemanticSearchPage{}, err
	}
	input.Query = strings.TrimSpace(input.Query)
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	language, region, err := normalizeResolutionOptions(input.Language, input.Region)
	if err != nil || !utf8.ValidString(input.Query) || utf8.RuneCountInString(input.Query) < 2 || utf8.RuneCountInString(input.Query) > 200 ||
		input.Page < 1 || input.Page > 1000 || input.Limit < 1 || input.Limit > MaximumSemanticSearchLimit ||
		!validSemanticMediaType(input.MediaType) || len(input.ExcludedIntentIDs) > maximumSemanticSearchExcludedIntents {
		return SemanticSearchPage{}, ErrInvalidInput
	}
	providers := collectionProviders(ctx)
	vocabulary, catalogPartial, err := service.semanticCatalog.vocabulary(ctx, providers.TMDB, language)
	if err != nil {
		return SemanticSearchPage{}, err
	}
	excluded, err := normalizeSemanticExcludedIntentIDsWithVocabulary(input.ExcludedIntentIDs, vocabulary)
	if err != nil {
		return SemanticSearchPage{}, err
	}
	parsed := parseSemanticQueryWithVocabulary(input.Query, input.MediaType, excluded, vocabulary, language)
	applySemanticConstraints(input.Query, language, time.Now().UTC(), &parsed, excluded)

	result := SemanticSearchPage{
		Intents: parsed.intents, TitleQuery: parsed.titleQuery, MediaTypes: parsed.mediaTypes,
		Items: []Item{}, Page: input.Page, Partial: catalogPartial,
	}
	if providers.TMDB == nil {
		parsed, extensionPartial, extensionErr := service.resolveSemanticExtensionOnly(ctx, parsed, vocabulary, excluded, language)
		if extensionErr != nil {
			return SemanticSearchPage{}, extensionErr
		}
		if _, err := service.validateActiveProfile(ctx, principal); err != nil {
			return SemanticSearchPage{}, err
		}
		result.Intents, result.TitleQuery, result.MediaTypes = parsed.intents, parsed.titleQuery, parsed.mediaTypes
		result.Partial = result.Partial || extensionPartial
		if semanticHasSearchCriteria(parsed) || semanticShouldSearchTitle(parsed, false) {
			result.Partial = true
		}
		return result, nil
	}

	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
		return SemanticSearchPage{}, err
	}
	ambiguity, err := service.resolveSemanticAmbiguity(ctx, providers.TMDB, parsed, vocabulary, excluded, input.Page, language, region)
	if err != nil {
		return SemanticSearchPage{}, err
	}
	parsed = ambiguity.parsed
	result.Partial = result.Partial || ambiguity.partial
	result.HasMore = ambiguity.hasMore
	if ambiguity.searchByTitle {
		return service.finishSemanticSearch(ctx, principal, input.Limit, parsed, result, ambiguity.pages, true)
	}
	if !semanticHasSearchCriteria(parsed) {
		return result, nil
	}
	keywordIDs, keywordPartial, keywordErr := service.semanticKeywordIDs(ctx, providers.TMDB, parsed.themes, language)
	if keywordErr != nil {
		return SemanticSearchPage{}, keywordErr
	}
	result.Partial = result.Partial || keywordPartial
	if len(parsed.themes) != 0 && len(keywordIDs) == 0 {
		result.Partial = true
		return result, nil
	}
	sources := semanticTMDBSources(parsed, keywordIDs)
	if len(sources) == 0 {
		return result, nil
	}
	outcomes := startSemanticSourceResolution(ctx, providers.TMDB, "", sources, input.Page, language, region, false)
	pages, hasMore, sourcePartial, _, err := service.collectSemanticSourceResolution(ctx, outcomes, len(sources), nil, false)
	result.HasMore = hasMore
	result.Partial = result.Partial || sourcePartial
	if err != nil {
		return SemanticSearchPage{}, err
	}
	return service.finishSemanticSearch(ctx, principal, input.Limit, parsed, result, pages, false)
}

type semanticAmbiguityResult struct {
	parsed        parsedSemanticQuery
	pages         []SourcePage
	hasMore       bool
	partial       bool
	searchByTitle bool
}

func (service *Service) resolveSemanticExtensionOnly(ctx context.Context, parsed parsedSemanticQuery, vocabulary *semanticVocabulary, excluded map[string]struct{}, language string) (parsedSemanticQuery, bool, error) {
	extension := service.currentSemanticExtension()
	if extension == nil || !parsed.needsExtension {
		return parsed, false, nil
	}
	candidates := semanticExtensionCandidates(vocabulary, language, excluded, parsed)
	if len(candidates) == 0 {
		return parsed, false, nil
	}
	matches, err := service.semanticMemo.resolve(ctx, extension, SemanticExtensionRequest{
		Query: parsed.titleQuery, Language: language, Candidates: candidates,
	})
	if err != nil {
		partial, requestErr := semanticExtensionFallback(ctx, err)
		if requestErr != nil {
			return parsed, false, requestErr
		}
		service.warnSemantic(ctx, "semantic local extension", err)
		return parsed, partial, nil
	}
	if len(matches) == 0 {
		return parsed, false, nil
	}
	if err := applySemanticExtension(&parsed, vocabulary, language, matches); err != nil {
		service.warnSemantic(ctx, "validate semantic local extension", err)
		return parsed, true, nil
	}
	return parsed, false, nil
}

func (service *Service) resolveSemanticAmbiguity(ctx context.Context, provider TMDBProvider, parsed parsedSemanticQuery, vocabulary *semanticVocabulary, excluded map[string]struct{}, page int, language, region string) (semanticAmbiguityResult, error) {
	result := semanticAmbiguityResult{parsed: parsed}
	if !semanticShouldSearchTitle(parsed, false) {
		return result, nil
	}
	titleSources := semanticTMDBTitleSources(parsed)
	if len(titleSources) == 0 {
		return result, nil
	}
	probeCtx, cancelProbes := context.WithCancel(ctx)
	extensionCtx, cancelExtension := context.WithCancel(ctx)
	defer cancelProbes()
	defer cancelExtension()

	titleOutcomes := startSemanticSourceResolution(probeCtx, provider, parsed.titleQuery, titleSources, page, language, region, true)
	var extensionOutcomes <-chan semanticExtensionOutcome
	candidates := semanticExtensionCandidates(vocabulary, language, excluded, parsed)
	extension := service.currentSemanticExtension()
	if extension != nil && len(candidates) != 0 {
		extensionOutcomes = service.startSemanticExtensionResolution(extensionCtx, extension, SemanticExtensionRequest{
			Query: parsed.titleQuery, Language: language, Candidates: candidates,
		})
	}

	var probePartial bool
	var exact bool
	var err error
	onExact := func() {
		cancelExtension()
	}
	result.pages, result.hasMore, probePartial, exact, err = service.collectSemanticSourceResolution(ctx, titleOutcomes, len(titleSources), onExact, true)
	if err != nil {
		return semanticAmbiguityResult{}, err
	}
	result.partial = probePartial
	if exact {
		result.searchByTitle = true
		return result, nil
	}
	if extensionOutcomes == nil {
		result.partial = probePartial
		result.searchByTitle = true
		return result, nil
	}
	extensionResult := <-extensionOutcomes
	if extensionResult.err != nil {
		extensionPartial, requestErr := semanticExtensionFallback(ctx, extensionResult.err)
		if requestErr != nil {
			return semanticAmbiguityResult{}, requestErr
		}
		result.partial = probePartial || extensionPartial
		result.searchByTitle = true
		service.warnSemantic(ctx, "semantic local extension", extensionResult.err)
		return result, nil
	}
	if len(extensionResult.matches) == 0 {
		result.partial = probePartial
		result.searchByTitle = true
		return result, nil
	}
	intentCount := len(result.parsed.intents)
	if applyErr := applySemanticExtension(&result.parsed, vocabulary, language, extensionResult.matches); applyErr != nil {
		result.partial = true
		result.searchByTitle = true
		service.warnSemantic(ctx, "validate semantic local extension", applyErr)
		return result, nil
	}
	if len(result.parsed.intents) == intentCount {
		result.partial = probePartial
		result.searchByTitle = true
	}
	return result, nil
}

type semanticExtensionOutcome struct {
	matches []string
	err     error
}

type semanticSourceOutcome struct {
	index int
	page  SourcePage
	err   error
}

func (service *Service) startSemanticExtensionResolution(ctx context.Context, extension SemanticExtension, request SemanticExtensionRequest) <-chan semanticExtensionOutcome {
	outcomes := make(chan semanticExtensionOutcome, 1)
	go func() {
		matches, err := service.semanticMemo.resolve(ctx, extension, request)
		outcomes <- semanticExtensionOutcome{matches: matches, err: err}
	}()
	return outcomes
}

func startSemanticSourceResolution(ctx context.Context, provider TMDBProvider, title string, sources []TMDBSource, page int, language, region string, searchByTitle bool) <-chan semanticSourceOutcome {
	outcomes := make(chan semanticSourceOutcome, len(sources))
	for index, source := range sources {
		go func(index int, source TMDBSource) {
			var resolved SourcePage
			var err error
			if searchByTitle {
				resolved, err = provider.SearchCollectionTitles(ctx, title, source, page, language, region)
			} else {
				resolved, err = provider.ResolveCollectionSource(ctx, source, page, language, region)
			}
			outcomes <- semanticSourceOutcome{index: index, page: resolved, err: err}
		}(index, source)
	}
	return outcomes
}

func (service *Service) collectSemanticSourceResolution(ctx context.Context, outcomes <-chan semanticSourceOutcome, count int, onExact context.CancelFunc, searchByTitle bool) ([]SourcePage, bool, bool, bool, error) {
	pages := make([]SourcePage, count)
	hasMore := false
	partial := false
	exact := false
	for range count {
		var resolved semanticSourceOutcome
		select {
		case <-ctx.Done():
			return nil, false, false, false, ctx.Err()
		case resolved = <-outcomes:
		}
		if requestErr := ctx.Err(); requestErr != nil {
			return nil, false, false, false, requestErr
		}
		if resolved.err != nil {
			if !searchByTitle && (errors.Is(resolved.err, context.Canceled) || errors.Is(resolved.err, context.DeadlineExceeded)) {
				return nil, false, false, false, resolved.err
			}
			partial = true
			operation := "semantic TMDB discover"
			if searchByTitle {
				operation = "semantic TMDB title search"
			}
			service.warnSemantic(ctx, operation, resolved.err)
			continue
		}
		pages[resolved.index] = resolved.page
		hasMore = hasMore || resolved.page.HasMore
		if searchByTitle && resolved.page.ExactTitleMatch && !exact {
			exact = true
			if onExact != nil {
				onExact()
			}
		}
	}
	return pages, hasMore, partial, exact, nil
}

func (service *Service) finishSemanticSearch(ctx context.Context, principal auth.Principal, limit int, parsed parsedSemanticQuery, result SemanticSearchPage, pages []SourcePage, preserveProviderOrder bool) (SemanticSearchPage, error) {
	if _, err := service.validateActiveProfile(ctx, principal); err != nil {
		return SemanticSearchPage{}, err
	}
	result.Intents = parsed.intents
	result.TitleQuery = parsed.titleQuery
	result.MediaTypes = parsed.mediaTypes
	items := deduplicateSemanticItems(semanticPageItems(pages, preserveProviderOrder || parsed.constraints.sort != ""))
	if !preserveProviderOrder && parsed.constraints.sort == "" {
		sort.SliceStable(items, func(left, right int) bool {
			return semanticPopularity(items[left]) > semanticPopularity(items[right])
		})
	}
	if len(items) > limit {
		items = items[:limit]
		result.HasMore = true
	}
	for index := range items {
		if items[index].ExternalIDs == nil {
			items[index].ExternalIDs = map[string]string{}
		}
		if items[index].Sources == nil {
			items[index].Sources = []SourceReference{}
		}
	}
	result.Items = items
	return result, nil
}

func semanticHasSearchCriteria(parsed parsedSemanticQuery) bool {
	return len(parsed.genres) != 0 || len(parsed.themes) != 0 || len(parsed.countries) != 0 ||
		slices.Contains(parsed.mediaTypes, "anime") || !semanticConstraintsEmpty(parsed.constraints)
}
func semanticExtensionFallback(ctx context.Context, extensionErr error) (bool, error) {
	if extensionErr == nil {
		return false, nil
	}
	if requestErr := ctx.Err(); requestErr != nil {
		return false, requestErr
	}
	return true, nil
}

func (service *Service) warnSemantic(ctx context.Context, message string, err error) {
	if service.logger == nil {
		return
	}
	attributes := []any{"error_code", semanticErrorCode(err)}
	if requestID := requestwork.RequestID(ctx); requestID != "" {
		attributes = append(attributes, "request_id", requestID)
	}
	service.logger.WarnContext(ctx, message, attributes...)
}

func semanticErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, metadata.ErrProviderUnauthorized):
		return "provider_unauthorized"
	case errors.Is(err, metadata.ErrProviderRateLimited):
		return "provider_rate_limited"
	case errors.Is(err, metadata.ErrProviderNotFound):
		return "provider_not_found"
	case errors.Is(err, metadata.ErrProviderUnavailable), errors.Is(err, ErrProviderUnavailable):
		return "provider_unavailable"
	case errors.Is(err, metadata.ErrProviderFailure):
		return "provider_failure"
	case errors.Is(err, ErrSemanticExtensionBusy):
		return "extension_busy"
	case errors.Is(err, ErrInvalidInput), errors.Is(err, errInvalidSemanticExtensionSelection):
		return "invalid_response"
	default:
		return "internal_failure"
	}
}

func (service *Service) semanticKeywordIDs(ctx context.Context, provider TMDBProvider, themes []string, language string) ([]int64, bool, error) {
	seen := make(map[int64]struct{})
	ids := make([]int64, 0, len(themes)*2)
	partial := false
	for _, value := range themes {
		theme, ok := semanticThemeByValue(value)
		if !ok {
			continue
		}
		results, err := provider.LookupCollectionSource(ctx, "keyword", theme.query, language, 1)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, false, err
			}
			partial = true
			service.warnSemantic(ctx, "resolve semantic TMDB keyword", err)
			continue
		}
		matched := 0
		for _, result := range results {
			if _, accepted := theme.acceptedNames[normalizeSemanticText(result.Name)]; !accepted || result.ID < 1 {
				continue
			}
			if _, duplicate := seen[result.ID]; duplicate {
				continue
			}
			seen[result.ID] = struct{}{}
			ids = append(ids, result.ID)
			matched++
			if matched == 3 {
				break
			}
		}
		if matched == 0 {
			partial = true
		}
	}
	return ids, partial, nil
}

func parseSemanticQuery(query, explicitMediaType string, excluded map[string]struct{}) parsedSemanticQuery {
	return parseSemanticQueryWithVocabulary(query, explicitMediaType, excluded, nil, "")
}

func parseSemanticQueryWithVocabulary(query, explicitMediaType string, excluded map[string]struct{}, vocabulary *semanticVocabulary, language string) parsedSemanticQuery {
	tokens := semanticTokens(query)
	claimed := make([]bool, len(tokens))
	removed := make([]bool, len(tokens))
	parsed := parsedSemanticQuery{intents: []SemanticSearchIntent{}, mediaTypes: []string{}}
	if explicitMediaType != "" {
		parsed.mediaTypes = append(parsed.mediaTypes, explicitMediaType)
	}
	genreDefinitions := semanticGenreDefinitions
	if vocabulary != nil {
		genreDefinitions = vocabulary.genres
	}
	compoundGenres := make(map[string]struct{})
	for _, definition := range genreDefinitions {
		_, isExcluded := excluded[definition.intent.ID]
		if matchSemanticCompoundDefinition(tokens, definition, claimed, removed, !isExcluded) {
			compoundGenres[definition.intent.ID] = struct{}{}
		}
	}
	for _, definition := range semanticTypeDefinitions {
		_, isExcluded := excluded[definition.intent.ID]
		if explicitMediaType != "" {
			markSemanticDefinition(tokens, definition, removed)
			continue
		}
		if isExcluded {
			continue
		}
		if !matchSemanticDefinition(tokens, definition, claimed, removed, true) {
			continue
		}
		intent := definition.intent
		if vocabulary != nil {
			intent = vocabulary.label(intent, language)
		}
		parsed.intents = append(parsed.intents, intent)
		parsed.mediaTypes = appendUniqueString(parsed.mediaTypes, definition.intent.Value)
	}
	for _, definition := range genreDefinitions {
		_, isExcluded := excluded[definition.intent.ID]
		_, compoundMatched := compoundGenres[definition.intent.ID]
		if !compoundMatched && !matchSemanticDefinition(tokens, definition, claimed, removed, !isExcluded) || isExcluded {
			continue
		}
		intent := definition.intent
		if vocabulary != nil {
			intent = vocabulary.label(intent, language)
		}
		parsed.intents = append(parsed.intents, intent)
		parsed.genres = appendUniqueString(parsed.genres, definition.intent.Value)
	}
	for _, theme := range semanticThemeDefinitions {
		_, isExcluded := excluded[theme.intent.ID]
		if !matchSemanticDefinition(tokens, theme.semanticDefinition, claimed, removed, !isExcluded) || isExcluded {
			continue
		}
		intent := theme.intent
		if vocabulary != nil {
			intent = vocabulary.label(intent, language)
		}
		parsed.intents = append(parsed.intents, intent)
		parsed.themes = appendUniqueString(parsed.themes, theme.intent.Value)
	}
	countryDefinitions := semanticCountryDefinitions
	if vocabulary != nil {
		countryDefinitions = vocabulary.countries
	}
	for _, definition := range countryDefinitions {
		_, isExcluded := excluded[definition.intent.ID]
		if !matchSemanticDefinition(tokens, definition, claimed, removed, !isExcluded) || isExcluded {
			continue
		}
		intent := definition.intent
		if vocabulary != nil {
			intent = vocabulary.label(intent, language)
		}
		parsed.intents = append(parsed.intents, intent)
		parsed.countries = appendUniqueString(parsed.countries, definition.intent.Value)
	}
	for index, token := range tokens {
		if claimed[index] {
			continue
		}
		if _, stopWord := semanticStopWords[token.normalized]; !stopWord {
			parsed.needsExtension = true
			break
		}
	}
	residual := make([]string, 0, len(tokens))
	removedAny := false
	for _, value := range removed {
		removedAny = removedAny || value
	}
	for index, token := range tokens {
		if removed[index] {
			continue
		}
		if _, stopWord := semanticStopWords[token.normalized]; removedAny && stopWord {
			continue
		}
		residual = append(residual, token.original)
	}
	parsed.titleQuery = strings.TrimSpace(strings.Join(residual, " "))
	if parsed.titleQuery == "" {
		parsed.titleQuery = strings.TrimSpace(query)
	}
	return parsed
}

func semanticDefinitionValue(kind, value, label string, aliases ...string) semanticDefinition {
	return semanticDefinition{
		intent:  SemanticSearchIntent{ID: kind + ":" + strings.ToLower(value), Kind: kind, Value: value, Label: label},
		aliases: semanticAliasTokens(aliases),
	}
}

func semanticThemeValue(value, label, query string, acceptedNames []string, aliases ...string) semanticTheme {
	accepted := make(map[string]struct{}, len(acceptedNames))
	for _, name := range acceptedNames {
		accepted[normalizeSemanticText(name)] = struct{}{}
	}
	return semanticTheme{
		semanticDefinition: semanticDefinitionValue("theme", value, label, aliases...),
		query:              query, acceptedNames: accepted,
	}
}

func semanticAliasTokens(values []string) [][]string {
	result := make([][]string, 0, len(values))
	for _, value := range values {
		fields := strings.Fields(normalizeSemanticText(value))
		if len(fields) != 0 {
			result = append(result, fields)
		}
	}
	sort.SliceStable(result, func(left, right int) bool { return len(result[left]) > len(result[right]) })
	return result
}

func semanticTokens(value string) []semanticToken {
	clean := strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return character
		}
		return ' '
	}, value)
	fields := strings.Fields(clean)
	result := make([]semanticToken, 0, len(fields))
	for _, field := range fields {
		result = append(result, semanticToken{original: field, normalized: normalizeSemanticText(field)})
	}
	return result
}

func normalizeSemanticText(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	return strings.Map(func(character rune) rune {
		if unicode.Is(unicode.Mn, character) {
			return -1
		}
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			return character
		}
		return ' '
	}, decomposed)
}

func matchSemanticDefinition(tokens []semanticToken, definition semanticDefinition, claimed, removed []bool, remove bool) bool {
	for _, alias := range definition.aliases {
		for start := 0; start+len(alias) <= len(tokens); start++ {
			matched := true
			for offset, expected := range alias {
				if claimed[start+offset] || tokens[start+offset].normalized != expected {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			for offset := range alias {
				claimed[start+offset] = true
				if remove {
					removed[start+offset] = true
				}
			}
			return true
		}
	}
	return false
}

func matchSemanticCompoundDefinition(tokens []semanticToken, definition semanticDefinition, claimed, removed []bool, remove bool) bool {
	for _, alias := range definition.aliases {
		if len(alias) < 2 {
			continue
		}
		for start := 0; start+len(alias) <= len(tokens); start++ {
			matched := true
			for offset, expected := range alias {
				if claimed[start+offset] || tokens[start+offset].normalized != expected {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			for offset := range alias {
				claimed[start+offset] = true
				if remove {
					removed[start+offset] = true
				}
			}
			return true
		}
	}
	return false
}

func markSemanticDefinition(tokens []semanticToken, definition semanticDefinition, removed []bool) {
	for _, alias := range definition.aliases {
		for start := 0; start+len(alias) <= len(tokens); start++ {
			matched := true
			for offset, expected := range alias {
				if tokens[start+offset].normalized != expected {
					matched = false
					break
				}
			}
			if matched {
				for offset := range alias {
					removed[start+offset] = true
				}
				return
			}
		}
	}
}

func normalizeSemanticExcludedIntentIDs(values []string) (map[string]struct{}, error) {
	return normalizeSemanticExcludedIntentIDsWithVocabulary(values, nil)
}

func normalizeSemanticExcludedIntentIDsWithVocabulary(values []string, vocabulary *semanticVocabulary) (map[string]struct{}, error) {
	excluded := make(map[string]struct{}, len(values))
	for _, id := range values {
		id = strings.ToLower(strings.TrimSpace(id))
		known := knownSemanticIntentID(id)
		if vocabulary != nil {
			known = known || vocabulary.knows(id)
		}
		if !known {
			return nil, ErrInvalidInput
		}
		if _, duplicate := excluded[id]; duplicate {
			return nil, ErrInvalidInput
		}
		excluded[id] = struct{}{}
	}
	return excluded, nil
}

func validSemanticMediaType(value string) bool {
	return value == "" || value == MediaTypeMovie || value == MediaTypeSeries || value == "anime" || value == MediaTypeTV || value == "other"
}

func knownSemanticIntentID(id string) bool {
	if knownSemanticConstraintIntentID(id) {
		return true
	}
	for _, definitions := range [][]semanticDefinition{semanticTypeDefinitions, semanticGenreDefinitions, semanticCountryDefinitions} {
		for _, definition := range definitions {
			if definition.intent.ID == id {
				return true
			}
		}
	}
	for _, definition := range semanticThemeDefinitions {
		if definition.intent.ID == id {
			return true
		}
	}
	return false
}

func semanticThemeByValue(value string) (semanticTheme, bool) {
	for _, definition := range semanticThemeDefinitions {
		if definition.intent.Value == value {
			return definition, true
		}
	}
	return semanticTheme{}, false
}

func semanticTMDBMediaTypes(values []string) []string {
	if len(values) == 0 {
		return []string{MediaTypeMovie, MediaTypeSeries}
	}
	result := make([]string, 0, 2)
	for _, value := range values {
		switch value {
		case MediaTypeMovie:
			result = appendUniqueString(result, MediaTypeMovie)
		case MediaTypeSeries:
			result = appendUniqueString(result, MediaTypeSeries)
		case "anime":
			result = appendUniqueString(result, MediaTypeMovie)
			result = appendUniqueString(result, MediaTypeSeries)
		}
	}
	return result
}

func semanticTMDBSources(parsed parsedSemanticQuery, keywordIDs []int64) []TMDBSource {
	if !semanticHasSearchCriteria(parsed) {
		return nil
	}
	mediaTypes := semanticTMDBMediaTypes(parsed.mediaTypes)
	semanticGenres := slices.Clone(parsed.genres)
	if slices.Contains(parsed.mediaTypes, "anime") {
		semanticGenres = appendUniqueString(semanticGenres, "animation")
	}
	sources := make([]TMDBSource, 0, len(mediaTypes))
	for _, mediaType := range mediaTypes {
		genreIDs, genreOK := semanticGenreIDs(semanticGenres, mediaType)
		if len(semanticGenres) != 0 && !genreOK {
			continue
		}
		filters := semanticTMDBFilters(parsed, mediaType)
		filters.Genres = genreIDs
		filters.Keywords = keywordIDs
		sortValue := parsed.constraints.sort
		if sortValue == "" {
			sortValue = "popularity.desc"
		}
		sources = append(sources, TMDBSource{
			SourceType: "discover", MediaType: mediaType, Sort: sortValue, Filters: filters,
		})
	}
	return sources
}
func semanticShouldSearchTitle(parsed parsedSemanticQuery, extensionMatched bool) bool {
	return parsed.needsExtension && !extensionMatched && strings.TrimSpace(parsed.titleQuery) != ""
}

func semanticTMDBTitleSources(parsed parsedSemanticQuery) []TMDBSource {
	mediaTypes := semanticTMDBMediaTypes(parsed.mediaTypes)
	if len(mediaTypes) == 0 && len(parsed.mediaTypes) != 0 {
		return nil
	}
	genreValues := slices.Clone(parsed.genres)
	if slices.Contains(parsed.mediaTypes, "anime") {
		genreValues = appendUniqueString(genreValues, "animation")
	}
	sources := make([]TMDBSource, 0, len(mediaTypes))
	for _, mediaType := range mediaTypes {
		genreIDs, genreOK := semanticGenreIDs(genreValues, mediaType)
		if len(genreValues) != 0 && !genreOK {
			continue
		}
		filters := semanticTMDBFilters(parsed, mediaType)
		filters.Genres = genreIDs
		sortValue := parsed.constraints.sort
		if sortValue == "" {
			sortValue = "original"
		}
		sources = append(sources, TMDBSource{SourceType: "search", MediaType: mediaType, Sort: sortValue, Filters: filters})
	}
	return sources
}

func semanticTMDBFilters(parsed parsedSemanticQuery, mediaType string) TMDBFilters {
	excludedGenreIDs, _ := semanticGenreIDs(parsed.constraints.excludedGenres, mediaType)
	filters := TMDBFilters{
		ExcludedGenres:  excludedGenreIDs,
		ReleaseDateFrom: parsed.constraints.releaseDateFrom,
		ReleaseDateTo:   parsed.constraints.releaseDateTo,
		VoteAverageMin:  parsed.constraints.voteAverageMin,
		VoteCountMin:    parsed.constraints.voteCountMin,
		RuntimeMin:      parsed.constraints.runtimeMin,
		RuntimeMax:      parsed.constraints.runtimeMax,
	}
	if slices.Contains(parsed.mediaTypes, "anime") {
		filters.OriginalLanguage = "ja"
	}
	if len(parsed.countries) != 0 {
		filters.OriginCountry = parsed.countries[0]
	}
	return filters
}

func semanticConstraintsEmpty(constraints semanticQueryConstraints) bool {
	return constraints.releaseDateFrom == "" && constraints.releaseDateTo == "" && constraints.voteAverageMin == nil && constraints.voteCountMin == nil &&
		constraints.runtimeMin == nil && constraints.runtimeMax == nil && len(constraints.excludedGenres) == 0 && constraints.sort == ""
}

func semanticPageItems(pages []SourcePage, preserveProviderOrder bool) []Item {
	items := make([]Item, 0)
	if preserveProviderOrder {
		maximumLength := 0
		for _, page := range pages {
			maximumLength = max(maximumLength, len(page.Items))
		}
		for itemIndex := range maximumLength {
			for _, page := range pages {
				if itemIndex < len(page.Items) {
					items = append(items, page.Items[itemIndex])
				}
			}
		}
		return items
	}
	for _, page := range pages {
		items = append(items, page.Items...)
	}
	return items
}

var semanticMovieGenreIDs = map[string][]int64{
	"action": {28}, "adventure": {12}, "animation": {16}, "comedy": {35}, "crime": {80},
	"documentary": {99}, "drama": {18}, "family": {10751}, "fantasy": {14}, "history": {36},
	"horror": {27}, "music": {10402}, "mystery": {9648}, "romance": {10749},
	"science_fiction": {878}, "thriller": {53}, "war": {10752}, "western": {37},
	"action_adventure": {28, 12}, "science_fiction_fantasy": {878, 14}, "tv_movie": {10770},
	"war_politics": {10752, 36},
}

var semanticSeriesGenreIDs = map[string][]int64{
	"action": {10759}, "adventure": {10759}, "animation": {16}, "comedy": {35}, "crime": {80},
	"documentary": {99}, "drama": {18}, "family": {10751}, "fantasy": {10765}, "history": {10768},
	"horror": {10765}, "music": {10767}, "mystery": {9648}, "romance": {18}, "science_fiction": {10765},
	"thriller": {18}, "war": {10768},
	"western": {37}, "action_adventure": {10759}, "science_fiction_fantasy": {10765}, "kids": {10762},
	"news": {10763}, "reality": {10764}, "soap": {10766}, "talk": {10767}, "war_politics": {10768},
}

func semanticGenreIDs(values []string, mediaType string) ([]int64, bool) {
	ids := make([]int64, 0, len(values)+1)
	genreIDs := semanticMovieGenreIDs
	if mediaType == MediaTypeSeries {
		genreIDs = semanticSeriesGenreIDs
	}
	for _, value := range values {
		mapped, ok := genreIDs[value]
		if !ok {
			return nil, false
		}
		for _, id := range mapped {
			ids = appendUniqueInt64(ids, id)
		}
	}
	return ids, true
}

func deduplicateSemanticItems(values []Item) []Item {
	seen := make(map[string]struct{}, len(values))
	result := make([]Item, 0, len(values))
	for _, value := range values {
		key := itemKey(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func semanticPopularity(value Item) float64 {
	if value.Popularity == nil {
		return 0
	}
	return *value.Popularity
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueInt64(values []int64, value int64) []int64 {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
