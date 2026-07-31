package profile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/password"
)

var (
	ErrInvalidInput            = errors.New("invalid profile input")
	ErrForbidden               = errors.New("profile operation forbidden")
	ErrNotFound                = errors.New("profile not found")
	ErrLastProfile             = errors.New("the final profile cannot be deleted")
	ErrLastUnrestrictedProfile = errors.New("at least one enabled unrestricted profile must remain")
	ErrInvalidPIN              = errors.New("invalid profile PIN")
	ErrPINRateLimited          = errors.New("too many invalid profile PIN attempts")
	ErrUnavailable             = errors.New("profile unavailable")
)

const (
	pinFailureLimit = 5
	PINLockSeconds  = 300
)

type Service struct {
	pool            *pgxpool.Pool
	grantTTL        time.Duration
	defaultTimezone string
}

type Profile struct {
	ID              string
	Name            string
	IsChild         bool
	HasPIN          bool
	CanManage       bool
	AvatarKind      string
	AvatarPreset    string
	Enabled         bool
	AvailableFrom   *string
	AvailableUntil  *string
	AccessStartTime *string
	AccessEndTime   *string
	AccessTimezone  string
	Accessible      bool
}

type Selection struct {
	Profile   Profile
	ExpiresAt time.Time
}

type CreateInput struct {
	Name            string
	IsChild         bool
	PIN             *string
	Enabled         *bool
	AvailableFrom   *string
	AvailableUntil  *string
	AccessStartTime *string
	AccessEndTime   *string
}

type UpdateInput struct {
	Name               *string
	IsChild            *bool
	PINSet             bool
	PIN                *string
	Enabled            *bool
	AvailableFromSet   bool
	AvailableFrom      *string
	AvailableUntilSet  bool
	AvailableUntil     *string
	AccessStartTimeSet bool
	AccessStartTime    *string
	AccessEndTimeSet   bool
	AccessEndTime      *string
}

func NewService(pool *pgxpool.Pool, grantTTL time.Duration, defaultTimezone string) *Service {
	return &Service{pool: pool, grantTTL: grantTTL, defaultTimezone: defaultTimezone}
}

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id::text, p.name, p.is_child, p.pin_hash IS NOT NULL,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $1
		WHERE $2 = 'admin' OR upa.user_id IS NOT NULL
		ORDER BY lower(p.name), p.id
	`, principal.UserID, principal.Role)
	if err != nil {
		return nil, fmt.Errorf("query profiles: %w", err)
	}
	defer rows.Close()

	profiles := make([]Profile, 0)
	for rows.Next() {
		var profile Profile
		var customAvatar bool
		if err := rows.Scan(
			&profile.ID, &profile.Name, &profile.IsChild, &profile.HasPIN, &profile.CanManage,
			&profile.AvatarPreset, &customAvatar, &profile.Enabled, &profile.AvailableFrom,
			&profile.AvailableUntil, &profile.AccessStartTime, &profile.AccessEndTime, &profile.AccessTimezone,
		); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		profile.AccessTimezone = s.defaultTimezone
		profile.Accessible = profileAccessible(profile, time.Now().UTC())
		profile.AvatarKind = "preset"
		if customAvatar {
			profile.AvatarKind = "custom"
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profiles: %w", err)
	}
	return profiles, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput) (Profile, error) {
	if principal.Role != "admin" {
		return Profile{}, ErrForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validName(input.Name) {
		return Profile{}, fmt.Errorf("%w: name must contain 1 to 80 characters", ErrInvalidInput)
	}
	pinHash, err := hashPIN(input.PIN)
	if err != nil {
		return Profile{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	timezone := s.defaultTimezone
	profile := Profile{
		Name: input.Name, IsChild: input.IsChild, HasPIN: pinHash != nil, CanManage: false,
		AvatarKind: "preset", AvatarPreset: defaultAvatarPreset, Enabled: enabled,
		AvailableFrom: input.AvailableFrom, AvailableUntil: input.AvailableUntil,
		AccessStartTime: input.AccessStartTime, AccessEndTime: input.AccessEndTime, AccessTimezone: timezone,
	}
	if err := validateAccess(profile); err != nil {
		return Profile{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin profile creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, `
		INSERT INTO profiles (
			name, pin_hash, is_child, enabled, available_from, available_until,
			access_start_time, access_end_time, access_timezone
		)
		VALUES ($1, $2, $3, $4, $5::date, $6::date, $7::time, $8::time, $9)
		RETURNING id::text
	`, input.Name, pinHash, input.IsChild, profile.Enabled, profile.AvailableFrom, profile.AvailableUntil,
		profile.AccessStartTime, profile.AccessEndTime, profile.AccessTimezone,
	).Scan(&profile.ID); err != nil {
		return Profile{}, fmt.Errorf("create profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1, $2, false)
	`, principal.UserID, profile.ID); err != nil {
		return Profile{}, fmt.Errorf("grant profile access: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO profile_settings (profile_id) VALUES ($1)", profile.ID); err != nil {
		return Profile{}, fmt.Errorf("create profile settings: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit profile creation: %w", err)
	}
	profile.Accessible = profileAccessible(profile, time.Now().UTC())
	return profile, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, profileID string, input UpdateInput) (Profile, error) {
	accessChanged := input.Enabled != nil || input.AvailableFromSet || input.AvailableUntilSet ||
		input.AccessStartTimeSet || input.AccessEndTimeSet
	selectionInvalidated := accessChanged || input.PINSet
	if profileID == "" || (!input.PINSet && input.Name == nil && input.IsChild == nil && !accessChanged) {
		return Profile{}, fmt.Errorf("%w: at least one field must be provided", ErrInvalidInput)
	}
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		input.Name = &trimmed
		if !validName(trimmed) {
			return Profile{}, fmt.Errorf("%w: name must contain 1 to 80 characters", ErrInvalidInput)
		}
	}
	var pinHash *string
	var err error
	if input.PINSet {
		pinHash, err = hashPIN(input.PIN)
		if err != nil {
			return Profile{}, err
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin profile update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "LOCK TABLE profiles IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return Profile{}, fmt.Errorf("lock profiles for update: %w", err)
	}

	var current Profile
	var currentPINHash *string
	var currentAvatarCustom bool
	err = tx.QueryRow(ctx, `
		SELECT p.id::text, p.name, p.is_child, p.pin_hash,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $2
		WHERE p.id::text = $1
		  AND ($3 = 'admin' OR COALESCE(upa.can_manage, false))
		FOR UPDATE OF p
	`, profileID, principal.UserID, principal.Role).Scan(
		&current.ID, &current.Name, &current.IsChild, &currentPINHash, &current.CanManage,
		&current.AvatarPreset, &currentAvatarCustom, &current.Enabled, &current.AvailableFrom,
		&current.AvailableUntil, &current.AccessStartTime, &current.AccessEndTime, &current.AccessTimezone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("query profile for update: %w", err)
	}

	current.AvatarKind = "preset"
	if currentAvatarCustom {
		current.AvatarKind = "custom"
	}
	current.AccessTimezone = s.defaultTimezone
	if input.Name != nil {
		current.Name = *input.Name
	}
	if input.IsChild != nil {
		current.IsChild = *input.IsChild
	}
	if input.PINSet {
		currentPINHash = pinHash
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.AvailableFromSet {
		current.AvailableFrom = input.AvailableFrom
	}
	if input.AvailableUntilSet {
		current.AvailableUntil = input.AvailableUntil
	}
	if input.AccessStartTimeSet {
		current.AccessStartTime = input.AccessStartTime
	}
	if input.AccessEndTimeSet {
		current.AccessEndTime = input.AccessEndTime
	}
	if err := validateAccess(current); err != nil {
		return Profile{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profiles
		SET name = $2, is_child = $3, pin_hash = $4, enabled = $5,
		    available_from = $6::date, available_until = $7::date,
		    access_start_time = $8::time, access_end_time = $9::time,
		    access_timezone = $10, updated_at = now()
		WHERE id = $1
	`, current.ID, current.Name, current.IsChild, currentPINHash, current.Enabled,
		current.AvailableFrom, current.AvailableUntil, current.AccessStartTime, current.AccessEndTime,
		current.AccessTimezone,
	); err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	var unrestrictedCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM profiles
		WHERE enabled
		  AND available_from IS NULL AND available_until IS NULL
		  AND access_start_time IS NULL AND access_end_time IS NULL
	`).Scan(&unrestrictedCount); err != nil {
		return Profile{}, fmt.Errorf("count unrestricted profiles: %w", err)
	}
	if err := ensureUnrestrictedProfile(unrestrictedCount); err != nil {
		return Profile{}, err
	}
	if selectionInvalidated {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET active_profile_id = NULL, profile_grant_expires_at = NULL
			WHERE active_profile_id::text = $1
		`, current.ID); err != nil {
			return Profile{}, fmt.Errorf("clear changed profile selections: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit profile update: %w", err)
	}
	current.HasPIN = currentPINHash != nil
	current.Accessible = profileAccessible(current, time.Now().UTC())
	return current, nil
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin profile deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "LOCK TABLE profiles IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("lock profiles for deletion: %w", err)
	}
	var authorized bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profiles p
			LEFT JOIN user_profile_access upa
			  ON upa.profile_id = p.id AND upa.user_id = $2
			WHERE p.id::text = $1
			  AND ($3 = 'admin' OR COALESCE(upa.can_manage, false))
		)
	`, profileID, principal.UserID, principal.Role).Scan(&authorized); err != nil {
		return fmt.Errorf("authorize profile deletion: %w", err)
	}
	if !authorized {
		return ErrNotFound
	}
	var profileCount int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM profiles").Scan(&profileCount); err != nil {
		return fmt.Errorf("count profiles: %w", err)
	}
	if profileCount <= 1 {
		return ErrLastProfile
	}
	var remainingUnrestricted int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM profiles
		WHERE id::text <> $1
		  AND enabled
		  AND available_from IS NULL AND available_until IS NULL
		  AND access_start_time IS NULL AND access_end_time IS NULL
	`, profileID).Scan(&remainingUnrestricted); err != nil {
		return fmt.Errorf("count remaining unrestricted profiles: %w", err)
	}
	if err := ensureUnrestrictedProfile(remainingUnrestricted); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = NULL, profile_grant_expires_at = NULL
		WHERE active_profile_id::text = $1
	`, profileID); err != nil {
		return fmt.Errorf("clear deleted profile selections: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM profiles WHERE id::text = $1", profileID); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile deletion: %w", err)
	}
	return nil
}

func (s *Service) Select(ctx context.Context, principal auth.Principal, profileID string, providedPIN *string) (Selection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Selection{}, fmt.Errorf("begin profile selection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var selected Profile
	var pinHash *string
	var selectedAvatarCustom bool
	err = tx.QueryRow(ctx, `
		SELECT p.id::text, p.name, p.is_child, p.pin_hash,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $2
		WHERE p.id::text = $1
		  AND ($3 = 'admin' OR upa.user_id IS NOT NULL)
		FOR SHARE OF p
	`, strings.TrimSpace(profileID), principal.UserID, principal.Role).Scan(
		&selected.ID, &selected.Name, &selected.IsChild, &pinHash, &selected.CanManage,
		&selected.AvatarPreset, &selectedAvatarCustom, &selected.Enabled, &selected.AvailableFrom,
		&selected.AvailableUntil, &selected.AccessStartTime, &selected.AccessEndTime, &selected.AccessTimezone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Selection{}, ErrNotFound
	}
	if err != nil {
		return Selection{}, fmt.Errorf("query profile for selection: %w", err)
	}
	selected.HasPIN = pinHash != nil
	selected.AvatarKind = "preset"
	if selectedAvatarCustom {
		selected.AvatarKind = "custom"
	}
	now := time.Now().UTC()
	selected.AccessTimezone = s.defaultTimezone
	selected.Accessible = profileAccessible(selected, now)
	if !selected.Accessible {
		return Selection{}, ErrUnavailable
	}
	if pinHash != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO profile_pin_failures (user_id, profile_id)
			VALUES ($1, $2)
			ON CONFLICT (user_id, profile_id) DO NOTHING
		`, principal.UserID, selected.ID); err != nil {
			return Selection{}, fmt.Errorf("initialize profile PIN failures: %w", err)
		}

		var failedAttempts int
		var lockedUntil *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT failed_attempts, locked_until
			FROM profile_pin_failures
			WHERE user_id = $1 AND profile_id = $2
			FOR UPDATE
		`, principal.UserID, selected.ID).Scan(&failedAttempts, &lockedUntil); err != nil {
			return Selection{}, fmt.Errorf("query profile PIN failures: %w", err)
		}
		if lockedUntil != nil && now.Before(*lockedUntil) {
			return Selection{}, ErrPINRateLimited
		}

		matches := false
		if providedPIN != nil {
			matches, err = password.Verify(*providedPIN, *pinHash)
			if err != nil {
				return Selection{}, fmt.Errorf("verify profile PIN: %w", err)
			}
		}
		if !matches {
			failedAttempts++
			selectionErr := error(ErrInvalidPIN)
			lockedUntil = nil
			if failedAttempts >= pinFailureLimit {
				failedAttempts = 0
				lockExpiresAt := now.Add(PINLockSeconds * time.Second)
				lockedUntil = &lockExpiresAt
				selectionErr = ErrPINRateLimited
			}
			if _, err := tx.Exec(ctx, `
				UPDATE profile_pin_failures
				SET failed_attempts = $3, locked_until = $4, updated_at = now()
				WHERE user_id = $1 AND profile_id = $2
			`, principal.UserID, selected.ID, failedAttempts, lockedUntil); err != nil {
				return Selection{}, fmt.Errorf("record profile PIN failure: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return Selection{}, fmt.Errorf("commit profile PIN failure: %w", err)
			}
			return Selection{}, selectionErr
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM profile_pin_failures
			WHERE user_id = $1 AND profile_id = $2
		`, principal.UserID, selected.ID); err != nil {
			return Selection{}, fmt.Errorf("clear profile PIN failures: %w", err)
		}
	}

	expiresAt := now.Add(s.grantTTL)
	command, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = $3, profile_grant_expires_at = $4, last_seen_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, principal.SessionID, principal.UserID, selected.ID, expiresAt)
	if err != nil {
		return Selection{}, fmt.Errorf("activate profile selection: %w", err)
	}
	if command.RowsAffected() == 0 {
		return Selection{}, ErrForbidden
	}
	if err := tx.Commit(ctx); err != nil {
		return Selection{}, fmt.Errorf("commit profile selection: %w", err)
	}
	return Selection{Profile: selected, ExpiresAt: expiresAt}, nil
}

func (s *Service) ClearSelection(ctx context.Context, principal auth.Principal) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = NULL, profile_grant_expires_at = NULL, last_seen_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, principal.SessionID, principal.UserID)
	if err != nil {
		return fmt.Errorf("clear profile selection: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrForbidden
	}
	return nil
}

func hasAccessSchedule(value Profile) bool {
	return value.AvailableFrom != nil || value.AvailableUntil != nil ||
		value.AccessStartTime != nil || value.AccessEndTime != nil
}

func ensureUnrestrictedProfile(count int) error {
	if count == 0 {
		return ErrLastUnrestrictedProfile
	}
	return nil
}

func validateAccess(profile Profile) error {
	if err := auth.ValidateProfileAccess(profileAccess(profile)); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	return nil
}

func profileAccessible(profile Profile, now time.Time) bool {
	return auth.ProfileAccessibleAt(profileAccess(profile), now)
}

func profileAccess(profile Profile) auth.ProfileAccess {
	return auth.ProfileAccess{
		Enabled: profile.Enabled, AvailableFrom: profile.AvailableFrom, AvailableUntil: profile.AvailableUntil,
		AccessStartTime: profile.AccessStartTime, AccessEndTime: profile.AccessEndTime, AccessTimezone: profile.AccessTimezone,
	}
}

func hashPIN(pin *string) (*string, error) {
	if pin == nil {
		return nil, nil
	}
	if len(*pin) < 4 || len(*pin) > 8 {
		return nil, fmt.Errorf("%w: PIN must contain 4 to 8 digits", ErrInvalidInput)
	}
	for _, character := range *pin {
		if character < '0' || character > '9' {
			return nil, fmt.Errorf("%w: PIN must contain only digits", ErrInvalidInput)
		}
	}
	hash, err := password.Hash(*pin)
	if err != nil {
		return nil, fmt.Errorf("hash profile PIN: %w", err)
	}
	return &hash, nil
}

func validName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 80
}
