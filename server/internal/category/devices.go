package category

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *Service) ListDevices(ctx context.Context, principal Actor, categoryID *string) ([]Device, error) {
	if !principal.GlobalAdministrator {
		return nil, ErrForbidden
	}
	if categoryID != nil {
		if !validUUID(*categoryID) {
			return nil, invalid("categoryId must be a valid category identifier")
		}
		canonical := canonicalUUID(*categoryID)
		categoryID = &canonical
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM access_categories WHERE id = $1::uuid)`, *categoryID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("validate device category filter: %w", err)
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	rows, err := s.pool.Query(ctx, deviceSelect+`
		WHERE ($1::uuid IS NULL OR d.category_id = $1::uuid)
		ORDER BY lower(d.name), d.id
	`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	result := make([]Device, 0)
	for rows.Next() {
		item, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return result, nil
}

func (s *Service) DeleteDevice(ctx context.Context, principal Actor, deviceID string) error {
	if !principal.GlobalAdministrator {
		return ErrForbidden
	}
	if !validUUID(deviceID) {
		return invalid("deviceId must be a valid device identifier")
	}
	deviceID = canonicalUUID(deviceID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin device deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var categoryID string
	err = tx.QueryRow(ctx, `SELECT category_id::text FROM devices WHERE id = $1::uuid FOR UPDATE`, deviceID).Scan(&categoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("query device for deletion: %w", err)
	}
	if err := audit(ctx, tx, principal.UserID, "device.deleted", "device", deviceID, &categoryID, nil, `{}`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM devices WHERE id = $1::uuid`, deviceID); err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device deletion: %w", err)
	}
	return nil
}

func (s *Service) UpdateDevice(ctx context.Context, principal Actor, deviceID string, input DeviceUpdateInput) (Device, error) {
	if !principal.GlobalAdministrator {
		return Device{}, ErrForbidden
	}
	if !validUUID(deviceID) {
		return Device{}, invalid("deviceId must be a valid device identifier")
	}
	if input.Name == nil && input.CategoryID == nil && !input.InternalNoteSet {
		return Device{}, invalid("at least one field must be provided")
	}
	var err error
	if input.Name != nil {
		name, validationErr := normalizeDeviceName(*input.Name)
		if validationErr != nil {
			return Device{}, validationErr
		}
		input.Name = &name
	}
	if input.CategoryID != nil {
		if !validUUID(*input.CategoryID) {
			return Device{}, invalid("categoryId must be a valid category identifier")
		}
		canonical := canonicalUUID(*input.CategoryID)
		input.CategoryID = &canonical
	}
	if input.InternalNoteSet {
		input.InternalNote, err = normalizeOptionalText(input.InternalNote, 500, "internalNote")
		if err != nil {
			return Device{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, fmt.Errorf("begin device update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Device{}, fmt.Errorf("lock categories for device update: %w", err)
	}
	if input.CategoryID != nil {
		if err := lockCategory(ctx, tx, *input.CategoryID); err != nil {
			return Device{}, err
		}
	}
	var current Device
	err = tx.QueryRow(ctx, deviceSelect+` WHERE d.id = $1::uuid FOR UPDATE OF d`, deviceID).Scan(deviceScanTargets(&current)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("query device for update: %w", err)
	}
	name, note, categoryID := current.Name, current.InternalNote, current.Category.ID
	if input.Name != nil {
		name = *input.Name
	}
	if input.InternalNoteSet {
		note = input.InternalNote
	}
	if input.CategoryID != nil {
		categoryID = *input.CategoryID
	}
	categoryChanged := !sameUUID(categoryID, current.Category.ID)
	metadataChanged := name != current.Name || !equalOptionalString(note, current.InternalNote)
	if categoryChanged {
		if err := revokeDeviceSessions(ctx, tx, []string{deviceID}); err != nil {
			return Device{}, err
		}
		if err := audit(ctx, tx, principal.UserID, "device.category_moved", "device", deviceID, &current.Category.ID, &categoryID, `{}`); err != nil {
			return Device{}, err
		}
	}
	if metadataChanged {
		if err := audit(ctx, tx, principal.UserID, "device.updated", "device", deviceID, &current.Category.ID, &categoryID, `{}`); err != nil {
			return Device{}, err
		}
	}
	if err := tx.QueryRow(ctx, `
		UPDATE devices SET name = $2, category_id = $3::uuid, internal_note = $4, updated_at = now()
		WHERE id = $1::uuid RETURNING updated_at
	`, deviceID, name, categoryID, note).Scan(&current.UpdatedAt); err != nil {
		return Device{}, fmt.Errorf("update device: %w", err)
	}
	if err := tx.QueryRow(ctx, deviceSelect+` WHERE d.id = $1::uuid`, deviceID).Scan(deviceScanTargets(&current)...); err != nil {
		return Device{}, fmt.Errorf("query updated device: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, fmt.Errorf("commit device update: %w", err)
	}
	return current, nil
}

func (s *Service) MoveDevice(ctx context.Context, principal Actor, deviceID, categoryID string) error {
	return s.MoveDevices(ctx, principal, []string{deviceID}, categoryID)
}

func (s *Service) MoveDevices(ctx context.Context, principal Actor, deviceIDs []string, categoryID string) error {
	if !principal.GlobalAdministrator {
		return ErrForbidden
	}
	if err := validateMoveIDs(deviceIDs, categoryID, "deviceIds"); err != nil {
		return err
	}
	categoryID = canonicalUUID(categoryID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin device category move: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock categories for device move: %w", err)
	}
	if err := lockCategory(ctx, tx, categoryID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM devices WHERE id = ANY($1::uuid[]) FOR UPDATE`, deviceIDs)
	if err != nil {
		return fmt.Errorf("lock devices for category move: %w", err)
	}
	matched := 0
	for rows.Next() {
		matched++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate devices for category move: %w", err)
	}
	rows.Close()
	if matched != len(deviceIDs) {
		return ErrNotFound
	}
	changed := make([]string, 0, len(deviceIDs))
	rows, err = tx.Query(ctx, `SELECT id::text FROM devices WHERE id = ANY($1::uuid[]) AND category_id <> $2::uuid`, deviceIDs, categoryID)
	if err != nil {
		return fmt.Errorf("query changed devices: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan changed device: %w", err)
		}
		changed = append(changed, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate changed devices: %w", err)
	}
	rows.Close()
	if len(changed) > 0 {
		if err := revokeDeviceSessions(ctx, tx, changed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_category_audit_events
				(actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details)
			SELECT $1::uuid, 'device.category_moved', 'device', d.id, d.category_id, $3::uuid, '{}'::jsonb
			FROM devices d WHERE d.id = ANY($2::uuid[]) AND d.category_id <> $3::uuid
		`, principal.UserID, changed, categoryID); err != nil {
			return fmt.Errorf("audit device category moves: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE devices SET category_id = $2::uuid, updated_at = now()
			WHERE id = ANY($1::uuid[])
		`, changed, categoryID); err != nil {
			return fmt.Errorf("move devices to category: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit device category move: %w", err)
	}
	return nil
}
