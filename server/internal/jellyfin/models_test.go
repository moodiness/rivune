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
		QueryResult[BaseItemDto]{}, BaseItemDto{}, UserItemDataDto{}, SearchHintDto{}, SearchHintResult{},
		PlaybackInfoRequest{}, PlaybackInfoResponse{}, MediaSourceInfo{}, DeviceProfile{},
		DirectPlayProfile{}, TranscodingProfile{}, SubtitleProfile{}, PlaybackProgressInfo{},
		DisplayPreferencesDto{},
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
		Id:         "item-id",
		ServerId:   "server-id",
		Name:       "Signal",
		Type:       "Movie",
		MediaType:  "Video",
		IsPlayable: true,
	})
	if err != nil {
		t.Fatalf("marshal BaseItemDto: %v", err)
	}
	encoded := string(payload)
	for _, key := range []string{"\"Id\"", "\"ServerId\"", "\"Name\"", "\"Type\"", "\"MediaType\"", "\"IsPlayable\""} {
		if !strings.Contains(encoded, key) {
			t.Errorf("encoded DTO does not contain %s: %s", key, encoded)
		}
	}
	for _, key := range []string{"\"id\"", "\"serverId\"", "\"name\"", "\"type\""} {
		if strings.Contains(encoded, key) {
			t.Errorf("encoded DTO contains native-style key %s: %s", key, encoded)
		}
	}
}

func TestCompatibilityIdentityConstantsAreProtocolVersions(t *testing.T) {
	if CompatibilityVersion != "10.11.0" || CompatibilityProduct != "Rivune Jellyfin Compatibility" {
		t.Fatalf("unexpected compatibility identity: %q %q", CompatibilityVersion, CompatibilityProduct)
	}
}
