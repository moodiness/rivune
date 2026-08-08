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
	headers.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	headers.Set("X-MediaBrowser-Authorization", `MediaBrowser Client="VidHub", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	if _, err := ParseClientIdentity(headers); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("conflicting client headers error = %v", err)
	}

	headers = make(http.Header)
	headers.Set("X-Emby-Authorization", `MediaBrowser Client="Infuse", Client="VidHub", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	if _, err := ParseClientIdentity(headers); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("duplicate client parameter error = %v", err)
	}
}

func TestParseClientIdentityAcceptsStrictMediaBrowserMetadata(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Authorization", `MediaBrowser Client="Infuse", Device="Living Room", DeviceId="device-1", Version="8.0"`)
	identity, err := ParseClientIdentity(headers)
	if err != nil {
		t.Fatalf("parse client identity: %v", err)
	}
	if identity.Client != "Infuse" || identity.Device != "Living Room" || identity.DeviceID != "device-1" || identity.Version != "8.0" {
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

func TestParseCompatTokenSeparatesAudienceAndRejectsAmbiguity(t *testing.T) {
	token, _, err := newCompatCredential()
	if err != nil {
		t.Fatalf("generate test compatibility token: %v", err)
	}

	same := httptest.NewRequest(http.MethodGet, "/Items/id/Images/Primary?api_key="+token, nil)
	same.Header.Set("X-Emby-Token", token)
	parsed, err := ParseCompatToken(same, true)
	if err != nil || parsed != token {
		t.Fatalf("same credential transports parsed %q with error %v", parsed, err)
	}

	mixedCaseQuery := httptest.NewRequest(http.MethodGet, "/Items/id/Images/Primary?API_KEY="+token, nil)
	parsed, err = ParseCompatToken(mixedCaseQuery, true)
	if err != nil || parsed != token {
		t.Fatalf("case-insensitive query credential parsed %q with error %v", parsed, err)
	}

	differentToken, _, err := newCompatCredential()
	if err != nil {
		t.Fatalf("generate second compatibility token: %v", err)
	}
	conflicting := httptest.NewRequest(http.MethodGet, "/Items", nil)
	conflicting.Header.Set("X-Emby-Token", token)
	conflicting.Header.Set("X-MediaBrowser-Token", differentToken)
	if _, err := ParseCompatToken(conflicting, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("different token transports error = %v", err)
	}

	duplicateQuery := httptest.NewRequest(http.MethodGet, "/Videos/id/stream?api_key="+token+"&api_key="+token, nil)
	if _, err := ParseCompatToken(duplicateQuery, true); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("duplicate query credential error = %v", err)
	}

	native := httptest.NewRequest(http.MethodGet, "/Items", nil)
	native.Header.Set("X-Emby-Token", "rivune_at_native-audience-token")
	if _, err := ParseCompatToken(native, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("native token audience error = %v", err)
	}

	emptyPreAuthToken := httptest.NewRequest(http.MethodGet, "/Items", nil)
	emptyPreAuthToken.Header.Set("Authorization", `MediaBrowser Token=""`)
	if _, err := ParseCompatToken(emptyPreAuthToken, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("empty pre-auth token credential error = %v", err)
	}

	emptyPreAuthWithQuery := httptest.NewRequest(http.MethodGet, "/Items/id/Images/Primary?api_key="+token, nil)
	emptyPreAuthWithQuery.Header.Set("Authorization", `MediaBrowser Client="VidHub", Device="Tablet", DeviceId="vidhub-device", Version="3.0.3", Token=""`)
	parsed, err = ParseCompatToken(emptyPreAuthWithQuery, true)
	if err != nil || parsed != token {
		t.Fatalf("empty pre-auth token with query credential parsed %q with error %v", parsed, err)
	}

	mixedAudience := httptest.NewRequest(http.MethodGet, "/Items", nil)
	mixedAudience.Header.Set("X-Emby-Token", token)
	mixedAudience.Header.Set("Authorization", "Bearer rivune_at_native-token")
	if _, err := ParseCompatToken(mixedAudience, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
		t.Fatalf("mixed native and compatibility authorization error = %v", err)
	}

	oversizeHeader := httptest.NewRequest(http.MethodGet, "/Items", nil)
	oversizeHeader.Header.Set("X-Emby-Authorization", "MediaBrowser Token=\""+token+"\", Padding=\""+strings.Repeat("x", maximumCompatAuthorizationHeaderBytes)+"\"")
	if _, err := ParseCompatToken(oversizeHeader, false); !errors.Is(err, ErrInvalidCompatAuthorization) {
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
