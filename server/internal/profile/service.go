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
	ErrInvalidInput = errors.New("invalid profile input")
	ErrForbidden    = errors.New("profile operation forbidden")
	ErrNotFound     = errors.New("profile not found")
	ErrLastProfile  = errors.New("the final profile cannot be deleted")
	ErrInvalidPIN   = errors.New("invalid profile PIN")
)

type Service struct {
	pool     *pgxpool.Pool
	grantTTL time.Duration
}

type Profile struct {
	ID           string
	Name         string
	IsChild      bool
	HasPIN       bool
	CanManage    bool
	AvatarKind   string
	AvatarPreset string
}

type Selection struct {
	Profile   Profile
	ExpiresAt time.Time
}

type CreateInput struct {
	Name    string
	IsChild bool
	PIN     *string
}

type UpdateInput struct {
	Name    *string
	IsChild *bool
	PINSet  bool
	PIN     *string
}

func NewService(pool *pgxpool.Pool, grantTTL time.Duration) *Service {
	return &Service{pool: pool, grantTTL: grantTTL}
}

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id::text, p.name, p.is_child, p.pin_hash IS NOT NULL,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id)
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
			&profile.AvatarPreset, &customAvatar,
		); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
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

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("begin profile creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	profile := Profile{
		Name: input.Name, IsChild: input.IsChild, HasPIN: pinHash != nil, CanManage: false,
		AvatarKind: "preset", AvatarPreset: defaultAvatarPreset,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO profiles (name, pin_hash, is_child)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, input.Name, pinHash, input.IsChild).Scan(&profile.ID); err != nil {
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
	return profile, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, profileID string, input UpdateInput) (Profile, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || (!input.PINSet && input.Name == nil && input.IsChild == nil) {
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

	var current Profile
	var currentPINHash *string
	var currentAvatarCustom bool
	err = tx.QueryRow(ctx, `
		SELECT p.id::text, p.name, p.is_child, p.pin_hash,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id)
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $2
		WHERE p.id::text = $1
		  AND ($3 = 'admin' OR COALESCE(upa.can_manage, false))
		FOR UPDATE OF p
	`, profileID, principal.UserID, principal.Role).Scan(
		&current.ID, &current.Name, &current.IsChild, &currentPINHash, &current.CanManage,
		&current.AvatarPreset, &currentAvatarCustom,
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
	if input.Name != nil {
		current.Name = *input.Name
	}
	if input.IsChild != nil {
		current.IsChild = *input.IsChild
	}
	if input.PINSet {
		currentPINHash = pinHash
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profiles
		SET name = $2, is_child = $3, pin_hash = $4, updated_at = now()
		WHERE id = $1
	`, current.ID, current.Name, current.IsChild, currentPINHash); err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Profile{}, fmt.Errorf("commit profile update: %w", err)
	}
	current.HasPIN = currentPINHash != nil
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
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id)
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $2
		WHERE p.id::text = $1
		  AND ($3 = 'admin' OR upa.user_id IS NOT NULL)
		FOR SHARE OF p
	`, strings.TrimSpace(profileID), principal.UserID, principal.Role).Scan(
		&selected.ID, &selected.Name, &selected.IsChild, &pinHash, &selected.CanManage,
		&selected.AvatarPreset, &selectedAvatarCustom,
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
	if pinHash != nil {
		if providedPIN == nil {
			return Selection{}, ErrInvalidPIN
		}
		matches, err := password.Verify(*providedPIN, *pinHash)
		if err != nil {
			return Selection{}, fmt.Errorf("verify profile PIN: %w", err)
		}
		if !matches {
			return Selection{}, ErrInvalidPIN
		}
	}

	expiresAt := time.Now().UTC().Add(s.grantTTL)
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
