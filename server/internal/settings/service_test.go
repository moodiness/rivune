package settings

import (
	"errors"
	"testing"
)

func TestValidatePatchRejectsUnknownValues(t *testing.T) {
	invalidTheme := "neon"
	invalidResolution := "8k"
	invalidLanguage := "not_a_language"
	invalidRegion := "France"
	tests := []Patch{
		{Theme: OptionalString{Set: true, Value: &invalidTheme}},
		{MaximumResolution: OptionalString{Set: true, Value: &invalidResolution}},
		{AudioLanguage: OptionalString{Set: true, Value: &invalidLanguage}},
		{MetadataRegion: OptionalString{Set: true, Value: &invalidRegion}},
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
	applyLayer(&effective, Values{Theme: &dark, MaximumResolution: &resolution1080}, "instance")
	applyLayer(&effective, Values{MaximumResolution: &resolution720, AudioLanguage: &french, PreferDirectPlay: &directPlay}, "profile")
	applyLayer(&effective, Values{HideUnreleased: &hideUnreleased}, "profile")
	applyLayer(&effective, Values{MetadataLanguage: &metadataLanguage, MetadataRegion: &metadataRegion}, "profile")

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
	if effective.Values.SubtitleLanguage != "auto" || effective.Sources["subtitleLanguage"] != "default" {
		t.Fatalf("unexpected default value: %+v", effective)
	}
}
