package main

import (
	"strings"
	"testing"
)

const minimalManifest = `{
  "version": 1,
  "defaults": {"timeoutMs": 1000, "maxResponseBytes": 4096, "headers": {}},
  "steps": [{
    "id": "ping",
    "clients": ["test"],
    "status": "required",
    "request": {"method": "GET", "path": "/ping"},
    "expect": {"statuses": [200]},
    "captures": [],
    "canonicalize": [],
    "compare": "exact"
  }]
}`

func TestDecodeManifestAcceptsSemanticComparisonAndHostNormalization(t *testing.T) {
	t.Parallel()
	data := strings.Replace(minimalManifest, `"canonicalize": []`, `"canonicalize": [{"op":"normalize-url-host","pointer":"/Url","optional":true,"reason":"target host"}]`, 1)
	data = strings.Replace(data, `"compare": "exact"`, `"compare": "semantic"`, 1)
	if _, err := decodeManifest([]byte(data)); err != nil {
		t.Fatalf("decodeManifest() rejected semantic comparison: %v", err)
	}
}

func TestDecodeManifestRejectsDuplicateUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    string
		message string
	}{
		{
			name:    "duplicate nested key",
			data:    strings.Replace(minimalManifest, `"method": "GET"`, `"method": "GET", "method": "POST"`, 1),
			message: `duplicate JSON key "method"`,
		},
		{
			name:    "unknown field",
			data:    strings.Replace(minimalManifest, `"version": 1`, `"version": 1, "unknown": true`, 1),
			message: `unknown field "unknown"`,
		},
		{
			name:    "case-variant field",
			data:    strings.Replace(minimalManifest, `"version": 1`, `"version": 1, "Version": 1`, 1),
			message: `unknown field "Version"`,
		},
		{
			name:    "trailing value",
			data:    minimalManifest + ` {}`,
			message: "trailing JSON token",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeManifest([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("decodeManifest() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestDecodeManifestValidatesTemplatesAndPointers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    string
		message string
	}{
		{
			name:    "future capture",
			data:    strings.Replace(minimalManifest, `"path": "/ping"`, `"path": "/{{later}}"`, 1),
			message: `capture "later" is not available from an earlier step`,
		},
		{
			name: "bad pointer escape",
			data: strings.Replace(
				minimalManifest,
				`"captures": []`,
				`"captures": [{"name":"value","pointer":"/bad~2escape","secret":false}]`,
				1,
			),
			message: "invalid RFC6901 escape",
		},
		{
			name: "missing capture secrecy",
			data: strings.Replace(
				minimalManifest,
				`"captures": []`,
				`"captures": [{"name":"value","pointer":"/value"}]`,
				1,
			),
			message: `field "secret" at /steps/0/captures/0 is required`,
		},
		{
			name:    "invalid header control",
			data:    strings.Replace(minimalManifest, `"headers": {}`, `"headers": {"X-Test":"bad\u0000value"}`, 1),
			message: "invalid control character",
		},
		{
			name:    "unclosed secret",
			data:    strings.Replace(minimalManifest, `"path": "/ping"`, `"path": "/{{secret:token"`, 1),
			message: "unclosed template",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeManifest([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("decodeManifest() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestPointerEscapesAndArrayIndexes(t *testing.T) {
	t.Parallel()
	value := map[string]any{
		"a/b": map[string]any{
			"~key": []any{"first", "second"},
		},
	}
	got, found, err := pointerValue(value, "/a~1b/~0key/1")
	if err != nil || !found || got != "second" {
		t.Fatalf("pointerValue() = (%v, %v, %v), want (second, true, nil)", got, found, err)
	}
	if _, _, err := pointerValue(value, "/a~1b/~0key/01"); err == nil {
		t.Fatal("pointerValue() accepted a non-canonical array index")
	}
}
