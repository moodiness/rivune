package instance

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/password"
)

var (
	ErrAlreadyConfigured = errors.New("instance is already configured")
	ErrInvalidSetupToken = errors.New("invalid setup token")
	ErrSetupUnavailable  = errors.New("setup token is not configured")
	ErrInvalidInput      = errors.New("invalid setup input")
)

const setupLockID int64 = 7_249_863_112

type Service struct {
	pool       *pgxpool.Pool
	setupToken string
	timezone   string
}

type Info struct {
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

func NewService(pool *pgxpool.Pool, setupToken, timezone string) *Service {
	return &Service{pool: pool, setupToken: setupToken, timezone: timezone}
}

func (s *Service) Info(ctx context.Context) (Info, error) {
	var name string
	err := s.pool.QueryRow(ctx, "SELECT name FROM instances WHERE id = 1").Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Info{Name: "Rivune", SetupRequired: true}, nil
	}
	if err != nil {
		return Info{}, fmt.Errorf("query instance: %w", err)
	}
	return Info{Name: name, SetupRequired: false}, nil
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
	if _, err := tx.Exec(ctx, "INSERT INTO instance_settings (instance_id) VALUES (1)"); err != nil {
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
