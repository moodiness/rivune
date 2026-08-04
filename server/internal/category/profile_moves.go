package category

import (
	"context"
	"fmt"
)

func (s *Service) MoveProfile(ctx context.Context, principal Actor, profileID, categoryID string) error {
	return s.MoveProfiles(ctx, principal, []string{profileID}, categoryID)
}

func (s *Service) MoveProfiles(ctx context.Context, principal Actor, profileIDs []string, categoryID string) error {
	if !principal.GlobalAdministrator {
		return ErrForbidden
	}
	if err := validateMoveIDs(profileIDs, categoryID, "profileIds"); err != nil {
		return err
	}
	categoryID = canonicalUUID(categoryID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin profile category move: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock categories for profile move: %w", err)
	}
	if err := lockCategory(ctx, tx, categoryID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM profiles WHERE id = ANY($1::uuid[]) FOR UPDATE`, profileIDs)
	if err != nil {
		return fmt.Errorf("lock profiles for category move: %w", err)
	}
	matched := 0
	for rows.Next() {
		matched++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate profiles for category move: %w", err)
	}
	rows.Close()
	if matched != len(profileIDs) {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_category_audit_events
			(actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details)
		SELECT $1::uuid, 'profile.category_moved', 'profile', p.id, p.category_id, $3::uuid, '{}'::jsonb
		FROM profiles p
		WHERE p.id = ANY($2::uuid[]) AND p.category_id <> $3::uuid
	`, principal.UserID, profileIDs, categoryID); err != nil {
		return fmt.Errorf("audit profile category moves: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = CASE WHEN revoked_at IS NULL THEN 'active profile category changed' ELSE revoked_reason END,
		    active_profile_id = NULL,
		    profile_grant_expires_at = NULL
		WHERE authorization_scope = 'category'
		  AND active_profile_id = ANY($1::uuid[])
		  AND active_profile_id IN (SELECT id FROM profiles WHERE category_id <> $2::uuid)
	`, profileIDs, categoryID); err != nil {
		return fmt.Errorf("revoke moved profile sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET active_profile_id = NULL, profile_grant_expires_at = NULL
		WHERE authorization_scope = 'global_admin'
		  AND active_profile_id = ANY($1::uuid[])
		  AND active_profile_id IN (SELECT id FROM profiles WHERE category_id <> $2::uuid)
	`, profileIDs, categoryID); err != nil {
		return fmt.Errorf("clear moved profile selections: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profiles SET category_id = $2::uuid, updated_at = now()
		WHERE id = ANY($1::uuid[]) AND category_id <> $2::uuid
	`, profileIDs, categoryID); err != nil {
		return fmt.Errorf("move profiles to category: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile category move: %w", err)
	}
	return nil
}
