package collection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestOllamaSemanticExtensionUsesPositionalStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/chat" || request.Method != http.MethodPost {
			t.Fatalf("unexpected Ollama request %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			Model    string          `json:"model"`
			Stream   bool            `json:"stream"`
			Think    bool            `json:"think"`
			Format   map[string]any  `json:"format"`
			Options  map[string]any  `json:"options"`
			Messages []ollamaMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		properties, ok := body.Format["properties"].(map[string]any)
		if !ok {
			t.Fatalf("missing format properties: %+v", body.Format)
		}
		indices, ok := properties["i"].(map[string]any)
		if !ok {
			t.Fatalf("missing positional output schema: %+v", properties)
		}
		items, ok := indices["items"].(map[string]any)
		if !ok || items["type"] != "integer" || items["minimum"] != float64(0) || items["maximum"] != float64(1) ||
			indices["uniqueItems"] != true || indices["maxItems"] != float64(maximumSemanticExtensionMatches) {
			t.Fatalf("unexpected positional schema: %+v", indices)
		}
		encodedFormat, _ := json.Marshal(body.Format)
		if strings.Contains(string(encodedFormat), "enum") || body.Model != "qwen3:0.6b" || body.Stream || body.Think ||
			body.Options["num_predict"] != float64(maximumSemanticExtensionOutputTokens) || len(body.Messages) != 2 ||
			strings.Count(body.Messages[1].Content, `"theme:space"`) != 1 || strings.Count(body.Messages[1].Content, `"genre:war"`) != 1 {
			t.Fatalf("unexpected Ollama payload: %+v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"model":"qwen3:0.6b","created_at":"2026-08-25T00:00:00Z","message":{"role":"assistant","content":"{\"i\":[0]}"},"done":true,"total_duration":42}`))
	}))
	defer server.Close()

	extension, err := NewOllamaSemanticExtension(server.URL, "qwen3:0.6b", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	matches, err := extension.Resolve(context.Background(), SemanticExtensionRequest{
		Query: "qui se déroule dans le cosmos", Language: "fr-FR",
		Candidates: []SemanticExtensionCandidate{
			{ID: " Theme:Space ", Kind: "theme", Label: "Space"},
			{ID: "genre:war", Kind: "genre", Label: "War"},
		},
	})
	if err != nil || !slices.Equal(matches, []string{"theme:space"}) {
		t.Fatalf("Ollama selection = %v, error=%v", matches, err)
	}
}

func TestOllamaSemanticExtensionRejectsHostileSelections(t *testing.T) {
	tests := map[string]string{
		"duplicate key": `{"i":[],"i":[]}`,
		"decimal":       `{"i":[0.0]}`,
		"negative":      `{"i":[-1]}`,
		"out of range":  `{"i":[9]}`,
		"duplicate":     `{"i":[0,0]}`,
		"over limit":    `{"i":[0,1,2,3,4,5,6,7,8]}`,
		"unknown key":   `{"i":[],"x":true}`,
		"trailing":      `{"i":[]} false`,
		"missing":       `{}`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				encoded, _ := json.Marshal(content)
				_, _ = response.Write([]byte(`{"message":{"role":"assistant","content":` + string(encoded) + `},"done":true}`))
			}))
			defer server.Close()
			extension, err := NewOllamaSemanticExtension(server.URL, "qwen3:0.6b", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			candidates := make([]SemanticExtensionCandidate, 9)
			for index := range candidates {
				candidates[index] = SemanticExtensionCandidate{ID: fmt.Sprintf("theme:test%d", index), Kind: "theme", Label: fmt.Sprintf("Test %d", index)}
			}
			if _, err := extension.Resolve(context.Background(), SemanticExtensionRequest{Query: "test query", Language: "en", Candidates: candidates}); err == nil {
				t.Fatal("hostile Ollama selection was accepted")
			}
		})
	}
}

func TestOllamaSemanticExtensionRejectsOversizedAndDuplicateResponseJSON(t *testing.T) {
	responses := map[string]string{
		"oversized":     strings.Repeat(" ", maximumSemanticExtensionBodyBytes+1),
		"duplicate key": `{"message":{"role":"assistant","content":"{\"i\":[]}"},"done":true,"done":true}`,
		"unknown key":   `{"message":{"role":"assistant","content":"{\"i\":[]}"},"done":true,"unexpected":1}`,
		"trailing":      `{"message":{"role":"assistant","content":"{\"i\":[]}"},"done":true} false`,
	}
	for name, body := range responses {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(body))
			}))
			defer server.Close()
			extension, err := NewOllamaSemanticExtension(server.URL, "qwen3:0.6b", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = extension.Resolve(context.Background(), SemanticExtensionRequest{
				Query: "test query", Candidates: []SemanticExtensionCandidate{{ID: "genre:war", Kind: "genre", Label: "War"}},
			})
			if err == nil {
				t.Fatal("hostile Ollama response was accepted")
			}
		})
	}
}

func TestOllamaSemanticExtensionAcceptsExactBodyAndMatchLimits(t *testing.T) {
	selection := `{"i":[0,1,2,3,4,5,6,7]}`
	encodedSelection, _ := json.Marshal(selection)
	body := `{"message":{"role":"assistant","content":` + string(encodedSelection) + `},"done":true}`
	body += strings.Repeat(" ", maximumSemanticExtensionBodyBytes-len(body))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()
	extension, err := NewOllamaSemanticExtension(server.URL, "qwen3:0.6b", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]SemanticExtensionCandidate, maximumSemanticExtensionMatches)
	want := make([]string, maximumSemanticExtensionMatches)
	for index := range candidates {
		want[index] = fmt.Sprintf("theme:test%d", index)
		candidates[index] = SemanticExtensionCandidate{ID: want[index], Kind: "theme", Label: fmt.Sprintf("Test %d", index)}
	}
	matches, err := extension.Resolve(context.Background(), SemanticExtensionRequest{Query: "test query", Candidates: candidates})
	if err != nil || !slices.Equal(matches, want) {
		t.Fatalf("exact-limit selection=%v error=%v", matches, err)
	}
}

func TestOllamaSemanticExtensionBoundsDirectInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid direct input reached Ollama")
	}))
	defer server.Close()
	extension, err := NewOllamaSemanticExtension(server.URL, "qwen3:0.6b", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	candidate := []SemanticExtensionCandidate{{ID: "genre:war", Kind: "genre", Label: "War"}}
	for _, query := range []string{"x", strings.Repeat("x", 201), string([]byte{0xff, 0xfe})} {
		if _, err := extension.Resolve(context.Background(), SemanticExtensionRequest{Query: query, Candidates: candidate}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("query %q error = %v", query, err)
		}
	}
}

func TestOllamaSemanticExtensionWarmupUsesProductionPathAndStableIdentity(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		var body struct {
			Format  map[string]any `json:"format"`
			Options map[string]any `json:"options"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Options["num_predict"] != float64(maximumSemanticExtensionOutputTokens) || body.Format["type"] != "object" {
			t.Fatalf("warmup did not use production payload: %+v", body)
		}
		_, _ = response.Write([]byte(`{"message":{"role":"assistant","content":"{\"i\":[]}"},"done":true}`))
	}))
	defer server.Close()
	extension, err := NewOllamaSemanticExtension(server.URL, "qwen3:0.6b", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := extension.Warmup(context.Background()); err != nil || requests != 1 {
		t.Fatalf("warmup requests=%d error=%v", requests, err)
	}
	identity := extension.SemanticCacheIdentity()
	if identity == "" || !strings.Contains(identity, "qwen3:0.6b") || !strings.Contains(identity, semanticExtensionPromptSchemaVersion) || identity != extension.SemanticCacheIdentity() {
		t.Fatalf("unstable semantic cache identity %q", identity)
	}
}

func TestApplySemanticExtensionAddsOnlyKnownCanonicalFilter(t *testing.T) {
	vocabulary := buildSemanticVocabulary(nil)
	parsed := parseSemanticQueryWithVocabulary("film angoissant", "", nil, vocabulary, "fr-FR")
	if !parsed.needsExtension || !slices.Equal(parsed.mediaTypes, []string{MediaTypeMovie}) {
		t.Fatalf("deterministic parse did not expose residual wording: %+v", parsed)
	}
	if err := applySemanticExtension(&parsed, vocabulary, "fr-FR", []string{"genre:horror"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(parsed.genres, []string{"horror"}) || parsed.intents[len(parsed.intents)-1].ID != "genre:horror" {
		t.Fatalf("extension did not add canonical horror filter: %+v", parsed)
	}
	if err := applySemanticExtension(&parsed, vocabulary, "fr-FR", []string{"genre:not_real"}); err == nil {
		t.Fatal("unknown extension intent was accepted")
	}
}

func TestSemanticExtensionCandidatesExcludeCountriesAndMediaTypes(t *testing.T) {
	vocabulary := buildSemanticVocabulary(nil)
	for _, parsed := range []parsedSemanticQuery{
		parseSemanticQueryWithVocabulary("film qui fait peur", "", nil, vocabulary, "fr-FR"),
		parseSemanticQueryWithVocabulary("unknown wording", "", nil, vocabulary, "en-US"),
	} {
		candidates := semanticExtensionCandidates(vocabulary, "fr-FR", nil, parsed)
		if len(candidates) == 0 {
			t.Fatal("semantic extension candidates are empty")
		}
		for _, candidate := range candidates {
			if candidate.Kind == "country" || candidate.Kind == "media_type" {
				t.Fatalf("irrelevant extension candidate leaked: %+v", candidate)
			}
		}
	}
}

func TestSemanticExtensionFallbackPreservesDeterministicSearchUnlessRequestWasCanceled(t *testing.T) {
	partial, err := semanticExtensionFallback(context.Background(), context.DeadlineExceeded)
	if err != nil || !partial {
		t.Fatalf("local extension timeout fallback = partial %t, error %v", partial, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	partial, err = semanticExtensionFallback(canceled, context.DeadlineExceeded)
	if partial || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request fallback = partial %t, error %v", partial, err)
	}
}
