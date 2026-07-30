package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/password"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidToken       = errors.New("invalid token")
	ErrInvalidInput       = errors.New("invalid authentication input")
	ErrSessionNotFound    = errors.New("session not found")
	ErrForbidden          = errors.New("authentication operation forbidden")
)

const (
	accessTokenPrefix    = "rivune_at_"
	refreshTokenPrefix   = "rivune_rt_"
	maximumLoginFailures = 5
	loginLockDuration    = 15 * time.Minute
)

type Service struct {
	pool       *pgxpool.Pool
	accessTTL  time.Duration
	refreshTTL time.Duration
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
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	SessionID        string
	DeviceID         string
}

type Principal struct {
	SessionID             string
	UserID                string
	DeviceID              string
	Username              string
	Role                  string
	ActiveProfileID       *string
	ProfileGrantExpiresAt *time.Time
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
	CreatedAt             time.Time
	LastSeenAt            time.Time
	ProfileGrantExpiresAt *time.Time
	Current               bool
}

func NewService(pool *pgxpool.Pool, accessTTL, refreshTTL time.Duration) (*Service, error) {
	dummyHash, err := password.Hash("rivune-invalid-password-sentinel")
	if err != nil {
		return nil, fmt.Errorf("create password timing sentinel: %w", err)
	}
	return &Service{pool: pool, accessTTL: accessTTL, refreshTTL: refreshTTL, dummyHash: dummyHash}, nil
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

	var userID, passwordHash string
	var lockedUntil *time.Time
	var failedLoginCount int
	err = tx.QueryRow(ctx, `
		SELECT id::text, password_hash, failed_login_count, locked_until
		FROM users
		WHERE lower(username) = lower($1)
		FOR UPDATE
	`, input.Username).Scan(&userID, &passwordHash, &failedLoginCount, &lockedUntil)
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

	tokens, err := s.createSession(ctx, tx, userID, input, now)
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

	var sessionID, deviceID string
	var refreshExpiresAt time.Time
	var consumedAt, revokedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT s.id::text, s.device_id::text, s.refresh_expires_at, rt.consumed_at, s.revoked_at
		FROM auth_refresh_tokens rt
		JOIN auth_sessions s ON s.id = rt.session_id
		WHERE rt.token_hash = $1
		FOR UPDATE OF rt, s
	`, tokenDigest(refreshToken)).Scan(&sessionID, &deviceID, &refreshExpiresAt, &consumedAt, &revokedAt)
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
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     newRefreshToken,
		RefreshExpiresAt: refreshExpiresAt,
		SessionID:        sessionID,
		DeviceID:         deviceID,
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, accessToken string) (Principal, error) {
	if !strings.HasPrefix(accessToken, accessTokenPrefix) || len(accessToken) <= len(accessTokenPrefix) {
		return Principal{}, ErrInvalidToken
	}

	var principal Principal
	var lastIPAddress string
	err := s.pool.QueryRow(ctx, `
		SELECT s.id::text, s.user_id::text, s.device_id::text, u.username, u.role,
		       CASE WHEN s.profile_grant_expires_at > now() THEN s.active_profile_id::text END,
		       CASE WHEN s.profile_grant_expires_at > now() THEN s.profile_grant_expires_at END,
		       COALESCE(host(s.last_ip), '')
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.access_token_hash = $1
		  AND s.access_expires_at > now()
		  AND s.revoked_at IS NULL
	`, tokenDigest(accessToken)).Scan(
		&principal.SessionID,
		&principal.UserID,
		&principal.DeviceID,
		&principal.Username,
		&principal.Role,
		&principal.ActiveProfileID,
		&principal.ProfileGrantExpiresAt,
		&lastIPAddress,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalidToken
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate access token: %w", err)
	}
	if currentIPAddress := clientIPFromContext(ctx); currentIPAddress != "" && currentIPAddress != lastIPAddress {
		if _, err := s.pool.Exec(ctx, "UPDATE auth_sessions SET last_ip = $2::inet WHERE id = $1", principal.SessionID, currentIPAddress); err != nil {
			return Principal{}, fmt.Errorf("update session IP address: %w", err)
		}
	}
	return principal, nil
}

func (s *Service) Account(ctx context.Context, principal Principal) (Account, error) {
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
		return Account{}, fmt.Errorf("query account profiles: %w", err)
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
			return Account{}, fmt.Errorf("scan account profile: %w", err)
		}
		profile.AvatarKind = "preset"
		if customAvatar {
			profile.AvatarKind = "custom"
		}
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return Account{}, fmt.Errorf("iterate account profiles: %w", err)
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
		SELECT s.id::text, d.id::text, d.name, d.platform, s.created_at, s.last_seen_at,
		       COALESCE(host(s.last_ip), '')
		FROM auth_sessions s
		JOIN devices d ON d.id = s.device_id
		WHERE s.user_id = $1 AND s.revoked_at IS NULL AND s.refresh_expires_at > now()
		ORDER BY s.last_seen_at DESC, s.created_at DESC
	`, principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var session Session
		if err := rows.Scan(
			&session.ID, &session.DeviceID, &session.DeviceName, &session.Platform,
			&session.CreatedAt, &session.LastSeenAt, &session.IPAddress,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
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
	authorized, err := s.canManageProfile(ctx, principal, profileID)
	if err != nil {
		return nil, err
	}
	if !authorized {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id::text, s.user_id::text, u.username, d.id::text, d.name, d.platform,
		       s.created_at, s.last_seen_at, s.profile_grant_expires_at, COALESCE(host(s.last_ip), '')
		FROM auth_sessions s
		JOIN users u ON u.id = s.user_id
		JOIN devices d ON d.id = s.device_id
		WHERE s.active_profile_id::text = $1
		  AND s.profile_grant_expires_at > now()
		  AND s.revoked_at IS NULL
		  AND s.refresh_expires_at > now()
		ORDER BY s.last_seen_at DESC, s.created_at DESC
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query profile sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var session Session
		if err := rows.Scan(
			&session.ID, &session.UserID, &session.Username, &session.DeviceID, &session.DeviceName,
			&session.Platform, &session.CreatedAt, &session.LastSeenAt, &session.ProfileGrantExpiresAt,
			&session.IPAddress,
		); err != nil {
			return nil, fmt.Errorf("scan profile session: %w", err)
		}
		session.Current = session.ID == principal.SessionID
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profile sessions: %w", err)
	}
	return sessions, nil
}

func (s *Service) RevokeProfileSession(ctx context.Context, principal Principal, profileID, sessionID string) error {
	profileID = strings.TrimSpace(profileID)
	sessionID = strings.TrimSpace(sessionID)
	if profileID == "" || sessionID == "" {
		return ErrSessionNotFound
	}
	authorized, err := s.canManageProfile(ctx, principal, profileID)
	if err != nil {
		return err
	}
	if !authorized {
		return ErrForbidden
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = COALESCE(revoked_reason, 'profile_manager_revoked')
		WHERE id::text = $1
		  AND active_profile_id::text = $2
		  AND profile_grant_expires_at > now()
		  AND revoked_at IS NULL
		  AND refresh_expires_at > now()
	`, sessionID, profileID)
	if err != nil {
		return fmt.Errorf("revoke profile session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (s *Service) canManageProfile(ctx context.Context, principal Principal, profileID string) (bool, error) {
	return CanManageProfiles(ctx, s.pool, principal, []string{profileID})
}

func (s *Service) RevokeSession(ctx context.Context, principal Principal, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return ErrSessionNotFound
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()), revoked_reason = COALESCE(revoked_reason, 'user_revoked')
		WHERE id::text = $1 AND user_id = $2 AND revoked_at IS NULL
	`, sessionID, principal.UserID)
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

func upsertDevice(ctx context.Context, tx pgx.Tx, userID string, input LoginInput) (string, error) {
	if input.DeviceID == "" {
		var deviceID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO devices (user_id, name, platform, last_seen_at)
			VALUES ($1, $2, $3, now())
			RETURNING id::text
		`, userID, input.DeviceName, input.Platform).Scan(&deviceID); err != nil {
			return "", fmt.Errorf("create device: %w", err)
		}
		return deviceID, nil
	}

	var deviceID string
	err := tx.QueryRow(ctx, `
		UPDATE devices
		SET name = $3, platform = $4, last_seen_at = now(), updated_at = now()
		WHERE id::text = $1 AND user_id = $2
		RETURNING id::text
	`, input.DeviceID, userID, input.DeviceName, input.Platform).Scan(&deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: deviceId does not belong to this user", ErrInvalidInput)
	}
	if err != nil {
		return "", fmt.Errorf("update device: %w", err)
	}
	return deviceID, nil
}

func (s *Service) createSession(ctx context.Context, tx pgx.Tx, userID string, input LoginInput, now time.Time) (TokenPair, error) {
	deviceID, err := upsertDevice(ctx, tx, userID, input)
	if err != nil {
		return TokenPair{}, err
	}
	tokens, accessHash, refreshHash, err := s.issueTokens(now)
	if err != nil {
		return TokenPair{}, err
	}
	tokens.DeviceID = deviceID

	if err := tx.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, access_token_hash, access_expires_at, refresh_expires_at, last_seen_at, last_ip
		) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::inet)
		RETURNING id::text
	`, userID, deviceID, accessHash, tokens.AccessExpiresAt, tokens.RefreshExpiresAt, now, clientIPFromContext(ctx)).Scan(&tokens.SessionID); err != nil {
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
