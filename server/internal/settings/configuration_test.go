package settings

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSchemaV2RuntimeSettingsDefaultsAndValidation(t *testing.T) {
	timezone := DefaultTimezone
	hardware := DefaultHardwareAcceleration
	bitrate := DefaultTranscodeMaxBitrateKbps
	media := DefaultMediaMaxStorageMB
	artwork := DefaultArtworkMaxStorageMB
	enabled, disabled := true, false
	values := Values{Timezone: &timezone, JellyfinEnabled: &disabled, JellyfinDebug: &disabled, HardwareAcceleration: &hardware, TranscodeMaxBitrateKbps: &bitrate, MediaMaxStorageMB: &media, ArtworkMaxStorageMB: &artwork, AllowTranscoding: &enabled}
	if err := validateMaterializedRuntimeValues(values); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	hybrid := "hybrid"
	hybridPatch := Patch{HardwareAcceleration: OptionalString{Set: true, Value: &hybrid}}
	if err := validateInstancePatch(hybridPatch); err != nil {
		t.Fatalf("hybrid hardware acceleration rejected: %v", err)
	}
	persisted, err := json.Marshal(applyPatch(Values{}, hybridPatch))
	if err != nil {
		t.Fatal(err)
	}
	var restored Values
	if err := json.Unmarshal(persisted, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.HardwareAcceleration == nil || *restored.HardwareAcceleration != hybrid {
		t.Fatalf("hybrid hardware acceleration did not survive persistence: %s", persisted)
	}
	for _, patch := range []Patch{
		{Timezone: OptionalString{Set: true}},
		{HardwareAcceleration: OptionalString{Set: true}},
		{TranscodeMaxBitrateKbps: OptionalInt{Set: true}},
	} {
		if err := validateInstancePatch(patch); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("nullable runtime patch error = %v", err)
		}
	}
	invalidTimezone := "Mars/Olympus"
	if err := validateInstancePatch(Patch{Timezone: OptionalString{Set: true, Value: &invalidTimezone}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("timezone error = %v", err)
	}
	invalidBitrate := MinimumTranscodeMaxBitrateKbps - 1
	if err := validateInstancePatch(Patch{TranscodeMaxBitrateKbps: OptionalInt{Set: true, Value: &invalidBitrate}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bitrate error = %v", err)
	}
}

func TestCredentialDTOCannotSerializeAndAuditSnapshotsAreRedacted(t *testing.T) {
	if encoded, err := json.Marshal(IntegrationCredentials{TMDBAccessToken: "must-not-appear"}); err == nil || encoded != nil {
		t.Fatalf("plaintext credentials serialized: %q, %v", encoded, err)
	}
	redacted := []byte(`{"tmdbAccessToken":true,"fanartApiKey":false,"mdblistApiKey":false,"tvdbApiKey":false,"tvdbPin":false,"traktClientId":false,"traktClientSecret":false,"simklClientId":false}`)
	if err := validateAuditSnapshot("integrations.updated", redacted); err != nil {
		t.Fatalf("redacted snapshot rejected: %v", err)
	}
	if err := validateAuditSnapshot("integrations.updated", []byte(`{"tmdbAccessToken":"secret"}`)); err == nil {
		t.Fatal("plaintext-shaped audit snapshot was accepted")
	}
	if err := ensureNoSecretSettings([]byte(`{"theme":"dark","traktClientSecret":"secret"}`)); err == nil {
		t.Fatal("settings secret denylist accepted a credential")
	}
}

func TestIntegrationAADIsBoundToGenerationAndKeyVersion(t *testing.T) {
	first := string(integrationAAD("instance", "tvdbApiKey", 1, 2))
	if first == string(integrationAAD("instance", "tvdbApiKey", 2, 2)) || first == string(integrationAAD("instance", "tvdbApiKey", 1, 3)) || first == string(integrationAAD("other", "tvdbApiKey", 1, 2)) {
		t.Fatal("integration AAD did not bind every storage generation component")
	}
}
