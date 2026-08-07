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
	Authenticate(context.Context, string) (auth.Principal, error)
	ReloadLinkedPrincipal(context.Context, string, string) (auth.Principal, error)
	RevokeUnfinishedLinkedSession(context.Context, string) error
	Account(context.Context, auth.Principal) (auth.Account, error)
	LogoutLinkedSession(context.Context, auth.Principal, string) error
}

// NativeCredentialLogin is injected by the HTTP composition root so native
// and compatibility login share one source and username admission budget.
type NativeCredentialLogin func(context.Context, auth.LoginInput) (auth.TokenPair, error)

type LinkedProfileSelector interface {
	SelectForLinkedSession(context.Context, auth.Principal, string, *string, bool) (profile.Selection, error)
}

type CompatLoginInput struct {
	Username   string
	Password   string
	ProfilePIN *string
	Client     ClientIdentity
}

type LoginResult struct {
	Credential CompatCredential
	Profile    profile.Profile
	Principal  auth.Principal
}

type AuthenticationService struct {
	login                     NativeCredentialLogin
	native                    NativeAuthentication
	profiles                  LinkedProfileSelector
	sessions                  *SessionStore
	failedLoginCleanupTimeout time.Duration
}

func NewAuthenticationService(login NativeCredentialLogin, native NativeAuthentication, profiles LinkedProfileSelector, sessions *SessionStore) (*AuthenticationService, error) {
	if login == nil || native == nil || profiles == nil || sessions == nil {
		return nil, fmt.Errorf("compatibility authentication dependencies are required")
	}
	return &AuthenticationService{
		login: login, native: native, profiles: profiles, sessions: sessions,
		failedLoginCleanupTimeout: defaultFailedLoginCleanupTimeout,
	}, nil
}

func (s *AuthenticationService) Login(ctx context.Context, input CompatLoginInput) (result LoginResult, resultErr error) {
	client, err := normalizeClientIdentity(input.Client)
	if err != nil {
		return LoginResult{}, ErrInvalidCompatLogin
	}
	accountName, profileSelector, qualified, ok := splitCompatUsername(input.Username)
	if !ok {
		return LoginResult{}, ErrInvalidCompatLogin
	}

	platform := client.Client
	if !boundedUTF8(platform, 1, 32) {
		platform = "jellyfin"
	}
	tokens, err := s.login(ctx, auth.LoginInput{
		Username:        accountName,
		Password:        input.Password,
		LinkedDeviceKey: client.DeviceID,
		DeviceName:      client.Device,
		Platform:        platform,
	})
	if err != nil {
		if isCredentialLoginError(err) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("compatibility native login: %w", err)
	}
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

	principal, err := s.native.Authenticate(ctx, tokens.AccessToken)
	if err != nil {
		return LoginResult{}, fmt.Errorf("authenticate newly linked native session: %w", err)
	}

	account, err := s.native.Account(ctx, principal)
	if err != nil {
		return LoginResult{}, fmt.Errorf("load compatibility login profiles: %w", err)
	}
	profileID, ok := resolveLoginProfile(account.Profiles, profileSelector, qualified)
	if !ok {
		return LoginResult{}, ErrInvalidCompatLogin
	}

	selection, err := s.profiles.SelectForLinkedSession(ctx, principal, profileID, input.ProfilePIN, false)
	if err != nil {
		if isProfileSelectionError(err) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("select compatibility profile: %w", err)
	}
	principal, err = s.native.ReloadLinkedPrincipal(ctx, tokens.SessionID, selection.Profile.ID)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("reload selected compatibility profile: %w", err)
	}
	credential, err := s.sessions.Issue(ctx, principal, selection.Profile.ID, client, selection.ExpiresAt)
	if err != nil {
		if errors.Is(err, ErrInvalidCompatCredential) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("issue compatibility session: %w", err)
	}

	keepSession = true
	return LoginResult{Credential: credential, Profile: selection.Profile, Principal: principal}, nil
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
		session.ProfileHasPIN = candidate.HasPIN
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

func splitCompatUsername(value string) (accountName, profileSelector string, qualified, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false, false
	}
	separatorCount := strings.Count(value, "/")
	if separatorCount == 0 {
		return value, "", false, true
	}
	if separatorCount != 1 {
		return "", "", false, false
	}
	parts := strings.SplitN(value, "/", 2)
	accountName = strings.TrimSpace(parts[0])
	profileSelector = strings.TrimSpace(parts[1])
	return accountName, profileSelector, true, accountName != "" && profileSelector != ""
}

func resolveLoginProfile(profiles []auth.Profile, selector string, qualified bool) (string, bool) {
	matches := make([]string, 0, 1)
	for _, candidate := range profiles {
		if !candidate.Accessible {
			continue
		}
		if qualified && !strings.EqualFold(candidate.ID, selector) && !strings.EqualFold(candidate.Name, selector) {
			continue
		}
		alreadyMatched := false
		for _, matchedID := range matches {
			alreadyMatched = alreadyMatched || strings.EqualFold(matchedID, candidate.ID)
		}
		if !alreadyMatched {
			matches = append(matches, candidate.ID)
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

func isCredentialLoginError(err error) bool {
	return errors.Is(err, auth.ErrInvalidCredentials) ||
		errors.Is(err, auth.ErrInvalidInput) ||
		errors.Is(err, auth.ErrDeviceQuotaReached)
}

func isProfileSelectionError(err error) bool {
	return errors.Is(err, profile.ErrNotFound) ||
		errors.Is(err, profile.ErrForbidden) ||
		errors.Is(err, profile.ErrInvalidPIN) ||
		errors.Is(err, profile.ErrPINRateLimited) ||
		errors.Is(err, profile.ErrUnavailable) ||
		errors.Is(err, profile.ErrManagementRequired)
}
