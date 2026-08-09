package jellyfin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	compatCredentialPrefix               = "rivune_jf_"
	compatCredentialBytes                = 32
	compatAuthenticationOperationTimeout = 5 * time.Second
	// maximumConcurrentCompatAuthenticationOperations conservatively bounds detached
	// compatibility credential lookups below the database pool's normal capacity.
	maximumConcurrentCompatAuthenticationOperations = 16
)

var (
	ErrInvalidCompatCredential       = errors.New("invalid compatibility credential")
	ErrInvalidClientIdentity         = errors.New("invalid compatibility client identity")
	ErrCompatAuthenticationSaturated = errors.New("compatibility authentication capacity exhausted")

	compatAuthenticationOperations = make(chan struct{}, maximumConcurrentCompatAuthenticationOperations)
)

type ClientIdentity struct {
	Client   string
	Device   string
	DeviceID string
	Version  string
}

type CompatCredential struct {
	Token     string
	SessionID string
	ExpiresAt time.Time
}

type AuthenticatedSession struct {
	ID          string
	ProfileID   string
	Client      ClientIdentity
	ExpiresAt   time.Time
	Principal   auth.Principal
	ProfileName string
}

type LinkedPrincipalReloader interface {
	ReloadLinkedPrincipal(context.Context, string, string) (auth.Principal, error)
}

type SessionStore struct {
	pool                     *pgxpool.Pool
	principals               LinkedPrincipalReloader
	authenticationMu         sync.Mutex
	authenticationFlights    map[string]*compatAuthenticationFlight
	authenticationOperations chan struct{}
}

type compatAuthenticationFlight struct {
	key     string
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	result  compatAuthenticationResult
	waiters int
	running bool
}

type compatAuthenticationResult struct {
	session AuthenticatedSession
	err     error
}

func NewSessionStore(pool *pgxpool.Pool, principals LinkedPrincipalReloader) (*SessionStore, error) {
	if pool == nil || principals == nil {
		return nil, fmt.Errorf("compatibility session store dependencies are required")
	}
	return &SessionStore{
		pool:                     pool,
		principals:               principals,
		authenticationFlights:    make(map[string]*compatAuthenticationFlight),
		authenticationOperations: compatAuthenticationOperations,
	}, nil
}

func (s *SessionStore) Issue(ctx context.Context, principal auth.Principal, profileID string, client ClientIdentity, expiresAt time.Time) (CompatCredential, error) {
	client, err := normalizeClientIdentity(client)
	if err != nil {
		return CompatCredential{}, err
	}
	profileID = strings.TrimSpace(profileID)
	if principal.SessionID == "" || principal.UserID == "" || principal.ActiveProfileID == nil ||
		profileID == "" || !strings.EqualFold(*principal.ActiveProfileID, profileID) ||
		!expiresAt.After(time.Now().UTC()) {
		return CompatCredential{}, ErrInvalidCompatCredential
	}

	plainText, digest, err := newCompatCredential()
	if err != nil {
		return CompatCredential{}, err
	}
	issued := CompatCredential{Token: plainText, ExpiresAt: expiresAt.UTC()}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CompatCredential{}, fmt.Errorf("begin compatibility credential issue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var authSessionID, authoritativeProfileID string
	err = tx.QueryRow(ctx, `
		SELECT id::text, active_profile_id::text
		FROM auth_sessions
		WHERE id = $1::uuid
		  AND user_id = $2::uuid
		  AND active_profile_id = $3::uuid
		  AND revoked_at IS NULL
		  AND refresh_expires_at >= $4
		  AND profile_grant_expires_at >= $4
		FOR SHARE
	`, principal.SessionID, principal.UserID, profileID, issued.ExpiresAt).Scan(
		&authSessionID, &authoritativeProfileID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CompatCredential{}, ErrInvalidCompatCredential
	}
	if err != nil {
		return CompatCredential{}, fmt.Errorf("lock compatibility native session: %w", err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO jellyfin_compat_sessions (
			auth_session_id, profile_id, token_hash,
			client_name, device_name, client_device_id, client_version,
			expires_at, last_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		RETURNING id::text
	`, authSessionID, authoritativeProfileID, digest[:], client.Client,
		client.Device, client.DeviceID, client.Version, issued.ExpiresAt,
	).Scan(&issued.SessionID)
	if err != nil {
		return CompatCredential{}, fmt.Errorf("store compatibility credential: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CompatCredential{}, fmt.Errorf("commit compatibility credential: %w", err)
	}
	return issued, nil
}

func (s *SessionStore) Authenticate(ctx context.Context, token string) (AuthenticatedSession, error) {
	digest, ok := compatCredentialDigest(token)
	if !ok {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return s.authenticateSingleflight(ctx, string(digest[:]), func(operationContext context.Context) (AuthenticatedSession, error) {
		return s.authenticateDigest(operationContext, digest)
	})
}

func (s *SessionStore) AuthenticateSession(ctx context.Context, sessionID string) (AuthenticatedSession, error) {
	if _, err := parseUUID(sessionID); err != nil {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return s.authenticateSingleflight(ctx, "session:"+sessionID, func(operationContext context.Context) (AuthenticatedSession, error) {
		return s.authenticateSessionID(operationContext, sessionID)
	})
}

func (s *SessionStore) authenticateSingleflight(
	ctx context.Context,
	key string,
	operation func(context.Context) (AuthenticatedSession, error),
) (AuthenticatedSession, error) {
	flight, owner, err := s.joinAuthenticationFlight(ctx, key)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	if owner {
		go s.runAuthenticationFlight(flight, operation)
	}
	select {
	case <-ctx.Done():
		s.leaveAuthenticationFlight(flight)
		return AuthenticatedSession{}, ctx.Err()
	case <-flight.done:
		s.leaveAuthenticationFlight(flight)
		return flight.result.session, flight.result.err
	}
}

func (s *SessionStore) joinAuthenticationFlight(ctx context.Context, key string) (*compatAuthenticationFlight, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.authenticationMu.Lock()
	defer s.authenticationMu.Unlock()
	if flight := s.authenticationFlights[key]; flight != nil {
		flight.waiters++
		return flight, false, nil
	}
	select {
	case s.authenticationOperations <- struct{}{}:
	default:
		return nil, false, ErrCompatAuthenticationSaturated
	}
	operationContext, cancel := context.WithTimeout(context.Background(), compatAuthenticationOperationTimeout)
	flight := &compatAuthenticationFlight{
		key: key, ctx: operationContext, cancel: cancel, done: make(chan struct{}), waiters: 1, running: true,
	}
	s.authenticationFlights[key] = flight
	return flight, true, nil
}

func (s *SessionStore) runAuthenticationFlight(
	flight *compatAuthenticationFlight,
	operation func(context.Context) (AuthenticatedSession, error),
) {
	if err := flight.ctx.Err(); err != nil {
		flight.result.err = err
	} else {
		flight.result.session, flight.result.err = operation(flight.ctx)
	}
	s.authenticationMu.Lock()
	flight.running = false
	if s.authenticationFlights[flight.key] == flight {
		delete(s.authenticationFlights, flight.key)
	}
	s.authenticationMu.Unlock()
	flight.cancel()
	<-s.authenticationOperations
	close(flight.done)
}

func (s *SessionStore) leaveAuthenticationFlight(flight *compatAuthenticationFlight) {
	s.authenticationMu.Lock()
	defer s.authenticationMu.Unlock()
	if flight.waiters > 0 {
		flight.waiters--
	}
	if flight.waiters != 0 || !flight.running {
		return
	}
	flight.cancel()
	if s.authenticationFlights[flight.key] == flight {
		delete(s.authenticationFlights, flight.key)
	}
}

func (s *SessionStore) authenticateDigest(ctx context.Context, digest [sha256.Size]byte) (AuthenticatedSession, error) {
	return s.authenticateRow(ctx, s.pool.QueryRow(ctx, `
		SELECT id::text, auth_session_id::text, profile_id::text,
		       client_name, device_name, client_device_id, client_version, expires_at
		FROM jellyfin_compat_sessions
		WHERE token_hash = $1
		  AND expires_at > now()
		  AND revoked_at IS NULL
	`, digest[:]))
}

func (s *SessionStore) authenticateSessionID(ctx context.Context, sessionID string) (AuthenticatedSession, error) {
	return s.authenticateRow(ctx, s.pool.QueryRow(ctx, `
		SELECT id::text, auth_session_id::text, profile_id::text,
		       client_name, device_name, client_device_id, client_version, expires_at
		FROM jellyfin_compat_sessions
		WHERE id = $1::uuid
		  AND expires_at > now()
		  AND revoked_at IS NULL
	`, sessionID))
}

type compatibilitySessionRow interface {
	Scan(...any) error
}

func (s *SessionStore) authenticateRow(ctx context.Context, row compatibilitySessionRow) (AuthenticatedSession, error) {
	var session AuthenticatedSession
	var authSessionID string
	err := row.Scan(
		&session.ID, &authSessionID, &session.ProfileID,
		&session.Client.Client, &session.Client.Device,
		&session.Client.DeviceID, &session.Client.Version, &session.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	if err != nil {
		return AuthenticatedSession{}, fmt.Errorf("lookup compatibility credential: %w", err)
	}

	principal, err := s.principals.ReloadLinkedPrincipal(ctx, authSessionID, session.ProfileID)
	if errors.Is(err, auth.ErrInvalidToken) {
		if revokeErr := s.revoke(ctx, session.ID, "linked_authorization_invalid"); revokeErr != nil {
			return AuthenticatedSession{}, revokeErr
		}
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	if err != nil {
		return AuthenticatedSession{}, fmt.Errorf("reload compatibility principal: %w", err)
	}
	if principal.ActiveProfileID == nil || !strings.EqualFold(*principal.ActiveProfileID, session.ProfileID) {
		if revokeErr := s.revoke(ctx, session.ID, "linked_profile_mismatch"); revokeErr != nil {
			return AuthenticatedSession{}, revokeErr
		}
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}

	command, err := s.pool.Exec(ctx, `
		UPDATE jellyfin_compat_sessions
		SET last_seen_at = now()
		WHERE id = $1::uuid AND expires_at > now() AND revoked_at IS NULL
	`, session.ID)
	if err != nil {
		return AuthenticatedSession{}, fmt.Errorf("touch compatibility session: %w", err)
	}
	if command.RowsAffected() != 1 {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	session.Principal = principal
	return session, nil
}

func (s *SessionStore) RevokeByToken(ctx context.Context, token, reason string) error {
	digest, ok := compatCredentialDigest(token)
	if !ok {
		return ErrInvalidCompatCredential
	}
	reason = normalizeRevocationReason(reason)
	command, err := s.pool.Exec(ctx, `
		UPDATE jellyfin_compat_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = COALESCE(revoked_reason, $2)
		WHERE token_hash = $1
	`, digest[:], reason)
	if err != nil {
		return fmt.Errorf("revoke compatibility credential: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrInvalidCompatCredential
	}
	return nil
}

func (s *SessionStore) RevokeAllActive(ctx context.Context, reason string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("compatibility session store database is required")
	}
	reason = normalizeRevocationReason(reason)
	if _, err := s.pool.Exec(ctx, `
		UPDATE jellyfin_compat_sessions
		SET revoked_at = now(),
		    revoked_reason = $1
		WHERE revoked_at IS NULL
	`, reason); err != nil {
		return fmt.Errorf("revoke active compatibility sessions: %w", err)
	}
	return nil
}

func (s *SessionStore) Revoke(ctx context.Context, sessionID, reason string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrInvalidCompatCredential
	}
	return s.revoke(ctx, sessionID, normalizeRevocationReason(reason))
}

func (s *SessionStore) revoke(ctx context.Context, sessionID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE jellyfin_compat_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = COALESCE(revoked_reason, $2)
		WHERE id = $1::uuid
	`, sessionID, reason)
	if err != nil {
		return fmt.Errorf("revoke compatibility session: %w", err)
	}
	return nil
}

func newCompatCredential() (string, [sha256.Size]byte, error) {
	var entropy [compatCredentialBytes]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("generate compatibility credential: %w", err)
	}
	plainText := compatCredentialPrefix + base64.RawURLEncoding.EncodeToString(entropy[:])
	return plainText, sha256.Sum256([]byte(plainText)), nil
}

func compatCredentialDigest(token string) ([sha256.Size]byte, bool) {
	if !strings.HasPrefix(token, compatCredentialPrefix) {
		return [sha256.Size]byte{}, false
	}
	encoded := strings.TrimPrefix(token, compatCredentialPrefix)
	entropy, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(entropy) != compatCredentialBytes {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}

func normalizeClientIdentity(client ClientIdentity) (ClientIdentity, error) {
	client.Client = strings.TrimSpace(client.Client)
	client.Device = strings.TrimSpace(client.Device)
	client.DeviceID = strings.TrimSpace(client.DeviceID)
	client.Version = strings.TrimSpace(client.Version)
	if !boundedUTF8(client.Client, 1, 64) || !boundedUTF8(client.Device, 1, 120) ||
		!boundedUTF8(client.DeviceID, 1, 128) || !boundedUTF8(client.Version, 1, 32) {
		return ClientIdentity{}, ErrInvalidClientIdentity
	}
	return client, nil
}

func boundedUTF8(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < minimum || utf8.RuneCountInString(value) > maximum {
		return false
	}
	for _, current := range value {
		if unicode.IsControl(current) {
			return false
		}
	}
	return true
}

func normalizeRevocationReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if !boundedUTF8(reason, 1, 128) {
		return "revoked"
	}
	return reason
}
