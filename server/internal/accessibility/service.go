package accessibility

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Service struct {
	repository repository
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{repository: postgresRepository{pool: pool}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, requestedProfileID string) (Document, error) {
	profileID, err := s.activeProfile(principal, requestedProfileID)
	if err != nil {
		return Document{}, err
	}
	return s.repository.Get(ctx, principal, profileID)
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, requestedProfileID string, input UpdateInput) (Document, error) {
	profileID, err := s.activeProfile(principal, requestedProfileID)
	if err != nil {
		return Document{}, err
	}
	if input.Revision < 0 || !valid(input.Preferences) {
		return Document{}, ErrInvalidInput
	}
	return s.repository.Update(ctx, principal, profileID, input)
}

func (s *Service) activeProfile(principal auth.Principal, requestedProfileID string) (string, error) {
	profileID := strings.ToLower(strings.TrimSpace(requestedProfileID))
	if !uuidPattern.MatchString(profileID) || principal.ActiveProfileID == nil ||
		strings.ToLower(strings.TrimSpace(*principal.ActiveProfileID)) != profileID ||
		principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(s.now()) {
		return "", ErrActiveProfileRequired
	}
	return profileID, nil
}
