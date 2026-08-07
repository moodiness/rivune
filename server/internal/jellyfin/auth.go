package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/profile"
)

const defaultFailedLoginCleanupTimeout = 2 * time.Second

var (
	ErrInvalidCompatLogin = errors.New("invalid compatibility login")
	ErrCompatLoginCleanup = errors.New("compatibility login cleanup failed")
)

type NativeAuthentication interface {
	ReloadLinkedPrincipal(context.Context, string, string) (auth.Principal, error)
	RevokeUnfinishedLinkedSession(context.Context, string) error
	Account(context.Context, auth.Principal) (auth.Account, error)
	LogoutLinkedSession(context.Context, auth.Principal, string) error
}

// JellyfinProfileLogin is injected by the HTTP composition root so native and
// compatibility login share the same source and opaque-username admission budgets.
type JellyfinProfileLogin func(context.Context, auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error)

type CompatLoginInput struct {
	Username string
	Password string
	Client   ClientIdentity
}

type LoginResult struct {
	Credential CompatCredential
	Profile    profile.Profile
	Principal  auth.Principal
}

type AuthenticationService struct {
	login                     JellyfinProfileLogin
	native                    NativeAuthentication
	sessions                  *SessionStore
	failedLoginCleanupTimeout time.Duration
}

func NewAuthenticationService(login JellyfinProfileLogin, native NativeAuthentication, sessions *SessionStore) (*AuthenticationService, error) {
	if login == nil || native == nil || sessions == nil {
		return nil, fmt.Errorf("compatibility authentication dependencies are required")
	}
	return &AuthenticationService{
		login: login, native: native, sessions: sessions,
		failedLoginCleanupTimeout: defaultFailedLoginCleanupTimeout,
	}, nil
}

func (s *AuthenticationService) Login(ctx context.Context, input CompatLoginInput) (result LoginResult, resultErr error) {
	client, err := normalizeClientIdentity(input.Client)
	if err != nil {
		return LoginResult{}, ErrInvalidCompatLogin
	}
	platform := client.Client
	if !boundedUTF8(platform, 1, 32) {
		platform = "jellyfin"
	}
	login, err := s.login(ctx, auth.JellyfinProfileLoginInput{
		Username:        input.Username,
		Password:        input.Password,
		LinkedDeviceKey: client.DeviceID,
		DeviceName:      client.Device,
		Platform:        platform,
	})
	if err != nil {
		if isCredentialLoginError(err) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("compatibility profile login: %w", err)
	}
	tokens := login.Tokens
	keepSession := false
	defer func() {
		if keepSession {
			return
		}
		cleanupErr := s.revokeFailedLoginSession(ctx, tokens.SessionID)
		if cleanupErr != nil {
			// The cleanup cause may contain driver details. Return only a stable,
			// credential-free error while making the failed cleanup observable.
			result = LoginResult{}
			resultErr = ErrCompatLoginCleanup
		}
	}()

	principal, err := s.native.ReloadLinkedPrincipal(ctx, tokens.SessionID, login.ProfileID)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("reload selected compatibility profile: %w", err)
	}
	credential, err := s.sessions.Issue(ctx, principal, login.ProfileID, client, tokens.RefreshExpiresAt)
	if err != nil {
		if errors.Is(err, ErrInvalidCompatCredential) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("issue compatibility session: %w", err)
	}

	keepSession = true
	return LoginResult{
		Credential: credential,
		Profile:    profile.Profile{ID: login.ProfileID, Name: login.ProfileName, Accessible: true},
		Principal:  principal,
	}, nil
}

func (s *AuthenticationService) revokeFailedLoginSession(requestContext context.Context, sessionID string) error {
	timeout := s.failedLoginCleanupTimeout
	if timeout <= 0 {
		timeout = defaultFailedLoginCleanupTimeout
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(requestContext), timeout)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- s.native.RevokeUnfinishedLinkedSession(cleanupContext, sessionID)
	}()
	select {
	case err := <-result:
		return err
	case <-cleanupContext.Done():
		return cleanupContext.Err()
	}
}

func (s *AuthenticationService) Authenticate(ctx context.Context, token string) (AuthenticatedSession, error) {
	session, err := s.sessions.Authenticate(ctx, token)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	account, err := s.native.Account(ctx, session.Principal)
	if err != nil {
		return AuthenticatedSession{}, fmt.Errorf("load compatibility profile identity: %w", err)
	}
	matched := false
	for _, candidate := range account.Profiles {
		if !candidate.Accessible || !strings.EqualFold(candidate.ID, session.ProfileID) {
			continue
		}
		if matched {
			if revokeErr := s.sessions.Revoke(context.WithoutCancel(ctx), session.ID, "linked_profile_ambiguous"); revokeErr != nil {
				return AuthenticatedSession{}, fmt.Errorf("revoke ambiguous compatibility profile: %w", revokeErr)
			}
			return AuthenticatedSession{}, ErrInvalidCompatCredential
		}
		matched = true
		session.ProfileName = candidate.Name
	}
	if !matched || !boundedUTF8(session.ProfileName, 1, 120) {
		if revokeErr := s.sessions.Revoke(context.WithoutCancel(ctx), session.ID, "linked_profile_unavailable"); revokeErr != nil {
			return AuthenticatedSession{}, fmt.Errorf("revoke unavailable compatibility profile: %w", revokeErr)
		}
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return session, nil
}

func (s *AuthenticationService) Logout(ctx context.Context, session AuthenticatedSession) error {
	err := s.native.LogoutLinkedSession(ctx, session.Principal, session.ID)
	if errors.Is(err, auth.ErrInvalidToken) {
		return ErrInvalidCompatCredential
	}
	if err != nil {
		return fmt.Errorf("logout linked native session: %w", err)
	}
	return nil
}

func isCredentialLoginError(err error) bool {
	return errors.Is(err, auth.ErrInvalidCredentials) ||
		errors.Is(err, auth.ErrInvalidInput) ||
		errors.Is(err, auth.ErrDeviceQuotaReached)
}
