package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCapturePreseedSkipsLoginAndKeepsTargetsIsolated(t *testing.T) {
	t.Parallel()
	type fixture struct {
		name   string
		token  string
		user   string
		login  atomic.Int32
		use    atomic.Int32
		server *httptest.Server
	}
	fixtures := []*fixture{
		{name: "upstream", token: "upstream-preseed-secret", user: "upstream-user"},
		{name: "rivune", token: "rivune-preseed-secret", user: "rivune-user"},
	}
	for _, item := range fixtures {
		item := item
		item.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/login":
				item.login.Add(1)
				http.Error(writer, "login must be skipped", http.StatusInternalServerError)
			case "/use/" + item.user:
				item.use.Add(1)
				if request.Header.Get("X-Token") != item.token {
					http.Error(writer, "target capture isolation failed", http.StatusUnauthorized)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(writer, `{"Token":%q,"Used":true}`, item.token)
			default:
				http.NotFound(writer, request)
			}
		}))
		defer item.server.Close()
	}
	manifest := preseedManifest(t)
	targets := make([]targetSpec, 0, len(fixtures))
	environment := make(map[string]string)
	for _, item := range fixtures {
		parsed, err := url.Parse(item.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, targetSpec{Name: item.name, URL: parsed})
		prefix := "JFCOMPAT_" + environmentToken(item.name) + "_CAPTURE_"
		environment[prefix+"ACCESS_TOKEN"] = item.token
		environment[prefix+"USER_ID"] = item.user
	}
	getenv := func(name string) (string, bool) {
		value, exists := environment[name]
		return value, exists
	}
	output := t.TempDir()
	for run := range 2 {
		if err := runManifest(context.Background(), manifest, targets, output, getenv); err != nil {
			t.Fatalf("preseed run %d failed: %v", run+1, err)
		}
	}
	for _, item := range fixtures {
		if got := item.login.Load(); got != 0 {
			t.Fatalf("%s login was requested %d time(s)", item.name, got)
		}
		if got := item.use.Load(); got != 2 {
			t.Fatalf("%s no-capture step was requested %d time(s), want 2", item.name, got)
		}
		if _, err := os.Stat(filepath.Join(output, item.name, "login.json")); !os.IsNotExist(err) {
			t.Fatalf("%s preseeded login snapshot exists; stat error = %v", item.name, err)
		}
		metadata := readTestFile(t, filepath.Join(output, item.name, "_meta.json"))
		if !strings.Contains(metadata, `"id":"login","compare":"exact","skipped":"preseeded"`) {
			t.Fatalf("%s metadata does not record preseed: %s", item.name, metadata)
		}
		allOutput := metadata + readTestFile(t, filepath.Join(output, item.name, "use.json")) + readTestFile(t, filepath.Join(output, item.name, "use.http"))
		if strings.Contains(allOutput, item.token) {
			t.Fatalf("%s preseeded secret leaked into snapshots: %s", item.name, allOutput)
		}
	}
	summary, err := compareSnapshots(filepath.Join(output, "upstream"), filepath.Join(output, "rivune"), filepath.Join(output, "diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Skipped) != 1 || summary.Skipped[0] != "login" || summary.Matched != 1 {
		t.Fatalf("unexpected preseed comparison summary: %+v", summary)
	}
}

func TestCapturePreseedRejectsPartialStepBeforeNetwork(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	targets := []targetSpec{{Name: "upstream", URL: parsed}, {Name: "rivune", URL: parsed}}
	const secret = "partial-secret-must-not-leak"
	environment := map[string]string{
		"JFCOMPAT_UPSTREAM_CAPTURE_ACCESS_TOKEN": secret,
		"JFCOMPAT_RIVUNE_CAPTURE_ACCESS_TOKEN":   "rivune-token",
		"JFCOMPAT_RIVUNE_CAPTURE_USER_ID":        "rivune-user",
	}
	err = runManifest(context.Background(), preseedManifest(t), targets, t.TempDir(), func(name string) (string, bool) {
		value, exists := environment[name]
		return value, exists
	})
	if err == nil || !strings.Contains(err.Error(), "partial capture preseed") {
		t.Fatalf("partial preseed error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("partial preseed error leaked secret: %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("partial preseed made %d network request(s)", got)
	}
}

func preseedManifest(t *testing.T) *Manifest {
	t.Helper()
	data := `{
		"version":1,
		"defaults":{"timeoutMs":1000,"maxResponseBytes":4096,"headers":{}},
		"steps":[
			{
				"id":"login","clients":["test"],"status":"required",
				"request":{"method":"POST","path":"/login","json":{"Username":"{{secret:username}}","Password":"{{secret:password}}"}},
				"expect":{"statuses":[200],"contentTypes":["application/json"]},
				"captures":[
					{"name":"access_token","pointer":"/AccessToken","secret":true},
					{"name":"user_id","pointer":"/User/Id","secret":false}
				],
				"canonicalize":[],"compare":"exact"
			},
			{
				"id":"use","clients":["test"],"status":"required",
				"request":{"method":"GET","path":"/use/{{user_id}}","headers":{"X-Token":"{{access_token}}"}},
				"expect":{"statuses":[200],"contentTypes":["application/json"]},
				"captures":[],"canonicalize":[],"compare":"exact"
			}
		]
	}`
	manifest, err := decodeManifest([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
