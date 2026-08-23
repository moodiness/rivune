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

	"github.com/moodiness/rivune/server/internal/category"
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
const (
	deviceAuthorizationPurposeNative               = "native"
	deviceAuthorizationPurposeJellyfinQuickConnect = "jellyfin_quick_connect"
)

type DeviceAuthorization struct {
	DeviceCode string
	UserCode   string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Interval   time.Duration
}

type JellyfinQuickConnectInput struct {
	ClientDeviceID string
	DeviceName     string
	AppName        string
	AppVersion     string
}

type JellyfinQuickConnectStatus struct {
	Secret        string
	UserCode      string
	Authenticated bool
	CreatedAt     time.Time
	ExpiresAt     time.Time
	DeviceName    string
	AppName       string
	AppVersion    string
	DeviceID      string
}

type JellyfinQuickConnectResult struct {
	Tokens      TokenPair
	ProfileID   string
	ProfileName string
	DeviceID    string
	DeviceName  string
	AppName     string
	AppVersion  string
}

type DeviceAuthorizationApproval struct {
	UserCode     string
	CategoryID   string
	DeviceName   *string
	InternalNote *string
}

func (s *Service) BeginDeviceAuthorization(ctx context.Context, deviceName, platform string) (DeviceAuthorization, error) {
	return s.beginDeviceAuthorization(ctx, deviceAuthorizationPurposeNative, "", deviceName, platform, "")
}

func (s *Service) BeginJellyfinQuickConnect(ctx context.Context, input JellyfinQuickConnectInput) (DeviceAuthorization, error) {
	input.ClientDeviceID = strings.TrimSpace(input.ClientDeviceID)
	input.AppVersion = strings.TrimSpace(input.AppVersion)
	if input.AppVersion == "" {
		input.AppVersion = "unknown"
	}
	if !validLength(input.ClientDeviceID, 1, 128) {
		return DeviceAuthorization{}, fmt.Errorf("%w: client device ID must contain 1 to 128 characters", ErrInvalidInput)
	}
	if !validLength(input.AppVersion, 1, 32) {
		return DeviceAuthorization{}, fmt.Errorf("%w: app version must contain 1 to 32 characters", ErrInvalidInput)
	}
	return s.beginDeviceAuthorization(
		ctx, deviceAuthorizationPurposeJellyfinQuickConnect, input.ClientDeviceID, input.DeviceName, input.AppName, input.AppVersion,
	)
}

func (s *Service) beginDeviceAuthorization(ctx context.Context, purpose, clientDeviceID, deviceName, platform, appVersion string) (DeviceAuthorization, error) {
	deviceName = strings.TrimSpace(deviceName)
	platform = strings.TrimSpace(platform)
	if !validLength(deviceName, 1, 120) {
		return DeviceAuthorization{}, fmt.Errorf("%w: deviceName must contain 1 to 120 characters", ErrInvalidInput)
	}
	if !validLength(platform, 1, 32) {
		return DeviceAuthorization{}, fmt.Errorf("%w: platform must contain 1 to 32 characters", ErrInvalidInput)
	}
	if purpose != deviceAuthorizationPurposeNative && purpose != deviceAuthorizationPurposeJellyfinQuickConnect {
		return DeviceAuthorization{}, ErrInvalidInput
	}
	if purpose == deviceAuthorizationPurposeNative {
		clientDeviceID = ""
		appVersion = ""
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
				device_code_hash, user_code, device_name, platform, source_hash, expires_at,
				purpose, initiating_client_device_id, initiating_app_version
			) VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''))
			ON CONFLICT DO NOTHING
		`, deviceCodeHash, userCode, deviceName, platform, sourceHash[:], expiresAt, purpose, clientDeviceID, appVersion)
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
			CreatedAt:  now,
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

	var purpose string
	if err := s.pool.QueryRow(ctx, `
		SELECT purpose
		FROM device_authorizations
		WHERE user_code = $1 AND consumed_at IS NULL AND expires_at > now()
	`, normalized).Scan(&purpose); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidUserCode
	} else if err != nil {
		return fmt.Errorf("discover device authorization purpose: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin device authorization approval: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	location, err := time.LoadLocation(s.runtimeTimezone(ctx))
	if err != nil {
		return fmt.Errorf("load device authorization timezone: %w", err)
	}
	lockedPrincipal, authorized, err := ReloadAndLockPrincipal(ctx, tx, principal, time.Now().UTC(), location)
	if err != nil {
		return fmt.Errorf("revalidate device authorization approver: %w", err)
	}
	if !authorized {
		return ErrForbidden
	}
	principal = lockedPrincipal

	var approvedProfileID *string
	if purpose == deviceAuthorizationPurposeJellyfinQuickConnect {
		if lockedPrincipal.ActiveProfileID == nil || !lockedPrincipal.ActiveProfileCanManage {
			return ErrForbidden
		}
		var profileID, profileCategoryID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text, category_id::text
			FROM profiles
			WHERE id = $1::uuid AND enabled
		`, *lockedPrincipal.ActiveProfileID).Scan(&profileID, &profileCategoryID); errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		} else if err != nil {
			return fmt.Errorf("load quick connect profile category: %w", err)
		}
		if lockedPrincipal.ActiveProfileID == nil || profileID != *lockedPrincipal.ActiveProfileID {
			return ErrForbidden
		}
		input.CategoryID = profileCategoryID
		// Global authority may manage the profile, but a category-scoped linked
		// compatibility session still requires durable profile assignment.
		var approverGrantExists bool
		if err := tx.QueryRow(ctx, `
			SELECT true
			FROM user_profile_access
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
			FOR SHARE
		`, lockedPrincipal.UserID, profileID).Scan(&approverGrantExists); errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		} else if err != nil {
			return fmt.Errorf("lock quick connect approver profile grant: %w", err)
		}
		input.DeviceName = nil
		input.InternalNote = nil
		principal = lockedPrincipal
		approvedProfileID = &profileID
	} else if purpose == deviceAuthorizationPurposeNative {
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
	} else {
		return ErrInvalidUserCode
	}

	var rowPurpose string
	var approvedUserID, storedProfileID, approvedCategoryID, approvedDeviceName, approvedInternalNote *string
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT purpose, approved_user_id::text, approved_profile_id::text,
		       approved_category_id::text, approved_device_name,
		       approved_internal_note, expires_at, consumed_at
		FROM device_authorizations
		WHERE user_code = $1
		FOR UPDATE
	`, normalized).Scan(
		&rowPurpose, &approvedUserID, &storedProfileID, &approvedCategoryID,
		&approvedDeviceName, &approvedInternalNote, &expiresAt, &consumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidUserCode
	}
	if err != nil {
		return fmt.Errorf("query device authorization: %w", err)
	}
	if rowPurpose != purpose || consumedAt != nil {
		return ErrInvalidUserCode
	}
	lockedPrincipal, authorized, validatedAt, err := reloadAndLockPrincipal(ctx, tx, principal, location, false)
	if err != nil {
		return fmt.Errorf("finally revalidate device authorization approver: %w", err)
	}
	if !authorized {
		return ErrForbidden
	}
	if purpose == deviceAuthorizationPurposeJellyfinQuickConnect &&
		(lockedPrincipal.ActiveProfileID == nil || approvedProfileID == nil ||
			*lockedPrincipal.ActiveProfileID != *approvedProfileID || !lockedPrincipal.ActiveProfileCanManage) {
		return ErrForbidden
	}
	principal = lockedPrincipal
	approvedAt := validatedAt
	if !expiresAt.After(approvedAt) {
		return ErrInvalidUserCode
	}
	if approvedUserID != nil {
		if *approvedUserID == principal.UserID &&
			equalOptionalString(storedProfileID, approvedProfileID) &&
			equalOptionalString(approvedCategoryID, &input.CategoryID) &&
			equalOptionalString(approvedDeviceName, input.DeviceName) &&
			equalOptionalString(approvedInternalNote, input.InternalNote) {
			return nil
		}
		return ErrDeviceAuthorizationClaimed
	}
	command, err := tx.Exec(ctx, `
		UPDATE device_authorizations
		SET approved_user_id = $2, approved_profile_id = $3, approved_category_id = $4,
		    approved_device_name = $5, approved_internal_note = $6, approved_at = $7
		WHERE user_code = $1 AND consumed_at IS NULL AND expires_at > clock_timestamp()
	`, normalized, principal.UserID, approvedProfileID, input.CategoryID, input.DeviceName, input.InternalNote, approvedAt)
	if err != nil {
		return fmt.Errorf("approve device authorization: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrInvalidUserCode
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device authorization approval: %w", err)
	}
	return nil
}

func (s *Service) PollJellyfinQuickConnect(ctx context.Context, secret, clientDeviceID string) (JellyfinQuickConnectStatus, error) {
	secret = strings.TrimSpace(secret)
	clientDeviceID = strings.TrimSpace(clientDeviceID)
	if !strings.HasPrefix(secret, deviceCodePrefix) || len(secret) <= len(deviceCodePrefix) || !validLength(clientDeviceID, 1, 128) {
		return JellyfinQuickConnectStatus{}, ErrInvalidDeviceCode
	}
	var status JellyfinQuickConnectStatus
	var consumedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT user_code, device_name, platform, initiating_client_device_id, initiating_app_version,
		       approved_user_id IS NOT NULL AND approved_profile_id IS NOT NULL,
		       created_at, expires_at, consumed_at
		FROM device_authorizations
		WHERE device_code_hash = $1 AND purpose = 'jellyfin_quick_connect'
	`, tokenDigest(secret)).Scan(
		&status.UserCode, &status.DeviceName, &status.AppName, &status.DeviceID, &status.AppVersion,
		&status.Authenticated, &status.CreatedAt, &status.ExpiresAt, &consumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return JellyfinQuickConnectStatus{}, ErrInvalidDeviceCode
	}
	if err != nil {
		return JellyfinQuickConnectStatus{}, fmt.Errorf("poll Jellyfin quick connect: %w", err)
	}
	if status.DeviceID != clientDeviceID || consumedAt != nil {
		return JellyfinQuickConnectStatus{}, ErrInvalidDeviceCode
	}
	if !status.ExpiresAt.After(time.Now().UTC()) {
		return JellyfinQuickConnectStatus{}, ErrDeviceAuthorizationExpired
	}
	status.Secret = secret
	status.UserCode = formatDeviceUserCode(status.UserCode)
	return status, nil
}

func (s *Service) ExchangeJellyfinQuickConnect(ctx context.Context, secret, clientDeviceID string) (JellyfinQuickConnectResult, error) {
	secret = strings.TrimSpace(secret)
	clientDeviceID = strings.TrimSpace(clientDeviceID)
	if !strings.HasPrefix(secret, deviceCodePrefix) || len(secret) <= len(deviceCodePrefix) || !validLength(clientDeviceID, 1, 128) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("begin Jellyfin quick connect exchange: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var result JellyfinQuickConnectResult
	var approvedUserID, approvedProfileID, approvedCategoryID *string
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT device_name, platform, initiating_client_device_id, initiating_app_version,
		       approved_user_id::text, approved_profile_id::text, approved_category_id::text,
		       expires_at, consumed_at
		FROM device_authorizations
		WHERE device_code_hash = $1 AND purpose = 'jellyfin_quick_connect'
	`, tokenDigest(secret)).Scan(
		&result.DeviceName, &result.AppName, &result.DeviceID, &result.AppVersion,
		&approvedUserID, &approvedProfileID, &approvedCategoryID, &expiresAt, &consumedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("query Jellyfin quick connect secret: %w", err)
	}
	if result.DeviceID != clientDeviceID || consumedAt != nil {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	if !expiresAt.After(time.Now().UTC()) {
		return JellyfinQuickConnectResult{}, ErrDeviceAuthorizationExpired
	}
	if approvedUserID == nil || approvedProfileID == nil || approvedCategoryID == nil {
		return JellyfinQuickConnectResult{}, ErrDeviceAuthorizationPending
	}

	var lockedUserID, userRole string
	if err := tx.QueryRow(ctx, `SELECT id::text, role FROM users WHERE id = $1::uuid FOR UPDATE`, *approvedUserID).Scan(&lockedUserID, &userRole); errors.Is(err, pgx.ErrNoRows) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	} else if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("lock Jellyfin quick connect user: %w", err)
	}
	var categoryID, categoryName string
	var categoryColor, categoryIcon *string
	var access ProfileAccess
	if err := tx.QueryRow(ctx, `
		SELECT profile.id::text, profile.name, profile.category_id::text,
		       category.name, category.color, category.icon,
		       profile.enabled, profile.available_from::text, profile.available_until::text,
		       to_char(profile.access_start_time, 'HH24:MI'),
		       to_char(profile.access_end_time, 'HH24:MI')
		FROM profiles profile
		JOIN access_categories category ON category.id = profile.category_id
		WHERE profile.id = $1::uuid AND profile.category_id = $2::uuid
		FOR SHARE OF profile, category
	`, *approvedProfileID, *approvedCategoryID).Scan(
		&result.ProfileID, &result.ProfileName, &categoryID,
		&categoryName, &categoryColor, &categoryIcon,
		&access.Enabled, &access.AvailableFrom, &access.AvailableUntil,
		&access.AccessStartTime, &access.AccessEndTime,
	); errors.Is(err, pgx.ErrNoRows) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	} else if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("lock Jellyfin quick connect profile: %w", err)
	}
	var grantCanManage bool
	if err := tx.QueryRow(ctx, `
		SELECT can_manage FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
		FOR SHARE
	`, *approvedUserID, result.ProfileID).Scan(&grantCanManage); errors.Is(err, pgx.ErrNoRows) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	} else if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("lock Jellyfin quick connect profile grant: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, result.ProfileID); err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("lock Jellyfin quick connect profile session: %w", err)
	}
	access.AccessTimezone = s.runtimeTimezone(ctx)
	if (userRole != "admin" && !grantCanManage) || categoryID != *approvedCategoryID {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	var lockedAuthorizationID string
	var lockedExpiresAt time.Time
	var lockedConsumedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id::text, expires_at, consumed_at
		FROM device_authorizations
		WHERE device_code_hash = $1
		  AND purpose = 'jellyfin_quick_connect'
		  AND initiating_client_device_id = $2
		  AND approved_user_id = $3::uuid
		  AND approved_profile_id = $4::uuid
		  AND approved_category_id = $5::uuid
		FOR UPDATE
	`, tokenDigest(secret), clientDeviceID, *approvedUserID, result.ProfileID, categoryID).Scan(
		&lockedAuthorizationID, &lockedExpiresAt, &lockedConsumedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	} else if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("lock Jellyfin quick connect authorization: %w", err)
	}
	if lockedConsumedAt != nil {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	var lockedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&lockedAt); err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("read locked Jellyfin quick connect time: %w", err)
	}
	if !lockedExpiresAt.After(lockedAt) {
		return JellyfinQuickConnectResult{}, ErrDeviceAuthorizationExpired
	}
	if !ProfileAccessibleAt(access, lockedAt) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}

	deviceID, err := upsertJellyfinProfileDevice(ctx, tx, *approvedUserID, result.ProfileID, categoryID, JellyfinProfileLoginInput{
		LinkedDeviceKey: result.DeviceID, DeviceName: result.DeviceName, Platform: result.AppName,
	})
	if errors.Is(err, ErrDeviceQuotaReached) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	if err != nil {
		return JellyfinQuickConnectResult{}, err
	}
	var issuedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&issuedAt); err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("read Jellyfin quick connect issue time: %w", err)
	}
	if !lockedExpiresAt.After(issuedAt) {
		return JellyfinQuickConnectResult{}, ErrDeviceAuthorizationExpired
	}
	if !ProfileAccessibleAt(access, issuedAt) {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	_, profileContextHash, err := NewProfileContext()
	if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("issue Jellyfin quick connect profile context: %w", err)
	}
	result.Tokens, err = s.createJellyfinQuickConnectSession(ctx, tx, *approvedUserID, deviceID, result.ProfileID,
		profileContextHash, &category.CategoryRef{ID: categoryID, Name: categoryName, Color: categoryColor, Icon: categoryIcon}, issuedAt)
	if err != nil {
		return JellyfinQuickConnectResult{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE device_authorizations SET consumed_at = $2
		WHERE device_code_hash = $1 AND consumed_at IS NULL AND expires_at > clock_timestamp()
	`, tokenDigest(secret), issuedAt)
	if err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("consume Jellyfin quick connect secret: %w", err)
	}
	if command.RowsAffected() != 1 {
		return JellyfinQuickConnectResult{}, ErrInvalidDeviceCode
	}
	if err := tx.Commit(ctx); err != nil {
		return JellyfinQuickConnectResult{}, fmt.Errorf("commit Jellyfin quick connect exchange: %w", err)
	}
	return result, nil
}

func (s *Service) createJellyfinQuickConnectSession(
	ctx context.Context,
	tx pgx.Tx,
	userID, deviceID, profileID string,
	profileContextHash []byte,
	sessionCategory *category.CategoryRef,
	now time.Time,
) (TokenPair, error) {
	tokens, accessHash, refreshHash, err := s.issueTokens(now)
	if err != nil {
		return TokenPair{}, err
	}
	tokens.DeviceID = deviceID
	tokens.AuthorizationScope = AuthorizationScopeCategory
	tokens.Category = sessionCategory
	if err := tx.QueryRow(ctx, `
		INSERT INTO auth_sessions (
			user_id, device_id, authorization_scope, category_id,
			active_profile_id, profile_grant_expires_at, profile_context_hash,
			access_token_hash, access_expires_at, refresh_expires_at, last_seen_at, last_ip
		) VALUES (
			$1::uuid, $2::uuid, 'category', $3::uuid,
			$4::uuid, $5, $6,
			$7, $8, $5, $9, NULLIF($10, '')::inet
		)
		RETURNING id::text
	`, userID, deviceID, sessionCategory.ID, profileID, tokens.RefreshExpiresAt,
		profileContextHash, accessHash, tokens.AccessExpiresAt, now, ClientIP(ctx)).Scan(&tokens.SessionID); err != nil {
		return TokenPair{}, fmt.Errorf("create Jellyfin quick connect session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_refresh_tokens (token_hash, session_id, expires_at)
		VALUES ($1, $2::uuid, $3)
	`, refreshHash, tokens.SessionID, tokens.RefreshExpiresAt); err != nil {
		return TokenPair{}, fmt.Errorf("store Jellyfin quick connect refresh token: %w", err)
	}
	return tokens, nil
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

	var discoveredApprovedUserID *string
	if err := tx.QueryRow(ctx, `
		SELECT approved_user_id::text
		FROM device_authorizations
		WHERE device_code_hash = $1 AND purpose = 'native'
	`, tokenDigest(deviceCode)).Scan(&discoveredApprovedUserID); errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidDeviceCode
	} else if err != nil {
		return TokenPair{}, fmt.Errorf("discover approved device authorization user: %w", err)
	}
	if discoveredApprovedUserID != nil {
		if err := lockDeviceOwner(ctx, tx, *discoveredApprovedUserID); err != nil {
			return TokenPair{}, err
		}
	}

	var requestedDeviceName, platform string
	var approvedUserID, approvedCategoryID, approvedDeviceName, approvedInternalNote *string
	var expiresAt time.Time
	var lastPolledAt, consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT device_name, platform, approved_user_id::text, approved_category_id::text,
		       approved_device_name, approved_internal_note, expires_at, last_polled_at, consumed_at
		FROM device_authorizations
		WHERE device_code_hash = $1 AND purpose = 'native'
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
		if discoveredApprovedUserID != nil {
			return TokenPair{}, ErrInvalidDeviceCode
		}
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
	if discoveredApprovedUserID == nil {
		return TokenPair{}, ErrDeviceAuthorizationPending
	}
	if *approvedUserID != *discoveredApprovedUserID || approvedCategoryID == nil {
		return TokenPair{}, ErrInvalidDeviceCode
	}
	if err := requireAvailableDeviceSlot(ctx, tx, *approvedUserID); err != nil {
		return TokenPair{}, err
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
		ctx, tx, *approvedUserID, deviceID, AuthorizationScopeCategory, deviceCategory, now, pairedDeviceSessionExpiry(),
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
