package fanart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	fanartCacheMaximumEntries  = 10_000
	fanartCachePruneBatch      = 256
	fanartCachePruneEveryStore = 32

	pruneFanartCacheSQL = `
		WITH expired AS (
			SELECT resource_type, external_id, language
			FROM fanart_response_cache
			WHERE expires_at <= now()
			ORDER BY expires_at, resource_type, external_id, language
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		),
		overflow AS (
			SELECT cached.resource_type, cached.external_id, cached.language
			FROM fanart_response_cache cached
			WHERE NOT EXISTS (
				SELECT 1
				FROM expired
				WHERE expired.resource_type = cached.resource_type
				  AND expired.external_id = cached.external_id
				  AND expired.language = cached.language
			)
			ORDER BY cached.updated_at DESC, cached.resource_type, cached.external_id, cached.language
			LIMIT GREATEST($1 - (SELECT count(*) FROM expired), 0)
			OFFSET $2
			FOR UPDATE OF cached SKIP LOCKED
		),
		victims AS (
			SELECT resource_type, external_id, language FROM expired
			UNION ALL
			SELECT resource_type, external_id, language FROM overflow
		)
		DELETE FROM fanart_response_cache cached
		USING victims
		WHERE cached.resource_type = victims.resource_type
		  AND cached.external_id = victims.external_id
		  AND cached.language = victims.language
	`
)

type artworkSnapshot struct {
	PosterURL     string         `json:"posterUrl,omitempty"`
	BackdropURL   string         `json:"backdropUrl,omitempty"`
	LogoURL       string         `json:"logoUrl,omitempty"`
	BannerURL     string         `json:"bannerUrl,omitempty"`
	ArtURL        string         `json:"artUrl,omitempty"`
	SeasonPosters map[int]string `json:"seasonPosters,omitempty"`
}

type artworkResponseCache interface {
	load(context.Context, artworkCacheKey) (artworkSnapshot, bool, time.Time, bool, error)
	store(context.Context, artworkCacheKey, artworkSnapshot, bool, time.Time) error
}

type postgresArtworkResponseCache struct {
	pool        *pgxpool.Pool
	cacheStores atomic.Uint64
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
	if cache.shouldPrune() {
		if _, err := cache.pool.Exec(ctx, pruneFanartCacheSQL, fanartCachePruneBatch, fanartCacheMaximumEntries); err != nil {
			return fmt.Errorf("prune Fanart response cache: %w", err)
		}
	}
	return nil
}

func (cache *postgresArtworkResponseCache) shouldPrune() bool {
	return cache.cacheStores.Add(1)%fanartCachePruneEveryStore == 1
}
