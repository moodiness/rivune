package jellyfin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestProtocolDTOsUseJellyfinPascalCase(t *testing.T) {
	models := []any{
		PublicSystemInfo{}, SystemInfo{}, SystemEndpointInfo{}, AuthenticateByName{},
		AuthenticationResult{}, UserDto{}, UserPolicy{}, UserConfiguration{}, SessionInfoDto{},
		CompatErrorResponse{}, CompatErrorStatus{},
		QueryResult[BaseItemDto]{}, BaseItemDto{}, BaseItemPerson{}, UserItemDataDto{}, UpdateUserItemDataDto{}, SearchHintDto{}, SearchHintResult{},
		PlaybackInfoRequest{}, PlaybackInfoResponse{}, MediaSourceInfo{}, MediaStreamInfo{}, DeviceProfile{},
		CodecProfile{}, ProfileCondition{}, ContainerProfile{}, DirectPlayProfile{}, TranscodingProfile{}, SubtitleProfile{}, PlaybackProgressInfo{},
		DisplayPreferencesDto{}, ClientCapabilitiesDto{}, PlayerStateInfo{}, UserDataChangeInfo{}, LibraryUpdateInfo{}, SessionUserInfoDto{}, QueueItemDto{}, SpecialViewOptionDto{}, WebSocketMessageDto{},
	}
	for _, model := range models {
		modelType := reflect.TypeOf(model)
		for index := range modelType.NumField() {
			field := modelType.Field(index)
			if field.PkgPath != "" {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.Anonymous && tag == "" {
				continue
			}
			if tag == "" || tag == "-" || tag != field.Name {
				t.Errorf("%s.%s has JSON tag %q, want exact PascalCase field name", modelType.Name(), field.Name, tag)
			}
		}
	}

	payload, err := json.Marshal(BaseItemDto{
		Id:                      "item-id",
		ServerId:                "server-id",
		Name:                    "Signal",
		Type:                    "Movie",
		MediaType:               "Video",
		IsPlayable:              true,
		PrimaryImageAspectRatio: 16.0 / 9.0,
	})
	if err != nil {
		t.Fatalf("marshal BaseItemDto: %v", err)
	}
	encoded := string(payload)
	for _, key := range []string{"\"Id\"", "\"ServerId\"", "\"Name\"", "\"Type\"", "\"MediaType\"", "\"IsPlayable\"", "\"PrimaryImageAspectRatio\""} {
		if !strings.Contains(encoded, key) {
			t.Errorf("encoded DTO does not contain %s: %s", key, encoded)
		}
	}
	for _, key := range []string{"\"id\"", "\"serverId\"", "\"name\"", "\"type\""} {
		if strings.Contains(encoded, key) {
			t.Errorf("encoded DTO contains native-style key %s: %s", key, encoded)
		}
	}
	withoutRatio, err := json.Marshal(BaseItemDto{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutRatio), "PrimaryImageAspectRatio") {
		t.Fatalf("zero aspect ratio was serialized: %s", withoutRatio)
	}
}

func TestUserConfigurationLanguagePreferencesAreRequiredNullableFields(t *testing.T) {
	payload, err := json.Marshal(UserConfiguration{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"AudioLanguagePreference", "SubtitleLanguagePreference"} {
		value, ok := fields[name]
		if !ok || string(value) != "null" {
			t.Fatalf("%s must be present and nullable, got %s in %s", name, value, payload)
		}
	}
}

func TestCompatibilityIdentityConstantsAreProtocolVersions(t *testing.T) {
	if CompatibilityVersion != "10.11.11" || CompatibilityProduct != "Rivune Jellyfin Compatibility" {
		t.Fatalf("unexpected compatibility identity: %q %q", CompatibilityVersion, CompatibilityProduct)
	}
}
