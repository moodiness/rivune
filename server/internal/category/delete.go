package category

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Service) Delete(ctx context.Context, principal Actor, categoryID string, reassignToCategoryID *string) error {
	if !principal.GlobalAdministrator {
		return ErrForbidden
	}
	if !validUUID(categoryID) {
		return invalid("categoryId must be a valid category identifier")
	}
	categoryID = canonicalUUID(categoryID)
	if reassignToCategoryID != nil {
		trimmed := strings.TrimSpace(*reassignToCategoryID)
		if !validUUID(trimmed) {
			return invalid("reassignToCategoryId must identify a different category")
		}
		canonical := canonicalUUID(trimmed)
		if canonical == categoryID {
			return invalid("reassignToCategoryId must identify a different category")
		}
		reassignToCategoryID = &canonical
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin category deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock categories for deletion: %w", err)
	}
	var position int
	var isDefault bool
	err = tx.QueryRow(ctx, `SELECT position, is_default FROM access_categories WHERE id = $1::uuid FOR UPDATE`, categoryID).Scan(&position, &isDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("query category for deletion: %w", err)
	}
	if isDefault {
		return ErrDefaultCategory
	}
	if reassignToCategoryID != nil {
		if err := lockCategory(ctx, tx, *reassignToCategoryID); err != nil {
			return err
		}
	}
	if reassignToCategoryID != nil {
		if _, err := tx.Exec(ctx, `
			SELECT id
			FROM profiles
			WHERE category_id = $1::uuid
			ORDER BY id
			FOR UPDATE
		`, categoryID); err != nil {
			return fmt.Errorf("lock reassigned profiles: %w", err)
		}
	}
	var profileCount, deviceCount, sessionCount, authorizationCount int64
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM profiles WHERE category_id = $1::uuid),
			(SELECT count(*) FROM devices WHERE category_id = $1::uuid),
			(SELECT count(*) FROM auth_sessions WHERE category_id = $1::uuid),
			(SELECT count(*) FROM device_authorizations WHERE approved_category_id = $1::uuid)
	`, categoryID).Scan(&profileCount, &deviceCount, &sessionCount, &authorizationCount); err != nil {
		return fmt.Errorf("count category references: %w", err)
	}
	if reassignToCategoryID == nil && (profileCount > 0 || deviceCount > 0 || sessionCount > 0 || authorizationCount > 0) {
		return ErrReassignmentRequired
	}
	if reassignToCategoryID != nil {
		destinationID := *reassignToCategoryID
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_category_audit_events
				(actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details)
			SELECT $1::uuid, 'profile.category_moved', 'profile', p.id, $2::uuid, $3::uuid,
			       jsonb_build_object('reason', 'category_deleted')
			FROM profiles p WHERE p.category_id = $2::uuid
		`, principal.UserID, categoryID, destinationID); err != nil {
			return fmt.Errorf("audit reassigned profiles: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_category_audit_events
				(actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details)
			SELECT $1::uuid, 'device.category_moved', 'device', d.id, $2::uuid, $3::uuid,
			       jsonb_build_object('reason', 'category_deleted')
			FROM devices d WHERE d.category_id = $2::uuid
		`, principal.UserID, categoryID, destinationID); err != nil {
			return fmt.Errorf("audit reassigned devices: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = COALESCE(revoked_at, now()),
			    revoked_reason = CASE WHEN revoked_at IS NULL THEN 'device category changed' ELSE revoked_reason END,
			    active_profile_id = NULL,
			    profile_grant_expires_at = NULL,
			    profile_context_hash = NULL
			WHERE device_id IN (SELECT id FROM devices WHERE category_id = $1::uuid)
		`, categoryID); err != nil {
			return fmt.Errorf("revoke reassigned device sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET revoked_at = COALESCE(revoked_at, now()),
			    revoked_reason = CASE WHEN revoked_at IS NULL THEN 'access category deleted' ELSE revoked_reason END,
			    active_profile_id = NULL,
			    profile_grant_expires_at = NULL,
			    profile_context_hash = NULL
			WHERE authorization_scope = 'category' AND category_id = $1::uuid
		`, categoryID); err != nil {
			return fmt.Errorf("revoke deleted category sessions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			SET active_profile_id = NULL, profile_grant_expires_at = NULL, profile_context_hash = NULL
			WHERE authorization_scope = 'global_admin'
			  AND active_profile_id IN (SELECT id FROM profiles WHERE category_id = $1::uuid)
		`, categoryID); err != nil {
			return fmt.Errorf("clear reassigned profile selections: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE profiles SET category_id = $2::uuid, updated_at = now() WHERE category_id = $1::uuid`, categoryID, destinationID); err != nil {
			return fmt.Errorf("reassign category profiles: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE devices SET category_id = $2::uuid, updated_at = now() WHERE category_id = $1::uuid`, categoryID, destinationID); err != nil {
			return fmt.Errorf("reassign category devices: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET category_id = $2::uuid WHERE category_id = $1::uuid`, categoryID, destinationID); err != nil {
			return fmt.Errorf("reassign revoked session categories: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE device_authorizations SET approved_category_id = $2::uuid WHERE approved_category_id = $1::uuid`, categoryID, destinationID); err != nil {
			return fmt.Errorf("reassign approved device authorizations: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_category_audit_events
			(actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details)
		VALUES ($1::uuid, 'category.deleted', 'category', $2::uuid, $2::uuid, $3::uuid,
		        jsonb_build_object('profileCount', $4::bigint, 'deviceCount', $5::bigint))
	`, principal.UserID, categoryID, reassignToCategoryID, profileCount, deviceCount); err != nil {
		return fmt.Errorf("audit category deletion: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM access_categories WHERE id = $1::uuid`, categoryID); err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE access_categories SET position = position - 1, updated_at = now() WHERE position > $1`, position); err != nil {
		return fmt.Errorf("compact category positions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit category deletion: %w", err)
	}
	return nil
}
