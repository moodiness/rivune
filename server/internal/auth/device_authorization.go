package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidDeviceCode           = errors.New("invalid device code")
	ErrInvalidUserCode             = errors.New("invalid user code")
	ErrDeviceAuthorizationPending  = errors.New("device authorization pending")
	ErrDeviceAuthorizationSlowDown = errors.New("device authorization polling too quickly")
	ErrDeviceAuthorizationExpired  = errors.New("device authorization expired")
	ErrDeviceAuthorizationClaimed  = errors.New("device authorization already claimed")
)

const (
	deviceCodePrefix             = "rivune_dc_"
	deviceAuthorizationTTL       = 10 * time.Minute
	deviceAuthorizationInterval  = 5 * time.Second
	deviceUserCodeLength         = 8
	deviceUserCodeAlphabet       = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	deviceUserCodeInsertAttempts = 5
)

type DeviceAuthorization struct {
	DeviceCode string
	UserCode   string
	ExpiresAt  time.Time
	Interval   time.Duration
}
type DeviceAuthorizationApproval struct {
	UserCode     string
	CategoryID   string
	DeviceName   *string
	InternalNote *string
}

func (s *Service) BeginDeviceAuthorization(ctx context.Context, deviceName, platform string) (DeviceAuthorization, error) {
	deviceName = strings.TrimSpace(deviceName)
	platform = strings.TrimSpace(platform)
	if !validLength(deviceName, 1, 120) {
		return DeviceAuthorization{}, fmt.Errorf("%w: deviceName must contain 1 to 120 characters", ErrInvalidInput)
	}
	if !validLength(platform, 1, 32) {
		return DeviceAuthorization{}, fmt.Errorf("%w: platform must contain 1 to 32 characters", ErrInvalidInput)
	}

	deviceCode, deviceCodeHash, err := newToken(deviceCodePrefix)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(deviceAuthorizationTTL)
	if _, err := s.pool.Exec(ctx, cleanupStaleDeviceAuthorizationsSQL); err != nil {
		return DeviceAuthorization{}, fmt.Errorf("clean device authorizations: %w", err)
	}

	for range deviceUserCodeInsertAttempts {
		userCode, err := newDeviceUserCode()
		if err != nil {
			return DeviceAuthorization{}, err
		}
		_, err = s.pool.Exec(ctx, `
			INSERT INTO device_authorizations (
				device_code_hash, user_code, device_name, platform, expires_at
			) VALUES ($1, $2, $3, $4, $5)
		`, deviceCodeHash, userCode, deviceName, platform, expiresAt)
		if err == nil {
			return DeviceAuthorization{
				DeviceCode: deviceCode,
				UserCode:   formatDeviceUserCode(userCode),
				ExpiresAt:  expiresAt,
				Interval:   deviceAuthorizationInterval,
			}, nil
		}
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
			return DeviceAuthorization{}, fmt.Errorf("create device authorization: %w", err)
		}
	}
	return DeviceAuthorization{}, errors.New("could not allocate a unique device user code")
}

func (s *Service) ApproveDeviceAuthorization(ctx context.Context, principal Principal, input DeviceAuthorizationApproval) error {
	normalized := normalizeDeviceUserCode(input.UserCode)
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	if len(normalized) != deviceUserCodeLength {
		return ErrInvalidUserCode
	}
	if input.CategoryID == "" {
		return fmt.Errorf("%w: categoryId is required", ErrInvalidInput)
	}
	if input.DeviceName != nil {
		name := strings.TrimSpace(*input.DeviceName)
		if !validLength(name, 1, 120) {
			return fmt.Errorf("%w: deviceName must contain 1 to 120 characters", ErrInvalidInput)
		}
		input.DeviceName = &name
	}
	if input.InternalNote != nil {
		note := strings.TrimSpace(*input.InternalNote)
		if !utf8.ValidString(note) || strings.ContainsRune(note, '\x00') || utf8.RuneCountInString(note) > 500 {
			return fmt.Errorf("%w: internalNote must contain at most 500 characters", ErrInvalidInput)
		}
		if note == "" {
			input.InternalNote = nil
		} else {
			input.InternalNote = &note
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin device authorization approval: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if principal.IsGlobalAdministrator() {
		if _, err := loadCategoryRef(ctx, tx, input.CategoryID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			return err
		}
	} else {
		if principal.AuthorizationScope != AuthorizationScopeCategory ||
			principal.CategoryID == nil || *principal.CategoryID != input.CategoryID {
			return ErrForbidden
		}
		var lockedProfileID string
		if err := tx.QueryRow(ctx, `
			SELECT profile.id::text
			FROM profiles profile
			JOIN user_profile_access access ON access.profile_id = profile.id
			WHERE profile.category_id::text = $1
			  AND access.user_id::text = $2
			  AND access.can_manage
			ORDER BY profile.id
			LIMIT 1
			FOR SHARE OF profile, access
		`, input.CategoryID, principal.UserID).Scan(&lockedProfileID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrForbidden
			}
			return fmt.Errorf("authorize category device approval: %w", err)
		}
	}

	var approvedUserID, approvedCategoryID, approvedDeviceName, approvedInternalNote *string
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT approved_user_id::text, approved_category_id::text, approved_device_name,
		       approved_internal_note, expires_at, consumed_at
		FROM device_authorizations
		WHERE user_code = $1
		FOR UPDATE
	`, normalized).Scan(
		&approvedUserID, &approvedCategoryID, &approvedDeviceName, &approvedInternalNote,
		&expiresAt, &consumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidUserCode
	}
	if err != nil {
		return fmt.Errorf("query device authorization: %w", err)
	}
	if consumedAt != nil || !expiresAt.After(time.Now().UTC()) {
		return ErrInvalidUserCode
	}
	if approvedUserID != nil {
		if *approvedUserID == principal.UserID &&
			equalOptionalString(approvedCategoryID, &input.CategoryID) &&
			equalOptionalString(approvedDeviceName, input.DeviceName) &&
			equalOptionalString(approvedInternalNote, input.InternalNote) {
			return nil
		}
		return ErrDeviceAuthorizationClaimed
	}
	if _, err := tx.Exec(ctx, `
		UPDATE device_authorizations
		SET approved_user_id = $2, approved_category_id = $3,
		    approved_device_name = $4, approved_internal_note = $5, approved_at = now()
		WHERE user_code = $1
	`, normalized, principal.UserID, input.CategoryID, input.DeviceName, input.InternalNote); err != nil {
		return fmt.Errorf("approve device authorization: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device authorization approval: %w", err)
	}
	return nil
}

func (s *Service) ExchangeDeviceAuthorization(ctx context.Context, deviceCode string) (TokenPair, error) {
	deviceCode = strings.TrimSpace(deviceCode)
	if !strings.HasPrefix(deviceCode, deviceCodePrefix) || len(deviceCode) <= len(deviceCodePrefix) {
		return TokenPair{}, ErrInvalidDeviceCode
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, fmt.Errorf("begin device authorization exchange: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var requestedDeviceName, platform string
	var approvedUserID, approvedCategoryID, approvedDeviceName, approvedInternalNote *string
	var expiresAt time.Time
	var lastPolledAt, consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT device_name, platform, approved_user_id::text, approved_category_id::text,
		       approved_device_name, approved_internal_note, expires_at, last_polled_at, consumed_at
		FROM device_authorizations
		WHERE device_code_hash = $1
		FOR UPDATE
	`, tokenDigest(deviceCode)).Scan(
		&requestedDeviceName, &platform, &approvedUserID, &approvedCategoryID,
		&approvedDeviceName, &approvedInternalNote, &expiresAt, &lastPolledAt, &consumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidDeviceCode
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("query device code: %w", err)
	}
	now := time.Now().UTC()
	if consumedAt != nil {
		return TokenPair{}, ErrInvalidDeviceCode
	}
	if !expiresAt.After(now) {
		return TokenPair{}, ErrDeviceAuthorizationExpired
	}
	if approvedUserID == nil {
		if lastPolledAt != nil && now.Sub(*lastPolledAt) < deviceAuthorizationInterval {
			return TokenPair{}, ErrDeviceAuthorizationSlowDown
		}
		if _, err := tx.Exec(ctx, "UPDATE device_authorizations SET last_polled_at = $2 WHERE device_code_hash = $1", tokenDigest(deviceCode), now); err != nil {
			return TokenPair{}, fmt.Errorf("record device authorization poll: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, fmt.Errorf("commit device authorization poll: %w", err)
		}
		return TokenPair{}, ErrDeviceAuthorizationPending
	}
	if approvedCategoryID == nil {
		return TokenPair{}, ErrInvalidDeviceCode
	}

	deviceName := requestedDeviceName
	if approvedDeviceName != nil {
		deviceName = *approvedDeviceName
	}
	var deviceID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO devices (
			user_id, name, platform, category_id, approved_at, internal_note, last_seen_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $5)
		RETURNING id::text
	`, *approvedUserID, deviceName, platform, *approvedCategoryID, now, approvedInternalNote).Scan(&deviceID); err != nil {
		return TokenPair{}, fmt.Errorf("create approved device: %w", err)
	}
	deviceCategory, err := loadCategoryRef(ctx, tx, *approvedCategoryID)
	if err != nil {
		return TokenPair{}, err
	}
	tokens, err := s.createSession(
		ctx, tx, *approvedUserID, deviceID, AuthorizationScopeCategory, deviceCategory, now,
	)
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE device_authorizations
		SET consumed_at = $2
		WHERE device_code_hash = $1
	`, tokenDigest(deviceCode), now); err != nil {
		return TokenPair{}, fmt.Errorf("consume device authorization: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, fmt.Errorf("commit device authorization exchange: %w", err)
	}
	return tokens, nil
}

func newDeviceUserCode() (string, error) {
	entropy := make([]byte, deviceUserCodeLength)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("generate device user code: %w", err)
	}
	code := make([]byte, deviceUserCodeLength)
	for index, value := range entropy {
		code[index] = deviceUserCodeAlphabet[int(value)%len(deviceUserCodeAlphabet)]
	}
	return string(code), nil
}

func normalizeDeviceUserCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func formatDeviceUserCode(value string) string {
	return value[:4] + "-" + value[4:]
}

func equalOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
