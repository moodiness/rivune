package readingqueue

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

const (
	operationRetryWindow = 24 * time.Hour
	operationCleanupBatch = 128
)

type Service struct {
	pool       *pgxpool.Pool
	repository repository
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, repository: postgresRepository{}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Queue(ctx context.Context, principal auth.Principal, requestedProfileID string) (Queue, error) {
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal, requestedProfileID)
	if err != nil {
		return Queue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	revision, err := s.repository.LockQueue(ctx, tx, profileID)
	if err != nil {
		return Queue{}, err
	}
	items, err := s.repository.List(ctx, tx, profileID)
	if err != nil {
		return Queue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Queue{}, fmt.Errorf("commit reading queue read: %w", err)
	}
	return Queue{Revision: revision, Items: items}, nil
}

func (s *Service) Add(ctx context.Context, principal auth.Principal, requestedProfileID string, input AddInput) (Mutation, error) {
	input = normalizeAdd(input)
	if !validAdd(input) {
		return Mutation{}, ErrInvalidInput
	}
	return s.mutate(ctx, principal, requestedProfileID, "add", input.OperationID, input.ExpectedRevision, input, func(tx pgx.Tx, profileID string, revision int64) (Mutation, error) {
		if existing, found, err := s.repository.FindIdentity(ctx, tx, profileID, input); err != nil {
			return Mutation{}, err
		} else if found {
			return Mutation{Revision: revision, AffectedItemID: existing.ID, Duplicate: true}, nil
		}
		items, err := s.repository.List(ctx, tx, profileID)
		if err != nil {
			return Mutation{}, err
		}
		if len(items) >= MaximumItems {
			return Mutation{}, ErrCapacity
		}
		item, err := s.repository.Insert(ctx, tx, profileID, input)
		if err != nil {
			return Mutation{}, err
		}
		next, err := s.repository.AdvanceRevision(ctx, tx, profileID)
		return Mutation{Revision: next, AffectedItemID: item.ID}, err
	})
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, requestedProfileID, itemID string, input UpdateInput) (Mutation, error) {
	input.OperationID = normalizeUUID(input.OperationID)
	itemID = normalizeUUID(itemID)
	input.Title = strings.TrimSpace(input.Title)
	input.PosterURL = strings.TrimSpace(input.PosterURL)
	if !validMutationHeader(input.OperationID, input.ExpectedRevision) || !uuidPattern.MatchString(itemID) || !bounded(input.Title, 1, 240) || !boundedOptional(input.PosterURL, 2048) {
		return Mutation{}, ErrInvalidInput
	}
	return s.mutate(ctx, principal, requestedProfileID, "update", input.OperationID, input.ExpectedRevision, struct {
		ItemID string      `json:"itemId"`
		Input  UpdateInput `json:"input"`
	}{itemID, input}, func(tx pgx.Tx, profileID string, _ int64) (Mutation, error) {
		item, err := s.repository.Update(ctx, tx, profileID, itemID, input)
		if err != nil {
			return Mutation{}, err
		}
		next, err := s.repository.AdvanceRevision(ctx, tx, profileID)
		return Mutation{Revision: next, AffectedItemID: item.ID}, err
	})
}

func (s *Service) Reorder(ctx context.Context, principal auth.Principal, requestedProfileID string, input ReorderInput) (Mutation, error) {
	input.OperationID = normalizeUUID(input.OperationID)
	if !validMutationHeader(input.OperationID, input.ExpectedRevision) || len(input.ItemIDs) > MaximumItems {
		return Mutation{}, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input.ItemIDs))
	for index := range input.ItemIDs {
		input.ItemIDs[index] = normalizeUUID(input.ItemIDs[index])
		if !uuidPattern.MatchString(input.ItemIDs[index]) {
			return Mutation{}, ErrInvalidInput
		}
		if _, duplicate := seen[input.ItemIDs[index]]; duplicate {
			return Mutation{}, ErrInvalidInput
		}
		seen[input.ItemIDs[index]] = struct{}{}
	}
	return s.mutate(ctx, principal, requestedProfileID, "reorder", input.OperationID, input.ExpectedRevision, input, func(tx pgx.Tx, profileID string, _ int64) (Mutation, error) {
		items, err := s.repository.List(ctx, tx, profileID)
		if err != nil {
			return Mutation{}, err
		}
		if len(items) != len(input.ItemIDs) {
			return Mutation{}, ErrConflict
		}
		for _, item := range items {
			if _, ok := seen[item.ID]; !ok {
				return Mutation{}, ErrConflict
			}
		}
		if err := s.repository.Reorder(ctx, tx, profileID, input.ItemIDs); err != nil {
			return Mutation{}, err
		}
		next, err := s.repository.AdvanceRevision(ctx, tx, profileID)
		return Mutation{Revision: next}, err
	})
}

func (s *Service) Remove(ctx context.Context, principal auth.Principal, requestedProfileID, itemID string, input MutationInput) (Mutation, error) {
	return s.delete(ctx, principal, requestedProfileID, itemID, input, "remove")
}

func (s *Service) Consume(ctx context.Context, principal auth.Principal, requestedProfileID, itemID string, input MutationInput) (Mutation, error) {
	return s.delete(ctx, principal, requestedProfileID, itemID, input, "consume")
}

func (s *Service) delete(ctx context.Context, principal auth.Principal, requestedProfileID, itemID string, input MutationInput, kind string) (Mutation, error) {
	input.OperationID = normalizeUUID(input.OperationID)
	itemID = normalizeUUID(itemID)
	if !validMutationHeader(input.OperationID, input.ExpectedRevision) || !uuidPattern.MatchString(itemID) {
		return Mutation{}, ErrInvalidInput
	}
	return s.mutate(ctx, principal, requestedProfileID, kind, input.OperationID, input.ExpectedRevision, struct {
		ItemID string        `json:"itemId"`
		Input  MutationInput `json:"input"`
	}{itemID, input}, func(tx pgx.Tx, profileID string, _ int64) (Mutation, error) {
		item, err := s.repository.Delete(ctx, tx, profileID, itemID)
		if err != nil {
			return Mutation{}, err
		}
		next, err := s.repository.AdvanceRevision(ctx, tx, profileID)
		return Mutation{Revision: next, AffectedItemID: item.ID}, err
	})
}

type mutationWork func(pgx.Tx, string, int64) (Mutation, error)

func (s *Service) mutate(ctx context.Context, principal auth.Principal, requestedProfileID, kind, operationID string, expectedRevision int64, request any, work mutationWork) (Mutation, error) {
	hash, err := hashRequest(request)
	if err != nil {
		return Mutation{}, fmt.Errorf("hash reading queue request: %w", err)
	}
	tx, profileID, err := s.beginAuthorizedProfileTx(ctx, principal, requestedProfileID)
	if err != nil {
		return Mutation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := s.now()
	revision, err := s.repository.LockQueue(ctx, tx, profileID)
	if err != nil {
		return Mutation{}, err
	}
	if _, err := s.repository.PruneOperations(ctx, tx, now, operationCleanupBatch); err != nil {
		return Mutation{}, err
	}
	if prior, found, err := s.repository.Operation(ctx, tx, profileID, operationID, now); err != nil {
		return Mutation{}, err
	} else if found {
		if prior.Kind != kind || !bytes.Equal(prior.RequestHash, hash[:]) {
			return Mutation{}, commitCleanup(ctx, tx, ErrOperationConflict)
		}
		if err := commitCleanup(ctx, tx, nil); err != nil {
			return Mutation{}, err
		}
		return Mutation{Revision: prior.Revision, AffectedItemID: prior.ItemID, Duplicate: prior.Deduplicated}, nil
	}
	if revision != expectedRevision {
		return Mutation{}, commitCleanup(ctx, tx, ErrConflict)
	}
	result, err := work(tx, profileID, revision)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrCapacity) {
			return Mutation{}, commitCleanup(ctx, tx, err)
		}
		return Mutation{}, err
	}
	if err := s.repository.RegisterOperation(ctx, tx, profileID, operationID, kind, hash[:], result.Revision, result.AffectedItemID, result.Duplicate, now, now.Add(operationRetryWindow)); err != nil {
		return Mutation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Mutation{}, fmt.Errorf("commit reading queue mutation: %w", err)
	}
	return result, nil
}

func commitCleanup(ctx context.Context, tx pgx.Tx, outcome error) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reading queue operation cleanup: %w", err)
	}
	return outcome
}

func (s *Service) beginAuthorizedProfileTx(ctx context.Context, principal auth.Principal, requestedProfileID string) (pgx.Tx, string, error) {
	requestedProfileID = normalizeUUID(requestedProfileID)
	if !uuidPattern.MatchString(requestedProfileID) || principal.ActiveProfileID == nil || normalizeUUID(*principal.ActiveProfileID) != requestedProfileID ||
		principal.ProfileGrantExpiresAt == nil || !principal.ProfileGrantExpiresAt.After(s.now()) || s.pool == nil {
		return nil, "", ErrActiveProfileRequired
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin reading queue transaction: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{requestedProfileID}, false)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("authorize reading queue profile: %w", err)
	}
	valid, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("lock reading queue profile selection: %w", err)
	}
	if !authorized || !valid {
		_ = tx.Rollback(ctx)
		return nil, "", ErrActiveProfileRequired
	}
	return tx, requestedProfileID, nil
}

func normalizeAdd(input AddInput) AddInput {
	input.OperationID = normalizeUUID(input.OperationID)
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.SourceAddonID = normalizeOptionalUUID(input.SourceAddonID)
	input.TitleID = normalizeOptionalUUID(input.TitleID)
	input.Title = strings.TrimSpace(input.Title)
	input.PosterURL = strings.TrimSpace(input.PosterURL)
	return input
}

func validAdd(input AddInput) bool {
	if !validMutationHeader(input.OperationID, input.ExpectedRevision) || !validMediaType(input.MediaType) ||
		!bounded(input.ResourceID, 1, 512) || !bounded(input.Title, 1, 240) || !boundedOptional(input.PosterURL, 2048) {
		return false
	}
	return (input.SourceAddonID == "" || uuidPattern.MatchString(input.SourceAddonID)) && (input.TitleID == "" || uuidPattern.MatchString(input.TitleID))
}

func validMutationHeader(operationID string, expectedRevision int64) bool {
	return uuidPattern.MatchString(operationID) && expectedRevision > 0
}

func validMediaType(value string) bool {
	switch value {
	case "movie", "series", "episode", "tv":
		return true
	default:
		return false
	}
}

func normalizeUUID(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func normalizeOptionalUUID(value string) string {
	value = normalizeUUID(value)
	if value == "00000000-0000-0000-0000-000000000000" {
		return ""
	}
	return value
}
func bounded(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= minimum && length <= maximum
}
func boundedOptional(value string, maximum int) bool { return value == "" || bounded(value, 1, maximum) }
func hashRequest(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}
