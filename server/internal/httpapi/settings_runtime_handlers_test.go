package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeSettingsPatchPreservesAllRuntimeAssignments(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(`{
		"timezone":"Europe/Paris",
		"jellyfinEnabled":true,
		"jellyfinDebug":true,
		"hardwareAcceleration":"hybrid",
		"preferredTranscodeVideoCodec":"hevc",
		"transcodeQualityPreset":"quality",
		"transcodeConcurrency":12,
		"transcodeMaxBitrateKbps":18000,
		"mediaMaxStorageMB":4096,
		"artworkMaxStorageMB":2048,
		"allowTranscoding":false
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	patch, ok := decodeSettingsPatch(response, request)
	if !ok || response.Code != http.StatusOK {
		t.Fatalf("runtime patch rejected: status=%d body=%s", response.Code, response.Body.String())
	}
	if !patch.Timezone.Set || patch.Timezone.Value == nil || *patch.Timezone.Value != "Europe/Paris" ||
		!patch.JellyfinEnabled.Set || patch.JellyfinEnabled.Value == nil || !*patch.JellyfinEnabled.Value ||
		!patch.JellyfinDebug.Set || patch.JellyfinDebug.Value == nil || !*patch.JellyfinDebug.Value ||
		!patch.HardwareAcceleration.Set || patch.HardwareAcceleration.Value == nil || *patch.HardwareAcceleration.Value != "hybrid" ||
		!patch.PreferredTranscodeVideoCodec.Set || patch.PreferredTranscodeVideoCodec.Value == nil || *patch.PreferredTranscodeVideoCodec.Value != "hevc" ||
		!patch.TranscodeQualityPreset.Set || patch.TranscodeQualityPreset.Value == nil || *patch.TranscodeQualityPreset.Value != "quality" ||
		!patch.TranscodeConcurrency.Set || patch.TranscodeConcurrency.Value == nil || *patch.TranscodeConcurrency.Value != 12 ||
		!patch.TranscodeMaxBitrateKbps.Set || patch.TranscodeMaxBitrateKbps.Value == nil || *patch.TranscodeMaxBitrateKbps.Value != 18000 ||
		!patch.MediaMaxStorageMB.Set || patch.MediaMaxStorageMB.Value == nil || *patch.MediaMaxStorageMB.Value != 4096 ||
		!patch.ArtworkMaxStorageMB.Set || patch.ArtworkMaxStorageMB.Value == nil || *patch.ArtworkMaxStorageMB.Value != 2048 ||
		!patch.AllowTranscoding.Set || patch.AllowTranscoding.Value == nil || *patch.AllowTranscoding.Value {
		t.Fatal("runtime assignments were not preserved")
	}
}

func TestDecodeSettingsPatchRejectsNullRuntimeButPreservesNullableAllowTranscoding(t *testing.T) {
	for _, field := range []string{"timezone", "hardwareAcceleration", "preferredTranscodeVideoCodec", "transcodeQualityPreset", "transcodeConcurrency"} {
		invalid := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"`+field+`":null}`))
		invalid.Header.Set("Content-Type", "application/json")
		invalidResponse := httptest.NewRecorder()
		if _, ok := decodeSettingsPatch(invalidResponse, invalid); ok || invalidResponse.Code != http.StatusBadRequest {
			t.Fatalf("null %s status=%d accepted=%t", field, invalidResponse.Code, ok)
		}
	}

	nullable := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"allowTranscoding":null}`))
	nullable.Header.Set("Content-Type", "application/json")
	nullableResponse := httptest.NewRecorder()
	patch, ok := decodeSettingsPatch(nullableResponse, nullable)
	if !ok || !patch.AllowTranscoding.Set || patch.AllowTranscoding.Value != nil {
		t.Fatalf("nullable allowTranscoding rejected: status=%d", nullableResponse.Code)
	}
}
