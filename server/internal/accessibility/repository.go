package accessibility

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/auth"
)

type repository interface {
	Get(context.Context, auth.Principal, string) (Document, error)
	Update(context.Context, auth.Principal, string, UpdateInput) (Document, error)
}

type postgresRepository struct{ pool *pgxpool.Pool }

func (r postgresRepository) Get(ctx context.Context, principal auth.Principal, profileID string) (Document, error) {
	tx, err := r.authorizedTransaction(ctx, principal, profileID)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureDocument(ctx, tx, profileID); err != nil {
		return Document{}, err
	}
	document, err := readDocument(ctx, tx, profileID)
	if err != nil {
		return Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("commit accessibility preference read: %w", err)
	}
	return document, nil
}

func (r postgresRepository) Update(ctx context.Context, principal auth.Principal, profileID string, input UpdateInput) (Document, error) {
	tx, err := r.authorizedTransaction(ctx, principal, profileID)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureDocument(ctx, tx, profileID); err != nil {
		return Document{}, err
	}
	var document Document
	err = tx.QueryRow(ctx, `
		UPDATE profile_accessibility_preferences
		SET revision=revision+1,
		    reduced_motion=$3,high_contrast=$4,text_scale=$5,captions=$6,
		    audio_description=$7,focus_indicators=$8,updated_at=clock_timestamp()
		WHERE profile_id=$1::uuid AND revision=$2
		RETURNING revision,reduced_motion,high_contrast,text_scale,captions,audio_description,focus_indicators
	`, profileID, input.Revision, input.ReducedMotion, input.HighContrast, input.TextScale,
		input.Captions, input.AudioDescription, input.FocusIndicators).Scan(
		&document.Revision, &document.ReducedMotion, &document.HighContrast, &document.TextScale,
		&document.Captions, &document.AudioDescription, &document.FocusIndicators,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrConflict
	}
	if err != nil {
		return Document{}, fmt.Errorf("update accessibility preferences: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("commit accessibility preference update: %w", err)
	}
	return document, nil
}

func (r postgresRepository) authorizedTransaction(ctx context.Context, principal auth.Principal, profileID string) (pgx.Tx, error) {
	if r.pool == nil {
		return nil, ErrActiveProfileRequired
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin accessibility preference transaction: %w", err)
	}
	authorized, err := auth.AuthorizeAndLockProfiles(ctx, tx, principal, []string{profileID}, false)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("authorize accessibility preference profile: %w", err)
	}
	selected, err := auth.LockActiveProfileSelection(ctx, tx, principal)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("lock accessibility preference profile selection: %w", err)
	}
	if !authorized || !selected {
		_ = tx.Rollback(ctx)
		return nil, ErrActiveProfileRequired
	}
	return tx, nil
}

func ensureDocument(ctx context.Context, tx pgx.Tx, profileID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO profile_accessibility_preferences (profile_id) VALUES ($1::uuid) ON CONFLICT (profile_id) DO NOTHING`, profileID); err != nil {
		return fmt.Errorf("create default accessibility preferences: %w", err)
	}
	return nil
}

func readDocument(ctx context.Context, tx pgx.Tx, profileID string) (Document, error) {
	var document Document
	if err := tx.QueryRow(ctx, `
		SELECT revision,reduced_motion,high_contrast,text_scale,captions,audio_description,focus_indicators
		FROM profile_accessibility_preferences WHERE profile_id=$1::uuid
	`, profileID).Scan(&document.Revision, &document.ReducedMotion, &document.HighContrast,
		&document.TextScale, &document.Captions, &document.AudioDescription, &document.FocusIndicators); err != nil {
		return Document{}, fmt.Errorf("read accessibility preferences: %w", err)
	}
	return document, nil
}
