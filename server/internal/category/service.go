package category

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal Actor) ([]Category, error) {
	if !principal.GlobalAdministrator {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, categorySelect+` ORDER BY c.position, c.id`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	result := make([]Category, 0)
	for rows.Next() {
		item, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, principal Actor, input CreateInput) (Category, error) {
	if !principal.GlobalAdministrator {
		return Category{}, ErrForbidden
	}
	name, normalized, err := normalizeAndValidateName(input.Name)
	if err != nil {
		return Category{}, err
	}
	description, err := normalizeOptionalText(input.Description, 500, "description")
	if err != nil {
		return Category{}, err
	}
	color, err := normalizeColor(input.Color)
	if err != nil {
		return Category{}, err
	}
	icon, err := normalizeIcon(input.Icon)
	if err != nil {
		return Category{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Category{}, fmt.Errorf("begin category creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Category{}, fmt.Errorf("lock categories: %w", err)
	}
	var item Category
	err = tx.QueryRow(ctx, `
		INSERT INTO access_categories (name, normalized_name, description, color, icon, position)
		VALUES ($1, $2, $3, $4, $5, (SELECT COALESCE(max(position) + 1, 0) FROM access_categories))
		RETURNING id::text, name, description, color, icon, position, is_default, created_at, updated_at
	`, name, normalized, description, color, icon).Scan(&item.ID, &item.Name, &item.Description,
		&item.Color, &item.Icon, &item.Position, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Category{}, ErrConflict
		}
		return Category{}, fmt.Errorf("create category: %w", err)
	}
	if err := audit(ctx, tx, principal.UserID, "category.created", "category", item.ID, nil, &item.ID, `{}`); err != nil {
		return Category{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Category{}, fmt.Errorf("commit category creation: %w", err)
	}
	return item, nil
}

func (s *Service) Update(ctx context.Context, principal Actor, categoryID string, input UpdateInput) (Category, error) {
	if !principal.GlobalAdministrator {
		return Category{}, ErrForbidden
	}
	if !validUUID(categoryID) {
		return Category{}, invalid("categoryId must be a valid category identifier")
	}
	categoryID = canonicalUUID(categoryID)
	if input.Name == nil && !input.DescriptionSet && !input.ColorSet && !input.IconSet && !input.MakeDefault {
		return Category{}, invalid("at least one field must be provided")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Category{}, fmt.Errorf("begin category update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.MakeDefault {
		if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return Category{}, fmt.Errorf("lock categories for default update: %w", err)
		}
	}
	var item Category
	var normalized string
	err = tx.QueryRow(ctx, `
		SELECT c.id::text, c.name, c.normalized_name, c.description, c.color, c.icon, c.position, c.is_default,
		       (SELECT count(*) FROM profiles p WHERE p.category_id = c.id),
		       (SELECT count(*) FROM devices d WHERE d.category_id = c.id),
		       c.created_at, c.updated_at
		FROM access_categories c WHERE c.id = $1::uuid FOR UPDATE OF c
	`, categoryID).Scan(&item.ID, &item.Name, &normalized, &item.Description, &item.Color, &item.Icon,
		&item.Position, &item.IsDefault, &item.ProfileCount, &item.DeviceCount, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("query category for update: %w", err)
	}
	if input.Name != nil {
		item.Name, normalized, err = normalizeAndValidateName(*input.Name)
		if err != nil {
			return Category{}, err
		}
	}
	if input.DescriptionSet {
		item.Description, err = normalizeOptionalText(input.Description, 500, "description")
		if err != nil {
			return Category{}, err
		}
	}
	if input.ColorSet {
		item.Color, err = normalizeColor(input.Color)
		if err != nil {
			return Category{}, err
		}
	}
	if input.IconSet {
		item.Icon, err = normalizeIcon(input.Icon)
		if err != nil {
			return Category{}, err
		}
	}
	if input.MakeDefault && !item.IsDefault {
		if _, err := tx.Exec(ctx, `
			UPDATE access_categories
			SET is_default = false, updated_at = now()
			WHERE is_default AND id <> $1::uuid
		`, item.ID); err != nil {
			return Category{}, fmt.Errorf("clear previous default category: %w", err)
		}
		item.IsDefault = true
	}
	err = tx.QueryRow(ctx, `
		UPDATE access_categories
		SET name = $2, normalized_name = $3, description = $4, color = $5, icon = $6,
		    is_default = $7, updated_at = now()
		WHERE id = $1::uuid RETURNING updated_at
	`, item.ID, item.Name, normalized, item.Description, item.Color, item.Icon, item.IsDefault).Scan(&item.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return Category{}, ErrConflict
		}
		return Category{}, fmt.Errorf("update category: %w", err)
	}
	if err := audit(ctx, tx, principal.UserID, "category.updated", "category", item.ID, &item.ID, &item.ID, `{}`); err != nil {
		return Category{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Category{}, fmt.Errorf("commit category update: %w", err)
	}
	return item, nil
}

func (s *Service) Reorder(ctx context.Context, principal Actor, categoryIDs []string) ([]Category, error) {
	if !principal.GlobalAdministrator {
		return nil, ErrForbidden
	}
	if len(categoryIDs) == 0 {
		return nil, invalid("categoryIds must contain every category")
	}
	seen := make(map[string]struct{}, len(categoryIDs))
	for index, id := range categoryIDs {
		if !validUUID(id) {
			return nil, invalid("categoryIds must contain valid category identifiers")
		}
		id = canonicalUUID(id)
		categoryIDs[index] = id
		if _, exists := seen[id]; exists {
			return nil, invalid("categoryIds must not contain duplicates")
		}
		seen[id] = struct{}{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin category reorder: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE access_categories IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, fmt.Errorf("lock categories for reorder: %w", err)
	}
	var total, matched int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM access_categories`).Scan(&total); err != nil {
		return nil, fmt.Errorf("count categories: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM access_categories WHERE id = ANY($1::uuid[])`, categoryIDs).Scan(&matched); err != nil {
		return nil, fmt.Errorf("validate category order: %w", err)
	}
	if total != len(categoryIDs) || matched != len(categoryIDs) {
		return nil, invalid("categoryIds must contain every category exactly once")
	}
	if _, err := tx.Exec(ctx, `
		WITH requested AS (
			SELECT id, ordinality - 1 AS new_position
			FROM unnest($2::uuid[]) WITH ORDINALITY AS ordered(id, ordinality)
		)
		INSERT INTO access_category_audit_events
			(actor_user_id, action, entity_type, entity_id, old_category_id, new_category_id, details)
		SELECT $1::uuid, 'category.reordered', 'category', c.id, c.id, c.id,
		       jsonb_build_object('oldPosition', c.position, 'newPosition', requested.new_position)
		FROM access_categories c JOIN requested ON requested.id = c.id
		WHERE c.position <> requested.new_position
	`, principal.UserID, categoryIDs); err != nil {
		return nil, fmt.Errorf("audit category reorder: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		WITH requested AS (
			SELECT id, ordinality - 1 AS new_position
			FROM unnest($1::uuid[]) WITH ORDINALITY AS ordered(id, ordinality)
		)
		UPDATE access_categories c
		SET position = requested.new_position, updated_at = now()
		FROM requested WHERE requested.id = c.id
	`, categoryIDs); err != nil {
		return nil, fmt.Errorf("reorder categories: %w", err)
	}
	rows, err := tx.Query(ctx, categorySelect+` ORDER BY c.position, c.id`)
	if err != nil {
		return nil, fmt.Errorf("list reordered categories: %w", err)
	}
	result := make([]Category, 0, len(categoryIDs))
	for rows.Next() {
		item, err := scanCategory(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan reordered category: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate reordered categories: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit category reorder: %w", err)
	}
	return result, nil
}
