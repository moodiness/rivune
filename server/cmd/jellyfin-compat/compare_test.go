package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareWritesPathDiffAndSummaryAndSkipsOnlyPerTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	for _, directory := range []string{left, right} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		metadata, err := marshalCanonical(snapshotMeta{
			Version: manifestVersion,
			Steps: []snapshotStepMeta{
				{ID: "same", Compare: "exact"},
				{ID: "different", Compare: "semantic"},
				{ID: "local", Compare: "per-target"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(directory, "_meta.json"), metadata)
		writeTestFile(t, filepath.Join(directory, "same.http"), []byte("HTTP/1.1 200 OK\n"))
		writeTestFile(t, filepath.Join(directory, "same.json"), []byte("{\n  \"value\": true\n}\n"))
		writeTestFile(t, filepath.Join(directory, "different.http"), []byte("HTTP/1.1 200 OK\n"))
		writeTestFile(t, filepath.Join(directory, "local.http"), []byte("HTTP/1.1 200 OK\n"))
		writeTestFile(t, filepath.Join(directory, "local.json"), []byte("{}\n"))
	}
	writeTestFile(t, filepath.Join(left, "different.json"), []byte("{\n  \"value\": 1\n}\n"))
	writeTestFile(t, filepath.Join(right, "different.json"), []byte("{\"value\":2}\n"))
	writeTestFile(t, filepath.Join(right, "local.json"), []byte("{\n  \"target\": true\n}\n"))
	output := filepath.Join(root, "diff")
	summary, err := compareSnapshots(left, right, output)
	if err == nil || !strings.Contains(err.Error(), "1 compared step(s) differ") {
		t.Fatalf("compareSnapshots() error = %v, want one difference", err)
	}
	if summary.Compared != 2 || summary.Matched != 1 || len(summary.Differences) != 1 || summary.Differences[0] != "different" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if len(summary.Mismatches) != 1 || summary.Mismatches[0].Path != "/value" || summary.Mismatches[0].Artifact != "json" {
		t.Fatalf("comparison did not report the JSON path: %+v", summary.Mismatches)
	}
	if len(summary.Skipped) != 1 || summary.Skipped[0] != "local" {
		t.Fatalf("per-target step was not skipped: %+v", summary)
	}
	diff := readTestFile(t, filepath.Join(output, "different.json.diff"))
	for _, want := range []string{"--- left/different.json", "@@ /value @@", "-1", "+2"} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff does not contain %q:\n%s", want, diff)
		}
	}
	var stored comparisonSummary
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(output, "summary.json"))), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Differences) != 1 || stored.Differences[0] != "different" || len(stored.Mismatches) != 1 || stored.Mismatches[0].Path != "/value" {
		t.Fatalf("stored summary does not report the path-level difference: %+v", stored)
	}
	if _, err := os.Stat(filepath.Join(output, "local.json.diff")); !os.IsNotExist(err) {
		t.Fatalf("per-target diff should not exist; stat error = %v", err)
	}
}

func TestCompareAcceptsMatchingSnapshots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	left := filepath.Join(root, "left")
	right := filepath.Join(root, "right")
	for _, directory := range []string{left, right} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		metadata, err := marshalCanonical(snapshotMeta{Version: manifestVersion, Steps: []snapshotStepMeta{{ID: "step", Compare: "exact"}}})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(directory, "_meta.json"), metadata)
		writeTestFile(t, filepath.Join(directory, "step.http"), []byte("HTTP/1.1 204 No Content\n"))
		writeTestFile(t, filepath.Join(directory, "step.json"), []byte("{\n  \"length\": 0\n}\n"))
	}
	output := filepath.Join(root, "out")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(output, "step.json.diff"), []byte("stale\n"))
	writeTestFile(t, filepath.Join(output, "summary.json"), []byte("stale\n"))
	writeTestFile(t, filepath.Join(output, "removed-step.http.diff"), []byte("stale\n"))
	summary, err := compareSnapshots(left, right, output)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Matched != 1 || summary.Compared != 1 || len(summary.Differences) != 0 {
		t.Fatalf("unexpected matching summary: %+v", summary)
	}
	if _, err := os.Stat(filepath.Join(output, "step.json.diff")); !os.IsNotExist(err) {
		t.Fatalf("stale diff was not removed; stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "removed-step.http.diff")); !os.IsNotExist(err) {
		t.Fatalf("removed-step diff was not removed; stat error = %v", err)
	}
}

func TestSemanticComparisonPreservesDTOOrderingPaginationAndHTTPContract(t *testing.T) {
	t.Parallel()
	equivalentJSON, err := compareSemanticSnapshots(
		"format",
		".json",
		[]byte(`{"Amount":1,"Object":{"A":true,"B":false}}`),
		[]byte("{\n  \"Object\": {\"B\": false, \"A\": true},\n  \"Amount\": 1.0\n}\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(equivalentJSON) != 0 {
		t.Fatalf("semantic JSON formatting produced mismatches: %+v", equivalentJSON)
	}
	equivalentHTTP, err := compareSemanticSnapshots(
		"headers",
		".http",
		[]byte("HTTP/1.1 200 Success\nContent-Type: application/json\nCache-Control: no-store\n\n"),
		[]byte("HTTP/1.1 200 OK\ncache-control: no-store\ncontent-type: application/json\n\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(equivalentHTTP) != 0 {
		t.Fatalf("semantic HTTP formatting produced mismatches: %+v", equivalentHTTP)
	}

	jsonMismatches, err := compareSemanticSnapshots(
		"catalog",
		".json",
		[]byte(`{"Items":["first","second"],"Optional":null,"TotalRecordCount":2}`),
		[]byte(`{"Items":["second","first"],"TotalRecordCount":3}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	wantJSONPaths := []string{"/Items/0", "/Items/1", "/Optional", "/TotalRecordCount"}
	if len(jsonMismatches) != len(wantJSONPaths) {
		t.Fatalf("JSON mismatches = %+v, want paths %v", jsonMismatches, wantJSONPaths)
	}
	for index, want := range wantJSONPaths {
		if jsonMismatches[index].Path != want {
			t.Fatalf("JSON mismatch %d path = %q, want %q; all = %+v", index, jsonMismatches[index].Path, want, jsonMismatches)
		}
	}

	httpMismatches, err := compareSemanticSnapshots(
		"auth",
		".http",
		[]byte("HTTP/1.1 401 Unauthorized\nCache-Control: no-store\nContent-Type: application/json\nX-Jellyfin-Compat-Body: json\n\n"),
		[]byte("HTTP/1.1 200 OK\nContent-Type: text/html\nX-Jellyfin-Compat-Body: json\n\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	wantHTTPPaths := []string{"/status", "/headers/Cache-Control", "/headers/Content-Type/0"}
	if len(httpMismatches) != len(wantHTTPPaths) {
		t.Fatalf("HTTP mismatches = %+v, want paths %v", httpMismatches, wantHTTPPaths)
	}
	for index, want := range wantHTTPPaths {
		if httpMismatches[index].Path != want {
			t.Fatalf("HTTP mismatch %d path = %q, want %q; all = %+v", index, httpMismatches[index].Path, want, httpMismatches)
		}
	}
}

func TestSemanticComparisonAcceptsDocumentedTargetNondeterminism(t *testing.T) {
	t.Parallel()
	rules := []Rule{
		{Op: "replace", Pointer: "/ServerId", Value: json.RawMessage(`"<server-id>"`)},
		{Op: "replace", Pointer: "/ServerName", Value: json.RawMessage(`"<server-name>"`)},
		{Op: "remove", Pointer: "/Timestamp"},
		{Op: "normalize-url-host", Pointer: "/Url"},
	}
	canonicalize := func(input string) []byte {
		t.Helper()
		value, err := decodeComparisonJSON([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		scrub := &scrubber{}
		for _, rule := range rules {
			value, err = applyRule(value, rule, scrub)
			if err != nil {
				t.Fatal(err)
			}
		}
		encoded, err := marshalCanonical(value)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	left := canonicalize(`{"ServerId":"oracle-id","ServerName":"Oracle","Timestamp":"2026-08-09T10:00:00Z","Url":"https://oracle.local/Items/movie/master.m3u8?quality=high","Value":true}`)
	right := canonicalize(`{"Value":true,"Url":"http://rivune.local:8096/Items/movie/master.m3u8?quality=high","Timestamp":"2026-08-09T10:00:01Z","ServerName":"Rivune","ServerId":"rivune-id"}`)
	mismatches, err := compareSemanticSnapshots("playback", ".json", left, right)
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 0 {
		t.Fatalf("documented target-local values should normalize: %+v", mismatches)
	}

	changedQuery := canonicalize(`{"Value":true,"Url":"http://rivune.local:8096/Items/movie/master.m3u8?quality=low","Timestamp":"2026-08-09T10:00:01Z","ServerName":"Rivune","ServerId":"rivune-id"}`)
	mismatches, err = compareSemanticSnapshots("playback", ".json", left, changedQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(mismatches) != 1 || mismatches[0].Path != "/Url" {
		t.Fatalf("URL normalization hid a semantic query delta: %+v", mismatches)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
