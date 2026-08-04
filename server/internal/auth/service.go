package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/category"
	"github.com/moodiness/rivune/server/internal/password"
)

var (
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrInvalidToken         = errors.New("invalid token")
	ErrInvalidInput         = errors.New("invalid authentication input")
	ErrSessionNotFound      = errors.New("session not found")
	ErrNotificationNotFound = errors.New("notification not found")
	ErrForbidden            = errors.New("authentication operation forbidden")
)

type AuthorizationScope string

const (
	AuthorizationScopeGlobalAdministrator AuthorizationScope = "global_admin"
	AuthorizationScopeCategory            AuthorizationScope = "category"
)

const (
	accessTokenPrefix                = "rivune_at_"
	refreshTokenPrefix               = "rivune_rt_"
	maximumLoginFailures             = 5
	loginLockDuration                = 15 * time.Minute
	maximumSessionNotificationLength = 500
)

type Service struct {
	pool       *pgxpool.Pool
	accessTTL  time.Duration
	refreshTTL time.Duration
	timezone   string
	dummyHash  string
}

type LoginInput struct {
	Username   string
	Password   string
	DeviceID   string
	DeviceName string
	Platform   string
}

type TokenPair struct {
	AccessToken        string
	AccessExpiresAt    time.Time
	RefreshToken       string
	RefreshExpiresAt   time.Time
	SessionID          string
	DeviceID           string
	AuthorizationScope AuthorizationScope
	Category           *category.CategoryRef
}

type Principal struct {
	SessionID              string
	UserID                 string
	DeviceID               string
	Username               string
	Role                   string
	AuthorizationScope     AuthorizationScope
	CategoryID             *string
	Category               *category.CategoryRef
	ActiveProfileID        *string
	ProfileGrantExpiresAt  *time.Time
	ActiveProfileCanManage bool
}

func (principal Principal) IsGlobalAdministrator() bool {
	return principal.Role == "admin" && principal.AuthorizationScope == AuthorizationScopeGlobalAdministrator
}

type Profile struct {
	ID              string
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
	Category        category.CategoryRef
}

type Account struct {
	Principal Principal
	Profiles  []Profile
}

type Session struct {
	ID                    string
	UserID                string
	Username              string
	DeviceID              string
	DeviceName            string
	Platform              string
	IPAddress             string
	AuthorizationScope    AuthorizationScope
	Category              *category.CategoryRef
	CreatedAt             time.Time
	LastSeenAt            time.Time
	ProfileGrantExpiresAt *time.Time
	Current               bool
}

type SessionNotification struct {
	ID             int64
	Message        string
	SenderUsername string
	CreatedAt      time.Time
}

type NotificationBroadcast struct {
	ID             string
	Message        string
	SenderUsername string
	RecipientCount int64
	CreatedAt      time.Time
}

func NewService(pool *pgxpool.Pool, accessTTL, refreshTTL time.Duration, timezone string) (*Service, error) {
	dummyHash, err := password.Hash("rivune-invalid-password-sentinel")
	if err != nil {
		return nil, fmt.Errorf("create password timing sentinel: %w", err)
	}
	return &Service{pool: pool, accessTTL: accessTTL, refreshTTL: refreshTTL, timezone: timezone, dummyHash: dummyHash}, nil
}

func (s *Service) Login(ctx context.Context, input LoginInput) (TokenPair, error) {
	input.Username = strings.TrimSpace(input.Username)
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.DeviceName = strings.TrimSpace(input.DeviceName)
	input.Platform = strings.TrimSpace(input.Platform)
	if err := validateLoginInput(input); err != nil {
		return TokenPair{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, fmt.Errorf("begin login: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID, passwordHash, role string
	var lockedUntil *time.Time
	var failedLoginCount int
	err = tx.QueryRow(ctx, `
		SELECT id::text, password_hash, role, failed_login_count, locked_until
		FROM users
		WHERE lower(username) = lower($1)
		FOR UPDATE
	`, input.Username).Scan(&userID, &passwordHash, &role, &failedLoginCount, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = password.Verify(input.Password, s.dummyHash)
		return TokenPair{}, ErrInvalidCredentials
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("query login user: %w", err)
	}

	matches, verifyErr := password.Verify(input.Password, passwordHash)
	if verifyErr != nil {
		return TokenPair{}, fmt.Errorf("verify password: %w", verifyErr)
	}
	now := time.Now().UTC()
	if !matches || (lockedUntil != nil && lockedUntil.After(now)) {
		if lockedUntil == nil || !lockedUntil.After(now) {
			failedLoginCount++
			if failedLoginCount >= maximumLoginFailures {
				if _, err := tx.Exec(ctx, "UPDATE users SET failed_login_count = 0, locked_until = $2 WHERE id = $1", userID, now.Add(loginLockDuration)); err != nil {
					return TokenPair{}, fmt.Errorf("lock user after failed login: %w", err)
				}
			} else if _, err := tx.Exec(ctx, "UPDATE users SET failed_login_count = $2 WHERE id = $1", userID, failedLoginCount); err != nil {
				return TokenPair{}, fmt.Errorf("record failed login: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, fmt.Errorf("commit failed login: %w", err)
		}
		return TokenPair{}, ErrInvalidCredentials
	}

	if _, err := tx.Exec(ctx, "UPDATE users SET failed_login_count = 0, locked_until = NULL WHERE id = $1", userID); err != nil {
		return TokenPair{}, fmt.Errorf("clear failed login state: %w", err)
	}
	deviceID, deviceCategory, err := upsertDevice(ctx, tx, userID, role, input)
	if err != nil {
		return TokenPair{}, err
	}
	scope := AuthorizationScopeCategory
	sessionCategory := deviceCategory
	if role == "admin" {
		scope = AuthorizationScopeGlobalAdministrator
		sessionCategory = nil
	}
	tokens, err := s.createSession(ctx, tx, userID, deviceID, scope, sessionCategory, now)
	if err != nil {
		return TokenPair{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, fmt.Errorf("commit login: %w", err)
	}
	return tokens, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	if !strings.HasPrefix(refreshToken, refreshTokenPrefix) || len(refreshToken) <= len(refreshTokenPrefix) {
		return TokenPair{}, ErrInvalidToken
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, fmt.Errorf("begin token refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sessionID, deviceID, role string
	var scope AuthorizationScope
	var sessionCategoryID, categoryName, categoryColor, categoryIcon, deviceCategoryID, activeProfileCategoryID *string
	var refreshExpiresAt time.Time
	var consumedAt, revokedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT s.id::text, s.device_id::text, u.role, s.authorization_scope,
		       s.category_id::text, c.name, c.color, c.icon, d.category_id::text,
		       p.category_id::text, s.refresh_expires_at, rt.consumed_at, s.revoked_at
		FROM auth_refresh_tokens rt
		JOIN auth_sessions s ON s.id = rt.session_id
		JOIN users u ON u.id = s.user_id
		JOIN devices d ON d.id = s.device_id
		LEFT JOIN access_categories c ON c.id = s.category_id
		LEFT JOIN profiles p ON p.id = s.active_profile_id
		WHERE rt.token_hash = $1
		FOR UPDATE OF rt, s
	`, tokenDigest(refreshToken)).Scan(
		&sessionID, &deviceID, &role, &scope,
		&sessionCategoryID, &categoryName, &categoryColor, &categoryIcon, &deviceCategoryID,
		&activeProfileCategoryID, &refreshExpiresAt, &consumedAt, &revokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidToken
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("query refresh token: %w", err)
	}

	now := time.Now().UTC()
	if consumedAt != nil || revokedAt != nil || !refreshExpiresAt.After(now) {
		if consumedAt != nil && revokedAt == nil {
			if _, err := tx.Exec(ctx, "UPDATE auth_sessions SET revoked_at = $2, revoked_reason = 'refresh_token_reuse' WHERE id = $1", sessionID, now); err != nil {
				return TokenPair{}, fmt.Errorf("revoke replayed session: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return TokenPair{}, fmt.Errorf("commit replay revocation: %w", err)
			}
		}
		return TokenPair{}, ErrInvalidToken
	}
	if !validSessionScope(role, scope, sessionCategoryID, deviceCategoryID, activeProfileCategoryID) {
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = $2, revoked_reason = 'authorization_category_mismatch'
			WHERE id = $1 AND revoked_at IS NULL
		`, sessionID, now); err != nil {
			return TokenPair{}, fmt.Errorf("revoke mismatched session: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, fmt.Errorf("commit mismatch revocation: %w", err)
		}
		return TokenPair{}, ErrInvalidToken
	}

	accessToken, accessHash, err := newToken(accessTokenPrefix)
	if err != nil {
		return TokenPair{}, err
	}
	newRefreshToken, newRefreshHash, err := newToken(refreshTokenPrefix)
	if err != nil {
		return TokenPair{}, err
	}
	accessExpiresAt := now.Add(s.accessTTL)
	if accessExpiresAt.After(refreshExpiresAt) {
		accessExpiresAt = refreshExpiresAt
	}

	if _, err := tx.Exec(ctx, "UPDATE auth_refresh_tokens SET consumed_at = $2 WHERE token_hash = $1", tokenDigest(refreshToken), now); err != nil {
		return TokenPair{}, fmt.Errorf("consume refresh token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_refresh_tokens (token_hash, session_id, expires_at)
		VALUES ($1, $2, $3)
	`, newRefreshHash, sessionID, refreshExpiresAt); err != nil {
		return TokenPair{}, fmt.Errorf("store rotated refresh token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET access_token_hash = $2, access_expires_at = $3, last_seen_at = $4,
		    last_ip = COALESCE(NULLIF($5, '')::inet, last_ip)
		WHERE id = $1
	`, sessionID, accessHash, accessExpiresAt, now, clientIPFromContext(ctx)); err != nil {
		return TokenPair{}, fmt.Errorf("rotate session token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, fmt.Errorf("commit token refresh: %w", err)
	}

	return TokenPair{
		AccessToken: accessToken, AccessExpiresAt: accessExpiresAt,
		RefreshToken: newRefreshToken, RefreshExpiresAt: refreshExpiresAt,
		SessionID: sessionID, DeviceID: deviceID, AuthorizationScope: scope,
		Category: newCategoryRef(sessionCategoryID, categoryName, categoryColor, categoryIcon),
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	if !strings.HasPrefix(accessToken, accessTokenPrefix) || len(accessToken) <= len(accessTokenPrefix) {
		return Principal{}, ErrInvalidToken
	}

	var principal Principal
	var lastIPAddress string
	var categoryName, categoryColor, categoryIcon, deviceCategoryID, activeProfileCategoryID *string
	var hasActiveProfileAccess bool
	var access ProfileAccess
	err := s.pool.QueryRow(ctx, `
		SELECT s.id::text, s.user_id::text, s.device_id::text, u.username, u.role,
		       s.authorization_scope, s.category_id::text, c.name, c.color, c.icon,
		       d.category_id::text, s.active_profile_id::text, p.category_id::text,
		       s.profile_grant_expires_at,
		       COALESCE(upa.can_manage, false),
		       upa.user_id IS NOT NULL,
		       COALESCE(host(s.last_ip), ''), COALESCE(p.enabled, false),
		       p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       COALESCE(p.access_timezone, 'UTC')
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		JOIN devices d ON d.id = s.device_id
		LEFT JOIN access_categories c ON c.id = s.category_id
		LEFT JOIN profiles p ON p.id = s.active_profile_id
		LEFT JOIN user_profile_access upa
		  ON upa.user_id = s.user_id AND upa.profile_id = s.active_profile_id
		WHERE s.access_token_hash = $1
		  AND s.access_expires_at > now()
		  AND s.revoked_at IS NULL
	`, tokenDigest(accessToken)).Scan(
		&principal.SessionID, &principal.UserID, &principal.DeviceID, &principal.Username, &principal.Role,
		&principal.AuthorizationScope, &principal.CategoryID, &categoryName, &categoryColor, &categoryIcon,
		&deviceCategoryID, &principal.ActiveProfileID, &activeProfileCategoryID,
		&principal.ProfileGrantExpiresAt, &principal.ActiveProfileCanManage, &hasActiveProfileAccess,
		&lastIPAddress, &access.Enabled, &access.AvailableFrom, &access.AvailableUntil,
		&access.AccessStartTime, &access.AccessEndTime, &access.AccessTimezone,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate access token: %w", err)
	}
	if !validSessionScope(principal.Role, principal.AuthorizationScope, principal.CategoryID, deviceCategoryID, activeProfileCategoryID) {
		if _, err := s.pool.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = now(), revoked_reason = 'authorization_category_mismatch'
			WHERE id = $1 AND revoked_at IS NULL
		`, principal.SessionID); err != nil {
			return Principal{}, fmt.Errorf("revoke mismatched session: %w", err)
		}
		return Principal{}, ErrInvalidToken
	}
	if principal.AuthorizationScope == AuthorizationScopeCategory &&
		principal.ActiveProfileID != nil && !hasActiveProfileAccess {
		activeProfileID := *principal.ActiveProfileID
		if _, err := s.pool.Exec(ctx, `
			UPDATE auth_sessions
			SET active_profile_id = NULL, profile_grant_expires_at = NULL
			WHERE id = $1 AND active_profile_id::text = $2
		`, principal.SessionID, activeProfileID); err != nil {
			return Principal{}, fmt.Errorf("clear unauthorized profile grant: %w", err)
		}
		principal.ActiveProfileID = nil
		principal.ProfileGrantExpiresAt = nil
		principal.ActiveProfileCanManage = false
	}
	principal.Category = newCategoryRef(principal.CategoryID, categoryName, categoryColor, categoryIcon)
	access.AccessTimezone = s.timezone
	activeProfileID := principal.ActiveProfileID
	if reconcileProfileGrant(&principal, access, time.Now().UTC()) {
		if _, err := s.pool.Exec(ctx, `
			UPDATE auth_sessions
			SET active_profile_id = NULL, profile_grant_expires_at = NULL
			WHERE id = $1 AND active_profile_id::text = $2
		`, principal.SessionID, *activeProfileID); err != nil {
			return Principal{}, fmt.Errorf("clear unavailable profile grant: %w", err)
		}
	}
	if currentIPAddress := clientIPFromContext(ctx); currentIPAddress != "" && currentIPAddress != lastIPAddress {
		if _, err := s.pool.Exec(ctx, "UPDATE auth_sessions SET last_ip = $2::inet WHERE id = $1", principal.SessionID, currentIPAddress); err != nil {
			return Principal{}, fmt.Errorf("update session IP address: %w", err)
		}
	}
	return principal, nil
}

func (s *Service) Account(ctx context.Context, principal Principal) (Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("begin account profiles: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	activeProfileID := ""
	if principal.ActiveProfileID != nil {
		activeProfileID = *principal.ActiveProfileID
	}
	rows, err := tx.Query(ctx, `
		WITH locked_profiles AS MATERIALIZED (
			SELECT p.id, p.name, p.description, p.is_child, p.pin_hash, p.avatar_preset,
			       p.enabled, p.available_from, p.available_until,
			       p.access_start_time, p.access_end_time, p.access_timezone, p.category_id
			FROM profiles p
			WHERE ($2 AND p.category_id = (SELECT category_id FROM devices WHERE id::text = $6))
			   OR (NOT $2 AND p.category_id::text = $4)
			   OR p.id::text = $5
			ORDER BY p.id
			FOR SHARE
		), locked_access AS MATERIALIZED (
			SELECT upa.profile_id, upa.can_manage
			FROM user_profile_access upa
			JOIN locked_profiles p ON p.id = upa.profile_id
			WHERE upa.user_id = $1
			  AND ($2 OR p.category_id::text = $4)
			ORDER BY upa.profile_id
			FOR SHARE OF upa
		)
		SELECT p.id::text, p.name, p.description, p.is_child, p.pin_hash IS NOT NULL,
		       COALESCE(upa.can_manage, false) AS can_manage,
		       p.avatar_preset,
		       EXISTS (SELECT 1 FROM profile_avatar_images avatar WHERE avatar.profile_id = p.id),
		       p.enabled, p.available_from::text, p.available_until::text,
		       to_char(p.access_start_time, 'HH24:MI'), to_char(p.access_end_time, 'HH24:MI'),
		       p.access_timezone, category.id::text, category.name, category.color, category.icon
		FROM locked_profiles p
		JOIN access_categories category ON category.id = p.category_id
		LEFT JOIN locked_access upa ON upa.profile_id = p.id
		WHERE ($2 AND p.category_id = (SELECT category_id FROM devices WHERE id::text = $6))
		   OR ($3 = 'category' AND p.category_id::text = $4 AND upa.profile_id IS NOT NULL)
		ORDER BY lower(p.name), p.id
	`, principal.UserID, principal.IsGlobalAdministrator(), principal.AuthorizationScope,
		principalCategoryID(principal), activeProfileID, principal.DeviceID)
	if err != nil {
		return Account{}, fmt.Errorf("query account profiles: %w", err)
	}

	profiles := make([]Profile, 0)
	activeProfileVisible := principal.ActiveProfileID == nil
	now := time.Now().UTC()
	for rows.Next() {
		var profile Profile
		var customAvatar bool
		if err := rows.Scan(
			&profile.ID, &profile.Name, &profile.Description, &profile.IsChild, &profile.HasPIN, &profile.CanManage,
			&profile.AvatarPreset, &customAvatar, &profile.Enabled, &profile.AvailableFrom,
			&profile.AvailableUntil, &profile.AccessStartTime, &profile.AccessEndTime, &profile.AccessTimezone,
			&profile.Category.ID, &profile.Category.Name, &profile.Category.Color, &profile.Category.Icon,
		); err != nil {
			rows.Close()
			return Account{}, fmt.Errorf("scan account profile: %w", err)
		}
		profile.AccessTimezone = s.timezone
		profile.Accessible = ProfileAccessibleAt(ProfileAccess{
			Enabled: profile.Enabled, AvailableFrom: profile.AvailableFrom, AvailableUntil: profile.AvailableUntil,
			AccessStartTime: profile.AccessStartTime, AccessEndTime: profile.AccessEndTime, AccessTimezone: profile.AccessTimezone,
		}, now)
		profile.AvatarKind = "preset"
		if customAvatar {
			profile.AvatarKind = "custom"
		}
		profiles = append(profiles, profile)
		activeProfileVisible = activeProfileVisible || profile.ID == activeProfileID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Account{}, fmt.Errorf("iterate account profiles: %w", err)
	}
	rows.Close()

	if !activeProfileVisible {
		principal.ActiveProfileID = nil
		principal.ProfileGrantExpiresAt = nil
		principal.ActiveProfileCanManage = false
	}
	if err := tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("commit account profiles: %w", err)
	}
	return Account{Principal: principal, Profiles: profiles}, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()), revoked_reason = COALESCE(revoked_reason, 'logout')
		WHERE id = $1 AND user_id = $2
	`, principal.SessionID, principal.UserID)
	if err != nil {
		return fmt.Errorf("revoke current session: %w", err)
	}
	return nil
}

func (s *Service) Sessions(ctx context.Context, principal Principal) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id::text, d.id::text, d.name, d.platform, s.authorization_scope,
		       category.id::text, category.name, category.color, category.icon,
		       s.created_at, s.last_seen_at, COALESCE(host(s.last_ip), '')
		FROM auth_sessions s
		JOIN devices d ON d.id = s.device_id
		LEFT JOIN profiles active_profile ON active_profile.id = s.active_profile_id
		LEFT JOIN access_categories category ON category.id = s.category_id
		WHERE s.user_id = $1
		  AND (
		    (s.authorization_scope = 'global_admin' AND s.category_id IS NULL)
		    OR (
		      s.authorization_scope = 'category'
		      AND s.category_id = d.category_id
		      AND (s.active_profile_id IS NULL OR active_profile.category_id = s.category_id)
		    )
		  )
		  AND ($2 OR (s.authorization_scope = 'category' AND s.category_id::text = $3))
		  AND s.revoked_at IS NULL AND s.refresh_expires_at > now()
		ORDER BY s.last_seen_at DESC, s.created_at DESC
	`, principal.UserID, principal.IsGlobalAdministrator(), principalCategoryID(principal))
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var session Session
		var categoryID, categoryName, categoryColor, categoryIcon *string
		if err := rows.Scan(
			&session.ID, &session.DeviceID, &session.DeviceName, &session.Platform, &session.AuthorizationScope,
			&categoryID, &categoryName, &categoryColor, &categoryIcon,
			&session.CreatedAt, &session.LastSeenAt, &session.IPAddress,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		session.Category = newCategoryRef(categoryID, categoryName, categoryColor, categoryIcon)
		session.Current = session.ID == principal.SessionID
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

func (s *Service) ProfileSessions(ctx context.Context, principal Principal, profileID string) ([]Session, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin profile sessions: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return nil, err
	}
	if !authorized {
		return nil, ErrForbidden
	}
	rows, err := tx.Query(ctx, `
		SELECT s.id::text, s.user_id::text, u.username, d.id::text, d.name, d.platform,
		       s.authorization_scope, category.id::text, category.name, category.color, category.icon,
		       s.created_at, s.last_seen_at, s.profile_grant_expires_at, COALESCE(host(s.last_ip), '')
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		JOIN devices d ON d.id = s.device_id
		LEFT JOIN access_categories category ON category.id = s.category_id
		WHERE s.active_profile_id::text = $1
		  AND (
		    (s.authorization_scope = 'global_admin' AND s.category_id IS NULL AND u.role = 'admin')
		    OR (s.authorization_scope = 'category' AND s.category_id = d.category_id)
		  )
		  AND (
		    $2
		    OR (
		      s.authorization_scope = 'category'
		      AND s.category_id::text = $3
		      AND s.category_id = d.category_id
		    )
		  )
		  AND s.profile_grant_expires_at > now()
		  AND s.revoked_at IS NULL
		  AND s.refresh_expires_at > now()
		ORDER BY s.last_seen_at DESC, s.created_at DESC
	`, profileID, principal.IsGlobalAdministrator(), principalCategoryID(principal))
	if err != nil {
		return nil, fmt.Errorf("query profile sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var session Session
		var categoryID, categoryName, categoryColor, categoryIcon *string
		if err := rows.Scan(
			&session.ID, &session.UserID, &session.Username, &session.DeviceID, &session.DeviceName,
			&session.Platform, &session.AuthorizationScope, &categoryID, &categoryName, &categoryColor, &categoryIcon,
			&session.CreatedAt, &session.LastSeenAt, &session.ProfileGrantExpiresAt, &session.IPAddress,
		); err != nil {
			return nil, fmt.Errorf("scan profile session: %w", err)
		}
		session.Category = newCategoryRef(categoryID, categoryName, categoryColor, categoryIcon)
		session.Current = session.ID == principal.SessionID
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profile sessions: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit profile sessions: %w", err)
	}
	return sessions, nil
}

func (s *Service) SendProfileSessionNotification(ctx context.Context, principal Principal, profileID, sessionID, message string) (SessionNotification, error) {
	profileID = strings.TrimSpace(profileID)
	sessionID = strings.TrimSpace(sessionID)
	message = strings.TrimSpace(message)
	if profileID == "" || sessionID == "" || message == "" || !utf8.ValidString(message) || strings.ContainsRune(message, '\x00') || utf8.RuneCountInString(message) > maximumSessionNotificationLength {
		return SessionNotification{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SessionNotification{}, fmt.Errorf("begin profile session notification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return SessionNotification{}, err
	}
	if !authorized {
		return SessionNotification{}, ErrForbidden
	}
	var notification SessionNotification
	err = tx.QueryRow(ctx, `
		WITH expired AS (
			DELETE FROM auth_session_notifications
			WHERE expires_at <= now()
		)
		INSERT INTO auth_session_notifications (session_id, sender_user_id, message)
		SELECT session.id, $3::uuid, $4
		FROM auth_sessions session
		JOIN devices device ON device.id = session.device_id
		WHERE session.id::text = $1
		  AND session.active_profile_id::text = $2
		  AND (
		    $5
		    OR (
		      session.authorization_scope = 'category'
		      AND session.category_id::text = $6
		      AND session.category_id = device.category_id
		    )
		  )
		  AND session.profile_grant_expires_at > now()
		  AND session.revoked_at IS NULL
		  AND session.refresh_expires_at > now()
		RETURNING id, message, created_at
	`, sessionID, profileID, principal.UserID, message,
		principal.IsGlobalAdministrator(), principalCategoryID(principal)).Scan(
		&notification.ID, &notification.Message, &notification.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionNotification{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionNotification{}, fmt.Errorf("send session notification: %w", err)
	}
	notification.SenderUsername = principal.Username
	if err := tx.Commit(ctx); err != nil {
		return SessionNotification{}, fmt.Errorf("commit profile session notification: %w", err)
	}
	return notification, nil
}

func (s *Service) BroadcastSessionNotification(ctx context.Context, principal Principal, broadcastID, message string) (NotificationBroadcast, error) {
	if !principal.IsGlobalAdministrator() {
		return NotificationBroadcast{}, ErrForbidden
	}
	broadcastID = strings.TrimSpace(broadcastID)
	message = strings.TrimSpace(message)
	var parsedBroadcastID pgtype.UUID
	if err := parsedBroadcastID.Scan(broadcastID); err != nil || !parsedBroadcastID.Valid ||
		message == "" || !utf8.ValidString(message) || strings.ContainsRune(message, '\x00') || utf8.RuneCountInString(message) > maximumSessionNotificationLength {
		return NotificationBroadcast{}, ErrInvalidInput
	}
	messageSum := sha256.Sum256([]byte(message))
	messageFingerprint := hex.EncodeToString(messageSum[:])

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NotificationBroadcast{}, fmt.Errorf("begin notification broadcast: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", broadcastID); err != nil {
		return NotificationBroadcast{}, fmt.Errorf("lock notification broadcast: %w", err)
	}

	var broadcast NotificationBroadcast
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO auth_notification_broadcasts (id, sender_user_id, message, message_fingerprint)
		VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT (id) DO NOTHING
		RETURNING id::text, message, created_at, expires_at
	`, broadcastID, principal.UserID, message, messageFingerprint).Scan(
		&broadcast.ID, &broadcast.Message, &broadcast.CreatedAt, &expiresAt,
	)
	inserted := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		var senderUserID, storedFingerprint string
		var storedMessage *string
		err = tx.QueryRow(ctx, `
			SELECT broadcast.id::text, broadcast.message, broadcast.message_fingerprint, sender.username,
			       broadcast.recipient_count, broadcast.created_at, broadcast.expires_at, broadcast.sender_user_id::text
			FROM auth_notification_broadcasts broadcast
			JOIN users sender ON sender.id = broadcast.sender_user_id
			WHERE broadcast.id = $1::uuid
		`, broadcastID).Scan(
			&broadcast.ID, &storedMessage, &storedFingerprint, &broadcast.SenderUsername, &broadcast.RecipientCount,
			&broadcast.CreatedAt, &expiresAt, &senderUserID,
		)
		if err == nil {
			if senderUserID != principal.UserID || storedFingerprint != messageFingerprint || (storedMessage != nil && *storedMessage != message) {
				return NotificationBroadcast{}, ErrInvalidInput
			}
			broadcast.Message = message
		}
	}
	if err != nil {
		return NotificationBroadcast{}, fmt.Errorf("persist notification broadcast: %w", err)
	}

	if inserted {
		command, err := tx.Exec(ctx, `
			INSERT INTO auth_session_notifications (
				session_id, sender_user_id, message, broadcast_id, created_at, expires_at
			)
			SELECT session.id, $2::uuid, $3, $1::uuid, $4, $5
			FROM auth_sessions session
			WHERE session.active_profile_id IS NOT NULL
			  AND session.profile_grant_expires_at > $4
			  AND session.revoked_at IS NULL
			  AND session.refresh_expires_at > $4
			ON CONFLICT (broadcast_id, session_id) WHERE broadcast_id IS NOT NULL DO NOTHING
		`, broadcastID, principal.UserID, message, broadcast.CreatedAt, expiresAt)
		if err != nil {
			return NotificationBroadcast{}, fmt.Errorf("fan out notification broadcast: %w", err)
		}
		broadcast.RecipientCount = command.RowsAffected()
		if _, err := tx.Exec(ctx, `
			UPDATE auth_notification_broadcasts
			SET recipient_count = $2
			WHERE id = $1::uuid
		`, broadcastID, broadcast.RecipientCount); err != nil {
			return NotificationBroadcast{}, fmt.Errorf("record notification broadcast audience: %w", err)
		}
		broadcast.SenderUsername = principal.Username
	}
	if err := tx.Commit(ctx); err != nil {
		return NotificationBroadcast{}, fmt.Errorf("commit notification broadcast: %w", err)
	}
	return broadcast, nil
}

func (s *Service) SessionNotifications(ctx context.Context, principal Principal, afterID int64) ([]SessionNotification, error) {
	if afterID < 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.pool.Query(ctx, `
		SELECT notification.id, notification.message, sender.username, notification.created_at
		FROM auth_session_notifications notification
		JOIN users sender ON sender.id = notification.sender_user_id
		WHERE notification.session_id = $1::uuid
		  AND notification.id > $2
		  AND notification.acknowledged_at IS NULL
		  AND notification.expires_at > now()
		ORDER BY notification.id
		LIMIT 100
	`, principal.SessionID, afterID)
	if err != nil {
		return nil, fmt.Errorf("query session notifications: %w", err)
	}
	defer rows.Close()
	notifications := make([]SessionNotification, 0)
	for rows.Next() {
		var notification SessionNotification
		if err := rows.Scan(
			&notification.ID, &notification.Message, &notification.SenderUsername, &notification.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session notification: %w", err)
		}
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session notifications: %w", err)
	}
	return notifications, nil
}

func (s *Service) AcknowledgeSessionNotification(ctx context.Context, principal Principal, notificationID int64) error {
	if notificationID <= 0 {
		return ErrInvalidInput
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_session_notifications
		SET acknowledged_at = COALESCE(acknowledged_at, now())
		WHERE id = $1
		  AND session_id = $2::uuid
		  AND expires_at > now()
	`, notificationID, principal.SessionID)
	if err != nil {
		return fmt.Errorf("acknowledge session notification: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotificationNotFound
	}
	return nil
}

func (s *Service) RevokeProfileSession(ctx context.Context, principal Principal, profileID, sessionID string) error {
	profileID = strings.TrimSpace(profileID)
	sessionID = strings.TrimSpace(sessionID)
	if profileID == "" || sessionID == "" {
		return ErrSessionNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin profile session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorized, err := AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, true)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrForbidden
	}
	command, err := tx.Exec(ctx, `
		UPDATE auth_sessions session
		SET revoked_at = COALESCE(session.revoked_at, now()),
		    revoked_reason = COALESCE(session.revoked_reason, 'profile_manager_revoked')
		WHERE session.id::text = $1
		  AND session.active_profile_id::text = $2
		  AND (
		    $3
		    OR (
		      session.authorization_scope = 'category'
		      AND session.category_id::text = $4
		      AND EXISTS (
		        SELECT 1 FROM devices device
		        WHERE device.id = session.device_id AND device.category_id = session.category_id
		      )
		    )
		  )
		  AND session.profile_grant_expires_at > now()
		  AND session.revoked_at IS NULL
		  AND session.refresh_expires_at > now()
	`, sessionID, profileID, principal.IsGlobalAdministrator(), principalCategoryID(principal))
	if err != nil {
		return fmt.Errorf("revoke profile session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile session revocation: %w", err)
	}
	return nil
}

func (s *Service) RevokeSession(ctx context.Context, principal Principal, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions session
		SET revoked_at = COALESCE(session.revoked_at, now()),
		    revoked_reason = COALESCE(session.revoked_reason, 'user_revoked')
		WHERE session.id::text = $1
		  AND session.user_id = $2
		  AND session.revoked_at IS NULL
		  AND (
		    $3
		    OR (
		      session.authorization_scope = 'category'
		      AND session.category_id::text = $4
		      AND EXISTS (
		        SELECT 1 FROM devices device
		        WHERE device.id = session.device_id AND device.category_id = session.category_id
		      )
		    )
		  )
	`, sessionID, principal.UserID, principal.IsGlobalAdministrator(), principalCategoryID(principal))
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *Service) issueTokens(now time.Time) (TokenPair, []byte, []byte, error) {
	accessToken, accessHash, err := newToken(accessTokenPrefix)
	if err != nil {
		return TokenPair{}, nil, nil, err
	}
	refreshToken, refreshHash, err := newToken(refreshTokenPrefix)
	if err != nil {
		return TokenPair{}, nil, nil, err
	}
	return TokenPair{
		AccessToken:      accessToken,
		AccessExpiresAt:  now.Add(s.accessTTL),
		RefreshToken:     refreshToken,
		RefreshExpiresAt: now.Add(s.refreshTTL),
	}, accessHash, refreshHash, nil
}

func upsertDevice(ctx context.Context, tx pgx.Tx, userID, role string, input LoginInput) (string, *category.CategoryRef, error) {
	preferredCategoryID, err := passwordLoginCategoryID(ctx, tx, userID, role)
	if err != nil {
		return "", nil, err
	}
	var deviceID, categoryID string
	if input.DeviceID == "" {
		if err := tx.QueryRow(ctx, `
			INSERT INTO devices (user_id, name, platform, category_id, approved_at, last_seen_at)
			VALUES (
				$1, $2, $3,
				COALESCE($4::uuid, (SELECT id FROM access_categories WHERE is_default)),
				now(), now()
			)
			RETURNING id::text, category_id::text
		`, userID, input.DeviceName, input.Platform, preferredCategoryID).Scan(&deviceID, &categoryID); err != nil {
			return "", nil, fmt.Errorf("create device: %w", err)
		}
	} else {
		err := tx.QueryRow(ctx, `
			UPDATE devices
			SET name = $3,
			    platform = $4,
			    category_id = COALESCE($5::uuid, category_id),
			    last_seen_at = now(),
			    updated_at = now()
			WHERE id::text = $1 AND user_id = $2
			RETURNING id::text, category_id::text
		`, input.DeviceID, userID, input.DeviceName, input.Platform, preferredCategoryID).Scan(&deviceID, &categoryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, fmt.Errorf("%w: deviceId does not belong to this user", ErrInvalidInput)
		}
		if err != nil {
			return "", nil, fmt.Errorf("update device: %w", err)
		}
	}
	deviceCategory, err := loadCategoryRef(ctx, tx, categoryID)
	if err != nil {
		return "", nil, err
	}
	return deviceID, deviceCategory, nil
}

func passwordLoginCategoryID(ctx context.Context, tx pgx.Tx, userID, role string) (*string, error) {
	if role != "admin" {
		return nil, nil
	}
	var categoryID string
	err := tx.QueryRow(ctx, `
		SELECT profile.category_id::text
		FROM user_profile_access access
		JOIN profiles profile ON profile.id = access.profile_id
		WHERE access.user_id = $1::uuid
		ORDER BY access.can_manage DESC, profile.created_at, profile.id
		LIMIT 1
	`, userID).Scan(&categoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve administrator login category: %w", err)
	}
	return &categoryID, nil
}

func loadCategoryRef(ctx context.Context, querier profileAuthorizationQuerier, categoryID string) (*category.CategoryRef, error) {
	var reference category.CategoryRef
	if err := querier.QueryRow(ctx, `
		SELECT id::text, name, color, icon
		FROM access_categories
		WHERE id::text = $1
	`, categoryID).Scan(&reference.ID, &reference.Name, &reference.Color, &reference.Icon); err != nil {
		return nil, fmt.Errorf("query access category: %w", err)
	}
	return &reference, nil
}

func (s *Service) createSession(
	ctx context.Context,
	tx pgx.Tx,
	userID, deviceID string,
	scope AuthorizationScope,
	sessionCategory *category.CategoryRef,
	now time.Time,
) (TokenPair, error) {
	var categoryID any
	if sessionCategory != nil {
		categoryID = sessionCategory.ID
	}
	tokens, accessHash, refreshHash, err := s.issueTokens(now)
	if err != nil {
		return TokenPair{}, err
	}
	tokens.DeviceID = deviceID
	tokens.AuthorizationScope = scope
	tokens.Category = sessionCategory

	if err := tx.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, authorization_scope, category_id,
			access_token_hash, access_expires_at, refresh_expires_at, last_seen_at, last_ip
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::inet)
		RETURNING id::text
	`, userID, deviceID, scope, categoryID, accessHash, tokens.AccessExpiresAt,
		tokens.RefreshExpiresAt, now, clientIPFromContext(ctx)).Scan(&tokens.SessionID); err != nil {
		return TokenPair{}, fmt.Errorf("create session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_refresh_tokens (token_hash, session_id, expires_at)
		VALUES ($1, $2, $3)
	`, refreshHash, tokens.SessionID, tokens.RefreshExpiresAt); err != nil {
		return TokenPair{}, fmt.Errorf("store refresh token: %w", err)
	}
	return tokens, nil
}

func validSessionScope(
	role string,
	scope AuthorizationScope,
	sessionCategoryID, deviceCategoryID, activeProfileCategoryID *string,
) bool {
	switch scope {
	case AuthorizationScopeGlobalAdministrator:
		return role == "admin" && sessionCategoryID == nil
	case AuthorizationScopeCategory:
		if sessionCategoryID == nil || deviceCategoryID == nil || *sessionCategoryID != *deviceCategoryID {
			return false
		}
		return activeProfileCategoryID == nil || *sessionCategoryID == *activeProfileCategoryID
	default:
		return false
	}
}

func newCategoryRef(id, name, color, icon *string) *category.CategoryRef {
	if id == nil || name == nil {
		return nil
	}
	return &category.CategoryRef{ID: *id, Name: *name, Color: color, Icon: icon}
}

func validateLoginInput(input LoginInput) error {
	if !validLength(input.Username, 3, 64) {
		return fmt.Errorf("%w: username must contain 3 to 64 characters", ErrInvalidInput)
	}
	if len(input.Password) < 1 || len(input.Password) > 256 {
		return fmt.Errorf("%w: password must contain 1 to 256 bytes", ErrInvalidInput)
	}
	if !validLength(input.DeviceName, 1, 120) {
		return fmt.Errorf("%w: deviceName must contain 1 to 120 characters", ErrInvalidInput)
	}
	if !validLength(input.Platform, 1, 32) {
		return fmt.Errorf("%w: platform must contain 1 to 32 characters", ErrInvalidInput)
	}
	return nil
}

func validLength(value string, minimum, maximum int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= minimum && utf8.RuneCountInString(value) <= maximum
}
