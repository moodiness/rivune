package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestModelsAreEvaluatedSmallestFirst(t *testing.T) {
	models, err := parseModels("qwen3:4b,qwen3:0.6b,qwen3:1.7b")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"qwen3:0.6b", "qwen3:1.7b", "qwen3:4b"}
	for index := range want {
		if models[index] != want[index] {
			t.Fatalf("model order = %v", models)
		}
	}
}

func TestEmbeddedCorpusHasRequiredMultilingualCoverage(t *testing.T) {
	cases, err := loadCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 120 {
		t.Fatalf("corpus cases = %d", len(cases))
	}
	counts := map[string]int{}
	for _, test := range cases {
		counts[test.Language]++
	}
	for _, language := range []string{"en-US", "fr-FR", "de-DE", "es-ES", "it-IT", "pt-BR"} {
		if counts[language] < 20 {
			t.Fatalf("%s cases = %d", language, counts[language])
		}
	}
}

func TestPreRegisteredGateRejectsQualityLatencyAndNondeterminism(t *testing.T) {
	passing := modelReport{
		Digest: "sha256:model", Quality: qualityReport{
			ExactAccuracy: 0.9, Deterministic: true,
			LanguageAccuracy: map[string]float64{"en-US": 0.8, "fr-FR": 0.9},
		},
		Latency: latencyReport{P95MS: 10_000},
	}
	if !passesGate(passing, gate) {
		t.Fatal("admissible model did not pass")
	}
	for name, mutate := range map[string]func(*modelReport){
		"quality": func(report *modelReport) { report.Quality.ExactAccuracy = 0.8 },
		"language": func(report *modelReport) { report.Quality.LanguageAccuracy["fr-FR"] = 0.7 },
		"proper title": func(report *modelReport) { report.Quality.ProperTitleFalsePositives = 1 },
		"latency": func(report *modelReport) { report.Latency.P95MS = 16_000 },
		"determinism": func(report *modelReport) { report.Quality.Deterministic = false },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := passing
			candidate.Quality.LanguageAccuracy = map[string]float64{"en-US": 0.8, "fr-FR": 0.9}
			mutate(&candidate)
			if passesGate(candidate, gate) {
				t.Fatal("inadmissible model passed")
			}
		})
	}
}

func TestRunEmitsMachineReadableReportAndFailsWhenNoModelPasses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/tags" {
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()
	var output bytes.Buffer
	err := run([]string{"-endpoint", server.URL, "-models", "qwen3:0.6b", "-repetitions", "1"}, &output)
	if err == nil {
		t.Fatal("benchmark without an admissible model succeeded")
	}
	var report benchmarkReport
	if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
		t.Fatalf("report is not JSON: %v\n%s", decodeErr, output.String())
	}
	if report.Passed || len(report.Models) != 1 || report.Models[0].Passed || report.Models[0].Error == "" || report.CorpusDigest == "" {
		t.Fatalf("unexpected failed report: %+v", report)
	}
}

func TestLatencyUsesNearestRankPercentiles(t *testing.T) {
	values := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond, 100 * time.Millisecond}
	report := measureLatency(values)
	if report.P50MS != 3 || report.P95MS != 100 || report.P99MS != 100 {
		t.Fatalf("latency percentiles = %+v", report)
	}
}
