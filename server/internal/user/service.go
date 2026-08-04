package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/password"
)

var (
	ErrInvalidInput     = errors.New("invalid user input")
	ErrForbidden        = errors.New("user operation forbidden")
	ErrNotFound         = errors.New("user not found")
	ErrProfileNotFound  = errors.New("profile not found")
	ErrUsernameConflict = errors.New("username already exists")
	ErrLastAdmin        = errors.New("the final administrator cannot be removed")
	ErrSelfDeletion     = errors.New("an administrator cannot delete their own account")
	ErrAccessNotFound   = errors.New("profile access not found")
)

type Service struct {
	pool *pgxpool.Pool
}

type User struct {
	ID        string
	Username  string
	Role      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateInput struct {
	Username string
	Password string
	Role     string
}

type UpdateInput struct {
	Username *string
	Password *string
	Role     *string
}

type ProfileAccess struct {
	ProfileID   string
	ProfileName string
	HasAccess   bool
	CanManage   bool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]User, error) {
	if !principal.IsGlobalAdministrator() {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, username, role, created_at, updated_at
		FROM users
		ORDER BY lower(username), id
	`)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var item User
		if err := rows.Scan(&item.ID, &item.Username, &item.Role, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput) (User, error) {
	if !principal.IsGlobalAdministrator() {
		return User{}, ErrForbidden
	}
	input.Username = strings.TrimSpace(input.Username)
	if input.Role == "" {
		input.Role = "member"
	}
	if err := validateUsername(input.Username); err != nil {
		return User{}, err
	}
	if err := validatePassword(input.Password); err != nil {
		return User{}, err
	}
	if err := validateRole(input.Role); err != nil {
		return User{}, err
	}
	hash, err := password.Hash(input.Password)
	if err != nil {
		return User{}, fmt.Errorf("hash user password: %w", err)
	}

	var created User
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id::text, username, role, created_at, updated_at
	`, input.Username, hash, input.Role).Scan(
		&created.ID, &created.Username, &created.Role, &created.CreatedAt, &created.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return User{}, ErrUsernameConflict
	}
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return created, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, userID string, input UpdateInput) (User, error) {
	if !principal.IsGlobalAdministrator() {
		return User{}, ErrForbidden
	}
	if input.Username == nil && input.Password == nil && input.Role == nil {
		return User{}, fmt.Errorf("%w: at least one field must be provided", ErrInvalidInput)
	}
	if input.Username != nil {
		trimmed := strings.TrimSpace(*input.Username)
		input.Username = &trimmed
		if err := validateUsername(trimmed); err != nil {
			return User{}, err
		}
	}
	if input.Password != nil {
		if err := validatePassword(*input.Password); err != nil {
			return User{}, err
		}
	}
	if input.Role != nil {
		if err := validateRole(*input.Role); err != nil {
			return User{}, err
		}
	}
	var passwordHash *string
	if input.Password != nil {
		hash, err := password.Hash(*input.Password)
		if err != nil {
			return User{}, fmt.Errorf("hash updated password: %w", err)
		}
		passwordHash = &hash
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin user update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return User{}, fmt.Errorf("lock users for update: %w", err)
	}

	var current User
	var currentPasswordHash string
	err = tx.QueryRow(ctx, `
		SELECT id::text, username, role, password_hash, created_at, updated_at
		FROM users WHERE id::text = $1
		FOR UPDATE
	`, strings.TrimSpace(userID)).Scan(
		&current.ID, &current.Username, &current.Role, &currentPasswordHash, &current.CreatedAt, &current.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user for update: %w", err)
	}
	if input.Role != nil && current.Role == "admin" && *input.Role != "admin" {
		if err := ensureAnotherAdmin(ctx, tx, current.ID); err != nil {
			return User{}, err
		}
	}
	if input.Username != nil {
		current.Username = *input.Username
	}
	if input.Role != nil {
		current.Role = *input.Role
	}
	if passwordHash != nil {
		currentPasswordHash = *passwordHash
	}

	err = tx.QueryRow(ctx, `
		UPDATE users
		SET username = $2, role = $3, password_hash = $4,
		    failed_login_count = CASE WHEN $5 THEN 0 ELSE failed_login_count END,
		    locked_until = CASE WHEN $5 THEN NULL ELSE locked_until END,
		    updated_at = now()
		WHERE id = $1
		RETURNING updated_at
	`, current.ID, current.Username, current.Role, currentPasswordHash, passwordHash != nil).Scan(&current.UpdatedAt)
	if isUniqueViolation(err) {
		return User{}, ErrUsernameConflict
	}
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	if input.Password != nil || input.Role != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = COALESCE(revoked_at, now()), revoked_reason = COALESCE(revoked_reason, 'account_updated')
			WHERE user_id = $1 AND revoked_at IS NULL
		`, current.ID); err != nil {
			return User{}, fmt.Errorf("revoke updated user sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit user update: %w", err)
	}
	return current, nil
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, userID string) error {
	if !principal.IsGlobalAdministrator() {
		return ErrForbidden
	}
	userID = strings.TrimSpace(userID)
	if userID == principal.UserID {
		return ErrSelfDeletion
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin user deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		return fmt.Errorf("lock users for deletion: %w", err)
	}

	var role string
	err = tx.QueryRow(ctx, "SELECT role FROM users WHERE id::text = $1 FOR UPDATE", userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("query user for deletion: %w", err)
	}
	if role == "admin" {
		if err := ensureAnotherAdmin(ctx, tx, userID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM devices WHERE user_id::text = $1", userID); err != nil {
		return fmt.Errorf("delete user devices: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM users WHERE id::text = $1", userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit user deletion: %w", err)
	}
	return nil
}

func (s *Service) ProfileAccess(ctx context.Context, principal auth.Principal, userID string) ([]ProfileAccess, error) {
	userID = strings.TrimSpace(userID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin profile access list: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1)", userID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("query user for profile access: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	rows, err := tx.Query(ctx, `
		/* ProfileAccess: lock profiles before grants */
		SELECT p.id::text
		FROM profiles p
		WHERE $1
		   OR ($3 = 'category' AND p.category_id::text = $2)
		ORDER BY p.id
		FOR SHARE
	`, principal.IsGlobalAdministrator(), principalCategoryID(principal), principal.AuthorizationScope)
	if err != nil {
		return nil, fmt.Errorf("lock profiles for profile access: %w", err)
	}
	profileIDs := make([]string, 0)
	for rows.Next() {
		var profileID string
		if err := rows.Scan(&profileID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan locked profile for profile access: %w", err)
		}
		profileIDs = append(profileIDs, profileID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate locked profiles for profile access: %w", err)
	}
	rows.Close()

	authorizedProfileIDs := profileIDs
	if !principal.IsGlobalAdministrator() {
		authorizedProfileIDs = make([]string, 0)
		if principal.AuthorizationScope == auth.AuthorizationScopeCategory && len(profileIDs) > 0 {
			rows, err = tx.Query(ctx, `
				/* ProfileAccess: lock actor management grants after profiles */
				SELECT upa.profile_id::text
				FROM user_profile_access upa
				WHERE upa.user_id::text = $1
				  AND upa.profile_id = ANY($2::uuid[])
				  AND upa.can_manage
				ORDER BY upa.profile_id
				FOR SHARE
			`, principal.UserID, profileIDs)
			if err != nil {
				return nil, fmt.Errorf("lock management grants for profile access: %w", err)
			}
			for rows.Next() {
				var profileID string
				if err := rows.Scan(&profileID); err != nil {
					rows.Close()
					return nil, fmt.Errorf("scan locked management grant for profile access: %w", err)
				}
				authorizedProfileIDs = append(authorizedProfileIDs, profileID)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, fmt.Errorf("iterate locked management grants for profile access: %w", err)
			}
			rows.Close()
		}
	}

	access := make([]ProfileAccess, 0, len(authorizedProfileIDs))
	if len(authorizedProfileIDs) > 0 {
		rows, err = tx.Query(ctx, `
			SELECT p.id::text, p.name, target_access.user_id IS NOT NULL, COALESCE(target_access.can_manage, false)
			FROM profiles p
			LEFT JOIN user_profile_access target_access
			  ON target_access.profile_id = p.id AND target_access.user_id::text = $1
			WHERE p.id = ANY($2::uuid[])
			ORDER BY lower(p.name), p.id
		`, userID, authorizedProfileIDs)
		if err != nil {
			return nil, fmt.Errorf("query profile access: %w", err)
		}
		for rows.Next() {
			var item ProfileAccess
			if err := rows.Scan(&item.ProfileID, &item.ProfileName, &item.HasAccess, &item.CanManage); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan profile access: %w", err)
			}
			access = append(access, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate profile access: %w", err)
		}
		rows.Close()
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit profile access list: %w", err)
	}
	return access, nil
}

func (s *Service) GrantProfileAccess(ctx context.Context, principal auth.Principal, userID, profileID string, canManage bool) (ProfileAccess, error) {
	userID = strings.TrimSpace(userID)
	profileID = strings.TrimSpace(profileID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProfileAccess{}, fmt.Errorf("begin profile access grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return ProfileAccess{}, fmt.Errorf("authorize profile access grant: %w", err)
	}
	if !authorized {
		return ProfileAccess{}, ErrProfileNotFound
	}

	var userExists bool
	var profileName string
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1)", userID).Scan(&userExists); err != nil {
		return ProfileAccess{}, fmt.Errorf("query profile access user: %w", err)
	}
	if !userExists {
		return ProfileAccess{}, ErrNotFound
	}
	if err := tx.QueryRow(ctx, "SELECT name FROM profiles WHERE id::text = $1", profileID).Scan(&profileName); errors.Is(err, pgx.ErrNoRows) {
		return ProfileAccess{}, ErrProfileNotFound
	} else if err != nil {
		return ProfileAccess{}, fmt.Errorf("query profile for access: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_profile_access (user_id, profile_id, can_manage)
		SELECT u.id, p.id, $3
		FROM users u, profiles p
		WHERE u.id::text = $1 AND p.id::text = $2
		ON CONFLICT (user_id, profile_id)
		DO UPDATE SET can_manage = EXCLUDED.can_manage
	`, userID, profileID, canManage); err != nil {
		return ProfileAccess{}, fmt.Errorf("grant profile access: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProfileAccess{}, fmt.Errorf("commit profile access grant: %w", err)
	}
	return ProfileAccess{ProfileID: profileID, ProfileName: profileName, HasAccess: true, CanManage: canManage}, nil
}

func (s *Service) RevokeProfileAccess(ctx context.Context, principal auth.Principal, userID, profileID string) error {
	userID = strings.TrimSpace(userID)
	profileID = strings.TrimSpace(profileID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin profile access revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return fmt.Errorf("authorize profile access revocation: %w", err)
	}
	if !authorized {
		return ErrAccessNotFound
	}

	command, err := tx.Exec(ctx, `
		DELETE FROM user_profile_access
		WHERE user_id::text = $1 AND profile_id::text = $2
	`, userID, profileID)
	if err != nil {
		return fmt.Errorf("revoke profile access: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrAccessNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = NULL, profile_grant_expires_at = NULL
		WHERE user_id::text = $1 AND active_profile_id::text = $2
	`, userID, profileID); err != nil {
		return fmt.Errorf("clear revoked profile selections: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile access revocation: %w", err)
	}
	return nil
}

func ensureAnotherAdmin(ctx context.Context, tx pgx.Tx, excludedUserID string) error {
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role = 'admin' AND id::text <> $1", excludedUserID).Scan(&count); err != nil {
		return fmt.Errorf("count remaining administrators: %w", err)
	}
	if count == 0 {
		return ErrLastAdmin
	}
	return nil
}

func validateUsername(value string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 3 || utf8.RuneCountInString(value) > 64 {
		return fmt.Errorf("%w: username must contain 3 to 64 characters", ErrInvalidInput)
	}
	return nil
}

func validatePassword(value string) error {
	if len(value) < 12 || len(value) > 256 {
		return fmt.Errorf("%w: password must contain 12 to 256 bytes", ErrInvalidInput)
	}
	return nil
}

func validateRole(value string) error {
	if value != "admin" && value != "member" {
		return fmt.Errorf("%w: role must be admin or member", ErrInvalidInput)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func principalCategoryID(principal auth.Principal) string {
	if principal.CategoryID == nil {
		return ""
	}
	return *principal.CategoryID
}
