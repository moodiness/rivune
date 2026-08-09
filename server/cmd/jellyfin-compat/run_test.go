package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunIsolatesCapturesCanonicalizesAndScrubs(t *testing.T) {
	t.Parallel()
	type fixture struct {
		name     string
		token    string
		user     string
		password string
		server   *httptest.Server
	}
	fixtures := []*fixture{
		{name: "upstream", token: "z-secret-token-aaa", user: "user-alpha", password: "password-alpha-private"},
		{name: "rivune", token: "0-secret-token-bbb", user: "user-bravo", password: "password-bravo-private"},
	}
	for _, item := range fixtures {
		item := item
		item.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/capture":
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Add("Cache-Control", "a")
				writer.Header().Add("Cache-Control", item.token)
				writer.Header().Set("X-Not-Allowlisted", item.token)
				fmt.Fprintf(writer, `{"Token":%q,"User":%q,"FutureSecret":%q,"arr":[3,1,2],"secretOrder":["a",%q],"untouched":[3,1,2],"number":1.2300,"obj":{"b":2,"a":1}}`, item.token, item.user, item.password, item.token)
			case "/use/" + item.user:
				if request.Header.Get("X-Token") != item.token {
					http.Error(writer, "capture crossed target boundary", http.StatusUnauthorized)
					return
				}
				if request.Header.Get("X-Password") != item.password {
					http.Error(writer, "secret crossed target boundary", http.StatusUnauthorized)
					return
				}
				requestBody, err := io.ReadAll(request.Body)
				if err != nil || !strings.Contains(string(requestBody), item.token) || !strings.Contains(string(requestBody), item.password) {
					http.Error(writer, "JSON templates were not expanded", http.StatusBadRequest)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(writer, `{"captured":%q,"environment":%q}`, item.token, item.password)
			default:
				http.NotFound(writer, request)
			}
		}))
		defer item.server.Close()
	}
	manifestJSON := `{
		"version":1,
		"defaults":{"timeoutMs":1000,"maxResponseBytes":4096,"headers":{}},
		"steps":[
			{
				"id":"capture","clients":["test"],"status":"required",
				"request":{"method":"GET","path":"/capture"},
				"expect":{"statuses":[200],"contentTypes":["application/json"]},
				"captures":[
					{"name":"token","pointer":"/Token","secret":true},
					{"name":"user","pointer":"/User","secret":false}
				],
				"canonicalize":[
					{"op":"replace","pointer":"/Token","value":"<token>","reason":"target local"},
					{"op":"replace","pointer":"/User","value":"<user>","reason":"target local"},
					{"op":"sort","pointer":"/arr","reason":"server order is irrelevant"},
					{"op":"sort","pointer":"/secretOrder","reason":"secret values must not affect canonical order"}
				],
				"compare":"exact"
			},
			{
				"id":"use","clients":["test"],"status":"required",
				"request":{"method":"POST","path":"/use/{{user}}","headers":{"X-Token":"{{token}}","X-Password":"{{secret:password}}"},"json":{"Password":"{{secret:password}}","Token":"{{token}}"}},
				"expect":{"statuses":[200],"contentTypes":["application/json"]},
				"captures":[],"canonicalize":[],"compare":"exact"
			}
		]
	}`
	manifest, err := decodeManifest([]byte(manifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]targetSpec, 0, len(fixtures))
	secrets := make(map[string]string)
	for _, item := range fixtures {
		parsed, err := url.Parse(item.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, targetSpec{Name: item.name, URL: parsed})
		secrets["JFCOMPAT_"+environmentToken(item.name)+"_PASSWORD"] = item.password
	}
	output := t.TempDir()
	getenv := func(name string) (string, bool) {
		value, exists := secrets[name]
		return value, exists
	}
	if err := runManifest(context.Background(), manifest, targets, output, getenv); err != nil {
		t.Fatal(err)
	}
	for _, item := range fixtures {
		capture := readTestFile(t, filepath.Join(output, item.name, "capture.json"))
		use := readTestFile(t, filepath.Join(output, item.name, "use.json"))
		allOutput := capture + use + readTestFile(t, filepath.Join(output, item.name, "capture.http"))
		for _, secret := range []string{item.token, item.password} {
			if strings.Contains(allOutput, secret) {
				t.Fatalf("output for %s leaked %q: %s", item.name, secret, allOutput)
			}
		}
		for _, want := range []string{
			`"arr":[1,2,3]`,
			`"secretOrder":["[REDACTED]","a"]`,
			`"untouched":[3,1,2]`,
			`"number":1.2300`,
			`"obj":{"a":1,"b":2}`,
			redacted,
		} {
			if !strings.Contains(capture+use, want) {
				t.Errorf("output for %s does not contain %q:\n%s\n%s", item.name, want, capture, use)
			}
		}
		if strings.Contains(allOutput, "X-Not-Allowlisted") {
			t.Fatalf("non-allowlisted response header was persisted: %s", allOutput)
		}
	}
	if err := runManifest(context.Background(), manifest, targets, output, getenv); err != nil {
		t.Fatalf("rerun over managed output failed: %v", err)
	}
	if _, err := compareSnapshots(filepath.Join(output, "upstream"), filepath.Join(output, "rivune"), filepath.Join(output, "diff")); err != nil {
		t.Fatalf("canonical snapshots should compare exactly: %v", err)
	}
}

func TestScrubberHandlesMixedCasePercentEscapes(t *testing.T) {
	t.Parallel()
	scrub := &scrubber{}
	scrub.Add("a/b:c")
	got := scrub.Text("prefix-a%2fb%3Ac-suffix")
	if strings.Contains(got, "a%2fb%3Ac") || got != "prefix-"+redacted+"-suffix" {
		t.Fatalf("mixed-case percent escape was not scrubbed: %q", got)
	}
	scrub.Add("supersecret")
	if got := scrub.Text("value=%73uper%73ecret"); got != "value="+redacted {
		t.Fatalf("over-encoded unreserved bytes were not scrubbed: %q", got)
	}
	if _, err := scrub.Value(map[string]any{"supersecret": 1, redacted: 2}); err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("structured scrub collision error = %v", err)
	}
}

func TestParseTargetPreservesEscapedBasePath(t *testing.T) {
	t.Parallel()
	target, err := parseTarget("upstream=http://example.test/a%2Fb/")
	if err != nil {
		t.Fatal(err)
	}
	if got := target.URL.String(); got != "http://example.test/a%2Fb" {
		t.Fatalf("target URL = %q, want escaped base path preserved", got)
	}
	if _, err := parseTarget("upstream=http://example.test?"); err == nil {
		t.Fatal("target with an empty query delimiter was accepted")
	}
	if _, err := parseTarget("upstream=http://:8080"); err == nil {
		t.Fatal("target without a hostname was accepted")
	}
}

func TestRunEnforcesSizeTimeoutRedirectAndStrictZero(t *testing.T) {
	t.Parallel()
	t.Run("size", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/octet-stream")
			_, _ = writer.Write([]byte("12345"))
		}))
		defer server.Close()
		manifest := testManifest(t, 1000, 4, `{"statuses":[200]}`)
		err := runAgainstServer(t, manifest, server.URL)
		if err == nil || !strings.Contains(err.Error(), "response exceeds 4 byte limit") {
			t.Fatalf("run error = %v, want response size failure", err)
		}
	})
	t.Run("duplicate response key", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"value":1,"value":2}`))
		}))
		defer server.Close()
		manifest := testManifest(t, 1000, 4096, `{"statuses":[200],"contentTypes":["application/json"]}`)
		err := runAgainstServer(t, manifest, server.URL)
		if err == nil || !strings.Contains(err.Error(), `duplicate JSON key "value"`) {
			t.Fatalf("run error = %v, want duplicate response key failure", err)
		}
	})
	t.Run("strict zero", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		manifest := testManifest(t, 1000, 4096, `{"statuses":[204],"maxBytes":0}`)
		if err := runAgainstServer(t, manifest, server.URL); err != nil {
			t.Fatalf("zero-byte response failed a strict zero bound: %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			writer.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()
		manifest := testManifest(t, 10, 4096, `{"statuses":[204]}`)
		err := runAgainstServer(t, manifest, server.URL)
		if err == nil || !strings.Contains(err.Error(), "HTTP request failed") {
			t.Fatalf("run error = %v, want timeout", err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		var followed atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/destination" {
				followed.Add(1)
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			http.Redirect(writer, request, "/destination", http.StatusFound)
		}))
		defer server.Close()
		manifest := testManifest(t, 1000, 4096, `{"statuses":[302]}`)
		if err := runAgainstServer(t, manifest, server.URL); err != nil {
			t.Fatal(err)
		}
		if got := followed.Load(); got != 0 {
			t.Fatalf("redirect destination was requested %d time(s)", got)
		}
	})
}

func TestRunScrubsSecretsFromErrors(t *testing.T) {
	t.Parallel()
	const secret = "supersecret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/"+secret)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	manifestJSON := `{
		"version":1,
		"defaults":{"timeoutMs":1000,"maxResponseBytes":4096,"headers":{"Authorization":"{{secret:token}}"}},
		"steps":[{"id":"request","clients":["test"],"status":"required","request":{"method":"GET","path":"/"},"expect":{"statuses":[200],"contentTypes":["application/json"]},"captures":[],"canonicalize":[],"compare":"exact"}]
	}`
	manifest, err := decodeManifest([]byte(manifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(server.URL)
	targets := []targetSpec{{Name: "left", URL: parsed}, {Name: "right", URL: parsed}}
	err = runManifest(context.Background(), manifest, targets, t.TempDir(), func(string) (string, bool) { return secret, true })
	if err == nil {
		t.Fatal("run succeeded with an unexpected content type")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), redacted) {
		t.Fatalf("error was not scrubbed: %v", err)
	}
}

func testManifest(t *testing.T, timeoutMS int, maxBytes int64, expect string) *Manifest {
	t.Helper()
	data := fmt.Sprintf(`{
		"version":1,
		"defaults":{"timeoutMs":%d,"maxResponseBytes":%d,"headers":{}},
		"steps":[{"id":"request","clients":["test"],"status":"required","request":{"method":"GET","path":"/"},"expect":%s,"captures":[],"canonicalize":[],"compare":"exact"}]
	}`, timeoutMS, maxBytes, expect)
	manifest, err := decodeManifest([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func runAgainstServer(t *testing.T, manifest *Manifest, rawURL string) error {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	targets := []targetSpec{{Name: "left", URL: parsed}, {Name: "right", URL: parsed}}
	return runManifest(context.Background(), manifest, targets, t.TempDir(), os.LookupEnv)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
