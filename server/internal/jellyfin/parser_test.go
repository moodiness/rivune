package jellyfin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
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

func TestResolveLoginProfileRequiresUniqueAccessibleProfile(t *testing.T) {
	profiles := []auth.Profile{
		{ID: "10000000-0000-4000-8000-000000000001", Name: "Adults", Accessible: true},
		{ID: "10000000-0000-4000-8000-000000000002", Name: "Kids", Accessible: true},
		{ID: "10000000-0000-4000-8000-000000000003", Name: "Away", Accessible: false},
	}
	if _, ok := resolveLoginProfile(profiles, "", false); ok {
		t.Fatal("bare account selected the first of multiple accessible profiles")
	}
	if profileID, ok := resolveLoginProfile(profiles, "kids", true); !ok || profileID != profiles[1].ID {
		t.Fatalf("qualified case-insensitive profile resolved %q, %t", profileID, ok)
	}
	if _, ok := resolveLoginProfile(profiles, "Away", true); ok {
		t.Fatal("qualified unavailable profile was selected")
	}
	if account, selector, qualified, ok := splitCompatUsername(" account / Kids "); !ok || !qualified || account != "account" || selector != "Kids" {
		t.Fatalf("qualified login parsed account=%q selector=%q qualified=%t ok=%t", account, selector, qualified, ok)
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
