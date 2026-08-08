package jellyfin

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseClientIdentityRejectsConflictingAndDuplicateAuthorization(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	headers.Set("X-MediaBrowser-Authorization", `MediaBrowser Client="Compatibility Client", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	if _, err := ParseClientIdentity(headers); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("conflicting client headers error = %v", err)
	}

	headers = make(http.Header)
	headers.Set("X-Emby-Authorization", `MediaBrowser Client="Generic Client", Client="Compatibility Client", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	if _, err := ParseClientIdentity(headers); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("duplicate client parameter error = %v", err)
	}
}

func TestParseClientIdentityAcceptsStrictMediaBrowserMetadata(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Authorization", `MediaBrowser Client="Generic Client", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	identity, err := ParseClientIdentity(headers)
	if err != nil {
		t.Fatalf("parse client identity: %v", err)
	}
	if identity.Client != "Generic Client" || identity.Device != "Living Room" || identity.DeviceID != "device-1" || identity.Version != "8.0" {
		t.Fatalf("parsed client identity = %+v", identity)
	}
}

func TestParseClientIdentityCanonicalizesLongJellyfinWebDeviceID(t *testing.T) {
	longDeviceID := strings.Repeat("d", 300)
	headers := make(http.Header)
	headers.Set("Authorization", `MediaBrowser Client="Jellyfin%20Web", Device="Chrome", DeviceId="`+longDeviceID+`", Version="12.0.0"`)
	first, err := ParseClientIdentity(headers)
	if err != nil {
		t.Fatalf("parse long Jellyfin Web device ID: %v", err)
	}
	second, err := ParseClientIdentity(headers)
	if err != nil {
		t.Fatalf("repeat long Jellyfin Web device ID: %v", err)
	}
	if first.DeviceID != second.DeviceID || len(first.DeviceID) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(first.DeviceID, "sha256:") {
		t.Fatalf("long device ID was not canonical and stable: first=%q second=%q", first.DeviceID, second.DeviceID)
	}
}

func TestParseCompatTokenFollowsJellyfinPrecedenceAndCasing(t *testing.T) {
	first, _, err := newCompatCredential()
	if err != nil {
		t.Fatalf("generate first compatibility token: %v", err)
	}
	second, _, err := newCompatCredential()
	if err != nil {
		t.Fatalf("generate second compatibility token: %v", err)
	}

	tests := []struct {
		name    string
		request *http.Request
		want    string
	}{
		{name: "authorization wins", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/Items?ApiKey="+second, nil)
			request.Header.Set("Authorization", `MediaBrowser Token="`+first+`"`)
			request.Header.Set("X-Emby-Token", second)
			return request
		}(), want: first},
		{name: "bearer authorization", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/Items", nil)
			request.Header.Set("Authorization", "Bearer "+first)
			return request
		}(), want: first},
		{name: "unschemed X-Emby authorization", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/Items", nil)
			request.Header.Set("X-Emby-Authorization", `Token="`+first+`"`)
			return request
		}(), want: first},
		{name: "unschemed X-MediaBrowser authorization", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/Items", nil)
			request.Header.Set("X-MediaBrowser-Authorization", `Token="`+first+`"`)
			return request
		}(), want: first},
		{name: "empty authorization token falls through", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/Items", nil)
			request.Header.Set("Authorization", `MediaBrowser Client="Compatibility Client", Token=""`)
			request.Header.Set("X-Emby-Token", first)
			return request
		}(), want: first},
		{name: "dedicated header wins query", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodGet, "/Items?api_key="+second, nil)
			request.Header.Set("X-Emby-Token", first)
			return request
		}(), want: first},
		{name: "ApiKey wins legacy query", request: httptest.NewRequest(http.MethodGet, "/Items?APIKEY="+first+"&api_key="+second, nil), want: first},
		{name: "legacy query globally accepted", request: httptest.NewRequest(http.MethodGet, "/Items?API_KEY="+first, nil), want: first},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, parseErr := ParseCompatToken(test.request, false)
			if parseErr != nil || got != test.want {
				t.Fatalf("token=%q want=%q error=%v", got, test.want, parseErr)
			}
		})
	}

	duplicate := httptest.NewRequest(http.MethodGet, "/Items?ApiKey="+first+"&ApiKey="+first, nil)
	if _, err := ParseCompatToken(duplicate, true); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("duplicate query credential error = %v", err)
	}
	native := httptest.NewRequest(http.MethodGet, "/Items", nil)
	native.Header.Set("X-Emby-Token", "rivune_at_native-audience-token")
	if _, err := ParseCompatToken(native, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("native token audience error = %v", err)
	}
	oversize := httptest.NewRequest(http.MethodGet, "/Items", nil)
	oversize.Header.Set("X-Emby-Authorization", "MediaBrowser Token=\""+first+"\", Padding=\""+strings.Repeat("x", maximumCompatAuthorizationHeaderBytes)+"\"")
	if _, err := ParseCompatToken(oversize, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("oversize authorization header error = %v", err)
	}
}

func TestCompatibilityCredentialHasDedicatedPrefixAndEntropy(t *testing.T) {
	token, digest, err := newCompatCredential()
	if err != nil {
		t.Fatalf("generate compatibility credential: %v", err)
	}
	if len(digest) != 32 {
		t.Fatalf("compatibility digest length = %d", len(digest))
	}
	if parsedDigest, ok := compatCredentialDigest(token); !ok || parsedDigest != digest {
		t.Fatal("generated compatibility credential did not validate against its SHA-256 digest")
	}
	if _, ok := compatCredentialDigest("rivune_at_not-a-compat-token"); ok {
		t.Fatal("native access token entered the compatibility audience")
	}
}
