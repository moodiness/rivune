package collection

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	semanticRecentYearWindow    = 2
	semanticHighRating          = 7.5
	semanticHighRatingVoteCount = 100
	semanticMinimumYear         = 1888
	semanticMaximumRuntime      = 600
)

type semanticQueryConstraints struct {
	releaseDateFrom string
	releaseDateTo   string
	voteAverageMin  *float64
	voteCountMin    *int
	runtimeMin      *int
	runtimeMax      *int
	excludedGenres  []string
	sort            string
}

type semanticConstraintMatch struct {
	start, end int
	intent     SemanticSearchIntent
	apply      func(*semanticQueryConstraints)
	genre      string
	excluded   bool
}

// applySemanticConstraints recognizes only explicit, deterministic filter wording. It runs
// after the ordinary semantic parser so a bare number in a title remains title text.
func applySemanticConstraints(query, language string, now time.Time, parsed *parsedSemanticQuery, excluded map[string]struct{}) {
	if parsed == nil {
		return
	}
	wasNeedsExtension := parsed.needsExtension
	tokens := semanticTokens(query)
	constraints := semanticQueryConstraints{excludedGenres: []string{}}
	matches := semanticConstraintMatches(tokens, language, now, query)
	used := make([]bool, len(tokens))
	accepted := make([]semanticConstraintMatch, 0, len(matches))
	for _, match := range matches {
		if semanticConstraintOverlap(used, match.start, match.end) {
			continue
		}
		if _, match.excluded = excluded[match.intent.ID]; match.excluded {
			accepted = append(accepted, match)
			continue
		}
		candidate := constraints
		match.apply(&candidate)
		if !validSemanticConstraintRanges(candidate) {
			continue
		}
		constraints = candidate
		for index := match.start; index < match.end; index++ {
			used[index] = true
		}
		accepted = append(accepted, match)
		parsed.intents = append(parsed.intents, match.intent)
	}
	for _, match := range accepted {
		if match.genre == "" {
			continue
		}
		parsed.genres = removeSemanticString(parsed.genres, match.genre)
		parsed.intents = removeSemanticIntent(parsed.intents, "genre:"+match.genre)
	}
	parsed.constraints = constraints
	parsed.titleQuery = semanticConstraintTitle(query, parsed.titleQuery, accepted)
	parsed.needsExtension = wasNeedsExtension && semanticConstraintResidual(parsed.titleQuery)
}

func semanticConstraintMatches(tokens []semanticToken, language string, now time.Time, query string) []semanticConstraintMatch {
	matches := make([]semanticConstraintMatch, 0, 8)
	matches = append(matches, semanticNegativeGenreMatches(tokens, language)...)
	if match, ok := semanticReleaseMatch(tokens, language, now); ok {
		matches = append(matches, match)
	}
	if match, ok := semanticRecentMatch(tokens, language, now); ok {
		matches = append(matches, match)
	}
	if match, ok := semanticRatingMatch(tokens, language, strings.ToLower(query)); ok {
		matches = append(matches, match)
	}
	matches = append(matches, semanticRuntimeMatches(tokens, language)...)
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].start != matches[right].start {
			return matches[left].start < matches[right].start
		}
		return matches[left].end > matches[right].end
	})
	return matches
}

func semanticReleaseMatch(tokens []semanticToken, language string, now time.Time) (semanticConstraintMatch, bool) {
	currentYear := now.UTC().Year()
	for index, token := range tokens {
		if decade, ok := semanticDecade(token.normalized, currentYear); ok {
			start := index
			if index > 0 && (tokens[index-1].normalized == "annees" || tokens[index-1].normalized == "decade") {
				start--
				for start > 0 && semanticOneOf(tokens[start-1].normalized, "des", "de", "les", "the", "from") {
					start--
				}
			} else if strings.HasSuffix(token.normalized, "s") {
				for start > 0 && semanticOneOf(tokens[start-1].normalized, "the", "from") {
					start--
				}
			} else {
				continue
			}
			from := fmt.Sprintf("%04d-01-01", decade)
			toYear := decade + 9
			if toYear > currentYear {
				toYear = currentYear
			}
			to := fmt.Sprintf("%04d-12-31", toYear)
			if toYear == currentYear {
				to = now.UTC().Format("2006-01-02")
			}
			value := fmt.Sprintf("%ds", decade)
			intent := semanticConstraintIntent("release_decade", value, semanticConstraintLabel(language, "Released in the "+value, "Sorti dans les années "+strconv.Itoa(decade)))
			return semanticConstraintMatch{start: start, end: index + 1, intent: intent, apply: func(constraints *semanticQueryConstraints) {
				constraints.releaseDateFrom, constraints.releaseDateTo = from, to
			}}, true
		}
	}
	for index, token := range tokens {
		year, err := strconv.Atoi(token.normalized)
		if err != nil || year < semanticMinimumYear || year > currentYear || len(token.normalized) != 4 {
			continue
		}
		start, explicit := semanticExplicitYearStart(tokens, index)
		if !explicit {
			continue
		}
		value := strconv.Itoa(year)
		intent := semanticConstraintIntent("release_year", value, semanticConstraintLabel(language, "Released in "+value, "Sorti en "+value))
		return semanticConstraintMatch{start: start, end: index + 1, intent: intent, apply: func(constraints *semanticQueryConstraints) {
			constraints.releaseDateFrom = value + "-01-01"
			constraints.releaseDateTo = value + "-12-31"
			if year == currentYear {
				constraints.releaseDateTo = now.UTC().Format("2006-01-02")
			}
		}}, true
	}
	return semanticConstraintMatch{}, false
}

func semanticExplicitYearStart(tokens []semanticToken, yearIndex int) (int, bool) {
	if yearIndex < 1 {
		return 0, false
	}
	previous := tokens[yearIndex-1].normalized
	if semanticOneOf(previous, "year", "annee") {
		start := yearIndex - 1
		if start > 0 && semanticOneOf(tokens[start-1].normalized, "from", "of", "de", "l") {
			start--
		}
		return start, true
	}
	if !semanticOneOf(previous, "in", "from", "en", "de") {
		return 0, false
	}
	start := yearIndex - 1
	if start > 0 && semanticOneOf(tokens[start-1].normalized, "released", "release", "sorti", "sortie", "sortis", "sorties") {
		start--
	}
	return start, true
}

func semanticDecade(value string, currentYear int) (int, bool) {
	digits := strings.TrimSuffix(value, "s")
	if len(digits) != 2 && len(digits) != 4 {
		return 0, false
	}
	decade, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	if len(digits) == 2 {
		candidate := 2000 + decade
		if candidate > currentYear/10*10 {
			candidate -= 100
		}
		decade = candidate
	}
	if decade%10 != 0 || decade < 1890 || decade > currentYear/10*10 {
		return 0, false
	}
	return decade, true
}

func semanticRecentMatch(tokens []semanticToken, language string, now time.Time) (semanticConstraintMatch, bool) {
	for index, token := range tokens {
		if !semanticOneOf(token.normalized, "recent", "recente", "recents", "recentes", "latest", "newest", "nouveaute", "nouveautes") {
			continue
		}
		start, end := index, index+1
		if index > 0 && semanticOneOf(tokens[index-1].normalized, "most", "plus", "les") {
			start--
		}
		if end < len(tokens) && semanticOneOf(tokens[end].normalized, "release", "releases", "film", "films", "movie", "movies", "series") {
			end++
		}
		fromYear := now.UTC().Year() - semanticRecentYearWindow
		intent := semanticConstraintIntent("release_recency", "recent", semanticConstraintLabel(language, "Recent releases", "Sorties récentes"))
		return semanticConstraintMatch{start: start, end: end, intent: intent, apply: func(constraints *semanticQueryConstraints) {
			constraints.releaseDateFrom = fmt.Sprintf("%04d-01-01", fromYear)
			constraints.releaseDateTo = now.UTC().Format("2006-01-02")
			constraints.sort = "release_date.desc"
		}}, true
	}
	for index, token := range tokens {
		if !semanticOneOf(token.normalized, "new", "nouveau", "nouveaux", "nouvelle", "nouvelles") || index+1 >= len(tokens) || !semanticOneOf(tokens[index+1].normalized, "release", "releases", "film", "films", "movie", "movies", "series") {
			continue
		}
		fromYear := now.UTC().Year() - semanticRecentYearWindow
		intent := semanticConstraintIntent("release_recency", "recent", semanticConstraintLabel(language, "Recent releases", "Sorties récentes"))
		return semanticConstraintMatch{start: index, end: index + 2, intent: intent, apply: func(constraints *semanticQueryConstraints) {
			constraints.releaseDateFrom = fmt.Sprintf("%04d-01-01", fromYear)
			constraints.releaseDateTo = now.UTC().Format("2006-01-02")
			constraints.sort = "release_date.desc"
		}}, true
	}
	return semanticConstraintMatch{}, false
}

func semanticRatingMatch(tokens []semanticToken, language, query string) (semanticConstraintMatch, bool) {
	highPhrases := [][]string{{"best", "rated"}, {"top", "rated"}, {"highly", "rated"}, {"les", "mieux", "notes"}, {"les", "mieux", "notees"}, {"mieux", "notes"}, {"mieux", "notees"}, {"bien", "note"}, {"bien", "notes"}, {"bien", "notee"}, {"bien", "notees"}}
	for _, phrase := range highPhrases {
		if start := semanticPhraseIndex(tokens, phrase); start >= 0 {
			value := semanticHighRating
			intent := semanticConstraintIntent("rating_quality", "high", semanticConstraintLabel(language, "Highly rated", "Bien noté"))
			return semanticConstraintMatch{start: start, end: start + len(phrase), intent: intent, apply: func(constraints *semanticQueryConstraints) {
				constraints.voteAverageMin = &value
				minimumVotes := semanticHighRatingVoteCount
				constraints.voteCountMin = &minimumVotes
				constraints.sort = "vote_average.desc"
			}}, true
		}
	}
	for index, token := range tokens {
		rating, err := strconv.ParseFloat(token.normalized, 64)
		numberEnd := index + 1
		if index+1 < len(tokens) {
			fraction := tokens[index+1].normalized
			if len(fraction) == 1 && semanticDecimalRatingWritten(query, token.original, tokens[index+1].original) {
				rating, err = strconv.ParseFloat(token.normalized+"."+fraction, 64)
				numberEnd++
			} else if _, splitNumberErr := strconv.Atoi(fraction); splitNumberErr == nil {
				continue
			}
		}
		if err != nil || rating < 0 || rating > 10 {
			continue
		}
		start, end, ok := semanticExplicitRatingRange(tokens, index, numberEnd)
		if !ok {
			continue
		}
		valueText := strconv.FormatFloat(rating, 'f', -1, 64)
		intent := semanticConstraintIntent("rating_min", valueText, semanticConstraintLabel(language, "Rated "+valueText+"+", "Note "+valueText+"+"))
		return semanticConstraintMatch{start: start, end: end, intent: intent, apply: func(constraints *semanticQueryConstraints) {
			value := rating
			constraints.voteAverageMin = &value
		}}, true
	}
	return semanticConstraintMatch{}, false
}

func semanticExplicitRatingRange(tokens []semanticToken, numberStart, numberEnd int) (int, int, bool) {
	patterns := []struct {
		before []string
		after  []string
	}{
		{before: []string{"rating", "at", "least"}, after: []string{"out", "of", "10"}},
		{before: []string{"note", "minimum", "de"}, after: []string{"sur", "10"}},
		{before: []string{"note", "au", "moins"}, after: []string{"sur", "10"}},
		{before: []string{"notee", "au", "moins"}, after: []string{"sur", "10"}},
		{before: []string{"rating", "at", "least"}}, {before: []string{"minimum", "rating"}},
		{before: []string{"min", "rating"}}, {before: []string{"rated", "at", "least"}},
		{before: []string{"note", "minimum", "de"}}, {before: []string{"note", "minimum"}},
		{before: []string{"note", "au", "moins"}}, {before: []string{"notee", "au", "moins"}},
		{before: []string{"rated"}, after: []string{"or", "higher"}},
		{before: []string{"rated"}, after: []string{"or", "above"}},
	}
	for _, pattern := range patterns {
		start := numberStart - len(pattern.before)
		end := numberEnd + len(pattern.after)
		if start < 0 || end > len(tokens) || !semanticTokensEqual(tokens[start:numberStart], pattern.before) || !semanticTokensEqual(tokens[numberEnd:end], pattern.after) {
			continue
		}
		return start, end, true
	}
	return 0, 0, false
}
func semanticDecimalRatingWritten(query, whole, fraction string) bool {
	return strings.Contains(query, whole+"."+fraction) || strings.Contains(query, whole+","+fraction)
}

func semanticRuntimeMatches(tokens []semanticToken, language string) []semanticConstraintMatch {
	matches := make([]semanticConstraintMatch, 0, 2)
	for number, token := range tokens {
		amount, err := strconv.Atoi(token.normalized)
		if err != nil || amount < 1 || number+1 >= len(tokens) {
			continue
		}
		unit := tokens[number+1].normalized
		minutes := amount
		if semanticOneOf(unit, "hour", "hours", "heure", "heures", "hr", "hrs") {
			minutes *= 60
		} else if !semanticOneOf(unit, "minute", "minutes", "min", "mins") {
			continue
		}
		if minutes < 1 || minutes > semanticMaximumRuntime {
			continue
		}
		start, kind, ok := semanticRuntimePrefix(tokens, number)
		if !ok {
			continue
		}
		value := minutes
		valueText := strconv.Itoa(value)
		labelEN, labelFR := "Over "+valueText+" min", "Plus de "+valueText+" min"
		if kind == "runtime_max" {
			labelEN, labelFR = "Under "+valueText+" min", "Moins de "+valueText+" min"
		}
		intent := semanticConstraintIntent(kind, valueText, semanticConstraintLabel(language, labelEN, labelFR))
		match := semanticConstraintMatch{start: start, end: number + 2, intent: intent}
		if kind == "runtime_max" {
			match.apply = func(constraints *semanticQueryConstraints) { runtime := value; constraints.runtimeMax = &runtime }
		} else {
			match.apply = func(constraints *semanticQueryConstraints) { runtime := value; constraints.runtimeMin = &runtime }
		}
		matches = append(matches, match)
	}
	return matches
}

func semanticRuntimePrefix(tokens []semanticToken, number int) (int, string, bool) {
	prefixes := []struct {
		words []string
		kind  string
	}{
		{[]string{"less", "than"}, "runtime_max"}, {[]string{"under"}, "runtime_max"}, {[]string{"max"}, "runtime_max"},
		{[]string{"at", "most"}, "runtime_max"}, {[]string{"moins", "de"}, "runtime_max"}, {[]string{"maximum"}, "runtime_max"},
		{[]string{"more", "than"}, "runtime_min"}, {[]string{"over"}, "runtime_min"}, {[]string{"at", "least"}, "runtime_min"},
		{[]string{"plus", "de"}, "runtime_min"}, {[]string{"minimum"}, "runtime_min"},
	}
	for _, prefix := range prefixes {
		start := number - len(prefix.words)
		if start >= 0 && semanticTokensEqual(tokens[start:number], prefix.words) {
			if start > 0 && semanticOneOf(tokens[start-1].normalized, "runtime", "duree", "duration", "de") {
				start--
			}
			return start, prefix.kind, true
		}
	}
	return 0, "", false
}

func semanticNegativeGenreMatches(tokens []semanticToken, language string) []semanticConstraintMatch {
	matches := make([]semanticConstraintMatch, 0, 2)
	prefixes := [][]string{{"without"}, {"no"}, {"sans"}, {"pas", "de"}}
	for _, definition := range semanticGenreDefinitions {
		for _, alias := range definition.aliases {
			for _, prefix := range prefixes {
				phrase := append(append([]string{}, prefix...), alias...)
				start := semanticPhraseIndex(tokens, phrase)
				if start < 0 {
					continue
				}
				genre := definition.intent.Value
				label := semanticConstraintLabel(language, "Without "+definition.intent.Label, "Sans "+semanticFrenchGenreLabel(genre, definition.intent.Label))
				intent := semanticConstraintIntent("exclude_genre", genre, label)
				matches = append(matches, semanticConstraintMatch{start: start, end: start + len(phrase), intent: intent, genre: genre, apply: func(constraints *semanticQueryConstraints) {
					constraints.excludedGenres = appendUniqueString(constraints.excludedGenres, genre)
				}})
				break
			}
		}
	}
	return matches
}

func semanticConstraintTitle(query, previous string, matches []semanticConstraintMatch) string {
	queryTokens := semanticTokens(query)
	keep := make([]bool, len(queryTokens))
	previousTokens := semanticTokens(previous)
	cursor := 0
	for _, token := range previousTokens {
		for cursor < len(queryTokens) && queryTokens[cursor].normalized != token.normalized {
			cursor++
		}
		if cursor < len(queryTokens) {
			keep[cursor] = true
			cursor++
		}
	}
	for _, match := range matches {
		for index := match.start; index < match.end && index < len(keep); index++ {
			keep[index] = match.excluded
		}
	}
	residual := make([]string, 0, len(queryTokens))
	for index, token := range queryTokens {
		if keep[index] {
			residual = append(residual, token.original)
		}
	}
	return strings.TrimSpace(strings.Join(residual, " "))
}

func semanticConstraintResidual(value string) bool {
	for _, token := range semanticTokens(value) {
		if _, stop := semanticStopWords[token.normalized]; !stop {
			return true
		}
	}
	return false
}

func semanticConstraintIntent(kind, value, label string) SemanticSearchIntent {
	return SemanticSearchIntent{ID: kind + ":" + strings.ToLower(value), Kind: kind, Value: value, Label: label}
}

func semanticConstraintLabel(language, english, french string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(language)), "fr") {
		return french
	}
	return english
}

func semanticFrenchGenreLabel(value, fallback string) string {
	labels := map[string]string{"science_fiction": "science-fiction", "war": "guerre", "comedy": "comédie", "documentary": "documentaire", "history": "historique", "horror": "horreur", "mystery": "mystère"}
	if label := labels[value]; label != "" {
		return label
	}
	return fallback
}

func knownSemanticConstraintIntentID(id string) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(id)), ":")
	if len(parts) != 2 || parts[1] == "" {
		return false
	}
	kind, value := parts[0], parts[1]
	switch kind {
	case "release_year":
		year, err := strconv.Atoi(value)
		return err == nil && len(value) == 4 && year >= semanticMinimumYear && year <= 9999
	case "release_decade":
		decade, ok := semanticDecade(value, 9999)
		return ok && decade >= 1890
	case "release_recency":
		return value == "recent"
	case "rating_min":
		rating, err := strconv.ParseFloat(value, 64)
		return err == nil && rating >= 0 && rating <= 10 && strconv.FormatFloat(rating, 'f', -1, 64) == value
	case "rating_quality":
		return value == "high"
	case "runtime_min", "runtime_max":
		runtime, err := strconv.Atoi(value)
		return err == nil && runtime >= 1 && runtime <= semanticMaximumRuntime && strconv.Itoa(runtime) == value
	case "exclude_genre":
		for _, definition := range semanticGenreDefinitions {
			if definition.intent.Value == value {
				return true
			}
		}
	}
	return false
}

func semanticConstraintOverlap(used []bool, start, end int) bool {
	for index := start; index < end; index++ {
		if index < 0 || index >= len(used) || used[index] {
			return true
		}
	}
	return false
}

func semanticPhraseIndex(tokens []semanticToken, phrase []string) int {
	for start := 0; start+len(phrase) <= len(tokens); start++ {
		if semanticTokensEqual(tokens[start:start+len(phrase)], phrase) {
			return start
		}
	}
	return -1
}

func semanticTokensEqual(tokens []semanticToken, values []string) bool {
	if len(tokens) != len(values) {
		return false
	}
	for index := range tokens {
		if tokens[index].normalized != values[index] {
			return false
		}
	}
	return true
}

func validSemanticConstraintRanges(value semanticQueryConstraints) bool {
	return value.runtimeMin == nil || value.runtimeMax == nil || *value.runtimeMin <= *value.runtimeMax
}

func semanticOneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func removeSemanticString(values []string, remove string) []string {
	result := values[:0]
	for _, value := range values {
		if value != remove {
			result = append(result, value)
		}
	}
	return result
}

func removeSemanticIntent(values []SemanticSearchIntent, id string) []SemanticSearchIntent {
	result := values[:0]
	for _, value := range values {
		if value.ID != id {
			result = append(result, value)
		}
	}
	return result
}
