package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/collection"
	"github.com/moodiness/rivune/server/internal/netguard"
)

const corpusVersion = "semantic-extension-v1"

//go:embed corpus.v1.json
var corpusJSON []byte

type corpusCase struct {
	ID          string                                  `json:"id"`
	Language    string                                  `json:"language"`
	Query       string                                  `json:"query"`
	Candidates  []collection.SemanticExtensionCandidate `json:"candidates"`
	Expected    []string                                `json:"expected"`
	ProperTitle bool                                    `json:"properTitle,omitempty"`
}

type gateDefinition struct {
	MinimumExactAccuracy    float64 `json:"minimumExactAccuracy"`
	MinimumLanguageAccuracy float64 `json:"minimumLanguageAccuracy"`
	MaximumProperTitleFP    int     `json:"maximumProperTitleFalsePositives"`
	MaximumP95Milliseconds  float64 `json:"maximumP95Milliseconds"`
	RequireDeterminism      bool    `json:"requireDeterminism"`
}

type qualityReport struct {
	Cases                     int                `json:"cases"`
	ExactMatches              int                `json:"exactMatches"`
	ExactAccuracy             float64            `json:"exactAccuracy"`
	Precision                 float64            `json:"precision"`
	Recall                    float64            `json:"recall"`
	F1                        float64            `json:"f1"`
	ProperTitleFalsePositives int                `json:"properTitleFalsePositives"`
	Failures                  int                `json:"failures"`
	Deterministic             bool               `json:"deterministic"`
	LanguageAccuracy          map[string]float64 `json:"languageAccuracy"`
}

type latencyReport struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50Milliseconds"`
	P95MS   float64 `json:"p95Milliseconds"`
	P99MS   float64 `json:"p99Milliseconds"`
}

type modelReport struct {
	Model   string        `json:"model"`
	Digest  string        `json:"digest"`
	Quality qualityReport `json:"quality"`
	Latency latencyReport `json:"latency"`
	Passed  bool          `json:"passed"`
	Error   string        `json:"error,omitempty"`
}

type benchmarkReport struct {
	SchemaVersion int            `json:"schemaVersion"`
	CorpusVersion string         `json:"corpusVersion"`
	CorpusDigest  string         `json:"corpusDigest"`
	Endpoint      string         `json:"endpoint"`
	Seed          int64          `json:"seed"`
	Repetitions   int            `json:"repetitions"`
	Gate          gateDefinition `json:"gate"`
	Models        []modelReport  `json:"models"`
	SelectedModel string         `json:"selectedModel,omitempty"`
	Passed        bool           `json:"passed"`
}

type benchmarkObservation struct {
	selection []string
	err       error
}

var gate = gateDefinition{
	MinimumExactAccuracy: 0.85, MinimumLanguageAccuracy: 0.75,
	MaximumProperTitleFP: 0, MaximumP95Milliseconds: 15_000, RequireDeterminism: true,
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("semantic-benchmark", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("endpoint", "http://127.0.0.1:11434", "local Ollama origin")
	modelsValue := flags.String("models", "qwen3:0.6b,qwen3:1.7b,qwen3:4b", "comma-separated Ollama models, smallest first")
	repetitions := flags.Int("repetitions", 3, "seeded repetitions per corpus case")
	seed := flags.Int64("seed", 42, "corpus execution-order seed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	models, err := parseModels(*modelsValue)
	if err != nil || *repetitions < 1 || *repetitions > 20 {
		return errors.New("models must be unique and repetitions must be between 1 and 20")
	}
	if _, err := collection.NewOllamaSemanticExtension(*endpoint, models[0], nil); err != nil {
		return fmt.Errorf("validate Ollama endpoint: %w", err)
	}
	cases, err := loadCorpus()
	if err != nil {
		return err
	}
	digests, err := ollamaDigests(context.Background(), *endpoint)
	if err != nil {
		return err
	}
	report := benchmarkReport{
		SchemaVersion: 1, CorpusVersion: corpusVersion, CorpusDigest: digestBytes(corpusJSON),
		Endpoint: strings.TrimRight(*endpoint, "/"), Seed: *seed, Repetitions: *repetitions, Gate: gate,
		Models: make([]modelReport, 0, len(models)),
	}
	for index, model := range models {
		modelResult := benchmarkModel(*endpoint, model, digests[model], cases, *repetitions, *seed+int64(index))
		report.Models = append(report.Models, modelResult)
		if report.SelectedModel == "" && modelResult.Passed {
			report.SelectedModel = model
		}
	}
	report.Passed = report.SelectedModel != ""
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode benchmark report: %w", err)
	}
	if !report.Passed {
		return errors.New("no semantic model passed the pre-registered gate")
	}
	return nil
}

func parseModels(value string) ([]string, error) {
	rank := map[string]int{"qwen3:0.6b": 0, "qwen3:1.7b": 1, "qwen3:4b": 2}
	var result []string
	seen := map[string]struct{}{}
	for _, raw := range strings.Split(value, ",") {
		model := strings.TrimSpace(raw)
		if _, supported := rank[model]; !supported {
			return nil, fmt.Errorf("unsupported benchmark model %q", model)
		}
		if _, duplicate := seen[model]; duplicate {
			return nil, errors.New("duplicate model")
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	if len(result) == 0 {
		return nil, errors.New("no models")
	}
	slices.SortFunc(result, func(left, right string) int { return rank[left] - rank[right] })
	return result, nil
}

func loadCorpus() ([]corpusCase, error) {
	var cases []corpusCase
	if err := json.Unmarshal(corpusJSON, &cases); err != nil {
		return nil, fmt.Errorf("decode embedded corpus: %w", err)
	}
	counts := map[string]int{}
	ids := map[string]struct{}{}
	for _, test := range cases {
		if test.ID == "" || test.Query == "" || len(test.Candidates) == 0 {
			return nil, errors.New("embedded corpus contains an incomplete case")
		}
		if _, duplicate := ids[test.ID]; duplicate {
			return nil, fmt.Errorf("embedded corpus contains duplicate case %q", test.ID)
		}
		ids[test.ID] = struct{}{}
		counts[test.Language]++
	}
	for _, language := range []string{"en-US", "fr-FR", "de-DE", "es-ES", "it-IT", "pt-BR"} {
		if counts[language] < 20 {
			return nil, fmt.Errorf("embedded corpus has only %d %s cases", counts[language], language)
		}
	}
	return cases, nil
}

func benchmarkModel(endpoint, model, digest string, cases []corpusCase, repetitions int, seed int64) modelReport {
	report := modelReport{Model: model, Digest: digest}
	if digest == "" {
		report.Error = "model is absent from Ollama or has no digest"
		return report
	}
	extension, err := collection.NewOllamaSemanticExtension(endpoint, model, nil)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	if err := extension.Warmup(context.Background()); err != nil {
		report.Error = "warm up model: " + err.Error()
		return report
	}
	observations := make([][]benchmarkObservation, len(cases))
	latencies := make([]time.Duration, 0, len(cases)*repetitions)
	order := rand.New(rand.NewSource(seed)).Perm(len(cases) * repetitions)
	for _, flatIndex := range order {
		caseIndex := flatIndex % len(cases)
		test := cases[caseIndex]
		started := time.Now()
		selection, callErr := extension.Resolve(context.Background(), collection.SemanticExtensionRequest{
			Query: test.Query, Language: test.Language, Candidates: test.Candidates,
		})
		latencies = append(latencies, time.Since(started))
		slices.Sort(selection)
		observations[caseIndex] = append(observations[caseIndex], benchmarkObservation{selection: selection, err: callErr})
	}
	report.Quality = measureQuality(cases, observations)
	report.Latency = measureLatency(latencies)
	report.Passed = passesGate(report, gate)
	return report
}

func measureQuality(cases []corpusCase, observations [][]benchmarkObservation) qualityReport {
	quality := qualityReport{Cases: len(cases), Deterministic: true, LanguageAccuracy: map[string]float64{}}
	languageCorrect, languageTotal := map[string]int{}, map[string]int{}
	var truePositive, falsePositive, falseNegative int
	for index, test := range cases {
		expected := slices.Clone(test.Expected)
		slices.Sort(expected)
		languageTotal[test.Language]++
		first := observations[index][0]
		if first.err != nil {
			quality.Failures++
			quality.Deterministic = false
			falseNegative += len(expected)
			continue
		}
		if slices.Equal(first.selection, expected) {
			quality.ExactMatches++
			languageCorrect[test.Language]++
		}
		if test.ProperTitle {
			quality.ProperTitleFalsePositives += len(first.selection)
		}
		expectedSet := map[string]struct{}{}
		for _, id := range expected {
			expectedSet[id] = struct{}{}
		}
		selectedSet := map[string]struct{}{}
		for _, id := range first.selection {
			selectedSet[id] = struct{}{}
		}
		for id := range selectedSet {
			if _, ok := expectedSet[id]; ok {
				truePositive++
			} else {
				falsePositive++
			}
		}
		for id := range expectedSet {
			if _, ok := selectedSet[id]; !ok {
				falseNegative++
			}
		}
		for _, repeated := range observations[index][1:] {
			if repeated.err != nil || !slices.Equal(repeated.selection, first.selection) {
				quality.Deterministic = false
				if repeated.err != nil {
					quality.Failures++
				}
			}
		}
	}
	quality.ExactAccuracy = ratio(quality.ExactMatches, quality.Cases)
	quality.Precision = ratio(truePositive, truePositive+falsePositive)
	quality.Recall = ratio(truePositive, truePositive+falseNegative)
	if quality.Precision+quality.Recall > 0 {
		quality.F1 = 2 * quality.Precision * quality.Recall / (quality.Precision + quality.Recall)
	}
	for language, total := range languageTotal {
		quality.LanguageAccuracy[language] = ratio(languageCorrect[language], total)
	}
	return quality
}

func measureLatency(values []time.Duration) latencyReport {
	slices.Sort(values)
	return latencyReport{Samples: len(values), P50MS: percentileMS(values, .50), P95MS: percentileMS(values, .95), P99MS: percentileMS(values, .99)}
}

func percentileMS(values []time.Duration, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*percentile)) - 1
	return float64(values[max(0, index)]) / float64(time.Millisecond)
}

func passesGate(report modelReport, definition gateDefinition) bool {
	if report.Error != "" || report.Quality.Failures != 0 || report.Quality.ExactAccuracy < definition.MinimumExactAccuracy ||
		report.Quality.ProperTitleFalsePositives > definition.MaximumProperTitleFP || report.Latency.P95MS > definition.MaximumP95Milliseconds {
		return false
	}
	if definition.RequireDeterminism && !report.Quality.Deterministic {
		return false
	}
	for _, accuracy := range report.Quality.LanguageAccuracy {
		if accuracy < definition.MinimumLanguageAccuracy {
			return false
		}
	}
	return true
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}

func ollamaDigests(ctx context.Context, endpoint string) (map[string]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = netguard.DialContextLocal
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Ollama redirects are not supported")
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list Ollama models: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list Ollama models: HTTP %d", response.StatusCode)
	}
	var body struct {
		Models []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode Ollama models: %w", err)
	}
	result := make(map[string]string, len(body.Models))
	for _, item := range body.Models {
		result[item.Name], result[item.Model] = item.Digest, item.Digest
	}
	return result, nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
