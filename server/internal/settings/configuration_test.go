package settings

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func TestSchemaV3RuntimeSettingsDefaultsValidationPersistenceAndScope(t *testing.T) {
	timezone := DefaultTimezone
	hardware := DefaultHardwareAcceleration
	bitrate := DefaultTranscodeMaxBitrateKbps
	media := DefaultMediaMaxStorageMB
	artwork := DefaultArtworkMaxStorageMB
	enabled, disabled := true, false
	values := materializeInstanceValues(Values{
		Timezone: &timezone, JellyfinEnabled: &disabled, JellyfinDebug: &disabled,
		HardwareAcceleration: &hardware, TranscodeMaxBitrateKbps: &bitrate,
		MediaMaxStorageMB: &media, ArtworkMaxStorageMB: &artwork, AllowTranscoding: &enabled,
	})
	if err := validateMaterializedRuntimeValues(values); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
	if values.PreferredTranscodeVideoCodec == nil || *values.PreferredTranscodeVideoCodec != "auto" ||
		values.TranscodeQualityPreset == nil || *values.TranscodeQualityPreset != "balanced" ||
		values.TranscodeConcurrency == nil || *values.TranscodeConcurrency != 4 {
		t.Fatalf("planner defaults were not materialized: %+v", values)
	}
	amf, av1, quality, concurrency := "amf", "av1", "quality", 32
	patch := Patch{
		HardwareAcceleration:         OptionalString{Set: true, Value: &amf},
		PreferredTranscodeVideoCodec: OptionalString{Set: true, Value: &av1},
		TranscodeQualityPreset:       OptionalString{Set: true, Value: &quality},
		TranscodeConcurrency:         OptionalInt{Set: true, Value: &concurrency},
	}
	if err := validateInstancePatch(patch); err != nil {
		t.Fatalf("valid transcoding planner settings rejected: %v", err)
	}
	persisted, err := json.Marshal(applyPatch(Values{}, patch))
	if err != nil {
		t.Fatal(err)
	}
	var restored Values
	if err := json.Unmarshal(persisted, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.HardwareAcceleration == nil || *restored.HardwareAcceleration != amf ||
		restored.PreferredTranscodeVideoCodec == nil || *restored.PreferredTranscodeVideoCodec != av1 ||
		restored.TranscodeQualityPreset == nil || *restored.TranscodeQualityPreset != quality ||
		restored.TranscodeConcurrency == nil || *restored.TranscodeConcurrency != concurrency {
		t.Fatalf("planner settings did not survive persistence: %s", persisted)
	}
	for _, nullPatch := range []Patch{
		{Timezone: OptionalString{Set: true}},
		{HardwareAcceleration: OptionalString{Set: true}},
		{PreferredTranscodeVideoCodec: OptionalString{Set: true}},
		{TranscodeQualityPreset: OptionalString{Set: true}},
		{TranscodeConcurrency: OptionalInt{Set: true}},
		{TranscodeMaxBitrateKbps: OptionalInt{Set: true}},
	} {
		if err := validateInstancePatch(nullPatch); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("nullable runtime patch error = %v", err)
		}
	}
	invalidTimezone, invalidCodec, invalidPreset := "Mars/Olympus", "vp9", "ultrafast"
	belowConcurrency, aboveConcurrency := MinimumTranscodeConcurrency-1, MaximumTranscodeConcurrency+1
	invalidBitrate := MinimumTranscodeMaxBitrateKbps - 1
	for _, invalid := range []Patch{
		{Timezone: OptionalString{Set: true, Value: &invalidTimezone}},
		{PreferredTranscodeVideoCodec: OptionalString{Set: true, Value: &invalidCodec}},
		{TranscodeQualityPreset: OptionalString{Set: true, Value: &invalidPreset}},
		{TranscodeConcurrency: OptionalInt{Set: true, Value: &belowConcurrency}},
		{TranscodeConcurrency: OptionalInt{Set: true, Value: &aboveConcurrency}},
		{TranscodeMaxBitrateKbps: OptionalInt{Set: true, Value: &invalidBitrate}},
	} {
		if err := validateInstancePatch(invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid runtime patch accepted: %+v error=%v", invalid, err)
		}
	}
	for _, instanceOnly := range []Patch{
		{PreferredTranscodeVideoCodec: OptionalString{Set: true, Value: &av1}},
		{TranscodeQualityPreset: OptionalString{Set: true, Value: &quality}},
		{TranscodeConcurrency: OptionalInt{Set: true, Value: &concurrency}},
	} {
		if err := validateProfilePatch(instanceOnly); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("profile runtime setting error = %v", err)
		}
	}
	keys := instancePatchKeys(patch)
	wantKeys := []string{"hardwareAcceleration", "preferredTranscodeVideoCodec", "transcodeConcurrency", "transcodeQualityPreset"}
	if !slices.Equal(keys, wantKeys) {
		t.Fatalf("audit keys = %v, want %v", keys, wantKeys)
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
