package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/runtimesettings"
)

var (
	ErrCredentialNotFound  = errors.New("Jellyfin profile credential not found")
	ErrCredentialExists    = errors.New("Jellyfin profile credential already exists")
	ErrCredentialForbidden = errors.New("Jellyfin profile credential forbidden")
)

// CredentialStatus is the non-secret, durable state of a profile credential.
// Username is empty only when the profile has never had a credential.
type CredentialStatus struct {
	Username   string
	Active     bool
	CanIssue   bool
	Generation int64
	CreatedAt  time.Time
	RotatedAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// ProfileCredential contains the one-shot password returned by Create and
// Rotate. Password is never loaded from or persisted to the database.
type ProfileCredential struct {
	CredentialStatus
	Password string
}

type CredentialStore struct {
	pool            *pgxpool.Pool
	runtimeSettings *runtimesettings.Source
	notifierMu      sync.RWMutex
	notifier        func([]string)
}

func NewCredentialStore(pool *pgxpool.Pool, runtimeSettings *runtimesettings.Source) (*CredentialStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("Jellyfin credential store database is required")
	}
	if runtimeSettings == nil || runtimeSettings.Load().Location == nil {
		return nil, fmt.Errorf("Jellyfin credential runtime settings are required")
	}
	return &CredentialStore{pool: pool, runtimeSettings: runtimeSettings}, nil
}

// SetCompatibilitySessionRevocationNotifier installs the best-effort callback
// invoked after a committed credential mutation revokes compatibility sessions.
func (s *CredentialStore) SetCompatibilitySessionRevocationNotifier(notifier func([]string)) {
	if s == nil {
		return
	}
	s.notifierMu.Lock()
	s.notifier = notifier
	s.notifierMu.Unlock()
}

func (s *CredentialStore) compatibilitySessionRevocationNotifier() func([]string) {
	if s == nil {
		return nil
	}
	s.notifierMu.RLock()
	notifier := s.notifier
	s.notifierMu.RUnlock()
	return notifier
}

func (s *CredentialStore) Status(ctx context.Context, principal auth.Principal, profileID string) (CredentialStatus, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CredentialStatus{}, fmt.Errorf("begin Jellyfin credential status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	principal, err = s.reloadAndLockPrincipal(ctx, tx, principal)
	if err != nil {
		return CredentialStatus{}, err
	}

	profileID = strings.TrimSpace(profileID)
	if err := authorizeCredentialProfile(ctx, tx, principal, profileID); err != nil {
		return CredentialStatus{}, err
	}
	status, err := credentialStatus(ctx, tx, profileID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		status = CredentialStatus{}
	} else if err != nil {
		return CredentialStatus{}, fmt.Errorf("load Jellyfin credential status: %w", err)
	}
	status.CanIssue, err = canIssueCredential(ctx, tx, principal.UserID, profileID)
	if err != nil {
		return CredentialStatus{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CredentialStatus{}, fmt.Errorf("commit Jellyfin credential status: %w", err)
	}
	return status, nil
}

func (s *CredentialStore) Create(ctx context.Context, principal auth.Principal, profileID string) (ProfileCredential, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProfileCredential{}, fmt.Errorf("begin Jellyfin credential creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	profileID = strings.TrimSpace(profileID)
	principal, err = s.reloadAndLockPrincipal(ctx, tx, principal)
	if err != nil {
		return ProfileCredential{}, err
	}
	if err := authorizeCredentialProfile(ctx, tx, principal, profileID); err != nil {
		return ProfileCredential{}, err
	}
	if err := requireCredentialOwnerGrant(ctx, tx, principal.UserID, profileID); err != nil {
		return ProfileCredential{}, err
	}
	if err := lockCredentialProfileMutation(ctx, tx, profileID); err != nil {
		return ProfileCredential{}, err
	}
	current, lookupErr := credentialStatus(ctx, tx, profileID, true)
	missing := errors.Is(lookupErr, pgx.ErrNoRows)
	if lookupErr != nil && !missing {
		return ProfileCredential{}, fmt.Errorf("lock Jellyfin credential: %w", lookupErr)
	}
	if !missing && current.Active {
		return ProfileCredential{}, ErrCredentialExists
	}
	password, digest, err := auth.NewJellyfinAppPassword()
	if err != nil {
		return ProfileCredential{}, fmt.Errorf("generate Jellyfin profile password: %w", err)
	}

	var issued CredentialStatus
	if missing {
		err = tx.QueryRow(ctx, `
			INSERT INTO profile_jellyfin_credentials (profile_id, owner_user_id, password_hash)
			VALUES ($1::uuid, $2::uuid, $3)
			RETURNING id::text, true, generation, created_at, rotated_at, last_used_at, revoked_at
		`, profileID, principal.UserID, digest).Scan(
			&issued.Username, &issued.Active, &issued.Generation, &issued.CreatedAt,
			&issued.RotatedAt, &issued.LastUsedAt, &issued.RevokedAt,
		)
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE profile_jellyfin_credentials
			SET owner_user_id = $2::uuid,
			    password_hash = $3,
			    generation = generation + 1,
			    rotated_at = now(),
			    last_used_at = NULL,
			    revoked_at = NULL
			WHERE id = $1::uuid AND revoked_at IS NOT NULL
			RETURNING id::text, true, generation, created_at, rotated_at, last_used_at, revoked_at
		`, current.Username, principal.UserID, digest).Scan(
			&issued.Username, &issued.Active, &issued.Generation, &issued.CreatedAt,
			&issued.RotatedAt, &issued.LastUsedAt, &issued.RevokedAt,
		)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileCredential{}, ErrCredentialExists
	}
	if err != nil {
		return ProfileCredential{}, fmt.Errorf("store Jellyfin profile credential: %w", err)
	}
	issued.CanIssue = true
	if err := tx.Commit(ctx); err != nil {
		return ProfileCredential{}, fmt.Errorf("commit Jellyfin credential creation: %w", err)
	}
	return ProfileCredential{CredentialStatus: issued, Password: password}, nil
}

func (s *CredentialStore) Rotate(ctx context.Context, principal auth.Principal, profileID string) (ProfileCredential, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProfileCredential{}, fmt.Errorf("begin Jellyfin credential rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	notifyRevoked := s.compatibilitySessionRevocationNotifier()

	profileID = strings.TrimSpace(profileID)
	principal, err = s.reloadAndLockPrincipal(ctx, tx, principal)
	if err != nil {
		return ProfileCredential{}, err
	}
	if err := authorizeCredentialProfile(ctx, tx, principal, profileID); err != nil {
		return ProfileCredential{}, err
	}
	if err := requireCredentialOwnerGrant(ctx, tx, principal.UserID, profileID); err != nil {
		return ProfileCredential{}, err
	}
	if err := lockCredentialProfileMutation(ctx, tx, profileID); err != nil {
		return ProfileCredential{}, err
	}
	current, lookupErr := credentialStatus(ctx, tx, profileID, true)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return ProfileCredential{}, ErrCredentialNotFound
	}
	if lookupErr != nil {
		return ProfileCredential{}, fmt.Errorf("lock Jellyfin credential for rotation: %w", lookupErr)
	}
	if !current.Active {
		return ProfileCredential{}, ErrCredentialNotFound
	}
	password, digest, err := auth.NewJellyfinAppPassword()
	if err != nil {
		return ProfileCredential{}, fmt.Errorf("generate rotated Jellyfin profile password: %w", err)
	}

	var rotated CredentialStatus
	if err := tx.QueryRow(ctx, `
		UPDATE profile_jellyfin_credentials
		SET owner_user_id = $2::uuid,
		    password_hash = $3,
		    generation = generation + 1,
		    rotated_at = now(),
		    last_used_at = NULL
		WHERE id = $1::uuid AND revoked_at IS NULL
		RETURNING id::text, true, generation, created_at, rotated_at, last_used_at, revoked_at
	`, current.Username, principal.UserID, digest).Scan(
		&rotated.Username, &rotated.Active, &rotated.Generation, &rotated.CreatedAt,
		&rotated.RotatedAt, &rotated.LastUsedAt, &rotated.RevokedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return ProfileCredential{}, ErrCredentialNotFound
	} else if err != nil {
		return ProfileCredential{}, fmt.Errorf("rotate Jellyfin profile credential: %w", err)
	}
	revokedSessionIDs, err := revokeCredentialSessions(ctx, tx, current.Username, "jellyfin_profile_credential_rotated")
	if err != nil {
		return ProfileCredential{}, err
	}
	rotated.CanIssue = true
	if err := tx.Commit(ctx); err != nil {
		return ProfileCredential{}, fmt.Errorf("commit Jellyfin credential rotation: %w", err)
	}
	notifyCompatibilitySessionRevocations(notifyRevoked, revokedSessionIDs)
	return ProfileCredential{CredentialStatus: rotated, Password: password}, nil
}

func (s *CredentialStore) Revoke(ctx context.Context, principal auth.Principal, profileID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin Jellyfin credential revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	notifyRevoked := s.compatibilitySessionRevocationNotifier()

	profileID = strings.TrimSpace(profileID)
	principal, err = s.reloadAndLockPrincipal(ctx, tx, principal)
	if err != nil {
		return err
	}
	if err := authorizeCredentialProfile(ctx, tx, principal, profileID); err != nil {
		return err
	}
	if err := lockCredentialProfileMutation(ctx, tx, profileID); err != nil {
		return err
	}
	current, lookupErr := credentialStatus(ctx, tx, profileID, true)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return nil
	}
	if lookupErr != nil {
		return fmt.Errorf("lock Jellyfin credential for revocation: %w", lookupErr)
	}
	if !current.Active {
		return nil
	}
	command, err := tx.Exec(ctx, `
		UPDATE profile_jellyfin_credentials
		SET password_hash = NULL,
		    generation = generation + 1,
		    revoked_at = now()
		WHERE id = $1::uuid AND revoked_at IS NULL
	`, current.Username)
	if err != nil {
		return fmt.Errorf("revoke Jellyfin profile credential: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrCredentialNotFound
	}
	revokedSessionIDs, err := revokeCredentialSessions(ctx, tx, current.Username, "jellyfin_profile_credential_revoked")
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Jellyfin credential revocation: %w", err)
	}
	notifyCompatibilitySessionRevocations(notifyRevoked, revokedSessionIDs)
	return nil
}

func (s *CredentialStore) reloadAndLockPrincipal(ctx context.Context, tx pgx.Tx, captured auth.Principal) (auth.Principal, error) {
	location := runtimesettings.Load(ctx, s.runtimeSettings).Location
	principal, authorized, err := auth.ReloadAndLockPrincipal(ctx, tx, captured, time.Now().UTC(), location)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("reload Jellyfin credential principal: %w", err)
	}
	if !authorized {
		return auth.Principal{}, ErrCredentialForbidden
	}
	return principal, nil
}

func lockCredentialProfileMutation(ctx context.Context, tx pgx.Tx, profileID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, profileID); err != nil {
		return fmt.Errorf("lock Jellyfin credential mutation: %w", err)
	}
	return nil
}

func canIssueCredential(ctx context.Context, tx pgx.Tx, userID, profileID string) (bool, error) {
	var allowed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_profile_access
			WHERE user_id = $1::uuid AND profile_id = $2::uuid
		)
	`, userID, profileID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("read Jellyfin credential issue permission: %w", err)
	}
	return allowed, nil
}

func authorizeCredentialProfile(ctx context.Context, tx pgx.Tx, principal auth.Principal, profileID string) error {
	ownProfile := principal.ActiveProfileID != nil &&
		strings.EqualFold(strings.TrimSpace(*principal.ActiveProfileID), profileID) &&
		principal.ProfileGrantExpiresAt != nil && principal.ProfileGrantExpiresAt.After(time.Now().UTC())
	requireManagement := !ownProfile
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, requireManagement)
	if err != nil {
		return fmt.Errorf("authorize Jellyfin profile credential: %w", err)
	}
	if !authorized {
		return ErrCredentialForbidden
	}
	return nil
}

func requireCredentialOwnerGrant(ctx context.Context, tx pgx.Tx, userID, profileID string) error {
	var lockedProfileID string
	err := tx.QueryRow(ctx, `
		SELECT profile_id::text
		FROM user_profile_access
		WHERE user_id = $1::uuid AND profile_id = $2::uuid
		FOR SHARE
	`, userID, profileID).Scan(&lockedProfileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCredentialForbidden
	}
	if err != nil {
		return fmt.Errorf("lock Jellyfin credential owner grant: %w", err)
	}
	return nil
}

func credentialStatus(ctx context.Context, tx pgx.Tx, profileID string, lock bool) (CredentialStatus, error) {
	query := `
		SELECT id::text, password_hash IS NOT NULL AND revoked_at IS NULL,
		       generation, created_at, rotated_at, last_used_at, revoked_at
		FROM profile_jellyfin_credentials
		WHERE profile_id::text = $1`
	if lock {
		query += " FOR UPDATE"
	}
	var status CredentialStatus
	err := tx.QueryRow(ctx, query, profileID).Scan(
		&status.Username, &status.Active, &status.Generation, &status.CreatedAt,
		&status.RotatedAt, &status.LastUsedAt, &status.RevokedAt,
	)
	return status, err
}

func revokeCredentialSessions(ctx context.Context, tx pgx.Tx, credentialID, reason string) ([]string, error) {
	if _, err := tx.Exec(ctx, `
		SELECT id
		FROM auth_sessions
		WHERE jellyfin_credential_id = $1::uuid
		ORDER BY id
		FOR UPDATE
	`, credentialID); err != nil {
		return nil, fmt.Errorf("lock Jellyfin credential native sessions: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT compat_session.id::text
		FROM jellyfin_compat_sessions compat_session
		JOIN auth_sessions native_session ON native_session.id = compat_session.auth_session_id
		WHERE native_session.jellyfin_credential_id = $1::uuid
		  AND compat_session.revoked_at IS NULL
		  AND compat_session.expires_at > now()
		ORDER BY compat_session.id
		FOR UPDATE OF compat_session
	`, credentialID)
	if err != nil {
		return nil, fmt.Errorf("lock Jellyfin credential compatibility sessions: %w", err)
	}
	revokedSessionIDs := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read Jellyfin credential compatibility session: %w", err)
		}
		revokedSessionIDs = append(revokedSessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate Jellyfin credential compatibility sessions: %w", err)
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = COALESCE(revoked_reason, $2)
		WHERE jellyfin_credential_id = $1::uuid
	`, credentialID, reason); err != nil {
		return nil, fmt.Errorf("revoke Jellyfin credential sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE jellyfin_compat_sessions compat_session
		SET revoked_at = COALESCE(compat_session.revoked_at, now()),
		    revoked_reason = COALESCE(compat_session.revoked_reason, $2)
		FROM auth_sessions native_session
		WHERE compat_session.auth_session_id = native_session.id
		  AND native_session.jellyfin_credential_id = $1::uuid
	`, credentialID, reason); err != nil {
		return nil, fmt.Errorf("revoke Jellyfin compatibility sessions: %w", err)
	}
	return revokedSessionIDs, nil
}

func notifyCompatibilitySessionRevocations(notifier func([]string), sessionIDs []string) {
	if notifier == nil || len(sessionIDs) == 0 {
		return
	}
	defer func() { _ = recover() }()
	notifier(sessionIDs)
}
