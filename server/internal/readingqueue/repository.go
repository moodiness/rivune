package readingqueue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const cleanupExpiredOperationsSQL = `
	WITH expired AS (
		SELECT profile_id, operation_id
		FROM profile_reading_queue_operations
		WHERE expires_at <= $1
		ORDER BY expires_at, profile_id, operation_id
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	)
	DELETE FROM profile_reading_queue_operations operation
	USING expired
	WHERE operation.profile_id=expired.profile_id AND operation.operation_id=expired.operation_id
`

type storedOperation struct {
	Kind         string
	RequestHash  []byte
	Revision     int64
	ItemID       string
	Deduplicated bool
}

type repository interface {
	LockQueue(context.Context, pgx.Tx, string) (int64, error)
	List(context.Context, pgx.Tx, string) ([]Item, error)
	Operation(context.Context, pgx.Tx, string, string, time.Time) (storedOperation, bool, error)
	RegisterOperation(context.Context, pgx.Tx, string, string, string, []byte, int64, string, bool, time.Time, time.Time) error
	PruneOperations(context.Context, pgx.Tx, time.Time, int) (int64, error)
	FindIdentity(context.Context, pgx.Tx, string, AddInput) (Item, bool, error)
	Insert(context.Context, pgx.Tx, string, AddInput) (Item, error)
	Update(context.Context, pgx.Tx, string, string, UpdateInput) (Item, error)
	Delete(context.Context, pgx.Tx, string, string) (Item, error)
	Reorder(context.Context, pgx.Tx, string, []string) error
	AdvanceRevision(context.Context, pgx.Tx, string) (int64, error)
}

type postgresRepository struct{}

func (postgresRepository) LockQueue(ctx context.Context, tx pgx.Tx, profileID string) (int64, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO profile_reading_queue_states (profile_id) VALUES ($1::uuid) ON CONFLICT (profile_id) DO NOTHING`, profileID); err != nil {
		return 0, fmt.Errorf("create reading queue state: %w", err)
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM profile_reading_queue_states WHERE profile_id=$1::uuid FOR UPDATE`, profileID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("lock reading queue state: %w", err)
	}
	return revision, nil
}

func (postgresRepository) List(ctx context.Context, tx pgx.Tx, profileID string) ([]Item, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, media_type, resource_id, COALESCE(source_addon_id::text,''), COALESCE(title_id::text,''),
		       display_title, COALESCE(poster_url,''), position, created_at, updated_at
		FROM profile_reading_queue_items
		WHERE profile_id=$1::uuid
		ORDER BY position, id
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("query reading queue: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.MediaType, &item.ResourceID, &item.SourceAddonID, &item.TitleID,
			&item.Title, &item.PosterURL, &item.Position, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan reading queue item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading queue: %w", err)
	}
	return items, nil
}

func (postgresRepository) Operation(ctx context.Context, tx pgx.Tx, profileID, operationID string, now time.Time) (storedOperation, bool, error) {
	var operation storedOperation
	if err := tx.QueryRow(ctx, `
		SELECT operation, request_hash, result_revision, COALESCE(result_item_id::text,''), result_deduplicated
		FROM profile_reading_queue_operations
		WHERE profile_id=$1::uuid AND operation_id=$2::uuid AND expires_at>$3
	`, profileID, operationID, now).Scan(&operation.Kind, &operation.RequestHash, &operation.Revision, &operation.ItemID, &operation.Deduplicated); errors.Is(err, pgx.ErrNoRows) {
		return storedOperation{}, false, nil
	} else if err != nil {
		return storedOperation{}, false, fmt.Errorf("query reading queue operation: %w", err)
	}
	return operation, true, nil
}

func (postgresRepository) RegisterOperation(ctx context.Context, tx pgx.Tx, profileID, operationID, kind string, requestHash []byte, revision int64, itemID string, deduplicated bool, createdAt, expiresAt time.Time) error {
	result, err := tx.Exec(ctx, `
		INSERT INTO profile_reading_queue_operations
		(profile_id,operation_id,operation,request_hash,result_revision,result_item_id,result_deduplicated,created_at,expires_at)
		VALUES ($1::uuid,$2::uuid,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9)
		ON CONFLICT (profile_id,operation_id) DO UPDATE
		SET operation=EXCLUDED.operation,request_hash=EXCLUDED.request_hash,result_revision=EXCLUDED.result_revision,
		    result_item_id=EXCLUDED.result_item_id,result_deduplicated=EXCLUDED.result_deduplicated,
		    created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at
		WHERE profile_reading_queue_operations.expires_at <= EXCLUDED.created_at
	`, profileID, operationID, kind, requestHash, revision, itemID, deduplicated, createdAt, expiresAt)
	if err != nil {
		return fmt.Errorf("store reading queue operation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrOperationConflict
	}
	return nil
}

func (postgresRepository) PruneOperations(ctx context.Context, tx pgx.Tx, now time.Time, limit int) (int64, error) {
	result, err := tx.Exec(ctx, cleanupExpiredOperationsSQL, now, limit)
	if err != nil {
		return 0, fmt.Errorf("prune expired reading queue operations: %w", err)
	}
	return result.RowsAffected(), nil
}

func (postgresRepository) FindIdentity(ctx context.Context, tx pgx.Tx, profileID string, input AddInput) (Item, bool, error) {
	var item Item
	err := tx.QueryRow(ctx, `
		SELECT id::text, media_type, resource_id, COALESCE(source_addon_id::text,''), COALESCE(title_id::text,''),
		       display_title, COALESCE(poster_url,''), position, created_at, updated_at
		FROM profile_reading_queue_items
		WHERE profile_id=$1::uuid AND media_type=$2 AND resource_id=$3
		  AND source_addon_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid
	`, profileID, input.MediaType, input.ResourceID, input.SourceAddonID).Scan(
		&item.ID, &item.MediaType, &item.ResourceID, &item.SourceAddonID, &item.TitleID,
		&item.Title, &item.PosterURL, &item.Position, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, fmt.Errorf("query duplicate reading queue item: %w", err)
	}
	return item, true, nil
}

func (postgresRepository) Insert(ctx context.Context, tx pgx.Tx, profileID string, input AddInput) (Item, error) {
	var item Item
	err := tx.QueryRow(ctx, `
		INSERT INTO profile_reading_queue_items
		(profile_id,media_type,resource_id,source_addon_id,title_id,display_title,poster_url,position)
		SELECT $1::uuid,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,NULLIF($7,''),count(*)
		FROM profile_reading_queue_items WHERE profile_id=$1::uuid
		RETURNING id::text,media_type,resource_id,COALESCE(source_addon_id::text,''),COALESCE(title_id::text,''),
		          display_title,COALESCE(poster_url,''),position,created_at,updated_at
	`, profileID, input.MediaType, input.ResourceID, input.SourceAddonID, input.TitleID, input.Title, input.PosterURL).Scan(
		&item.ID, &item.MediaType, &item.ResourceID, &item.SourceAddonID, &item.TitleID,
		&item.Title, &item.PosterURL, &item.Position, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return Item{}, fmt.Errorf("insert reading queue item: %w", err)
	}
	return item, nil
}

func (postgresRepository) Update(ctx context.Context, tx pgx.Tx, profileID, itemID string, input UpdateInput) (Item, error) {
	var item Item
	err := tx.QueryRow(ctx, `
		UPDATE profile_reading_queue_items
		SET display_title=$3,poster_url=NULLIF($4,''),updated_at=clock_timestamp()
		WHERE profile_id=$1::uuid AND id=$2::uuid
		RETURNING id::text,media_type,resource_id,COALESCE(source_addon_id::text,''),COALESCE(title_id::text,''),
		          display_title,COALESCE(poster_url,''),position,created_at,updated_at
	`, profileID, itemID, input.Title, input.PosterURL).Scan(
		&item.ID, &item.MediaType, &item.ResourceID, &item.SourceAddonID, &item.TitleID,
		&item.Title, &item.PosterURL, &item.Position, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("update reading queue item: %w", err)
	}
	return item, nil
}

func (postgresRepository) Delete(ctx context.Context, tx pgx.Tx, profileID, itemID string) (Item, error) {
	var item Item
	err := tx.QueryRow(ctx, `
		DELETE FROM profile_reading_queue_items
		WHERE profile_id=$1::uuid AND id=$2::uuid
		RETURNING id::text,media_type,resource_id,COALESCE(source_addon_id::text,''),COALESCE(title_id::text,''),
		          display_title,COALESCE(poster_url,''),position,created_at,updated_at
	`, profileID, itemID).Scan(
		&item.ID, &item.MediaType, &item.ResourceID, &item.SourceAddonID, &item.TitleID,
		&item.Title, &item.PosterURL, &item.Position, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("delete reading queue item: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE profile_reading_queue_items SET position=position-1,updated_at=clock_timestamp() WHERE profile_id=$1::uuid AND position>$2`, profileID, item.Position); err != nil {
		return Item{}, fmt.Errorf("compact reading queue: %w", err)
	}
	return item, nil
}

func (postgresRepository) Reorder(ctx context.Context, tx pgx.Tx, profileID string, itemIDs []string) error {
	_, err := tx.Exec(ctx, `
		WITH requested AS (
			SELECT id, (ordinality-1)::integer AS position
			FROM unnest($2::uuid[]) WITH ORDINALITY AS input(id,ordinality)
		)
		UPDATE profile_reading_queue_items item
		SET position=requested.position,updated_at=clock_timestamp()
		FROM requested
		WHERE item.profile_id=$1::uuid AND item.id=requested.id
	`, profileID, itemIDs)
	if err != nil {
		return fmt.Errorf("reorder reading queue: %w", err)
	}
	return nil
}

func (postgresRepository) AdvanceRevision(ctx context.Context, tx pgx.Tx, profileID string) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, `UPDATE profile_reading_queue_states SET revision=revision+1,updated_at=clock_timestamp() WHERE profile_id=$1::uuid RETURNING revision`, profileID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("advance reading queue revision: %w", err)
	}
	return revision, nil
}
