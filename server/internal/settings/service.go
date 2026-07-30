package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

const schemaVersion = 1

var (
	ErrInvalidInput      = errors.New("invalid settings input")
	ErrForbidden         = errors.New("settings operation forbidden")
	ErrProfileNotFound   = errors.New("profile not found")
	ErrSelectionRequired = errors.New("active profile selection required")
	languageTagPattern   = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	regionCodePattern    = regexp.MustCompile(`^[A-Z]{2}$`)
)

type Service struct {
	pool *pgxpool.Pool
}

type OptionalString struct {
	Set   bool
	Value *string
}

type OptionalBool struct {
	Set   bool
	Value *bool
}

type Patch struct {
	Theme             OptionalString
	MaximumResolution OptionalString
	PreferDirectPlay  OptionalBool
	HideUnreleased    OptionalBool
	MetadataLanguage  OptionalString
	MetadataRegion    OptionalString
	AudioLanguage     OptionalString
	SubtitleLanguage  OptionalString
}

type Values struct {
	Theme             *string `json:"theme,omitempty"`
	MaximumResolution *string `json:"maximumResolution,omitempty"`
	PreferDirectPlay  *bool   `json:"preferDirectPlay,omitempty"`
	HideUnreleased    *bool   `json:"hideUnreleased,omitempty"`
	MetadataLanguage  *string `json:"metadataLanguage,omitempty"`
	MetadataRegion    *string `json:"metadataRegion,omitempty"`
	AudioLanguage     *string `json:"audioLanguage,omitempty"`
	SubtitleLanguage  *string `json:"subtitleLanguage,omitempty"`
}

type Layer struct {
	SchemaVersion int
	Values        Values
	UpdatedAt     *time.Time
}

type EffectiveValues struct {
	Theme             string `json:"theme"`
	MaximumResolution string `json:"maximumResolution"`
	PreferDirectPlay  bool   `json:"preferDirectPlay"`
	HideUnreleased    bool   `json:"hideUnreleased"`
	MetadataLanguage  string `json:"metadataLanguage"`
	MetadataRegion    string `json:"metadataRegion"`
	AudioLanguage     string `json:"audioLanguage"`
	SubtitleLanguage  string `json:"subtitleLanguage"`
}

type Effective struct {
	SchemaVersion int
	Values        EffectiveValues
	Sources       map[string]string
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) Instance(ctx context.Context) (Layer, error) {
	return queryLayer(ctx, s.pool, "SELECT schema_version, settings, updated_at FROM instance_settings WHERE instance_id = 1")
}

func (s *Service) UpdateInstance(ctx context.Context, principal auth.Principal, patch Patch) (Layer, error) {
	if principal.Role != "admin" {
		return Layer{}, ErrForbidden
	}
	if err := validatePatch(patch); err != nil {
		return Layer{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Layer{}, fmt.Errorf("begin instance settings update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := queryLayer(ctx, tx, "SELECT schema_version, settings, updated_at FROM instance_settings WHERE instance_id = 1 FOR UPDATE")
	if err != nil {
		return Layer{}, err
	}
	current.Values = applyPatch(current.Values, patch)
	encoded, err := json.Marshal(current.Values)
	if err != nil {
		return Layer{}, fmt.Errorf("encode instance settings: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		UPDATE instance_settings
		SET schema_version = $1, settings = $2, updated_at = now()
		WHERE instance_id = 1
		RETURNING updated_at
	`, schemaVersion, encoded).Scan(&current.UpdatedAt); err != nil {
		return Layer{}, fmt.Errorf("update instance settings: %w", err)
	}
	current.SchemaVersion = schemaVersion
	if err := tx.Commit(ctx); err != nil {
		return Layer{}, fmt.Errorf("commit instance settings: %w", err)
	}
	return current, nil
}

func (s *Service) Profile(ctx context.Context, principal auth.Principal, profileID string) (Layer, error) {
	return s.queryProfileLayer(ctx, s.pool, principal, profileID)
}

func (s *Service) UpdateProfile(ctx context.Context, principal auth.Principal, profileID string, patch Patch) (Layer, error) {
	if err := validatePatch(patch); err != nil {
		return Layer{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Layer{}, fmt.Errorf("begin profile settings update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := s.queryProfileLayer(ctx, tx, principal, profileID)
	if err != nil {
		return Layer{}, err
	}
	current.Values = applyPatch(current.Values, patch)
	encoded, err := json.Marshal(current.Values)
	if err != nil {
		return Layer{}, fmt.Errorf("encode profile settings: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		UPDATE profile_settings
		SET schema_version = $2, settings = $3, updated_at = now()
		WHERE profile_id::text = $1
		RETURNING updated_at
	`, strings.TrimSpace(profileID), schemaVersion, encoded).Scan(&current.UpdatedAt); err != nil {
		return Layer{}, fmt.Errorf("update profile settings: %w", err)
	}
	current.SchemaVersion = schemaVersion
	if err := tx.Commit(ctx); err != nil {
		return Layer{}, fmt.Errorf("commit profile settings: %w", err)
	}
	return current, nil
}

func (s *Service) Effective(ctx context.Context, principal auth.Principal, profileID string) (Effective, error) {
	profileID = strings.TrimSpace(profileID)
	if principal.ActiveProfileID == nil || principal.ProfileGrantExpiresAt == nil || *principal.ActiveProfileID != profileID {
		return Effective{}, ErrSelectionRequired
	}

	var instanceVersion, profileVersion int
	var instanceRaw, profileRaw json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT i.schema_version, i.settings,
		       ps.schema_version, ps.settings
		FROM instance_settings i
		JOIN profiles p ON true
		JOIN profile_settings ps ON ps.profile_id = p.id
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $2
		WHERE i.instance_id = 1
		  AND p.id::text = $1
		  AND ($3 = 'admin' OR upa.user_id IS NOT NULL)
	`, profileID, principal.UserID, principal.Role).Scan(
		&instanceVersion, &instanceRaw,
		&profileVersion, &profileRaw,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Effective{}, ErrProfileNotFound
	}
	if err != nil {
		return Effective{}, fmt.Errorf("query effective settings layers: %w", err)
	}
	if instanceVersion != schemaVersion || profileVersion != schemaVersion {
		return Effective{}, fmt.Errorf(
			"unsupported settings schema versions instance=%d profile=%d",
			instanceVersion, profileVersion,
		)
	}
	var instanceValues, profileValues Values
	if err := json.Unmarshal(instanceRaw, &instanceValues); err != nil {
		return Effective{}, fmt.Errorf("decode instance settings: %w", err)
	}
	if err := json.Unmarshal(profileRaw, &profileValues); err != nil {
		return Effective{}, fmt.Errorf("decode profile settings: %w", err)
	}

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
	applyLayer(&effective, instanceValues, "instance")
	applyLayer(&effective, profileValues, "profile")
	return effective, nil
}

func (s *Service) queryProfileLayer(ctx context.Context, querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, principal auth.Principal, profileID string) (Layer, error) {
	lockClause := ""
	if _, ok := querier.(pgx.Tx); ok {
		lockClause = " FOR UPDATE OF ps"
	}
	query := `
		SELECT ps.schema_version, ps.settings, ps.updated_at
		FROM profile_settings ps
		JOIN profiles p ON p.id = ps.profile_id
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $2
		WHERE p.id::text = $1
		  AND ($3 = 'admin' OR upa.user_id IS NOT NULL)` + lockClause
	layer, err := queryLayer(ctx, querier, query, strings.TrimSpace(profileID), principal.UserID, principal.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Layer{}, ErrProfileNotFound
	}
	return layer, err
}

func queryLayer(ctx context.Context, querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, query string, arguments ...any) (Layer, error) {
	var layer Layer
	var raw json.RawMessage
	if err := querier.QueryRow(ctx, query, arguments...).Scan(&layer.SchemaVersion, &raw, &layer.UpdatedAt); err != nil {
		return Layer{}, err
	}
	if layer.SchemaVersion != schemaVersion {
		return Layer{}, fmt.Errorf("unsupported settings schema version %d", layer.SchemaVersion)
	}
	if err := json.Unmarshal(raw, &layer.Values); err != nil {
		return Layer{}, fmt.Errorf("decode settings: %w", err)
	}
	return layer, nil
}

func validatePatch(patch Patch) error {
	if !patch.Theme.Set && !patch.MaximumResolution.Set && !patch.PreferDirectPlay.Set && !patch.HideUnreleased.Set && !patch.MetadataLanguage.Set && !patch.MetadataRegion.Set && !patch.AudioLanguage.Set && !patch.SubtitleLanguage.Set {
		return fmt.Errorf("%w: at least one setting must be provided", ErrInvalidInput)
	}
	if value := patch.Theme.Value; patch.Theme.Set && value != nil {
		switch *value {
		case "system", "light", "dark":
		default:
			return fmt.Errorf("%w: theme must be system, light, or dark", ErrInvalidInput)
		}
	}
	if value := patch.MaximumResolution.Value; patch.MaximumResolution.Set && value != nil {
		switch *value {
		case "auto", "2160p", "1080p", "720p", "480p":
		default:
			return fmt.Errorf("%w: maximumResolution is invalid", ErrInvalidInput)
		}
	}
	for name, value := range map[string]OptionalString{
		"metadataLanguage": patch.MetadataLanguage, "audioLanguage": patch.AudioLanguage, "subtitleLanguage": patch.SubtitleLanguage,
	} {
		if value.Set && value.Value != nil && *value.Value != "auto" && !languageTagPattern.MatchString(*value.Value) {
			return fmt.Errorf("%w: %s must be auto or a BCP 47 language tag", ErrInvalidInput, name)
		}
	}
	if value := patch.MetadataRegion.Value; patch.MetadataRegion.Set && value != nil && *value != "auto" && !regionCodePattern.MatchString(*value) {
		return fmt.Errorf("%w: metadataRegion must be auto or an uppercase ISO 3166-1 alpha-2 code", ErrInvalidInput)
	}
	return nil
}

func applyPatch(values Values, patch Patch) Values {
	if patch.Theme.Set {
		values.Theme = patch.Theme.Value
	}
	if patch.MaximumResolution.Set {
		values.MaximumResolution = patch.MaximumResolution.Value
	}
	if patch.PreferDirectPlay.Set {
		values.PreferDirectPlay = patch.PreferDirectPlay.Value
	}
	if patch.HideUnreleased.Set {
		values.HideUnreleased = patch.HideUnreleased.Value
	}
	if patch.MetadataLanguage.Set {
		values.MetadataLanguage = patch.MetadataLanguage.Value
	}
	if patch.MetadataRegion.Set {
		values.MetadataRegion = patch.MetadataRegion.Value
	}
	if patch.AudioLanguage.Set {
		values.AudioLanguage = patch.AudioLanguage.Value
	}
	if patch.SubtitleLanguage.Set {
		values.SubtitleLanguage = patch.SubtitleLanguage.Value
	}
	return values
}

func applyLayer(effective *Effective, values Values, source string) {
	if values.Theme != nil {
		effective.Values.Theme = *values.Theme
		effective.Sources["theme"] = source
	}
	if values.MaximumResolution != nil {
		effective.Values.MaximumResolution = *values.MaximumResolution
		effective.Sources["maximumResolution"] = source
	}
	if values.PreferDirectPlay != nil {
		effective.Values.PreferDirectPlay = *values.PreferDirectPlay
		effective.Sources["preferDirectPlay"] = source
	}
	if values.HideUnreleased != nil {
		effective.Values.HideUnreleased = *values.HideUnreleased
		effective.Sources["hideUnreleased"] = source
	}
	if values.MetadataLanguage != nil {
		effective.Values.MetadataLanguage = *values.MetadataLanguage
		effective.Sources["metadataLanguage"] = source
	}
	if values.MetadataRegion != nil {
		effective.Values.MetadataRegion = *values.MetadataRegion
		effective.Sources["metadataRegion"] = source
	}
	if values.AudioLanguage != nil {
		effective.Values.AudioLanguage = *values.AudioLanguage
		effective.Sources["audioLanguage"] = source
	}
	if values.SubtitleLanguage != nil {
		effective.Values.SubtitleLanguage = *values.SubtitleLanguage
		effective.Sources["subtitleLanguage"] = source
	}
}
