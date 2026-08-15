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

type NativeQuickConnect interface {
	BeginJellyfinQuickConnect(context.Context, auth.JellyfinQuickConnectInput) (auth.DeviceAuthorization, error)
	PollJellyfinQuickConnect(context.Context, string, string) (auth.JellyfinQuickConnectStatus, error)
	ExchangeJellyfinQuickConnect(context.Context, string, string) (auth.JellyfinQuickConnectResult, error)
}

// JellyfinProfileLogin is injected by the HTTP composition root so native and
// compatibility login share the same source and opaque-username admission budgets.
type JellyfinProfileLogin func(context.Context, auth.JellyfinProfileLoginInput) (auth.JellyfinProfileLoginResult, error)

type CompatLoginInput struct {
	Username string
	Password string
	Client   ClientIdentity
}

type QuickConnectStatus struct {
	Secret        string
	Code          string
	Authenticated bool
	DateAdded     time.Time
	DeviceID      string
	DeviceName    string
	AppName       string
	AppVersion    string
}

type LoginResult struct {
	Credential CompatCredential
	Profile    profile.Profile
	Principal  auth.Principal
	Client     ClientIdentity
}

type AuthenticationService struct {
	login                     JellyfinProfileLogin
	native                    NativeAuthentication
	quickConnect              NativeQuickConnect
	sessions                  *SessionStore
	failedLoginCleanupTimeout time.Duration
}

func NewAuthenticationService(login JellyfinProfileLogin, native NativeAuthentication, sessions *SessionStore) (*AuthenticationService, error) {
	if login == nil || native == nil || sessions == nil {
		return nil, fmt.Errorf("compatibility authentication dependencies are required")
	}
	service := &AuthenticationService{
		login: login, native: native, sessions: sessions,
		failedLoginCleanupTimeout: defaultFailedLoginCleanupTimeout,
	}
	service.quickConnect, _ = native.(NativeQuickConnect)
	return service, nil
}

func (s *AuthenticationService) Login(ctx context.Context, input CompatLoginInput) (LoginResult, error) {
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
	return s.bindLogin(ctx, login.Tokens, login.ProfileID, login.ProfileName, client)
}

func (s *AuthenticationService) BeginQuickConnect(ctx context.Context, client ClientIdentity) (QuickConnectStatus, error) {
	if s.quickConnect == nil {
		return QuickConnectStatus{}, ErrInvalidCompatLogin
	}
	client, err := normalizeClientIdentity(client)
	if err != nil {
		return QuickConnectStatus{}, ErrInvalidCompatLogin
	}
	platform := client.Client
	if !boundedUTF8(platform, 1, 32) {
		platform = "jellyfin"
	}
	authorization, err := s.quickConnect.BeginJellyfinQuickConnect(ctx, auth.JellyfinQuickConnectInput{
		ClientDeviceID: client.DeviceID, DeviceName: client.Device, AppName: platform, AppVersion: client.Version,
	})
	if err != nil {
		return QuickConnectStatus{}, err
	}
	return QuickConnectStatus{
		Secret: authorization.DeviceCode, Code: authorization.UserCode,
		DateAdded: authorization.CreatedAt, DeviceID: client.DeviceID,
		DeviceName: client.Device, AppName: platform, AppVersion: client.Version,
	}, nil
}

func (s *AuthenticationService) PollQuickConnect(ctx context.Context, secret string, client ClientIdentity) (QuickConnectStatus, error) {
	if s.quickConnect == nil {
		return QuickConnectStatus{}, ErrInvalidCompatLogin
	}
	client, err := normalizeQuickConnectDeviceIdentity(client)
	if err != nil {
		return QuickConnectStatus{}, ErrInvalidCompatLogin
	}
	status, err := s.quickConnect.PollJellyfinQuickConnect(ctx, secret, client.DeviceID)
	if err != nil {
		return QuickConnectStatus{}, err
	}
	return QuickConnectStatus{
		Secret: status.Secret, Code: status.UserCode, Authenticated: status.Authenticated,
		DateAdded: status.CreatedAt, DeviceID: status.DeviceID, DeviceName: status.DeviceName,
		AppName: status.AppName, AppVersion: status.AppVersion,
	}, nil
}

func (s *AuthenticationService) LoginQuickConnect(ctx context.Context, secret string, client ClientIdentity) (LoginResult, error) {
	if s.quickConnect == nil {
		return LoginResult{}, ErrInvalidCompatLogin
	}
	client, err := normalizeQuickConnectDeviceIdentity(client)
	if err != nil {
		return LoginResult{}, ErrInvalidCompatLogin
	}
	login, err := s.quickConnect.ExchangeJellyfinQuickConnect(ctx, secret, client.DeviceID)
	if err != nil {
		return LoginResult{}, err
	}
	initiatingClient := ClientIdentity{
		Client: login.AppName, Device: login.DeviceName, DeviceID: login.DeviceID, Version: login.AppVersion,
	}
	return s.bindLogin(ctx, login.Tokens, login.ProfileID, login.ProfileName, initiatingClient)
}

func normalizeQuickConnectDeviceIdentity(client ClientIdentity) (ClientIdentity, error) {
	deviceID, ok := canonicalCompatDeviceID(client.DeviceID)
	if !ok {
		return ClientIdentity{}, ErrInvalidClientIdentity
	}
	return ClientIdentity{DeviceID: deviceID}, nil
}

func (s *AuthenticationService) bindLogin(ctx context.Context, tokens auth.TokenPair, profileID, profileName string, client ClientIdentity) (result LoginResult, resultErr error) {
	keepSession := false
	defer func() {
		if keepSession {
			return
		}
		cleanupErr := s.revokeFailedLoginSession(ctx, tokens.SessionID)
		if cleanupErr != nil {
			result = LoginResult{}
			resultErr = ErrCompatLoginCleanup
		}
	}()

	principal, err := s.native.ReloadLinkedPrincipal(ctx, tokens.SessionID, profileID)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("reload selected compatibility profile: %w", err)
	}
	credential, err := s.sessions.Issue(ctx, principal, profileID, client, tokens.RefreshExpiresAt)
	if err != nil {
		if errors.Is(err, ErrInvalidCompatCredential) {
			return LoginResult{}, ErrInvalidCompatLogin
		}
		return LoginResult{}, fmt.Errorf("issue compatibility session: %w", err)
	}

	keepSession = true
	return LoginResult{
		Credential: credential,
		Profile:    profile.Profile{ID: profileID, Name: profileName, Accessible: true},
		Principal:  principal,
		Client:     client,
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
	return s.authorizeSession(ctx, session)
}

func (s *AuthenticationService) Revalidate(ctx context.Context, expected AuthenticatedSession) (AuthenticatedSession, error) {
	session, err := s.sessions.AuthenticateSession(ctx, expected.ID)
	if err != nil {
		return AuthenticatedSession{}, err
	}
	if !sameAuthenticatedSessionOwner(expected, session) {
		return AuthenticatedSession{}, ErrInvalidCompatCredential
	}
	return s.authorizeSession(ctx, session)
}

func (s *AuthenticationService) authorizeSession(ctx context.Context, session AuthenticatedSession) (AuthenticatedSession, error) {
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

func sameAuthenticatedSessionOwner(expected, actual AuthenticatedSession) bool {
	return expected.ID != "" && expected.ID == actual.ID && expected.ProfileID == actual.ProfileID &&
		expected.Client.DeviceID == actual.Client.DeviceID && expected.Principal.SessionID == actual.Principal.SessionID &&
		expected.Principal.UserID == actual.Principal.UserID && expected.Principal.DeviceID == actual.Principal.DeviceID &&
		actual.Principal.ActiveProfileID != nil && *actual.Principal.ActiveProfileID == actual.ProfileID
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
