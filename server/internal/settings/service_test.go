package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/moodiness/rivune/server/internal/auth"
)

func TestValidatePatchRejectsUnknownValues(t *testing.T) {
	invalidTheme := "neon"
	invalidResolution := "8k"
	invalidLanguage := "not_a_language"
	invalidRegion := "France"
	invalidMapping := "anidb"
	tests := []Patch{
		{Theme: OptionalString{Set: true, Value: &invalidTheme}},
		{MaximumResolution: OptionalString{Set: true, Value: &invalidResolution}},
		{AudioLanguage: OptionalString{Set: true, Value: &invalidLanguage}},
		{ForcedSubtitleLanguage: OptionalString{Set: true, Value: &invalidLanguage}},
		{MetadataRegion: OptionalString{Set: true, Value: &invalidRegion}},
		{SeriesMappingProvider: OptionalString{Set: true, Value: &invalidMapping}},
		{},
	}
	for _, patch := range tests {
		if err := validatePatch(patch); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid settings error, got %v", err)
		}
	}
}

func TestApplyPatchCanClearOverride(t *testing.T) {
	dark := "dark"
	values := Values{Theme: &dark}
	updated := applyPatch(values, Patch{Theme: OptionalString{Set: true, Value: nil}})
	if updated.Theme != nil {
		t.Fatalf("expected theme override to be removed, got %q", *updated.Theme)
	}
}

func TestForcedSubtitleLanguageNormalizationAndInheritance(t *testing.T) {
	serverInput := "FR-ca"
	if err := validatePatch(Patch{ForcedSubtitleLanguage: OptionalString{Set: true, Value: &serverInput}}); err != nil {
		t.Fatalf("valid forced subtitle language was rejected: %v", err)
	}
	serverValues := applyPatch(Values{}, Patch{ForcedSubtitleLanguage: OptionalString{Set: true, Value: &serverInput}})
	if serverValues.ForcedSubtitleLanguage == nil || *serverValues.ForcedSubtitleLanguage != "fr-CA" {
		t.Fatalf("forced subtitle language was not normalized: %+v", serverValues)
	}

	effective := defaultEffective()
	applyLayer(&effective, serverValues, "instance")
	applyLayer(&effective, Values{}, "profile")
	if effective.Values.ForcedSubtitleLanguage != "fr-CA" || effective.Sources["forcedSubtitleLanguage"] != "instance" {
		t.Fatalf("profile did not inherit server forced subtitle language: %+v", effective)
	}

	profileInput := "EN-us"
	profileValues := applyPatch(Values{}, Patch{ForcedSubtitleLanguage: OptionalString{Set: true, Value: &profileInput}})
	applyLayer(&effective, profileValues, "profile")
	if effective.Values.ForcedSubtitleLanguage != "en-US" || effective.Sources["forcedSubtitleLanguage"] != "profile" {
		t.Fatalf("profile forced subtitle override was not applied: %+v", effective)
	}
}

func TestSkipMarkerDefaultsAndProfileOverrides(t *testing.T) {
	effective := defaultEffective()
	if !effective.Values.SkipIntroEnabled || !effective.Values.SkipRecapEnabled || !effective.Values.SkipOutroEnabled {
		t.Fatalf("manual skip actions should be enabled by default: %+v", effective.Values)
	}

	disabled := false
	instance := applyPatch(Values{}, Patch{
		SkipIntroEnabled: OptionalBool{Set: true, Value: &disabled},
		SkipRecapEnabled: OptionalBool{Set: true, Value: &disabled},
	})
	applyLayer(&effective, instance, "instance")
	if effective.Values.SkipIntroEnabled || effective.Values.SkipRecapEnabled || !effective.Values.SkipOutroEnabled {
		t.Fatalf("server skip defaults were not applied: %+v", effective.Values)
	}

	enabled := true
	profile := applyPatch(Values{}, Patch{SkipIntroEnabled: OptionalBool{Set: true, Value: &enabled}})
	applyLayer(&effective, profile, "profile")
	if !effective.Values.SkipIntroEnabled || effective.Sources["skipIntroEnabled"] != "profile" ||
		effective.Values.SkipRecapEnabled || effective.Sources["skipRecapEnabled"] != "instance" {
		t.Fatalf("profile skip override did not take precedence: %+v", effective)
	}

	cleared := applyPatch(profile, Patch{SkipIntroEnabled: OptionalBool{Set: true, Value: nil}})
	if cleared.SkipIntroEnabled != nil {
		t.Fatalf("nullable skip override was not cleared: %+v", cleared)
	}
}

func TestEffectiveLayerPrecedence(t *testing.T) {
	effective := Effective{
		SchemaVersion: schemaVersion,
		Values: EffectiveValues{
			Theme: "system", MaximumResolution: "auto", PreferDirectPlay: true,
			HideUnreleased: false, MetadataLanguage: "auto", MetadataRegion: "auto",
			AudioLanguage: "auto", SubtitleLanguage: "auto",
		},
		Sources: map[string]string{
			"theme": "default", "maximumResolution": "default", "preferDirectPlay": "default",
			"hideUnreleased": "default", "metadataLanguage": "default", "metadataRegion": "default",
			"audioLanguage": "default", "subtitleLanguage": "default",
		},
	}
	dark := "dark"
	resolution1080 := "1080p"
	resolution720 := "720p"
	french := "fr"
	directPlay := false
	hideUnreleased := true
	metadataLanguage := "fr-FR"
	metadataRegion := "FR"
	tvdbMapping := "tvdb"
	applyLayer(&effective, Values{Theme: &dark, MaximumResolution: &resolution1080}, "instance")
	applyLayer(&effective, Values{MaximumResolution: &resolution720, AudioLanguage: &french, PreferDirectPlay: &directPlay}, "profile")
	applyLayer(&effective, Values{HideUnreleased: &hideUnreleased}, "profile")
	applyLayer(&effective, Values{MetadataLanguage: &metadataLanguage, MetadataRegion: &metadataRegion, SeriesMappingProvider: &tvdbMapping}, "profile")

	if effective.Values.Theme != "dark" || effective.Sources["theme"] != "instance" {
		t.Fatalf("unexpected theme resolution: %+v", effective)
	}
	if effective.Values.MaximumResolution != "720p" || effective.Sources["maximumResolution"] != "profile" {
		t.Fatalf("unexpected resolution precedence: %+v", effective)
	}
	if effective.Values.PreferDirectPlay || effective.Sources["preferDirectPlay"] != "profile" {
		t.Fatalf("unexpected profile override: %+v", effective)
	}
	if !effective.Values.HideUnreleased || effective.Sources["hideUnreleased"] != "profile" {
		t.Fatalf("unexpected unreleased-title setting resolution: %+v", effective)
	}
	if effective.Values.MetadataLanguage != "fr-FR" || effective.Values.MetadataRegion != "FR" || effective.Sources["metadataLanguage"] != "profile" || effective.Sources["metadataRegion"] != "profile" {
		t.Fatalf("unexpected metadata locale resolution: %+v", effective)
	}
	if effective.Values.SeriesMappingProvider != "tvdb" || effective.Sources["seriesMappingProvider"] != "profile" {
		t.Fatalf("unexpected series mapping resolution: %+v", effective)
	}
	if effective.Values.SubtitleLanguage != "auto" || effective.Sources["subtitleLanguage"] != "default" {
		t.Fatalf("unexpected default value: %+v", effective)
	}
}

func TestValidateNewSettings(t *testing.T) {
	badDensity := "dense"
	badColorShort := "#12345"
	badColorLong := "#1234567"
	badColorDigits := "#12FG00"
	tests := []Patch{
		{CardDensity: OptionalString{Set: true, Value: &badDensity}},
		{SubtitleTextColor: OptionalString{Set: true, Value: &badColorShort}},
		{SubtitleTextColor: OptionalString{Set: true, Value: &badColorLong}},
		{SubtitleTextColor: OptionalString{Set: true, Value: &badColorDigits}},
		{SubtitleSizePercent: OptionalInt{Set: true, Value: new(49)}},
		{SubtitleSizePercent: OptionalInt{Set: true, Value: new(201)}},
		{SubtitleBackgroundOpacityPercent: OptionalInt{Set: true, Value: new(-1)}},
		{SubtitleBackgroundOpacityPercent: OptionalInt{Set: true, Value: new(101)}},
		{NotificationDurationSeconds: OptionalInt{Set: true, Value: new(1)}},
		{NotificationDurationSeconds: OptionalInt{Set: true, Value: new(31)}},
		{NotificationPollIntervalSeconds: OptionalInt{Set: true, Value: new(4)}},
		{NotificationPollIntervalSeconds: OptionalInt{Set: true, Value: new(301)}},
	}
	for _, patch := range tests {
		if err := validatePatch(patch); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid settings error for %+v, got %v", patch, err)
		}
	}
	for _, patch := range []Patch{
		{SubtitleSizePercent: OptionalInt{Set: true, Value: new(50)}},
		{SubtitleSizePercent: OptionalInt{Set: true, Value: new(200)}},
		{SubtitleBackgroundOpacityPercent: OptionalInt{Set: true, Value: new(0)}},
		{SubtitleBackgroundOpacityPercent: OptionalInt{Set: true, Value: new(100)}},
		{NotificationDurationSeconds: OptionalInt{Set: true, Value: new(2)}},
		{NotificationDurationSeconds: OptionalInt{Set: true, Value: new(30)}},
		{NotificationPollIntervalSeconds: OptionalInt{Set: true, Value: new(5)}},
		{NotificationPollIntervalSeconds: OptionalInt{Set: true, Value: new(300)}},
	} {
		if err := validatePatch(patch); err != nil {
			t.Fatalf("boundary setting was rejected for %+v: %v", patch, err)
		}
	}

	color := "#a1b2c3"
	if err := validatePatch(Patch{SubtitleTextColor: OptionalString{Set: true, Value: &color}}); err != nil {
		t.Fatalf("valid subtitle color was rejected: %v", err)
	}
	updated := applyPatch(Values{}, Patch{SubtitleTextColor: OptionalString{Set: true, Value: &color}})
	if updated.SubtitleTextColor == nil || *updated.SubtitleTextColor != "#A1B2C3" {
		t.Fatalf("subtitle color was not normalized: %+v", updated.SubtitleTextColor)
	}
}

func TestNewSettingsClearEveryOverride(t *testing.T) {
	enabled := true
	number := 10
	text := "value"
	values := Values{
		AutoplayNextEpisode: &enabled, SkipIntroEnabled: &enabled, SkipRecapEnabled: &enabled, SkipOutroEnabled: &enabled,
		CardDensity: &text, AnimationsEnabled: &enabled,
		SubtitleSizePercent: &number, SubtitleTextColor: &text, SubtitleBackgroundOpacityPercent: &number,
		NotificationsEnabled: &enabled, NotificationDurationSeconds: &number, NotificationPollIntervalSeconds: &number,
	}
	updated := applyPatch(values, Patch{
		AutoplayNextEpisode: OptionalBool{Set: true}, SkipIntroEnabled: OptionalBool{Set: true},
		SkipRecapEnabled: OptionalBool{Set: true}, SkipOutroEnabled: OptionalBool{Set: true}, CardDensity: OptionalString{Set: true},
		AnimationsEnabled: OptionalBool{Set: true}, SubtitleSizePercent: OptionalInt{Set: true},
		SubtitleTextColor: OptionalString{Set: true}, SubtitleBackgroundOpacityPercent: OptionalInt{Set: true},
		NotificationsEnabled: OptionalBool{Set: true}, NotificationDurationSeconds: OptionalInt{Set: true},
		NotificationPollIntervalSeconds: OptionalInt{Set: true},
	})
	if updated != (Values{}) {
		t.Fatalf("new setting overrides were not all cleared: %+v", updated)
	}
}

func TestNewSettingsLayerInheritanceAndPrecedence(t *testing.T) {
	disabled, enabled := false, true
	compact, comfortable := "compact", "comfortable"
	size80, size120 := 80, 120
	black, accent := "#000000", "#A1B2C3"
	opacity20, opacity80 := 20, 80
	duration10, duration20 := 10, 20
	poll30, poll60 := 30, 60
	instance := Values{
		AutoplayNextEpisode: &disabled, CardDensity: &compact, AnimationsEnabled: &disabled,
		SubtitleSizePercent: &size80, SubtitleTextColor: &black, SubtitleBackgroundOpacityPercent: &opacity20,
		NotificationsEnabled: &disabled, NotificationDurationSeconds: &duration10, NotificationPollIntervalSeconds: &poll30,
	}
	profile := Values{
		AutoplayNextEpisode: &enabled, CardDensity: &comfortable, AnimationsEnabled: &enabled,
		SubtitleSizePercent: &size120, SubtitleTextColor: &accent, SubtitleBackgroundOpacityPercent: &opacity80,
		NotificationsEnabled: &enabled, NotificationDurationSeconds: &duration20, NotificationPollIntervalSeconds: &poll60,
	}
	effective := defaultEffective()
	applyLayer(&effective, instance, "instance")
	if effective.Values.AutoplayNextEpisode || effective.Values.CardDensity != "compact" || effective.Values.AnimationsEnabled ||
		effective.Values.SubtitleSizePercent != 80 || effective.Values.SubtitleTextColor != "#000000" || effective.Values.SubtitleBackgroundOpacityPercent != 20 ||
		effective.Values.NotificationsEnabled || effective.Values.NotificationDurationSeconds != 10 || effective.Values.NotificationPollIntervalSeconds != 30 {
		t.Fatalf("instance settings were not inherited: %+v", effective.Values)
	}
	for _, name := range []string{
		"autoplayNextEpisode", "cardDensity", "animationsEnabled", "subtitleSizePercent", "subtitleTextColor",
		"subtitleBackgroundOpacityPercent", "notificationsEnabled", "notificationDurationSeconds", "notificationPollIntervalSeconds",
	} {
		if effective.Sources[name] != "instance" {
			t.Fatalf("%s source = %q, want instance", name, effective.Sources[name])
		}
	}
	applyLayer(&effective, profile, "profile")
	if !effective.Values.AutoplayNextEpisode || effective.Values.CardDensity != "comfortable" || !effective.Values.AnimationsEnabled ||
		effective.Values.SubtitleSizePercent != 120 || effective.Values.SubtitleTextColor != "#A1B2C3" || effective.Values.SubtitleBackgroundOpacityPercent != 80 ||
		!effective.Values.NotificationsEnabled || effective.Values.NotificationDurationSeconds != 20 || effective.Values.NotificationPollIntervalSeconds != 60 {
		t.Fatalf("profile settings did not take precedence: %+v", effective.Values)
	}
	for _, name := range []string{
		"autoplayNextEpisode", "cardDensity", "animationsEnabled", "subtitleSizePercent", "subtitleTextColor",
		"subtitleBackgroundOpacityPercent", "notificationsEnabled", "notificationDurationSeconds", "notificationPollIntervalSeconds",
	} {
		if effective.Sources[name] != "profile" {
			t.Fatalf("%s source = %q, want profile", name, effective.Sources[name])
		}
	}
}

func TestLegacySettingsJSONUsesNewDefaults(t *testing.T) {
	var legacy Values
	if err := json.Unmarshal([]byte(`{"theme":"dark"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	effective := defaultEffective()
	applyLayer(&effective, legacy, "instance")
	if effective.Values.Theme != "dark" || !effective.Values.AutoplayNextEpisode ||
		!effective.Values.SkipIntroEnabled || !effective.Values.SkipRecapEnabled || !effective.Values.SkipOutroEnabled || effective.Values.CardDensity != "comfortable" ||
		!effective.Values.AnimationsEnabled || effective.Values.SubtitleSizePercent != 100 || effective.Values.SubtitleTextColor != "#FFFFFF" ||
		effective.Values.SubtitleBackgroundOpacityPercent != 60 || !effective.Values.NotificationsEnabled ||
		effective.Values.NotificationDurationSeconds != 5 || effective.Values.NotificationPollIntervalSeconds != 5 {
		t.Fatalf("legacy settings did not resolve with new defaults: %+v", effective.Values)
	}

	encoded, err := json.Marshal(Values{SubtitleSizePercent: new(125)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"subtitleSizePercent":125`) {
		t.Fatalf("new setting used the wrong JSON name: %s", encoded)
	}
}

func TestMaintenanceMessageBoundAndAuthorization(t *testing.T) {
	message := strings.Repeat("é", MaintenanceMessageMaximumSize+1)
	service := NewService(nil)
	_, err := service.UpdateMaintenance(context.Background(), auth.Principal{Role: "admin"}, Maintenance{Enabled: true, Message: &message})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected oversized message rejection, got %v", err)
	}

	_, err = service.UpdateMaintenance(context.Background(), auth.Principal{Role: "member"}, Maintenance{Enabled: true})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected member update rejection, got %v", err)
	}
}
