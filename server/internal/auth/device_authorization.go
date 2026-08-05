package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidDeviceCode           = errors.New("invalid device code")
	ErrInvalidUserCode             = errors.New("invalid user code")
	ErrDeviceAuthorizationPending  = errors.New("device authorization pending")
	ErrDeviceAuthorizationSlowDown = errors.New("device authorization polling too quickly")
	ErrDeviceAuthorizationExpired  = errors.New("device authorization expired")
	ErrDeviceAuthorizationClaimed  = errors.New("device authorization already claimed")
	ErrDeviceAuthorizationCapacity = errors.New("device authorization capacity reached")
)

const (
	deviceCodePrefix                                  = "rivune_dc_"
	deviceAuthorizationTTL                            = 10 * time.Minute
	deviceAuthorizationInterval                       = 5 * time.Second
	deviceUserCodeLength                              = 8
	deviceUserCodeAlphabet                            = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	deviceUserCodeInsertAttempts                      = 5
	maximumOutstandingDeviceAuthorizations            = 10_000
	maximumOutstandingDeviceAuthorizationsPerSource   = 4
	deviceAuthorizationProtectedReservePercent        = 10
	deviceAuthorizationCleanupBatch                   = 500
	deviceAuthorizationAdmissionLockID                = int64(7_249_863_113)
	deviceAuthorizationSourceHashDomain               = "rivune/device-authorization/source/v1\x00"
	deviceAuthorizationSourceHashMissingAddressMarker = byte(0)
	deviceAuthorizationSourceHashIPv4Marker           = byte(4)
	deviceAuthorizationSourceHashIPv6Marker           = byte(6)
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
	sourceHash := deviceAuthorizationSourceHash(ClientIP(ctx))

	deviceCode, deviceCodeHash, err := newToken(deviceCodePrefix)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(deviceAuthorizationTTL)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("begin device authorization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", deviceAuthorizationAdmissionLockID); err != nil {
		return DeviceAuthorization{}, fmt.Errorf("lock device authorization admission: %w", err)
	}
	if _, err := tx.Exec(ctx, cleanupStaleDeviceAuthorizationsSQL, deviceAuthorizationCleanupBatch); err != nil {
		return DeviceAuthorization{}, fmt.Errorf("clean device authorizations: %w", err)
	}
	var outstanding, sourceOutstanding int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE source_hash = $1)
		FROM device_authorizations
		WHERE consumed_at IS NULL AND expires_at > now()
	`, sourceHash[:]).Scan(&outstanding, &sourceOutstanding); err != nil {
		return DeviceAuthorization{}, fmt.Errorf("count outstanding device authorizations: %w", err)
	}
	capacity := s.deviceAuthorizationCapacity
	if capacity <= 0 {
		capacity = maximumOutstandingDeviceAuthorizations
	}
	sourceCapacity := s.deviceAuthorizationSourceCapacity
	if sourceCapacity <= 0 {
		sourceCapacity = maximumOutstandingDeviceAuthorizationsPerSource
	}
	generalCapacity := deviceAuthorizationGeneralCapacity(capacity)
	if outstanding >= capacity ||
		sourceOutstanding >= sourceCapacity ||
		(outstanding >= generalCapacity && sourceOutstanding > 0) {
		return DeviceAuthorization{}, ErrDeviceAuthorizationCapacity
	}

	for range deviceUserCodeInsertAttempts {
		userCode, err := newDeviceUserCode()
		if err != nil {
			return DeviceAuthorization{}, err
		}
		command, err := tx.Exec(ctx, `
			INSERT INTO device_authorizations (
				device_code_hash, user_code, device_name, platform, source_hash, expires_at
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT DO NOTHING
		`, deviceCodeHash, userCode, deviceName, platform, sourceHash[:], expiresAt)
		if err != nil {
			return DeviceAuthorization{}, fmt.Errorf("create device authorization: %w", err)
		}
		if command.RowsAffected() == 0 {
			continue
		}
		if err := tx.Commit(ctx); err != nil {
			return DeviceAuthorization{}, fmt.Errorf("commit device authorization: %w", err)
		}
		return DeviceAuthorization{
			DeviceCode: deviceCode,
			UserCode:   formatDeviceUserCode(userCode),
			ExpiresAt:  expiresAt,
			Interval:   deviceAuthorizationInterval,
		}, nil
	}
	return DeviceAuthorization{}, errors.New("could not allocate a unique device user code")
}

// deviceAuthorizationGeneralCapacity preserves a small part of the hard cap
// for the first active code from a previously inactive network source. Once
// the general capacity is full, a source cannot consume a second reserve slot.
//
// Network-source quotas are not a complete Sybil defense. Distributed abuse
// still requires infrastructure-level controls such as edge rate limiting and
// reputation in addition to this database-enforced bound.
func deviceAuthorizationGeneralCapacity(hardCapacity int) int {
	if hardCapacity <= 0 {
		return 0
	}
	reserve := (hardCapacity*deviceAuthorizationProtectedReservePercent + 99) / 100
	if reserve < 1 {
		reserve = 1
	}
	return hardCapacity - reserve
}

func deviceAuthorizationSourceHash(source string) [sha256.Size]byte {
	var material [len(deviceAuthorizationSourceHashDomain) + 1 + 16]byte
	length := copy(material[:], deviceAuthorizationSourceHashDomain)

	address, err := netip.ParseAddr(source)
	if err != nil {
		material[length] = deviceAuthorizationSourceHashMissingAddressMarker
		return sha256.Sum256(material[:length+1])
	}
	address = address.Unmap()
	if address.Is4() {
		material[length] = deviceAuthorizationSourceHashIPv4Marker
		length++
		bytes := address.As4()
		length += copy(material[length:], bytes[:])
		return sha256.Sum256(material[:length])
	}

	material[length] = deviceAuthorizationSourceHashIPv6Marker
	length++
	bytes := address.As16()
	length += copy(material[length:], bytes[:8])
	return sha256.Sum256(material[:length])
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
	if err := reserveDeviceSlot(ctx, tx, *approvedUserID); err != nil {
		return TokenPair{}, err
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
