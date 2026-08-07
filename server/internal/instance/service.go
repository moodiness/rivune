package instance

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/password"
)

var (
	ErrAlreadyConfigured   = errors.New("instance is already configured")
	ErrInvalidSetupToken   = errors.New("invalid setup token")
	ErrSetupUnavailable    = errors.New("setup token is not configured")
	ErrInvalidInput        = errors.New("invalid setup input")
	ErrDemoSessionCapacity = errors.New("demo session capacity reached")
)

const (
	setupLockID                int64 = 7_249_863_112
	demoSessionAdmissionLockID int64 = 7_249_863_113
	demoSessionCleanupLimit          = 128
)

type Service struct {
	pool            *pgxpool.Pool
	setupToken      string
	timezone        string
	jellyfinEnabled bool
}

type Info struct {
	PublicID      string
	Name          string
	SetupRequired bool
}

type SetupInput struct {
	InstanceName string
	Username     string
	Password     string
	ProfileName  string
}

type SetupResult struct {
	InstanceID string
	UserID     string
	ProfileID  string
}

func NewService(pool *pgxpool.Pool, setupToken, timezone string, jellyfinEnabled bool) *Service {
	return &Service{pool: pool, setupToken: setupToken, timezone: timezone, jellyfinEnabled: jellyfinEnabled}
}

func (s *Service) Info(ctx context.Context) (Info, error) {
	var publicID, name string
	err := s.pool.QueryRow(ctx, "SELECT public_id::text, name FROM instances WHERE id = 1").Scan(&publicID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Info{Name: "Rivune", SetupRequired: true}, nil
	}
	if err != nil {
		return Info{}, fmt.Errorf("query instance: %w", err)
	}
	return Info{PublicID: publicID, Name: name, SetupRequired: false}, nil
}

// AcquireSetupPending holds a process-independent shared admission lock while a
// pre-setup handler is running. Setup takes the matching exclusive transaction
// lock, so a committed setup can never overlap an admitted demo response.
func (s *Service) AcquireSetupPending(ctx context.Context) (func(), error) {
	_, release, err := s.acquireSetupPendingConnection(ctx)
	return release, err
}

func (s *Service) acquireSetupPendingConnection(ctx context.Context) (*pgxpool.Conn, func(), error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire setup admission connection: %w", err)
	}
	unlock := func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(releaseCtx, "SELECT pg_advisory_unlock_shared($1)", setupLockID).Scan(&released); err != nil || !released {
			raw := conn.Hijack()
			_ = raw.Close(context.Background())
			return
		}
		conn.Release()
	}

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock_shared($1)", setupLockID); err != nil {
		raw := conn.Hijack()
		_ = raw.Close(context.Background())
		return nil, nil, fmt.Errorf("lock setup admission: %w", err)
	}
	var configured bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		unlock()
		return nil, nil, fmt.Errorf("check setup admission state: %w", err)
	}
	if configured {
		unlock()
		return nil, nil, ErrAlreadyConfigured
	}

	var once sync.Once
	return conn, func() { once.Do(unlock) }, nil
}

// AdmitDemoSession serializes quota checks across server processes. prepare is
// called only after capacity is available, and its failure rolls back every
// ledger mutation, including replacement and expired-row cleanup.
func (s *Service) AdmitDemoSession(
	ctx context.Context,
	sourceHash [sha256.Size]byte,
	replacedAdmissionID string,
	now, expiresAt time.Time,
	globalLimit, sourceLimit int,
	prepare func() error,
) (string, func(), error) {
	if globalLimit <= 0 || sourceLimit <= 0 || !expiresAt.After(now) || prepare == nil {
		return "", nil, errors.New("invalid demo session admission")
	}
	conn, release, err := s.acquireSetupPendingConnection(ctx)
	if err != nil {
		return "", nil, err
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			release()
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("begin demo session admission: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", demoSessionAdmissionLockID); err != nil {
		return "", nil, fmt.Errorf("lock demo session admission: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM demo_session_admissions
		WHERE id IN (
			SELECT id
			FROM demo_session_admissions
			WHERE expires_at <= $1
			ORDER BY expires_at, id
			LIMIT $2
		)
	`, now, demoSessionCleanupLimit); err != nil {
		return "", nil, fmt.Errorf("clean expired demo session admissions: %w", err)
	}

	var globalCount, currentSourceCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE source_hash = $1)
		FROM demo_session_admissions
		WHERE expires_at > $2
		  AND ($3 = '' OR id <> NULLIF($3, '')::uuid)
	`, sourceHash[:], now, replacedAdmissionID).Scan(&globalCount, &currentSourceCount); err != nil {
		return "", nil, fmt.Errorf("count demo session admissions: %w", err)
	}
	if globalCount >= globalLimit || currentSourceCount >= sourceLimit {
		return "", nil, ErrDemoSessionCapacity
	}
	if err := prepare(); err != nil {
		return "", nil, fmt.Errorf("prepare demo session: %w", err)
	}

	if replacedAdmissionID != "" {
		if _, err := tx.Exec(ctx, "DELETE FROM demo_session_admissions WHERE id = $1::uuid", replacedAdmissionID); err != nil {
			return "", nil, fmt.Errorf("replace demo session admission: %w", err)
		}
	}
	var admissionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO demo_session_admissions (source_hash, created_at, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, sourceHash[:], now, expiresAt).Scan(&admissionID); err != nil {
		return "", nil, fmt.Errorf("create demo session admission: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, fmt.Errorf("commit demo session admission: %w", err)
	}
	releaseOnError = false
	return admissionID, release, nil
}

func (s *Service) ReleaseDemoSession(ctx context.Context, admissionID string) (func(), error) {
	conn, release, err := s.acquireSetupPendingConnection(ctx)
	if err != nil {
		return nil, err
	}
	releaseOnError := true
	defer func() {
		if releaseOnError {
			release()
		}
	}()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin demo session release: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", demoSessionAdmissionLockID); err != nil {
		return nil, fmt.Errorf("lock demo session release: %w", err)
	}
	if admissionID != "" {
		if _, err := tx.Exec(ctx, "DELETE FROM demo_session_admissions WHERE id = $1::uuid", admissionID); err != nil {
			return nil, fmt.Errorf("release demo session admission: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit demo session release: %w", err)
	}
	releaseOnError = false
	return release, nil
}

func (s *Service) Setup(ctx context.Context, token string, input SetupInput) (SetupResult, error) {
	if s.setupToken == "" {
		return SetupResult{}, ErrSetupUnavailable
	}
	if !tokensMatch(token, s.setupToken) {
		return SetupResult{}, ErrInvalidSetupToken
	}

	input.InstanceName = strings.TrimSpace(input.InstanceName)
	input.Username = strings.TrimSpace(input.Username)
	input.ProfileName = strings.TrimSpace(input.ProfileName)
	if err := validateInput(input); err != nil {
		return SetupResult{}, err
	}

	passwordHash, err := password.Hash(input.Password)
	if err != nil {
		return SetupResult{}, fmt.Errorf("hash administrator password: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SetupResult{}, fmt.Errorf("begin setup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", setupLockID); err != nil {
		return SetupResult{}, fmt.Errorf("lock setup: %w", err)
	}

	var configured bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM instances WHERE id = 1)").Scan(&configured); err != nil {
		return SetupResult{}, fmt.Errorf("check setup state: %w", err)
	}
	if configured {
		return SetupResult{}, ErrAlreadyConfigured
	}

	if _, err := tx.Exec(ctx, "DELETE FROM demo_session_admissions"); err != nil {
		return SetupResult{}, fmt.Errorf("clear demo session admissions: %w", err)
	}
	var result SetupResult
	if err := tx.QueryRow(ctx,
		"INSERT INTO instances (id, name) VALUES (1, $1) RETURNING public_id::text",
		input.InstanceName,
	).Scan(&result.InstanceID); err != nil {
		return SetupResult{}, fmt.Errorf("create instance: %w", err)
	}
	if err := tx.QueryRow(ctx,
		"INSERT INTO users (username, password_hash, role) VALUES ($1, $2, 'admin') RETURNING id::text",
		input.Username,
		passwordHash,
	).Scan(&result.UserID); err != nil {
		return SetupResult{}, fmt.Errorf("create administrator: %w", err)
	}
	if err := tx.QueryRow(ctx,
		"INSERT INTO profiles (name, access_timezone) VALUES ($1, $2) RETURNING id::text",
		input.ProfileName,
		s.timezone,
	).Scan(&result.ProfileID); err != nil {
		return SetupResult{}, fmt.Errorf("create profile: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO user_profile_access (user_id, profile_id, can_manage) VALUES ($1, $2, true)",
		result.UserID,
		result.ProfileID,
	); err != nil {
		return SetupResult{}, fmt.Errorf("grant profile access: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO instance_settings (instance_id, settings) VALUES (1, jsonb_build_object('jellyfinEnabled', $1::boolean))",
		s.jellyfinEnabled,
	); err != nil {
		return SetupResult{}, fmt.Errorf("create instance settings: %w", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO profile_settings (profile_id) VALUES ($1)", result.ProfileID); err != nil {
		return SetupResult{}, fmt.Errorf("create profile settings: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return SetupResult{}, fmt.Errorf("commit setup: %w", err)
	}
	return result, nil
}

func validateInput(input SetupInput) error {
	if !validLength(input.InstanceName, 1, 80) {
		return fmt.Errorf("%w: instanceName must contain 1 to 80 characters", ErrInvalidInput)
	}
	if !validLength(input.Username, 3, 64) {
		return fmt.Errorf("%w: username must contain 3 to 64 characters", ErrInvalidInput)
	}
	if len(input.Password) < 12 || len(input.Password) > 256 {
		return fmt.Errorf("%w: password must contain 12 to 256 bytes", ErrInvalidInput)
	}
	if !validLength(input.ProfileName, 1, 80) {
		return fmt.Errorf("%w: profileName must contain 1 to 80 characters", ErrInvalidInput)
	}
	return nil
}

func validLength(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func tokensMatch(provided, expected string) bool {
	providedDigest := sha256.Sum256([]byte(provided))
	expectedDigest := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedDigest[:], expectedDigest[:]) == 1
}
