package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/config"
)

type LegacyEnvironment = config.LegacyEnvironment

func (s *Service) ImportLegacyEnvironment(ctx context.Context, legacy LegacyEnvironment) (bool, error) {
	if s.keyring == nil {
		return false, errors.New("integration credential encryption is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin legacy environment import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var publicID string
	var importedAt *time.Time
	var legacyKeys []string
	err = tx.QueryRow(ctx, `
		SELECT public_id::text, legacy_environment_imported_at, legacy_instance_setting_keys
		FROM instances WHERE id = 1 FOR UPDATE
	`).Scan(&publicID, &importedAt, &legacyKeys)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock legacy environment import: %w", err)
	}
	if importedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit completed legacy environment import: %w", err)
		}
		return false, nil
	}
	legacyKeySet := make(map[string]bool, len(legacyKeys))
	for _, key := range legacyKeys {
		legacyKeySet[key] = true
	}
	for _, name := range []string{"timezone", "jellyfinEnabled", "jellyfinDebug", "hardwareAcceleration", "transcodeMaxBitrateKbps", "mediaMaxStorageMB", "artworkMaxStorageMB", "allowTranscoding"} {
		if !legacyKeySet[name] && legacy.ValidationError(name) != nil {
			return false, fmt.Errorf("%w: legacy %s is invalid", ErrInvalidInput, name)
		}
	}
	current, err := queryInstanceLayer(ctx, tx, true)
	if err != nil {
		return false, fmt.Errorf("load settings for legacy environment import: %w", err)
	}
	current.Values = materializeInstanceValues(current.Values)
	changed := make([]string, 0, 16)
	applyString := func(name string, source *string, target **string) {
		if source != nil && !legacyKeySet[name] {
			value := *source
			*target = &value
			changed = append(changed, name)
		}
	}
	applyBool := func(name string, source *bool, target **bool) {
		if source != nil && !legacyKeySet[name] {
			value := *source
			*target = &value
			changed = append(changed, name)
		}
	}
	applyInt := func(name string, source *int, target **int) {
		if source != nil && !legacyKeySet[name] {
			value := *source
			*target = &value
			changed = append(changed, name)
		}
	}
	applyString("timezone", legacy.Timezone, &current.Values.Timezone)
	applyBool("jellyfinEnabled", legacy.JellyfinEnabled, &current.Values.JellyfinEnabled)
	applyBool("jellyfinDebug", legacy.JellyfinDebug, &current.Values.JellyfinDebug)
	applyString("hardwareAcceleration", legacy.HardwareAcceleration, &current.Values.HardwareAcceleration)
	applyInt("transcodeMaxBitrateKbps", legacy.TranscodeMaxBitrateKbps, &current.Values.TranscodeMaxBitrateKbps)
	applyInt("mediaMaxStorageMB", legacy.MediaMaxStorageMB, &current.Values.MediaMaxStorageMB)
	applyInt("artworkMaxStorageMB", legacy.ArtworkMaxStorageMB, &current.Values.ArtworkMaxStorageMB)
	applyBool("allowTranscoding", legacy.AllowTranscoding, &current.Values.AllowTranscoding)
	if err := validateMaterializedRuntimeValues(current.Values); err != nil {
		return false, err
	}

	stored, credentialValues, err := s.loadIntegrationCredentialsForUpdate(ctx, tx, publicID)
	if err != nil {
		return false, err
	}
	legacyCredentials := map[string]string{
		"tmdbAccessToken": legacy.TMDBAccessToken, "fanartApiKey": legacy.FanartAPIKey,
		"mdblistApiKey": legacy.MDBListAPIKey, "tvdbApiKey": legacy.TVDBAPIKey, "tvdbPin": legacy.TVDBPIN,
		"traktClientId": legacy.TraktClientID, "traktClientSecret": legacy.TraktClientSecret, "simklClientId": legacy.SimklClientID,
	}
	for _, name := range integrationNames {
		value := legacyCredentials[name]
		if value == "" {
			continue
		}
		if _, exists := stored[name]; exists {
			continue
		}
		keyVersion := s.keyring.ActiveVersion()
		envelope, err := s.keyring.Encrypt([]byte(value), integrationAAD(publicID, name, 1, keyVersion))
		if err != nil {
			return false, errors.New("legacy integration credential could not be encrypted")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO instance_integration_credentials
			(instance_id, name, ciphertext, cipher_version, encryption_key_version, generation, updated_at)
			VALUES (1, $1, $2, $3, $4, 1, now())
		`, name, envelope.Ciphertext, envelope.CipherVersion, envelope.KeyVersion); err != nil {
			return false, fmt.Errorf("import legacy integration credential: %w", err)
		}
		credentialValues[name] = value
		changed = append(changed, name)
	}
	if credentialValues["tvdbPin"] != "" && credentialValues["tvdbApiKey"] == "" {
		return false, fmt.Errorf("%w: tvdbPin requires tvdbApiKey", ErrInvalidInput)
	}
	if credentialValues["traktClientSecret"] != "" && credentialValues["traktClientId"] == "" {
		return false, fmt.Errorf("%w: traktClientSecret requires traktClientId", ErrInvalidInput)
	}

	encodedSettings, err := marshalSettings(current.Values)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE instance_settings SET schema_version = $1, settings = $2, updated_at = now()
		WHERE instance_id = 1
	`, schemaVersion, encodedSettings); err != nil {
		return false, fmt.Errorf("import legacy runtime settings: %w", err)
	}
	revision, err := incrementConfigurationRevision(ctx, tx)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE instances SET legacy_environment_imported_at = now() WHERE id = 1`); err != nil {
		return false, fmt.Errorf("mark legacy environment imported: %w", err)
	}
	status, err := queryIntegrationStatus(ctx, tx)
	if err != nil {
		return false, err
	}
	snapshot, err := json.Marshal(map[string]any{"settings": current.Values, "credentials": configuredSnapshot(status.Credentials)})
	if err != nil {
		return false, fmt.Errorf("encode legacy import audit snapshot: %w", err)
	}
	sort.Strings(changed)
	if err := insertAuditEvent(ctx, tx, revision, "", "legacy_environment.imported", changed, snapshot); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit legacy environment import: %w", err)
	}
	if err := s.publishIntegrations(ctx, credentialsFromMap(credentialValues, revision)); err != nil {
		s.scheduleIntegrationReconciliation()
	}
	return true, nil
}

func validateMaterializedRuntimeValues(values Values) error {
	if values.Timezone == nil || values.JellyfinEnabled == nil || values.JellyfinDebug == nil ||
		values.HardwareAcceleration == nil || values.PreferredTranscodeVideoCodec == nil ||
		values.TranscodeQualityPreset == nil || values.TranscodeConcurrency == nil ||
		values.TranscodeMaxBitrateKbps == nil || values.MediaMaxStorageMB == nil ||
		values.ArtworkMaxStorageMB == nil || values.AllowTranscoding == nil {
		return errors.New("instance settings schema v3 runtime values are incomplete")
	}
	patch := Patch{
		Timezone: OptionalString{Set: true, Value: values.Timezone}, JellyfinEnabled: OptionalBool{Set: true, Value: values.JellyfinEnabled},
		JellyfinDebug: OptionalBool{Set: true, Value: values.JellyfinDebug}, HardwareAcceleration: OptionalString{Set: true, Value: values.HardwareAcceleration},
		PreferredTranscodeVideoCodec: OptionalString{Set: true, Value: values.PreferredTranscodeVideoCodec},
		TranscodeQualityPreset:       OptionalString{Set: true, Value: values.TranscodeQualityPreset},
		TranscodeConcurrency:         OptionalInt{Set: true, Value: values.TranscodeConcurrency},
		TranscodeMaxBitrateKbps:      OptionalInt{Set: true, Value: values.TranscodeMaxBitrateKbps}, MediaMaxStorageMB: OptionalInt{Set: true, Value: values.MediaMaxStorageMB},
		ArtworkMaxStorageMB: OptionalInt{Set: true, Value: values.ArtworkMaxStorageMB}, AllowTranscoding: OptionalBool{Set: true, Value: values.AllowTranscoding},
	}
	return validateInstancePatch(patch)
}
