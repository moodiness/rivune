package profile

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/text/unicode/norm"
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
	ErrManagementRequired      = errors.New("profile management permission required")
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
	CategoryID      string
	CategoryName    string
	CategoryColor   *string
	CategoryIcon    *string
	Name            string
	Description     *string
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
	Profile        Profile
	ExpiresAt      time.Time
	ProfileContext string
}

type CreateInput struct {
	Name            string
	CategoryID      string
	Description     *string
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
	DescriptionSet     bool
	Description        *string
	CategoryID         *string
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin profile list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	candidateRows, err := tx.Query(ctx, `
		SELECT p.id::text
		FROM profiles p
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $1
		WHERE $2
		   OR ($4 = 'category' AND p.category_id::text = $3 AND upa.user_id IS NOT NULL)
		ORDER BY p.id
	`, principal.UserID, principal.IsGlobalAdministrator(), principalCategoryID(principal), principal.AuthorizationScope)
	if err != nil {
		return nil, fmt.Errorf("query visible profile candidates: %w", err)
	}
	candidateProfileIDs := make([]string, 0)
	for candidateRows.Next() {
		var profileID string
		if err := candidateRows.Scan(&profileID); err != nil {
			candidateRows.Close()
			return nil, fmt.Errorf("scan visible profile candidate: %w", err)
		}
		candidateProfileIDs = append(candidateProfileIDs, profileID)
	}
	err = candidateRows.Err()
	candidateRows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate visible profile candidates: %w", err)
	}

	profileLockRows, err := tx.Query(ctx, `
		SELECT id::text
		FROM profiles
		WHERE id = ANY($1::uuid[])
		ORDER BY id
		FOR SHARE
	`, candidateProfileIDs)
	if err != nil {
		return nil, fmt.Errorf("lock visible profiles: %w", err)
	}
	lockedProfileIDs := make([]string, 0, len(candidateProfileIDs))
	for profileLockRows.Next() {
		var profileID string
		if err := profileLockRows.Scan(&profileID); err != nil {
			profileLockRows.Close()
			return nil, fmt.Errorf("scan locked visible profile: %w", err)
		}
		lockedProfileIDs = append(lockedProfileIDs, profileID)
	}
	err = profileLockRows.Err()
	profileLockRows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate locked visible profiles: %w", err)
	}

	visibleProfileIDs := lockedProfileIDs
	if !principal.IsGlobalAdministrator() {
		visibleProfileIDs = make([]string, 0)
		if principal.AuthorizationScope == auth.AuthorizationScopeCategory {
			grantLockRows, err := tx.Query(ctx, `
				SELECT profile_id::text
				FROM user_profile_access
				WHERE user_id = $1
				  AND profile_id = ANY($2::uuid[])
				ORDER BY profile_id
				FOR SHARE
			`, principal.UserID, lockedProfileIDs)
			if err != nil {
				return nil, fmt.Errorf("lock visible profile grants: %w", err)
			}
			for grantLockRows.Next() {
				var profileID string
				if err := grantLockRows.Scan(&profileID); err != nil {
					grantLockRows.Close()
					return nil, fmt.Errorf("scan locked visible profile grant: %w", err)
				}
				visibleProfileIDs = append(visibleProfileIDs, profileID)
			}
			err = grantLockRows.Err()
			grantLockRows.Close()
			if err != nil {
				return nil, fmt.Errorf("iterate locked visible profile grants: %w", err)
			}
		}
	}

	rows, err := tx.Query(ctx, `
		SELECT p.id::text, p.category_id::text, category.name, category.color, category.icon,
		       p.name, p.description, p.is_child, p.pin_hash IS NOT NULL,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone
		FROM profiles p
		JOIN access_categories category ON category.id = p.category_id
		LEFT JOIN user_profile_access upa
		  ON upa.profile_id = p.id AND upa.user_id = $1
		WHERE p.id = ANY($5::uuid[])
		  AND (
		    $2
		    OR ($4 = 'category' AND p.category_id::text = $3 AND upa.user_id IS NOT NULL)
		  )
		ORDER BY lower(p.name), p.id
	`, principal.UserID, principal.IsGlobalAdministrator(), principalCategoryID(principal),
		principal.AuthorizationScope, visibleProfileIDs)
	if err != nil {
		return nil, fmt.Errorf("query profiles: %w", err)
	}

	profiles := make([]Profile, 0)
	for rows.Next() {
		var profile Profile
		var customAvatar bool
		if err := rows.Scan(
			&profile.ID, &profile.CategoryID, &profile.CategoryName, &profile.CategoryColor, &profile.CategoryIcon,
			&profile.Name, &profile.Description, &profile.IsChild, &profile.HasPIN, &profile.CanManage,
			&profile.AvatarPreset, &customAvatar, &profile.Enabled, &profile.AvailableFrom,
			&profile.AvailableUntil, &profile.AccessStartTime, &profile.AccessEndTime, &profile.AccessTimezone,
		); err != nil {
			rows.Close()
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
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("iterate profiles: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit profile list: %w", err)
	}
	return profiles, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput) (Profile, error) {
	globalAdministrator := principal.IsGlobalAdministrator()
	if !globalAdministrator && (principal.AuthorizationScope != auth.AuthorizationScopeCategory || principal.CategoryID == nil) {
		return Profile{}, ErrForbidden
	}
	input.CategoryID = strings.ToLower(strings.TrimSpace(input.CategoryID))
	if input.CategoryID == "" {
		return Profile{}, fmt.Errorf("%w: categoryId is required", ErrInvalidInput)
	}
	if !globalAdministrator && input.CategoryID != *principal.CategoryID {
		return Profile{}, ErrForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	if !validName(input.Name) {
		return Profile{}, fmt.Errorf("%w: name must contain 1 to 80 characters", ErrInvalidInput)
	}
	description, err := normalizeDescription(input.Description)
	if err != nil {
		return Profile{}, err
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
		CategoryID: input.CategoryID,
		Name:       input.Name, Description: description, IsChild: input.IsChild, HasPIN: pinHash != nil, CanManage: !globalAdministrator,
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
	if globalAdministrator {
		if err := authorizePersistedGlobalProfileOrigin(ctx, tx, principal); err != nil {
			return Profile{}, err
		}
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return Profile{}, fmt.Errorf("lock access categories for profile creation: %w", err)
	}
	if !globalAdministrator {
		var managedProfileID string
		err := tx.QueryRow(ctx, `
			SELECT profile.id::text
			FROM profiles profile
			JOIN user_profile_access access
			  ON access.profile_id = profile.id
			 AND access.user_id::text = $2
			 AND access.can_manage
			WHERE profile.category_id::text = $1
			ORDER BY profile.id
			LIMIT 1
			FOR SHARE OF profile, access
		`, input.CategoryID, principal.UserID).Scan(&managedProfileID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Profile{}, ErrForbidden
		}
		if err != nil {
			return Profile{}, fmt.Errorf("authorize profile creation: %w", err)
		}
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO profiles (
			category_id, name, description, pin_hash, is_child, enabled, available_from, available_until,
			access_start_time, access_end_time, access_timezone
		)
		SELECT category.id, $2, $3, $4, $5, $6, $7::date, $8::date, $9::time, $10::time, $11
		FROM access_categories category
		WHERE category.id::text = $1
		RETURNING id::text,
		          (SELECT name FROM access_categories WHERE id::text = $1),
		          (SELECT color FROM access_categories WHERE id::text = $1),
		          (SELECT icon FROM access_categories WHERE id::text = $1)
	`, input.CategoryID, input.Name, profile.Description, pinHash, input.IsChild, profile.Enabled, profile.AvailableFrom,
		profile.AvailableUntil, profile.AccessStartTime, profile.AccessEndTime, profile.AccessTimezone,
	).Scan(&profile.ID, &profile.CategoryName, &profile.CategoryColor, &profile.CategoryIcon); errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, fmt.Errorf("%w: categoryId does not identify an access category", ErrInvalidInput)
	} else if err != nil {
		return Profile{}, fmt.Errorf("create profile: %w", err)
	}
	if err := validateCategoryMoveResourceIntegrity(ctx, tx, profile.ID, profile.CategoryID); err != nil {
		return Profile{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		VALUES ($1, $2, $3)
	`, principal.UserID, profile.ID, !globalAdministrator); err != nil {
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
	profileID = strings.TrimSpace(profileID)
	accessChanged := input.Enabled != nil || input.AvailableFromSet || input.AvailableUntilSet ||
		input.AccessStartTimeSet || input.AccessEndTimeSet
	if profileID == "" || (!input.PINSet && input.Name == nil && input.CategoryID == nil && input.IsChild == nil && !input.DescriptionSet && !accessChanged) {
		return Profile{}, fmt.Errorf("%w: at least one field must be provided", ErrInvalidInput)
	}
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		input.Name = &trimmed
		if !validName(trimmed) {
			return Profile{}, fmt.Errorf("%w: name must contain 1 to 80 characters", ErrInvalidInput)
		}
	}
	if input.DescriptionSet {
		normalizedDescription, err := normalizeDescription(input.Description)
		if err != nil {
			return Profile{}, err
		}
		input.Description = normalizedDescription
	}
	if input.CategoryID != nil {
		trimmed := strings.ToLower(strings.TrimSpace(*input.CategoryID))
		input.CategoryID = &trimmed
		if trimmed == "" {
			return Profile{}, fmt.Errorf("%w: categoryId is required", ErrInvalidInput)
		}
		if !principal.IsGlobalAdministrator() && (principal.CategoryID == nil || trimmed != *principal.CategoryID) {
			return Profile{}, ErrNotFound
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
	if input.CategoryID != nil && principal.IsGlobalAdministrator() {
		if err := authorizePersistedGlobalProfileOrigin(ctx, tx, principal); err != nil {
			return Profile{}, err
		}
	}
	if input.CategoryID != nil {
		if _, err := tx.Exec(ctx, "LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return Profile{}, fmt.Errorf("lock access categories for profile update: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, "LOCK TABLE profiles IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return Profile{}, fmt.Errorf("lock profiles for update: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return Profile{}, err
	}
	if !authorized {
		return Profile{}, ErrNotFound
	}

	var current Profile
	var currentPINHash *string
	var currentAvatarCustom bool
	err = tx.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, category.name, category.color, category.icon,
		       p.name, p.description, p.is_child, p.pin_hash,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone
		FROM profiles p
		JOIN access_categories category ON category.id = p.category_id
		WHERE p.id::text = $1
		FOR UPDATE OF p
	`, profileID).Scan(
		&current.ID, &current.CategoryID, &current.CategoryName, &current.CategoryColor, &current.CategoryIcon,
		&current.Name, &current.Description, &current.IsChild, &currentPINHash,
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
	current.CanManage = true
	oldCategoryID := current.CategoryID
	categoryChanged := input.CategoryID != nil && *input.CategoryID != current.CategoryID
	if categoryChanged {
		err := tx.QueryRow(ctx, `
			SELECT id::text, name, color, icon
			FROM access_categories
			WHERE id::text = $1
			FOR SHARE
		`, *input.CategoryID).Scan(&current.CategoryID, &current.CategoryName, &current.CategoryColor, &current.CategoryIcon)
		if errors.Is(err, pgx.ErrNoRows) {
			return Profile{}, fmt.Errorf("%w: categoryId does not identify an access category", ErrInvalidInput)
		}
		if err != nil {
			return Profile{}, fmt.Errorf("query profile category: %w", err)
		}
		if err := validateCategoryMoveResourceIntegrity(ctx, tx, current.ID, current.CategoryID); err != nil {
			return Profile{}, err
		}
	}
	if input.Name != nil {
		current.Name = *input.Name
	}
	if input.DescriptionSet {
		current.Description = input.Description
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
		SET category_id = $2::uuid, name = $3, is_child = $4, pin_hash = $5, enabled = $6,
		    available_from = $7::date, available_until = $8::date,
		    access_start_time = $9::time, access_end_time = $10::time,
		    access_timezone = $11, description = $12, updated_at = now()
		WHERE id = $1
	`, current.ID, current.CategoryID, current.Name, current.IsChild, currentPINHash, current.Enabled,
		current.AvailableFrom, current.AvailableUntil, current.AccessStartTime, current.AccessEndTime,
		current.AccessTimezone, current.Description,
	); err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	categoryIDs := []string{oldCategoryID}
	if categoryChanged {
		categoryIDs = append(categoryIDs, current.CategoryID)
	}
	for _, categoryID := range categoryIDs {
		var unrestrictedCount int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM profiles
			WHERE category_id::text = $1
			  AND enabled
			  AND available_from IS NULL AND available_until IS NULL
			  AND access_start_time IS NULL AND access_end_time IS NULL
		`, categoryID).Scan(&unrestrictedCount); err != nil {
			return Profile{}, fmt.Errorf("count unrestricted category profiles: %w", err)
		}
		if err := ensureUnrestrictedProfile(unrestrictedCount); err != nil {
			return Profile{}, err
		}
	}
	if categoryChanged {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = COALESCE(revoked_at, now()),
			    revoked_reason = COALESCE(revoked_reason, 'profile_category_changed'),
			    active_profile_id = NULL, profile_grant_expires_at = NULL, profile_context_hash = NULL
			WHERE active_profile_id::text = $1
			  AND authorization_scope = 'category'
			  AND revoked_at IS NULL
		`, current.ID); err != nil {
			return Profile{}, fmt.Errorf("revoke changed profile category sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET active_profile_id = NULL, profile_grant_expires_at = NULL, profile_context_hash = NULL
			WHERE active_profile_id::text = $1
			  AND authorization_scope = 'global_admin'
		`, current.ID); err != nil {
			return Profile{}, fmt.Errorf("clear global profile selections after category change: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_category_audit_events (
				actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details
			)
			VALUES ($1::uuid, 'profile.category_moved', 'profile', $2::uuid, $3::uuid, $4::uuid, '{}'::jsonb)
		`, principal.UserID, current.ID, oldCategoryID, current.CategoryID); err != nil {
			return Profile{}, fmt.Errorf("audit profile category change: %w", err)
		}
	} else if accessChanged || input.PINSet {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET active_profile_id = NULL, profile_grant_expires_at = NULL, profile_context_hash = NULL
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
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return fmt.Errorf("authorize profile deletion: %w", err)
	}
	if !authorized {
		return ErrNotFound
	}
	var categoryID string
	if err := tx.QueryRow(ctx, `
		SELECT category_id::text
		FROM profiles
		WHERE id::text = $1
		FOR UPDATE
	`, profileID).Scan(&categoryID); err != nil {
		return fmt.Errorf("query profile deletion category: %w", err)
	}
	var remainingProfiles int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM profiles
		WHERE category_id::text = $2
		  AND id::text <> $1
	`, profileID, categoryID).Scan(&remainingProfiles); err != nil {
		return fmt.Errorf("count remaining category profiles: %w", err)
	}
	if remainingProfiles == 0 {
		return ErrLastProfile
	}
	if !principal.IsGlobalAdministrator() {
		var remainingManagedProfiles int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM profiles profile
			JOIN user_profile_access access
			  ON access.profile_id = profile.id
			 AND access.user_id::text = $3
			 AND access.can_manage
			WHERE profile.category_id::text = $2
			  AND profile.id::text <> $1
		`, profileID, categoryID, principal.UserID).Scan(&remainingManagedProfiles); err != nil {
			return fmt.Errorf("count remaining manageable category profiles: %w", err)
		}
		if remainingManagedProfiles == 0 {
			return ErrLastProfile
		}
	}
	var remainingUnrestricted int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM profiles
		WHERE category_id::text = $2
		  AND id::text <> $1
		  AND enabled
		  AND available_from IS NULL AND available_until IS NULL
		  AND access_start_time IS NULL AND access_end_time IS NULL
	`, profileID, categoryID).Scan(&remainingUnrestricted); err != nil {
		return fmt.Errorf("count remaining unrestricted category profiles: %w", err)
	}
	if err := ensureUnrestrictedProfile(remainingUnrestricted); err != nil {
		return err
	}
	var candidateAddonIDs, candidateCollectionIDs []string
	if err := tx.QueryRow(ctx, `
		SELECT
			ARRAY(
				SELECT id::text FROM profile_addons WHERE profile_id::text = $1
				UNION
				SELECT addon_id::text FROM addon_profile_access WHERE profile_id::text = $1
			),
			ARRAY(
				SELECT id::text FROM profile_collections WHERE profile_id::text = $1
				UNION
				SELECT collection_id::text FROM collection_profile_access WHERE profile_id::text = $1
			)
	`, profileID).Scan(&candidateAddonIDs, &candidateCollectionIDs); err != nil {
		return fmt.Errorf("query profile deletion resources: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = NULL, profile_grant_expires_at = NULL, profile_context_hash = NULL
		WHERE active_profile_id::text = $1
	`, profileID); err != nil {
		return fmt.Errorf("clear deleted profile selections: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM profiles WHERE id::text = $1", profileID); err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM profile_addons addon
		WHERE addon.id = ANY($1::uuid[])
		  AND NOT EXISTS (
			SELECT 1 FROM addon_profile_access access WHERE access.addon_id = addon.id
		  ) AND NOT EXISTS (
			SELECT 1 FROM addon_category_access access WHERE access.addon_id = addon.id
		  )
	`, candidateAddonIDs); err != nil {
		return fmt.Errorf("delete orphaned profile add-ons: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM profile_collections collection
		WHERE collection.id = ANY($1::uuid[])
		  AND NOT EXISTS (
			SELECT 1 FROM collection_profile_access access WHERE access.collection_id = collection.id
		  ) AND NOT EXISTS (
			SELECT 1 FROM collection_category_access access WHERE access.collection_id = collection.id
		  )
	`, candidateCollectionIDs); err != nil {
		return fmt.Errorf("delete orphaned profile collections: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile deletion: %w", err)
	}
	return nil
}

func (s *Service) Select(ctx context.Context, principal auth.Principal, profileID string, providedPIN *string, requireManagement bool) (Selection, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Selection{}, fmt.Errorf("begin profile selection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	profileID = strings.TrimSpace(profileID)
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		return Selection{}, fmt.Errorf("authorize profile selection: %w", err)
	}
	if !authorized {
		return Selection{}, ErrNotFound
	}
	var canManage bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT can_manage
			FROM user_profile_access
			WHERE user_id::text = $1 AND profile_id::text = $2
		), false)
	`, principal.UserID, profileID).Scan(&canManage); err != nil {
		return Selection{}, fmt.Errorf("query profile management grant: %w", err)
	}
	var deviceCategoryID string
	if principal.IsGlobalAdministrator() && principal.DeviceID != "" {
		if err := tx.QueryRow(ctx, `
			SELECT category_id::text
			FROM devices
			WHERE id::text = $1
			FOR SHARE
		`, principal.DeviceID).Scan(&deviceCategoryID); errors.Is(err, pgx.ErrNoRows) {
			return Selection{}, ErrNotFound
		} else if err != nil {
			return Selection{}, fmt.Errorf("query profile selection device category: %w", err)
		}
	}

	var selected Profile
	var pinHash *string
	var selectedAvatarCustom bool
	err = tx.QueryRow(ctx, `
		SELECT p.id::text, p.category_id::text, category.name, category.color, category.icon,
		       p.name, p.description, p.is_child, p.pin_hash,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone
		FROM profiles p
		JOIN access_categories category ON category.id = p.category_id
		WHERE p.id::text = $1
		FOR SHARE OF p
	`, profileID).Scan(
		&selected.ID, &selected.CategoryID, &selected.CategoryName, &selected.CategoryColor, &selected.CategoryIcon,
		&selected.Name, &selected.Description, &selected.IsChild, &pinHash,
		&selected.AvatarPreset, &selectedAvatarCustom, &selected.Enabled, &selected.AvailableFrom,
		&selected.AvailableUntil, &selected.AccessStartTime, &selected.AccessEndTime, &selected.AccessTimezone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Selection{}, ErrNotFound
	}
	if err != nil {
		return Selection{}, fmt.Errorf("query profile for selection: %w", err)
	}
	if deviceCategoryID != "" && selected.CategoryID != deviceCategoryID {
		return Selection{}, ErrNotFound
	}
	selected.HasPIN = pinHash != nil
	selected.CanManage = canManage
	selected.AvatarKind = "preset"
	if selectedAvatarCustom {
		selected.AvatarKind = "custom"
	}
	now := time.Now().UTC()
	selected.AccessTimezone = s.defaultTimezone
	selected.Accessible = profileAccessible(selected, now)
	if requireManagement && !selected.CanManage {
		return Selection{}, ErrManagementRequired
	}
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
	profileContext, profileContextHash, err := auth.NewProfileContext()
	if err != nil {
		return Selection{}, fmt.Errorf("issue profile context: %w", err)
	}
	command, err := tx.Exec(ctx, `
		UPDATE auth_sessions session
		SET active_profile_id = $3::uuid, profile_grant_expires_at = $4,
		    profile_context_hash = $5, last_seen_at = now()
		FROM profiles target
		WHERE session.id = $1::uuid
		  AND session.user_id = $2::uuid
		  AND session.revoked_at IS NULL
		  AND target.id = $3::uuid
		  AND (
		    session.authorization_scope = 'global_admin'
		    OR (
		      session.authorization_scope = 'category'
		      AND session.category_id = target.category_id
		      AND EXISTS (
		        SELECT 1
		        FROM user_profile_access access
		        WHERE access.user_id = session.user_id
		          AND access.profile_id = target.id
		      )
		    )
		  )
	`, principal.SessionID, principal.UserID, selected.ID, expiresAt, profileContextHash)
	if err != nil {
		return Selection{}, fmt.Errorf("activate profile selection: %w", err)
	}
	if command.RowsAffected() == 0 {
		return Selection{}, ErrForbidden
	}
	if err := tx.Commit(ctx); err != nil {
		return Selection{}, fmt.Errorf("commit profile selection: %w", err)
	}
	return Selection{Profile: selected, ExpiresAt: expiresAt, ProfileContext: profileContext}, nil
}

func (s *Service) ClearSelection(ctx context.Context, principal auth.Principal) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = NULL, profile_grant_expires_at = NULL, profile_context_hash = NULL, last_seen_at = now()
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

func normalizeDescription(description *string) (*string, error) {
	if description == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(norm.NFKC.String(*description))
	if normalized == "" {
		return nil, nil
	}
	if !utf8.ValidString(normalized) || strings.ContainsRune(normalized, '\x00') || utf8.RuneCountInString(normalized) > 500 {
		return nil, fmt.Errorf("%w: description must contain at most 500 characters", ErrInvalidInput)
	}
	return &normalized, nil
}

func validName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) >= 1 && utf8.RuneCountInString(name) <= 80
}

func principalCategoryID(principal auth.Principal) string {
	if principal.CategoryID == nil {
		return ""
	}
	return *principal.CategoryID
}

func authorizePersistedGlobalProfileOrigin(ctx context.Context, tx pgx.Tx, principal auth.Principal) error {
	if !principal.IsGlobalAdministrator() {
		return ErrForbidden
	}
	var administrator bool
	if err := tx.QueryRow(ctx, `
		SELECT role = 'admin'
		FROM users
		WHERE id = $1::uuid
		FOR SHARE
	`, principal.UserID).Scan(&administrator); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		return fmt.Errorf("authorize persisted global profile origin: %w", err)
	}
	if !administrator {
		return ErrForbidden
	}
	return nil
}

func validateCategoryMoveResourceIntegrity(ctx context.Context, tx pgx.Tx, profileID, destinationCategoryID string) error {
	var collectionLimitExceeded, duplicateAddonTransport bool
	if err := tx.QueryRow(ctx, `
		WITH effective_collections AS (
			SELECT collection_id
			FROM collection_profile_access
			WHERE profile_id = $1::uuid
			UNION
			SELECT collection_id
			FROM collection_category_access
			WHERE category_id = $2::uuid
		), effective_addons AS (
			SELECT addon_id
			FROM addon_profile_access
			WHERE profile_id = $1::uuid
			UNION
			SELECT addon_id
			FROM addon_category_access
			WHERE category_id = $2::uuid
		)
		SELECT
			(SELECT count(*) > 100 FROM effective_collections),
			EXISTS (
				SELECT 1
				FROM effective_addons access
				JOIN profile_addons installed ON installed.id = access.addon_id
				GROUP BY installed.transport_url
				HAVING count(*) > 1
			)
	`, profileID, destinationCategoryID).Scan(&collectionLimitExceeded, &duplicateAddonTransport); err != nil {
		return fmt.Errorf("validate profile category move resource access: %w", err)
	}
	if collectionLimitExceeded {
		return fmt.Errorf("%w: the profile would have more than 100 collections", ErrInvalidInput)
	}
	if duplicateAddonTransport {
		return fmt.Errorf("%w: the profile would have duplicate add-on transports", ErrInvalidInput)
	}
	return nil
}
