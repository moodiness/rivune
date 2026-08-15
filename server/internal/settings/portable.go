package settings

// ValidatePortableProfileValues applies the same value and profile-scope rules
// as a profile settings update without requiring a database transaction.
func ValidatePortableProfileValues(values Values) error {
	patch := Patch{
		InterfaceLanguage:                optionalString(values.InterfaceLanguage),
		Theme:                            optionalString(values.Theme),
		MaximumResolution:                optionalString(values.MaximumResolution),
		MaximumCastMembers:               optionalInt(values.MaximumCastMembers),
		MaximumDirectTitles:              optionalInt(values.MaximumDirectTitles),
		PreferDirectPlay:                 optionalBool(values.PreferDirectPlay),
		AllowTranscoding:                 optionalBool(values.AllowTranscoding),
		JellyfinEnabled:                  optionalBool(values.JellyfinEnabled),
		Timezone:                         optionalString(values.Timezone),
		JellyfinDebug:                    optionalBool(values.JellyfinDebug),
		HardwareAcceleration:             optionalString(values.HardwareAcceleration),
		PreferredTranscodeVideoCodec:     optionalString(values.PreferredTranscodeVideoCodec),
		TranscodeQualityPreset:           optionalString(values.TranscodeQualityPreset),
		TranscodeConcurrency:             optionalInt(values.TranscodeConcurrency),
		TranscodeMaxBitrateKbps:          optionalInt(values.TranscodeMaxBitrateKbps),
		MediaMaxStorageMB:                optionalInt(values.MediaMaxStorageMB),
		ArtworkMaxStorageMB:              optionalInt(values.ArtworkMaxStorageMB),
		Transcoding:                      optionalTranscoding(values.Transcoding),
		HideUnreleased:                   optionalBool(values.HideUnreleased),
		MetadataLanguage:                 optionalString(values.MetadataLanguage),
		MetadataRegion:                   optionalString(values.MetadataRegion),
		SeriesMappingProvider:            optionalString(values.SeriesMappingProvider),
		AudioLanguage:                    optionalString(values.AudioLanguage),
		SubtitleLanguage:                 optionalString(values.SubtitleLanguage),
		ForcedSubtitleLanguage:           optionalString(values.ForcedSubtitleLanguage),
		AutoplayNextEpisode:              optionalBool(values.AutoplayNextEpisode),
		SkipIntroEnabled:                 optionalBool(values.SkipIntroEnabled),
		SkipRecapEnabled:                 optionalBool(values.SkipRecapEnabled),
		SkipOutroEnabled:                 optionalBool(values.SkipOutroEnabled),
		CardDensity:                      optionalString(values.CardDensity),
		AnimationsEnabled:                optionalBool(values.AnimationsEnabled),
		SubtitleSizePercent:              optionalInt(values.SubtitleSizePercent),
		SubtitleTextColor:                optionalString(values.SubtitleTextColor),
		SubtitleBackgroundOpacityPercent: optionalInt(values.SubtitleBackgroundOpacityPercent),
		NotificationsEnabled:             optionalBool(values.NotificationsEnabled),
		NotificationDurationSeconds:      optionalInt(values.NotificationDurationSeconds),
		NotificationPollIntervalSeconds:  optionalInt(values.NotificationPollIntervalSeconds),
	}
	if portablePatchEmpty(patch) {
		return nil
	}
	return validateProfilePatch(patch)
}

func optionalString(value *string) OptionalString {
	return OptionalString{Set: value != nil, Value: value}
}
func optionalBool(value *bool) OptionalBool { return OptionalBool{Set: value != nil, Value: value} }
func optionalInt(value *int) OptionalInt    { return OptionalInt{Set: value != nil, Value: value} }
func optionalTranscoding(value *TranscodingMode) OptionalString {
	if value == nil {
		return OptionalString{}
	}
	converted := string(*value)
	return OptionalString{Set: true, Value: &converted}
}
func portablePatchEmpty(patch Patch) bool {
	return !patch.InterfaceLanguage.Set && !patch.Theme.Set && !patch.MaximumResolution.Set &&
		!patch.MaximumCastMembers.Set && !patch.MaximumDirectTitles.Set && !patch.PreferDirectPlay.Set &&
		!patch.AllowTranscoding.Set && !patch.JellyfinEnabled.Set && !patch.Timezone.Set &&
		!patch.JellyfinDebug.Set && !patch.HardwareAcceleration.Set && !patch.PreferredTranscodeVideoCodec.Set &&
		!patch.TranscodeQualityPreset.Set && !patch.TranscodeConcurrency.Set && !patch.TranscodeMaxBitrateKbps.Set &&
		!patch.MediaMaxStorageMB.Set && !patch.ArtworkMaxStorageMB.Set && !patch.Transcoding.Set &&
		!patch.HideUnreleased.Set && !patch.MetadataLanguage.Set && !patch.MetadataRegion.Set &&
		!patch.SeriesMappingProvider.Set && !patch.AudioLanguage.Set && !patch.SubtitleLanguage.Set &&
		!patch.ForcedSubtitleLanguage.Set && !patch.AutoplayNextEpisode.Set && !patch.SkipIntroEnabled.Set &&
		!patch.SkipRecapEnabled.Set && !patch.SkipOutroEnabled.Set && !patch.CardDensity.Set &&
		!patch.AnimationsEnabled.Set && !patch.SubtitleSizePercent.Set && !patch.SubtitleTextColor.Set &&
		!patch.SubtitleBackgroundOpacityPercent.Set && !patch.NotificationsEnabled.Set &&
		!patch.NotificationDurationSeconds.Set && !patch.NotificationPollIntervalSeconds.Set
}
