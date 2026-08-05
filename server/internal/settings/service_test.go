package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

func TestEffectiveRejectsExpiredGrantBeforeDatabaseAccess(t *testing.T) {
	profileID := "profile-id"
	expired := time.Now().UTC().Add(-time.Minute)

	_, err := NewService(nil).Effective(context.Background(), auth.Principal{
		ActiveProfileID:       &profileID,
		ProfileGrantExpiresAt: &expired,
	}, profileID)
	if !errors.Is(err, ErrSelectionRequired) {
		t.Fatalf("expired profile grant error = %v, want selection required", err)
	}
}

func TestValidateEffectiveSelectionRequiresStrictlyCurrentMatchingGrant(t *testing.T) {
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	profileID := "profile-id"
	otherProfileID := "other-profile-id"
	expired := now.Add(-time.Nanosecond)
	valid := now.Add(time.Nanosecond)

	tests := []struct {
		name      string
		principal auth.Principal
		wantErr   bool
	}{
		{name: "missing profile", principal: auth.Principal{ProfileGrantExpiresAt: &valid}, wantErr: true},
		{name: "missing expiration", principal: auth.Principal{ActiveProfileID: &profileID}, wantErr: true},
		{name: "different profile", principal: auth.Principal{ActiveProfileID: &otherProfileID, ProfileGrantExpiresAt: &valid}, wantErr: true},
		{name: "expired", principal: auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &expired}, wantErr: true},
		{name: "expires now", principal: auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &now}, wantErr: true},
		{name: "valid", principal: auth.Principal{ActiveProfileID: &profileID, ProfileGrantExpiresAt: &valid}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateEffectiveSelection(test.principal, profileID, now)
			if test.wantErr && !errors.Is(err, ErrSelectionRequired) {
				t.Fatalf("selection validation error = %v, want selection required", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("valid profile grant was rejected: %v", err)
			}
		})
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

func TestMaximumCastMembersDefaultsValidationPersistenceAndInheritance(t *testing.T) {
	effective := defaultEffective()
	if effective.Values.MaximumCastMembers != DefaultMaximumCastMembers || effective.Sources["maximumCastMembers"] != "default" {
		t.Fatalf("maximum cast default = %d from %q, want %d from default", effective.Values.MaximumCastMembers, effective.Sources["maximumCastMembers"], DefaultMaximumCastMembers)
	}

	for _, invalid := range []int{MinimumMaximumCastMembers - 1, MaximumMaximumCastMembers + 1} {
		patch := Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &invalid}}
		if err := validatePatch(patch); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("maximumCastMembers %d validation error = %v, want invalid input", invalid, err)
		}
	}
	for _, valid := range []int{MinimumMaximumCastMembers, MaximumMaximumCastMembers} {
		patch := Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &valid}}
		if err := validatePatch(patch); err != nil {
			t.Fatalf("maximumCastMembers %d was rejected: %v", valid, err)
		}
	}

	serverLimit := 60
	serverValues := applyPatch(Values{}, Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &serverLimit}})
	encodedServer, err := json.Marshal(serverValues)
	if err != nil {
		t.Fatal(err)
	}
	var persistedServer Values
	if err := json.Unmarshal(encodedServer, &persistedServer); err != nil {
		t.Fatal(err)
	}
	if persistedServer.MaximumCastMembers == nil || *persistedServer.MaximumCastMembers != serverLimit {
		t.Fatalf("server maximumCastMembers did not survive JSON persistence: %s", encodedServer)
	}

	profileLimit := 15
	profileValues := applyPatch(Values{}, Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &profileLimit}})
	encodedProfile, err := json.Marshal(profileValues)
	if err != nil {
		t.Fatal(err)
	}
	var persistedProfile Values
	if err := json.Unmarshal(encodedProfile, &persistedProfile); err != nil {
		t.Fatal(err)
	}
	if persistedProfile.MaximumCastMembers == nil || *persistedProfile.MaximumCastMembers != profileLimit {
		t.Fatalf("profile maximumCastMembers did not survive JSON persistence: %s", encodedProfile)
	}

	inherited := applyPatch(persistedProfile, Patch{MaximumCastMembers: OptionalInt{Set: true}})
	if inherited.MaximumCastMembers != nil {
		t.Fatalf("null profile maximumCastMembers did not restore inheritance: %+v", inherited)
	}
	effective = defaultEffective()
	applyLayer(&effective, persistedServer, "instance")
	applyLayer(&effective, inherited, "profile")
	applyMaximumCastMembersPolicy(&effective, persistedServer, inherited)
	if effective.Values.MaximumCastMembers != serverLimit || effective.Sources["maximumCastMembers"] != "instance" {
		t.Fatalf("inherited maximumCastMembers = %d from %q, want %d from instance", effective.Values.MaximumCastMembers, effective.Sources["maximumCastMembers"], serverLimit)
	}
}

func TestProfileMaximumCastMembersRejectsServerOverflowAndCapsStaleOverride(t *testing.T) {
	aboveDefault := DefaultMaximumCastMembers + 1
	if err := validateProfileMaximumCastMembers(Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &aboveDefault}}, Values{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("profile value above default server limit error = %v, want invalid input", err)
	}

	serverLimit := 40
	aboveServer := serverLimit + 1
	instance := Values{MaximumCastMembers: &serverLimit}
	if err := validateProfileMaximumCastMembers(Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &aboveServer}}, instance); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("profile value above configured server limit error = %v, want invalid input", err)
	}
	if err := validateProfileMaximumCastMembers(Patch{MaximumCastMembers: OptionalInt{Set: true, Value: &serverLimit}}, instance); err != nil {
		t.Fatalf("profile value equal to server limit was rejected: %v", err)
	}

	loweredServerLimit := 10
	staleProfileLimit := 50
	loweredInstance := Values{MaximumCastMembers: &loweredServerLimit}
	staleProfile := Values{MaximumCastMembers: &staleProfileLimit}
	effective := defaultEffective()
	applyLayer(&effective, loweredInstance, "instance")
	applyLayer(&effective, staleProfile, "profile")
	applyMaximumCastMembersPolicy(&effective, loweredInstance, staleProfile)
	if effective.Values.MaximumCastMembers != loweredServerLimit || effective.Sources["maximumCastMembers"] != "instance" {
		t.Fatalf("stale profile override was not bounded after server decrease: %+v", effective)
	}
}
func TestInterfaceLanguageValidationAndLayering(t *testing.T) {
	for _, language := range []string{
		"en", "fr", "es", "it", "de", "ru", "pt-PT", "pt-BR", "ar", "ja", "ko", "zh-CN", "pl", "hy",
		"es-MX", "es-AR", "es-CL", "es-CO", "es-PE", "fr-CA", "zh-TW", "nl", "sv", "da", "fi", "nb",
		"tr", "uk", "cs", "sk", "ro", "el", "he", "hi", "id", "vi", "th", "hu", "bg", "hr", "sr", "ms",
		"ca", "fa", "fil",
	} {
		language := language
		if err := validatePatch(Patch{InterfaceLanguage: OptionalString{Set: true, Value: &language}}); err != nil {
			t.Fatalf("supported interface language %q was rejected: %v", language, err)
		}
	}
	for _, language := range []string{"EN", "pt", "pt-br", "zh-HK", "fr-FR", "auto", ""} {
		language := language
		if err := validatePatch(Patch{InterfaceLanguage: OptionalString{Set: true, Value: &language}}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unsupported interface language %q was accepted: %v", language, err)
		}
	}

	effective := defaultEffective()
	if effective.Values.InterfaceLanguage != "en" || effective.Sources["interfaceLanguage"] != "default" {
		t.Fatalf("unexpected built-in interface language: %+v", effective)
	}

	arabic := "ar"
	instance := applyPatch(Values{}, Patch{InterfaceLanguage: OptionalString{Set: true, Value: &arabic}})
	applyLayer(&effective, instance, "instance")
	applyLayer(&effective, Values{}, "profile")
	if effective.Values.InterfaceLanguage != "ar" || effective.Sources["interfaceLanguage"] != "instance" {
		t.Fatalf("profile did not inherit instance interface language: %+v", effective)
	}

	armenian := "hy"
	profile := applyPatch(Values{}, Patch{InterfaceLanguage: OptionalString{Set: true, Value: &armenian}})
	applyLayer(&effective, profile, "profile")
	if effective.Values.InterfaceLanguage != "hy" || effective.Sources["interfaceLanguage"] != "profile" {
		t.Fatalf("profile interface language override was not applied: %+v", effective)
	}

	profile = applyPatch(profile, Patch{InterfaceLanguage: OptionalString{Set: true, Value: nil}})
	effective = defaultEffective()
	applyLayer(&effective, instance, "instance")
	applyLayer(&effective, profile, "profile")
	if effective.Values.InterfaceLanguage != "ar" || effective.Sources["interfaceLanguage"] != "instance" {
		t.Fatalf("cleared profile interface language did not inherit instance: %+v", effective)
	}

	instance = applyPatch(instance, Patch{InterfaceLanguage: OptionalString{Set: true, Value: nil}})
	effective = defaultEffective()
	applyLayer(&effective, instance, "instance")
	if effective.Values.InterfaceLanguage != "en" || effective.Sources["interfaceLanguage"] != "default" {
		t.Fatalf("cleared instance interface language did not use built-in default: %+v", effective)
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
			InterfaceLanguage: "en",
			Theme:             "system", MaximumResolution: "auto", PreferDirectPlay: true,
			HideUnreleased: false, MetadataLanguage: "auto", MetadataRegion: "auto",
			AudioLanguage: "auto", SubtitleLanguage: "auto",
		},
		Sources: map[string]string{
			"interfaceLanguage": "default",
			"theme":             "default", "maximumResolution": "default", "preferDirectPlay": "default",
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
		InterfaceLanguage:   &text,
		MaximumCastMembers:  &number,
		AutoplayNextEpisode: &enabled, SkipIntroEnabled: &enabled, SkipRecapEnabled: &enabled, SkipOutroEnabled: &enabled,
		CardDensity: &text, AnimationsEnabled: &enabled,
		SubtitleSizePercent: &number, SubtitleTextColor: &text, SubtitleBackgroundOpacityPercent: &number,
		NotificationsEnabled: &enabled, NotificationDurationSeconds: &number, NotificationPollIntervalSeconds: &number,
	}
	updated := applyPatch(values, Patch{
		InterfaceLanguage:   OptionalString{Set: true},
		MaximumCastMembers:  OptionalInt{Set: true},
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
	applyNotificationPolicy(&effective, instance)
	if !effective.Values.AutoplayNextEpisode || effective.Values.CardDensity != "comfortable" || !effective.Values.AnimationsEnabled ||
		effective.Values.SubtitleSizePercent != 120 || effective.Values.SubtitleTextColor != "#A1B2C3" || effective.Values.SubtitleBackgroundOpacityPercent != 80 {
		t.Fatalf("profile settings did not take precedence: %+v", effective.Values)
	}
	for _, name := range []string{
		"autoplayNextEpisode", "cardDensity", "animationsEnabled", "subtitleSizePercent", "subtitleTextColor", "subtitleBackgroundOpacityPercent",
	} {
		if effective.Sources[name] != "profile" {
			t.Fatalf("%s source = %q, want profile", name, effective.Sources[name])
		}
	}
	if effective.Values.NotificationsEnabled || effective.Values.NotificationDurationSeconds != 10 || effective.Values.NotificationPollIntervalSeconds != 30 {
		t.Fatalf("profile notification overrides changed server values: %+v", effective.Values)
	}
	for _, name := range []string{"notificationsEnabled", "notificationDurationSeconds", "notificationPollIntervalSeconds"} {
		if effective.Sources[name] != "instance" {
			t.Fatalf("%s source = %q, want instance", name, effective.Sources[name])
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
	if effective.Values.InterfaceLanguage != "en" || effective.Values.Theme != "dark" || effective.Values.MaximumCastMembers != DefaultMaximumCastMembers || !effective.Values.AutoplayNextEpisode ||
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
	_, err := service.UpdateMaintenance(context.Background(), auth.Principal{
		Role: "admin", AuthorizationScope: auth.AuthorizationScopeGlobalAdministrator,
	}, Maintenance{Enabled: true, Message: &message})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected oversized message rejection, got %v", err)
	}

	_, err = service.UpdateMaintenance(context.Background(), auth.Principal{Role: "member"}, Maintenance{Enabled: true})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected member update rejection, got %v", err)
	}
}

func TestTranscodingPolicyDefaultsAndMatrix(t *testing.T) {
	if TranscodingModeInherit != "inherit" || TranscodingModeEnabled != "enabled" || TranscodingModeDisabled != "disabled" {
		t.Fatalf("unexpected transcoding mode wire values")
	}

	tests := []struct {
		name            string
		instanceAllowed bool
		mode            TranscodingMode
		wantAllowed     bool
		wantSource      string
	}{
		{name: "allowed inherit", instanceAllowed: true, mode: TranscodingModeInherit, wantAllowed: true, wantSource: "instance"},
		{name: "allowed enabled", instanceAllowed: true, mode: TranscodingModeEnabled, wantAllowed: true, wantSource: "instance"},
		{name: "allowed disabled", instanceAllowed: true, mode: TranscodingModeDisabled, wantAllowed: false, wantSource: "profile"},
		{name: "global veto inherit", instanceAllowed: false, mode: TranscodingModeInherit, wantAllowed: false, wantSource: "instance"},
		{name: "global veto enabled", instanceAllowed: false, mode: TranscodingModeEnabled, wantAllowed: false, wantSource: "instance"},
		{name: "global veto disabled", instanceAllowed: false, mode: TranscodingModeDisabled, wantAllowed: false, wantSource: "instance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instanceAllowed := test.instanceAllowed
			mode := test.mode
			effective := defaultEffective()
			applyTranscodingPolicy(&effective, Values{AllowTranscoding: &instanceAllowed}, Values{Transcoding: &mode})
			if effective.Values.AllowTranscoding != test.wantAllowed || effective.Values.Transcoding != test.mode {
				t.Fatalf("effective policy = allow %t mode %q, want allow %t mode %q", effective.Values.AllowTranscoding, effective.Values.Transcoding, test.wantAllowed, test.mode)
			}
			if effective.Sources["allowTranscoding"] != test.wantSource || effective.Sources["transcoding"] != "profile" {
				t.Fatalf("unexpected policy sources: %+v", effective.Sources)
			}
			if got := CanProfileTranscode(test.instanceAllowed, string(test.mode)); got != test.wantAllowed {
				t.Fatalf("CanProfileTranscode(%t, %q) = %t, want %t", test.instanceAllowed, test.mode, got, test.wantAllowed)
			}
		})
	}

	effective := defaultEffective()
	applyTranscodingPolicy(&effective, Values{}, Values{})
	if !effective.Values.AllowTranscoding || effective.Values.Transcoding != TranscodingModeInherit ||
		effective.Sources["allowTranscoding"] != "default" || effective.Sources["transcoding"] != "default" {
		t.Fatalf("unexpected legacy/default policy: %+v", effective)
	}
	if CanProfileTranscode(true, "unexpected") {
		t.Fatal("unknown profile mode must not authorize transcoding")
	}
}

func TestTranscodingPatchScopeValidationAndNullInheritance(t *testing.T) {
	enabled, disabled := true, false
	inherit, profileEnabled, profileDisabled := "inherit", "enabled", "disabled"

	for _, allowed := range []*bool{&enabled, &disabled, nil} {
		if err := validateInstancePatch(Patch{AllowTranscoding: OptionalBool{Set: true, Value: allowed}}); err != nil {
			t.Fatalf("valid instance allowTranscoding patch rejected: %v", err)
		}
	}
	for _, mode := range []*string{&inherit, &profileEnabled, &profileDisabled, nil} {
		if err := validateProfilePatch(Patch{Transcoding: OptionalString{Set: true, Value: mode}}); err != nil {
			t.Fatalf("valid profile transcoding patch rejected: %v", err)
		}
	}

	if err := validateInstancePatch(Patch{Transcoding: OptionalString{Set: true, Value: &profileEnabled}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("instance accepted profile-only transcoding: %v", err)
	}
	if err := validateProfilePatch(Patch{AllowTranscoding: OptionalBool{Set: true, Value: &enabled}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("profile accepted instance-only allowTranscoding: %v", err)
	}
	invalid := "force"
	if err := validateProfilePatch(Patch{Transcoding: OptionalString{Set: true, Value: &invalid}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("profile accepted invalid transcoding mode: %v", err)
	}

	mode := TranscodingModeDisabled
	values := Values{AllowTranscoding: &disabled, Transcoding: &mode}
	values = applyPatch(values, Patch{
		AllowTranscoding: OptionalBool{Set: true, Value: nil},
		Transcoding:      OptionalString{Set: true, Value: nil},
	})
	if values.AllowTranscoding != nil || values.Transcoding != nil {
		t.Fatalf("null patches did not restore inheritance/defaults: %+v", values)
	}
}

func TestTranscodingSettingsJSONPersistence(t *testing.T) {
	allowed := false
	mode := TranscodingModeEnabled
	encoded, err := json.Marshal(Values{AllowTranscoding: &allowed, Transcoding: &mode})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"allowTranscoding":false,"transcoding":"enabled"}` {
		t.Fatalf("unexpected persisted transcoding JSON: %s", encoded)
	}

	var decoded Values
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AllowTranscoding == nil || *decoded.AllowTranscoding || decoded.Transcoding == nil || *decoded.Transcoding != TranscodingModeEnabled {
		t.Fatalf("transcoding JSON did not round trip: %+v", decoded)
	}
	if defaultEffective().SchemaVersion != 1 {
		t.Fatalf("transcoding settings must not change schemaVersion")
	}
}

func TestInstanceTranscodingUpdateKeepsAdminPermission(t *testing.T) {
	allowed := true
	_, err := NewService(nil).UpdateInstance(context.Background(), auth.Principal{Role: "member"}, Patch{
		AllowTranscoding: OptionalBool{Set: true, Value: &allowed},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("member instance transcoding update error = %v, want forbidden", err)
	}
}

func TestProfileTranscodingUpdateRequiresGlobalAdministrator(t *testing.T) {
	mode := "disabled"
	_, err := NewService(nil).UpdateProfile(context.Background(), auth.Principal{
		Role:               "admin",
		AuthorizationScope: auth.AuthorizationScopeCategory,
	}, "profile-id", Patch{Transcoding: OptionalString{Set: true, Value: &mode}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("category profile transcoding update error = %v, want forbidden", err)
	}
}

func TestProfileNotificationUpdatesAreRejected(t *testing.T) {
	enabled := true
	duration := 10
	interval := 30
	patches := map[string]Patch{
		"enabled":  {NotificationsEnabled: OptionalBool{Set: true, Value: &enabled}},
		"duration": {NotificationDurationSeconds: OptionalInt{Set: true, Value: &duration}},
		"interval": {NotificationPollIntervalSeconds: OptionalInt{Set: true, Value: &interval}},
	}
	for _, role := range []string{"admin", "member"} {
		for name, patch := range patches {
			t.Run(role+"/"+name, func(t *testing.T) {
				_, err := NewService(nil).UpdateProfile(context.Background(), auth.Principal{Role: role}, "profile-id", patch)
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("%s profile notification update error = %v, want invalid input", role, err)
				}
			})
		}
	}
}

func TestUpdateProfileSettingsRequiresProfileManagement(t *testing.T) {
	databaseURL := os.Getenv("RIVUNE_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("RIVUNE_DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("set RIVUNE_TEST_DATABASE_URL or RIVUNE_DATABASE_URL to run the PostgreSQL profile settings authorization test")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)

	ctx := context.Background()
	categoryID := "11111111-1111-4111-8111-111111111111"
	const (
		userID    = "22222222-2222-4222-8222-222222222222"
		profileID = "33333333-3333-4333-8333-333333333333"
	)
	if _, err := pool.Exec(ctx, `
		CREATE TEMPORARY TABLE profiles (
			id uuid PRIMARY KEY,
			category_id uuid
		);
		CREATE TEMPORARY TABLE user_profile_access (
			user_id uuid NOT NULL,
			profile_id uuid NOT NULL,
			can_manage boolean NOT NULL DEFAULT false,
			PRIMARY KEY (user_id, profile_id)
		);
		CREATE TEMPORARY TABLE profile_settings (
			profile_id uuid PRIMARY KEY,
			schema_version integer NOT NULL,
			settings jsonb NOT NULL,
			updated_at timestamptz NOT NULL
		);
		CREATE TEMPORARY TABLE instance_settings (
			instance_id integer PRIMARY KEY,
			schema_version integer NOT NULL,
			settings jsonb NOT NULL,
			updated_at timestamptz NOT NULL
		);
	`); err != nil {
		t.Fatalf("create profile settings authorization fixtures: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO profiles (id, category_id)
		VALUES ($1::uuid, $2::uuid);
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($3::uuid, $1::uuid, false);
		INSERT INTO profile_settings (profile_id, schema_version, settings, updated_at)
		VALUES ($1::uuid, 1, '{"theme":"dark"}'::jsonb, '2026-01-02T03:04:05Z');
		INSERT INTO instance_settings (instance_id, schema_version, settings, updated_at)
		VALUES (1, 1, '{}'::jsonb, '2026-01-02T03:04:05Z');
	`, pgx.QueryExecModeSimpleProtocol, profileID, categoryID, userID); err != nil {
		t.Fatalf("seed profile settings authorization boundary: %v", err)
	}

	principal := auth.Principal{
		UserID:             userID,
		Role:               "member",
		AuthorizationScope: auth.AuthorizationScopeCategory,
		CategoryID:         &categoryID,
	}
	var before string
	if err := pool.QueryRow(ctx, `
		SELECT ps::text
		FROM profile_settings ps
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&before); err != nil {
		t.Fatalf("read profile settings row before denied update: %v", err)
	}

	language := "fr"
	service := NewService(pool)
	readable, err := service.Profile(ctx, principal, profileID)
	if err != nil {
		t.Fatalf("access-only profile settings read: %v", err)
	}
	if readable.Values.Theme == nil || *readable.Values.Theme != "dark" {
		t.Fatalf("access-only profile settings read returned unexpected layer: %+v", readable)
	}
	if _, err := service.UpdateProfile(ctx, principal, profileID, Patch{
		InterfaceLanguage: OptionalString{Set: true, Value: &language},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("access-only profile settings update error = %v, want forbidden", err)
	}
	var after string
	if err := pool.QueryRow(ctx, `
		SELECT ps::text
		FROM profile_settings ps
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&after); err != nil {
		t.Fatalf("read profile settings row after denied update: %v", err)
	}
	if after != before {
		t.Fatalf("denied profile settings update changed the stored row: before=%q after=%q", before, after)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE user_profile_access
		SET can_manage = true
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
	`, userID, profileID); err != nil {
		t.Fatalf("grant profile management: %v", err)
	}
	updated, err := service.UpdateProfile(ctx, principal, profileID, Patch{
		InterfaceLanguage: OptionalString{Set: true, Value: &language},
	})
	if err != nil {
		t.Fatalf("managed profile settings update: %v", err)
	}
	if updated.Values.InterfaceLanguage == nil || *updated.Values.InterfaceLanguage != language {
		t.Fatalf("managed profile settings update returned unexpected layer: %+v", updated)
	}
	var persistedLanguage string
	if err := pool.QueryRow(ctx, `
		SELECT settings->>'interfaceLanguage'
		FROM profile_settings
		WHERE profile_id = $1::uuid
	`, profileID).Scan(&persistedLanguage); err != nil {
		t.Fatalf("read managed profile settings update: %v", err)
	}
	if persistedLanguage != language {
		t.Fatalf("persisted interface language = %q, want %q", persistedLanguage, language)
	}
}
