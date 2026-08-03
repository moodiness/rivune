package fanart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type artworkSnapshot struct {
	PosterURL       string         `json:"posterUrl,omitempty"`
	BackdropURL     string         `json:"backdropUrl,omitempty"`
	LogoURL         string         `json:"logoUrl,omitempty"`
	SeasonPosters   map[int]string `json:"seasonPosters,omitempty"`
	SeasonBackdrops map[int]string `json:"seasonBackdrops,omitempty"`
}

type artworkResponseCache interface {
	load(context.Context, artworkCacheKey) (artworkSnapshot, bool, time.Time, bool, error)
	store(context.Context, artworkCacheKey, artworkSnapshot, bool, time.Time) error
}

type postgresArtworkResponseCache struct {
	pool *pgxpool.Pool
}

func (cache *postgresArtworkResponseCache) load(ctx context.Context, key artworkCacheKey) (artworkSnapshot, bool, time.Time, bool, error) {
	var payload []byte
	var available bool
	var expiresAt time.Time
	err := cache.pool.QueryRow(ctx, `
		SELECT payload, available, expires_at
		FROM fanart_response_cache
		WHERE resource_type = $1 AND external_id = $2 AND language = $3 AND expires_at > now()
	`, key.resourceType, key.externalID, key.language).Scan(&payload, &available, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return artworkSnapshot{}, false, time.Time{}, false, nil
	}
	if err != nil {
		return artworkSnapshot{}, false, time.Time{}, false, fmt.Errorf("query Fanart response cache: %w", err)
	}
	var snapshot artworkSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return artworkSnapshot{}, false, time.Time{}, false, fmt.Errorf("decode Fanart response cache: %w", err)
	}
	return snapshot, available, expiresAt, true, nil
}

func (cache *postgresArtworkResponseCache) store(ctx context.Context, key artworkCacheKey, snapshot artworkSnapshot, available bool, expiresAt time.Time) error {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode Fanart response cache: %w", err)
	}
	if _, err := cache.pool.Exec(ctx, `
		INSERT INTO fanart_response_cache (resource_type, external_id, language, payload, available, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (resource_type, external_id, language) DO UPDATE
		SET payload = EXCLUDED.payload,
		    available = EXCLUDED.available,
		    expires_at = EXCLUDED.expires_at,
		    updated_at = now()
	`, key.resourceType, key.externalID, key.language, payload, available, expiresAt); err != nil {
		return fmt.Errorf("store Fanart response cache: %w", err)
	}
	return nil
}
