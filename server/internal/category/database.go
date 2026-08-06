package category

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const categorySelect = `
	SELECT c.id::text, c.name, c.description, c.color, c.icon, c.position, c.is_default,
	       (SELECT count(*) FROM profiles p WHERE p.category_id = c.id),
	       (SELECT count(*) FROM devices d WHERE d.category_id = c.id),
	       c.created_at, c.updated_at
	FROM access_categories c`

const deviceSelect = `
	SELECT d.id::text, d.name, d.platform, c.id::text, c.name, c.color, c.icon,
	       d.internal_note, d.approved_at, d.last_seen_at, d.created_at, d.updated_at
	FROM devices d JOIN access_categories c ON c.id = d.category_id`

type rowScanner interface{ Scan(...any) error }

func scanCategory(row rowScanner) (Category, error) {
	var item Category
	err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Color, &item.Icon, &item.Position,
		&item.IsDefault, &item.ProfileCount, &item.DeviceCount, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanDevice(row rowScanner) (Device, error) {
	var item Device
	err := row.Scan(deviceScanTargets(&item)...)
	return item, err
}

func deviceScanTargets(item *Device) []any {
	return []any{&item.ID, &item.Name, &item.Platform, &item.Category.ID, &item.Category.Name,
		&item.Category.Color, &item.Category.Icon, &item.InternalNote, &item.ApprovedAt,
		&item.LastSeenAt, &item.CreatedAt, &item.UpdatedAt}
}

func authorizeAndLockGlobalAdministrator(ctx context.Context, tx pgx.Tx, principal Actor) error {
	var administrator bool
	if err := tx.QueryRow(ctx, `
		SELECT role = 'admin'
		FROM users
		WHERE id = $1::uuid
		FOR SHARE
	`, principal.UserID).Scan(&administrator); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		return fmt.Errorf("authorize global administrator: %w", err)
	}
	if !administrator {
		return ErrForbidden
	}
	return nil
}

func lockCategory(ctx context.Context, tx pgx.Tx, categoryID string) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT true FROM access_categories WHERE id = $1::uuid FOR UPDATE`, categoryID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock destination category: %w", err)
	}
	return nil
}

func revokeDeviceSessions(ctx context.Context, tx pgx.Tx, deviceIDs []string) error {
	_, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = COALESCE(revoked_at, now()),
		    revoked_reason = CASE WHEN revoked_at IS NULL THEN 'device category changed' ELSE revoked_reason END,
		    active_profile_id = NULL,
		    profile_grant_expires_at = NULL,
		    profile_context_hash = NULL
		WHERE device_id = ANY($1::uuid[])
	`, deviceIDs)
	if err != nil {
		return fmt.Errorf("revoke moved device sessions: %w", err)
	}
	return nil
}

func audit(ctx context.Context, tx pgx.Tx, actorID, action, entityType, entityID string, oldCategoryID, newCategoryID *string, details string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO access_category_audit_events (
			actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details
		) VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6::uuid, $7::jsonb)
	`, actorID, action, entityType, entityID, oldCategoryID, newCategoryID, details)
	if err != nil {
		return fmt.Errorf("write access category audit event: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}
