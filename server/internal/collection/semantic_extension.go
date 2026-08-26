package collection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moodiness/rivune/server/internal/netguard"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	maximumSemanticExtensionCandidates   = 384
	maximumSemanticExtensionMatches      = 8
	maximumSemanticExtensionOutputTokens = 32
	maximumSemanticExtensionBodyBytes    = 256 << 10
	semanticExtensionTimeout             = 20 * time.Second
	semanticExtensionPromptSchemaVersion = "2"
)

type SemanticExtensionCandidate struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type SemanticExtensionRequest struct {
	Query      string
	Language   string
	Candidates []SemanticExtensionCandidate
}

type SemanticExtension interface {
	Resolve(context.Context, SemanticExtensionRequest) ([]string, error)
}

type OllamaSemanticExtension struct {
	endpoint   string
	model      string
	httpClient *http.Client
}

func NewOllamaSemanticExtension(origin, model string, httpClient *http.Client) (*OllamaSemanticExtension, error) {
	parsed, err := normalizeSemanticExtensionOrigin(origin)
	if err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	if !validSemanticExtensionModel(model) {
		return nil, errors.New("semantic Ollama model is invalid")
	}
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.MaxResponseHeaderBytes = 64 << 10
		transport.DialContext = netguard.DialContextLocal
		httpClient = &http.Client{
			Transport: transport,
			Timeout:   semanticExtensionTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("semantic Ollama redirects are not supported")
			},
		}
	}
	return &OllamaSemanticExtension{
		endpoint: strings.TrimRight(parsed, "/") + "/api/chat", model: model, httpClient: httpClient,
	}, nil
}

func (extension *OllamaSemanticExtension) Resolve(ctx context.Context, input SemanticExtensionRequest) ([]string, error) {
	call, err := extension.newCall(input)
	if err != nil {
		return nil, err
	}
	return extension.execute(ctx, call)
}

func (extension *OllamaSemanticExtension) Warmup(ctx context.Context) error {
	call, err := extension.newCall(SemanticExtensionRequest{
		Query: "Alien", Language: "en",
		Candidates: []SemanticExtensionCandidate{{ID: "genre:horror", Kind: "genre", Label: "Horror"}},
	})
	if err != nil {
		return err
	}
	_, err = extension.execute(ctx, call)
	return err
}

func (extension *OllamaSemanticExtension) SemanticCacheIdentity() string {
	if extension == nil {
		return ""
	}
	return "ollama:" + extension.model + ":semantic-prompt-schema:" + semanticExtensionPromptSchemaVersion
}

type semanticOllamaCall struct {
	payload      []byte
	candidateIDs []string
}

func (extension *OllamaSemanticExtension) newCall(input SemanticExtensionRequest) (semanticOllamaCall, error) {
	input.Query = strings.TrimSpace(input.Query)
	if extension == nil || extension.httpClient == nil || !utf8.ValidString(input.Query) ||
		utf8.RuneCountInString(input.Query) < 2 || utf8.RuneCountInString(input.Query) > 200 ||
		len(input.Candidates) == 0 || len(input.Candidates) > maximumSemanticExtensionCandidates {
		return semanticOllamaCall{}, ErrInvalidInput
	}
	candidateIDs := make([]string, 0, len(input.Candidates))
	seenCandidates := make(map[string]struct{}, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidate.ID = strings.ToLower(strings.TrimSpace(candidate.ID))
		candidate.Kind = strings.TrimSpace(candidate.Kind)
		candidate.Label = strings.TrimSpace(candidate.Label)
		if candidate.ID == "" || candidate.Kind == "" || !validSemanticCatalogName(candidate.Label) {
			return semanticOllamaCall{}, ErrInvalidInput
		}
		if _, duplicate := seenCandidates[candidate.ID]; duplicate {
			return semanticOllamaCall{}, ErrInvalidInput
		}
		seenCandidates[candidate.ID] = struct{}{}
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	candidatesJSON, err := json.Marshal(candidateIDs)
	if err != nil {
		return semanticOllamaCall{}, fmt.Errorf("encode semantic Ollama candidates: %w", err)
	}
	format := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"i"},
		"properties": map[string]any{
			"i": map[string]any{
				"type": "array", "uniqueItems": true, "maxItems": maximumSemanticExtensionMatches,
				"items": map[string]any{"type": "integer", "minimum": 0, "maximum": len(candidateIDs) - 1},
			},
		},
	}
	prompt := "Choose direct semantic intents from the zero-based candidate table. Translate slang, descriptions, and paraphrases. " +
		"Never infer from a proper title. Return only {\"i\":[indices]}, with at most 8 unique indices.\n" +
		"Examples: scary movie -> {\"i\":[0]}; Alien -> {\"i\":[]}. Example indices only refer to their own unseen tables.\n" +
		"Candidate table (array position is its index): " + string(candidatesJSON) +
		"\nSearch language: " + canonicalSemanticLanguage(input.Language) + "\nSearch: " + input.Query
	payload := struct {
		Model    string          `json:"model"`
		Messages []ollamaMessage `json:"messages"`
		Stream   bool            `json:"stream"`
		Think    bool            `json:"think"`
		Format   map[string]any  `json:"format"`
		Options  map[string]any  `json:"options"`
	}{
		Model: extension.model,
		Messages: []ollamaMessage{
			{Role: "system", Content: "You are a precise multilingual media-search intent classifier. Follow the supplied JSON schema exactly."},
			{Role: "user", Content: prompt},
		},
		Stream: false, Think: false, Format: format,
		Options: map[string]any{"temperature": 0, "top_k": 1, "top_p": 1, "seed": 42, "num_predict": maximumSemanticExtensionOutputTokens},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return semanticOllamaCall{}, fmt.Errorf("encode semantic Ollama request: %w", err)
	}
	return semanticOllamaCall{payload: encoded, candidateIDs: candidateIDs}, nil
}

func (extension *OllamaSemanticExtension) execute(ctx context.Context, call semanticOllamaCall) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, extension.endpoint, bytes.NewReader(call.payload))
	if err != nil {
		return nil, fmt.Errorf("construct semantic Ollama request: %w", netguard.SanitizeURLError(err))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	requestwork.PropagateRequestID(request)
	requestwork.BeginOutbound(ctx, requestwork.Now())
	response, err := extension.httpClient.Do(request)
	if err != nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		return nil, fmt.Errorf("call semantic Ollama extension: %w", netguard.SanitizeURLError(err))
	}
	if response.Body == nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		response.Body = http.NoBody
	} else {
		response.Body = requestwork.ObserveBody(ctx, response.Body)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("semantic Ollama extension returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumSemanticExtensionBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read semantic Ollama response: %w", err)
	}
	if len(body) > maximumSemanticExtensionBodyBytes {
		return nil, errors.New("semantic Ollama response exceeds body limit")
	}
	if err := validateUniqueJSON(body); err != nil {
		return nil, fmt.Errorf("decode semantic Ollama response: %w", err)
	}
	var result struct {
		Model              string        `json:"model"`
		CreatedAt          string        `json:"created_at"`
		Message            ollamaMessage `json:"message"`
		Done               bool          `json:"done"`
		DoneReason         string        `json:"done_reason"`
		TotalDuration      int64         `json:"total_duration"`
		LoadDuration       int64         `json:"load_duration"`
		PromptEvalCount    int64         `json:"prompt_eval_count"`
		PromptEvalDuration int64         `json:"prompt_eval_duration"`
		EvalCount          int64         `json:"eval_count"`
		EvalDuration       int64         `json:"eval_duration"`
	}
	resultDecoder := json.NewDecoder(bytes.NewReader(body))
	resultDecoder.DisallowUnknownFields()
	if err := resultDecoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode semantic Ollama response: %w", err)
	}
	if !result.Done || result.Message.Role != "assistant" || strings.TrimSpace(result.Message.Content) == "" {
		return nil, errors.New("semantic Ollama response is incomplete")
	}
	return parseSemanticOllamaSelection(result.Message.Content, call.candidateIDs)
}

func parseSemanticOllamaSelection(content string, candidateIDs []string) ([]string, error) {
	selectionJSON := []byte(content)
	if err := validateUniqueJSON(selectionJSON); err != nil {
		return nil, fmt.Errorf("decode semantic Ollama selection: %w", err)
	}
	var selection struct {
		Indices *[]int `json:"i"`
	}
	decoder := json.NewDecoder(bytes.NewReader(selectionJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		return nil, fmt.Errorf("decode semantic Ollama selection: %w", err)
	}
	if selection.Indices == nil {
		return nil, errors.New("decode semantic Ollama selection: missing indices")
	}
	if len(*selection.Indices) > maximumSemanticExtensionMatches {
		return nil, errors.New("semantic Ollama selection exceeds match limit")
	}
	matches := make([]string, 0, len(*selection.Indices))
	seenIndices := make(map[int]struct{}, len(*selection.Indices))
	seenMatches := make(map[string]struct{}, len(*selection.Indices))
	offered := make(map[string]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		if id == "" || !utf8.ValidString(id) || id != strings.ToLower(strings.TrimSpace(id)) {
			return nil, errors.New("semantic Ollama candidate snapshot is invalid")
		}
		if _, duplicate := offered[id]; duplicate {
			return nil, errors.New("semantic Ollama candidate snapshot contains duplicates")
		}
		offered[id] = struct{}{}
	}
	for _, index := range *selection.Indices {
		if index < 0 || index >= len(candidateIDs) {
			return nil, errors.New("semantic Ollama selected an unknown intent index")
		}
		if _, duplicate := seenIndices[index]; duplicate {
			return nil, errors.New("semantic Ollama selected a duplicate intent index")
		}
		seenIndices[index] = struct{}{}
		id := candidateIDs[index]
		if _, exists := offered[id]; !exists || id == "" || id != strings.ToLower(strings.TrimSpace(id)) {
			return nil, errors.New("semantic Ollama mapped an invalid intent")
		}
		if _, duplicate := seenMatches[id]; duplicate {
			return nil, errors.New("semantic Ollama mapped a duplicate intent")
		}
		seenMatches[id] = struct{}{}
		matches = append(matches, id)
	}
	return matches, nil
}

func validateUniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateUniqueJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON content")
		}
		return err
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closingDelimiter, ok := closing.(json.Delim); !ok || delimiter == '{' && closingDelimiter != '}' || delimiter == '[' && closingDelimiter != ']' {
		return errors.New("invalid JSON closing delimiter")
	}
	return nil
}

type ollamaMessage struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Thinking  string          `json:"thinking,omitempty"`
	Images    []string        `json:"images,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

func normalizeSemanticExtensionOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawPath != "" || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(parsed.Host, "\\") {
		return "", errors.New("semantic Ollama URL must be an exact HTTP(S) origin")
	}
	if parsed.Port() == "" {
		return "", errors.New("semantic Ollama URL must include an explicit port")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func validSemanticExtensionModel(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:/-", character) {
			continue
		}
		return false
	}
	return true
}
