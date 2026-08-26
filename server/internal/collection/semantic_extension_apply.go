package collection

import (
	"errors"
	"slices"
	"strings"
)

func semanticExtensionCandidates(vocabulary *semanticVocabulary, language string, excluded map[string]struct{}, parsed parsedSemanticQuery) []SemanticExtensionCandidate {
	if vocabulary == nil {
		return nil
	}
	selected := make(map[string]struct{}, len(parsed.intents))
	for _, intent := range parsed.intents {
		selected[intent.ID] = struct{}{}
	}
	result := make([]SemanticExtensionCandidate, 0, min(len(vocabulary.known), maximumSemanticExtensionCandidates))
	for _, intent := range vocabulary.intents() {
		if _, skip := excluded[intent.ID]; skip {
			continue
		}
		if _, skip := selected[intent.ID]; skip {
			continue
		}
		if intent.Kind == "country" || intent.Kind == "media_type" {
			continue
		}
		intent = vocabulary.label(intent, language)
		result = append(result, SemanticExtensionCandidate{ID: intent.ID, Kind: intent.Kind, Label: intent.Label})
		if len(result) == maximumSemanticExtensionCandidates {
			break
		}
	}
	return result
}

func applySemanticExtension(parsed *parsedSemanticQuery, vocabulary *semanticVocabulary, language string, matches []string) error {
	if parsed == nil || vocabulary == nil {
		return ErrInvalidInput
	}
	updated := *parsed
	updated.intents = slices.Clone(parsed.intents)
	updated.mediaTypes = slices.Clone(parsed.mediaTypes)
	updated.genres = slices.Clone(parsed.genres)
	updated.themes = slices.Clone(parsed.themes)
	updated.countries = slices.Clone(parsed.countries)
	definitions := make(map[string]SemanticSearchIntent, len(vocabulary.known))
	for _, intent := range vocabulary.intents() {
		definitions[intent.ID] = vocabulary.label(intent, language)
	}
	seen := make(map[string]struct{}, len(updated.intents)+len(matches))
	for _, intent := range updated.intents {
		seen[intent.ID] = struct{}{}
	}
	for _, raw := range matches {
		id := strings.ToLower(strings.TrimSpace(raw))
		intent, ok := definitions[id]
		if !ok {
			return errors.New("local semantic extension returned an unknown intent")
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		switch intent.Kind {
		case "media_type":
			updated.mediaTypes = appendUniqueString(updated.mediaTypes, intent.Value)
		case "genre":
			updated.genres = appendUniqueString(updated.genres, intent.Value)
		case "theme":
			updated.themes = appendUniqueString(updated.themes, intent.Value)
		case "country":
			if len(updated.countries) != 0 && !slices.Contains(updated.countries, intent.Value) {
				return errors.New("local semantic extension returned conflicting countries")
			}
			updated.countries = appendUniqueString(updated.countries, intent.Value)
		default:
			return errors.New("local semantic extension returned an unsupported intent kind")
		}
		seen[id] = struct{}{}
		updated.intents = append(updated.intents, intent)
	}
	*parsed = updated
	return nil
}
