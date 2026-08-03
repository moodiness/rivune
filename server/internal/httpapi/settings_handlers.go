package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/settings"
)

type nullableBool struct {
	Set   bool
	Value *bool
}

func (value *nullableBool) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded bool
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

type nullableInt struct {
	Set   bool
	Value *int
}

func (value *nullableInt) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var decoded int
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

type settingsPatchRequest struct {
	InterfaceLanguage                nullableString `json:"interfaceLanguage,omitempty"`
	Theme                            nullableString `json:"theme,omitempty"`
	MaximumResolution                nullableString `json:"maximumResolution,omitempty"`
	PreferDirectPlay                 nullableBool   `json:"preferDirectPlay,omitempty"`
	AllowTranscoding                 nullableBool   `json:"allowTranscoding,omitempty"`
	Transcoding                      nullableString `json:"transcoding,omitempty"`
	HideUnreleased                   nullableBool   `json:"hideUnreleased,omitempty"`
	MetadataLanguage                 nullableString `json:"metadataLanguage,omitempty"`
	MetadataRegion                   nullableString `json:"metadataRegion,omitempty"`
	SeriesMappingProvider            nullableString `json:"seriesMappingProvider,omitempty"`
	AudioLanguage                    nullableString `json:"audioLanguage,omitempty"`
	SubtitleLanguage                 nullableString `json:"subtitleLanguage,omitempty"`
	ForcedSubtitleLanguage           nullableString `json:"forcedSubtitleLanguage,omitempty"`
	AutoplayNextEpisode              nullableBool   `json:"autoplayNextEpisode,omitempty"`
	SkipIntroEnabled                 nullableBool   `json:"skipIntroEnabled,omitempty"`
	SkipRecapEnabled                 nullableBool   `json:"skipRecapEnabled,omitempty"`
	SkipOutroEnabled                 nullableBool   `json:"skipOutroEnabled,omitempty"`
	CardDensity                      nullableString `json:"cardDensity,omitempty"`
	AnimationsEnabled                nullableBool   `json:"animationsEnabled,omitempty"`
	SubtitleSizePercent              nullableInt    `json:"subtitleSizePercent,omitempty"`
	SubtitleTextColor                nullableString `json:"subtitleTextColor,omitempty"`
	SubtitleBackgroundOpacityPercent nullableInt    `json:"subtitleBackgroundOpacityPercent,omitempty"`
	NotificationsEnabled             nullableBool   `json:"notificationsEnabled,omitempty"`
	NotificationDurationSeconds      nullableInt    `json:"notificationDurationSeconds,omitempty"`
	NotificationPollIntervalSeconds  nullableInt    `json:"notificationPollIntervalSeconds,omitempty"`
}

func (a *API) instanceSettings(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
	layer, err := a.settings.Instance(r.Context())
	if err != nil {
		a.internalError(w, "read instance settings", err)
		return
	}
	writeJSON(w, http.StatusOK, newSettingsLayerResponse(layer))
}

func (a *API) updateInstanceSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	patch, ok := decodeSettingsPatch(w, r)
	if !ok {
		return
	}
	layer, err := a.settings.UpdateInstance(r.Context(), principal, patch)
	if writeSettingsError(a, w, err, "update instance settings") {
		return
	}
	writeJSON(w, http.StatusOK, newSettingsLayerResponse(layer))
}
func (a *API) maintenanceSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if principal.Role != "admin" {
		writeError(w, http.StatusForbidden, "settings_forbidden", "This account cannot read maintenance settings")
		return
	}
	state, err := a.settings.Maintenance(r.Context())
	if err != nil {
		a.internalError(w, "read maintenance settings", err)
		return
	}
	writeJSON(w, http.StatusOK, newMaintenanceSettingsResponse(state))
}

func (a *API) updateMaintenanceSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		Enabled nullableBool   `json:"enabled"`
		Message nullableString `json:"message,omitempty"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !request.Enabled.Set || request.Enabled.Value == nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_settings", "enabled must be a boolean")
		return
	}
	state, err := a.settings.UpdateMaintenance(r.Context(), principal, settings.Maintenance{
		Enabled: *request.Enabled.Value,
		Message: request.Message.Value,
	})
	if writeSettingsError(a, w, err, "update maintenance settings") {
		return
	}
	writeJSON(w, http.StatusOK, newMaintenanceSettingsResponse(state))
}

func (a *API) profileSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	layer, err := a.settings.Profile(r.Context(), principal, r.PathValue("profileId"))
	if writeSettingsError(a, w, err, "read profile settings") {
		return
	}
	writeJSON(w, http.StatusOK, newSettingsLayerResponse(layer))
}

func (a *API) updateProfileSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	patch, ok := decodeSettingsPatch(w, r)
	if !ok {
		return
	}
	layer, err := a.settings.UpdateProfile(r.Context(), principal, r.PathValue("profileId"), patch)
	if writeSettingsError(a, w, err, "update profile settings") {
		return
	}
	writeJSON(w, http.StatusOK, newSettingsLayerResponse(layer))
}

func (a *API) effectiveSettings(w http.ResponseWriter, r *http.Request, principal auth.Principal) {
	effective, err := a.settings.Effective(r.Context(), principal, r.PathValue("profileId"))
	if writeSettingsError(a, w, err, "resolve effective settings") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": effective.SchemaVersion,
		"settings":      effective.Values,
		"sources":       effective.Sources,
	})
}

func decodeSettingsPatch(w http.ResponseWriter, r *http.Request) (settings.Patch, bool) {
	if !requireJSON(w, r) {
		return settings.Patch{}, false
	}
	var request settingsPatchRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return settings.Patch{}, false
	}
	return settings.Patch{
		InterfaceLanguage:                settings.OptionalString{Set: request.InterfaceLanguage.Set, Value: request.InterfaceLanguage.Value},
		Theme:                            settings.OptionalString{Set: request.Theme.Set, Value: request.Theme.Value},
		MaximumResolution:                settings.OptionalString{Set: request.MaximumResolution.Set, Value: request.MaximumResolution.Value},
		PreferDirectPlay:                 settings.OptionalBool{Set: request.PreferDirectPlay.Set, Value: request.PreferDirectPlay.Value},
		AllowTranscoding:                 settings.OptionalBool{Set: request.AllowTranscoding.Set, Value: request.AllowTranscoding.Value},
		Transcoding:                      settings.OptionalString{Set: request.Transcoding.Set, Value: request.Transcoding.Value},
		HideUnreleased:                   settings.OptionalBool{Set: request.HideUnreleased.Set, Value: request.HideUnreleased.Value},
		MetadataLanguage:                 settings.OptionalString{Set: request.MetadataLanguage.Set, Value: request.MetadataLanguage.Value},
		MetadataRegion:                   settings.OptionalString{Set: request.MetadataRegion.Set, Value: request.MetadataRegion.Value},
		SeriesMappingProvider:            settings.OptionalString{Set: request.SeriesMappingProvider.Set, Value: request.SeriesMappingProvider.Value},
		AudioLanguage:                    settings.OptionalString{Set: request.AudioLanguage.Set, Value: request.AudioLanguage.Value},
		SubtitleLanguage:                 settings.OptionalString{Set: request.SubtitleLanguage.Set, Value: request.SubtitleLanguage.Value},
		ForcedSubtitleLanguage:           settings.OptionalString{Set: request.ForcedSubtitleLanguage.Set, Value: request.ForcedSubtitleLanguage.Value},
		AutoplayNextEpisode:              settings.OptionalBool{Set: request.AutoplayNextEpisode.Set, Value: request.AutoplayNextEpisode.Value},
		SkipIntroEnabled:                 settings.OptionalBool{Set: request.SkipIntroEnabled.Set, Value: request.SkipIntroEnabled.Value},
		SkipRecapEnabled:                 settings.OptionalBool{Set: request.SkipRecapEnabled.Set, Value: request.SkipRecapEnabled.Value},
		SkipOutroEnabled:                 settings.OptionalBool{Set: request.SkipOutroEnabled.Set, Value: request.SkipOutroEnabled.Value},
		CardDensity:                      settings.OptionalString{Set: request.CardDensity.Set, Value: request.CardDensity.Value},
		AnimationsEnabled:                settings.OptionalBool{Set: request.AnimationsEnabled.Set, Value: request.AnimationsEnabled.Value},
		SubtitleSizePercent:              settings.OptionalInt{Set: request.SubtitleSizePercent.Set, Value: request.SubtitleSizePercent.Value},
		SubtitleTextColor:                settings.OptionalString{Set: request.SubtitleTextColor.Set, Value: request.SubtitleTextColor.Value},
		SubtitleBackgroundOpacityPercent: settings.OptionalInt{Set: request.SubtitleBackgroundOpacityPercent.Set, Value: request.SubtitleBackgroundOpacityPercent.Value},
		NotificationsEnabled:             settings.OptionalBool{Set: request.NotificationsEnabled.Set, Value: request.NotificationsEnabled.Value},
		NotificationDurationSeconds:      settings.OptionalInt{Set: request.NotificationDurationSeconds.Set, Value: request.NotificationDurationSeconds.Value},
		NotificationPollIntervalSeconds:  settings.OptionalInt{Set: request.NotificationPollIntervalSeconds.Set, Value: request.NotificationPollIntervalSeconds.Value},
	}, true
}

func writeSettingsError(a *API, w http.ResponseWriter, err error, operation string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, settings.ErrInvalidInput):
		message := strings.TrimPrefix(err.Error(), settings.ErrInvalidInput.Error()+": ")
		writeError(w, http.StatusUnprocessableEntity, "invalid_settings", message)
	case errors.Is(err, settings.ErrForbidden):
		writeError(w, http.StatusForbidden, "settings_forbidden", "This account cannot modify these settings")
	case errors.Is(err, settings.ErrProfileNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", "The profile does not exist")
	case errors.Is(err, settings.ErrSelectionRequired):
		writeError(w, http.StatusConflict, "profile_selection_required", "Select this profile before resolving its effective settings")
	default:
		a.internalError(w, operation, err)
	}
	return true
}

func newMaintenanceSettingsResponse(state settings.Maintenance) map[string]any {
	return map[string]any{
		"enabled": state.Enabled,
		"message": state.Message,
	}
}

func newSettingsLayerResponse(layer settings.Layer) map[string]any {
	layer.Values.MaintenanceEnabled = nil
	layer.Values.MaintenanceMessage = nil
	return map[string]any{
		"schemaVersion": layer.SchemaVersion,
		"settings":      layer.Values,
		"updatedAt":     layer.UpdatedAt,
	}
}
