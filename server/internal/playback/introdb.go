package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/moodiness/rivune/server/internal/auth"
	"github.com/moodiness/rivune/server/internal/netguard"
	"github.com/moodiness/rivune/server/internal/requestwork"
)

const (
	introDBDefaultBaseURL       = "https://api.introdb.app"
	introDBCacheTTL             = 24 * time.Hour
	introDBMissCacheTTL         = 6 * time.Hour
	introDBMaxBodyBytes         = 64 << 10
	introDBMaxMarkerTime        = 24 * time.Hour / time.Second
	introDBCachePruneEveryStore = 32
	introDBCachePruneBatch      = 128

	pruneIntroDBCacheSQL = `
		WITH expired AS (
			SELECT imdb_id, season_number, episode_number
			FROM introdb_segment_cache
			WHERE expires_at <= now()
			ORDER BY expires_at, imdb_id, season_number, episode_number
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM introdb_segment_cache cached
		USING expired
		WHERE cached.imdb_id = expired.imdb_id
		  AND cached.season_number = expired.season_number
		  AND cached.episode_number = expired.episode_number
	`
	storeIntroDBCacheSQL = `
		INSERT INTO introdb_segment_cache (imdb_id, season_number, episode_number, segments, expires_at, updated_at)
		VALUES ($1, $2, $3, $4::jsonb, now() + make_interval(secs => $5), now())
		ON CONFLICT (imdb_id, season_number, episode_number) DO UPDATE
		SET segments = EXCLUDED.segments, expires_at = EXCLUDED.expires_at, updated_at = now()
	`
)

var introDBIMDBIDPattern = regexp.MustCompile(`^tt[0-9]{7,8}$`)

type MarkerType string

const (
	MarkerTypeIntro MarkerType = "intro"
	MarkerTypeRecap MarkerType = "recap"
	MarkerTypeOutro MarkerType = "outro"
)

type MarkerInput struct {
	IMDBID       string
	Season       int
	Episode      int
	IncludeIntro bool
	IncludeRecap bool
	IncludeOutro bool
}

type Marker struct {
	Type            MarkerType `json:"type"`
	StartSeconds    float64    `json:"startSeconds"`
	EndSeconds      float64    `json:"endSeconds"`
	Confidence      float64    `json:"confidence"`
	SubmissionCount int        `json:"submissionCount"`
}

type MarkerList struct {
	Markers []Marker `json:"markers"`
}

type introDBSegment struct {
	StartMS         int64   `json:"start_ms"`
	EndMS           int64   `json:"end_ms"`
	StartSeconds    float64 `json:"start_sec"`
	EndSeconds      float64 `json:"end_sec"`
	Confidence      float64 `json:"confidence"`
	SubmissionCount int     `json:"submission_count"`
}

type introDBResponse struct {
	IMDBID  string          `json:"imdb_id"`
	Season  int             `json:"season"`
	Episode int             `json:"episode"`
	Intro   *introDBSegment `json:"intro"`
	Recap   *introDBSegment `json:"recap"`
	Outro   *introDBSegment `json:"outro"`
}

func (service *Service) Markers(ctx context.Context, principal auth.Principal, input MarkerInput) (MarkerList, error) {
	input.IMDBID = strings.TrimSpace(input.IMDBID)
	tx, err := service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return MarkerList{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !introDBIMDBIDPattern.MatchString(input.IMDBID) || input.Season < 1 || input.Season > 2_147_483_647 || input.Episode < 1 || input.Episode > 2_147_483_647 {
		return MarkerList{}, ErrInvalidInput
	}
	if !input.IncludeIntro && !input.IncludeRecap && !input.IncludeOutro {
		result := MarkerList{Markers: []Marker{}}
		if err := tx.Commit(ctx); err != nil {
			return MarkerList{}, fmt.Errorf("commit marker profile authorization: %w", err)
		}
		return result, nil
	}

	markers, found, cacheErr := service.cachedIntroDBMarkers(ctx, tx, input)
	if cacheErr == nil && found {
		result := MarkerList{Markers: filterMarkers(markers, input)}
		if err := tx.Commit(ctx); err != nil {
			return MarkerList{}, fmt.Errorf("commit marker profile authorization: %w", err)
		}
		return result, nil
	}
	if cacheErr == nil {
		if err := tx.Commit(ctx); err != nil {
			return MarkerList{}, fmt.Errorf("commit marker profile authorization: %w", err)
		}
	} else {
		_ = tx.Rollback(ctx)
	}

	markers, found, err = service.fetchIntroDBMarkers(ctx, input)
	if err != nil {
		result := MarkerList{Markers: []Marker{}}
		return result, service.commitAuthorizedProfileBoundary(ctx, principal)
	}
	result := MarkerList{Markers: filterMarkers(markers, input)}
	tx, err = service.beginAuthorizedProfileTx(ctx, principal)
	if err != nil {
		return MarkerList{}, err
	}
	if err := service.cacheIntroDBMarkers(ctx, tx, input, markers, found); err == nil {
		if err := tx.Commit(ctx); err == nil {
			return result, nil
		}
	}
	_ = tx.Rollback(ctx)
	return result, service.commitAuthorizedProfileBoundary(ctx, principal)
}

func (service *Service) cachedIntroDBMarkers(ctx context.Context, tx playbackProfileTransaction, input MarkerInput) ([]Marker, bool, error) {
	if tx == nil {
		return nil, false, nil
	}
	var encoded []byte
	err := tx.QueryRow(ctx, `
		SELECT segments
		FROM introdb_segment_cache
		WHERE imdb_id = $1 AND season_number = $2 AND episode_number = $3 AND expires_at > now()
	`, input.IMDBID, input.Season, input.Episode).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read IntroDB cache: %w", err)
	}
	var markers []Marker
	if err := json.Unmarshal(encoded, &markers); err != nil {
		return nil, false, fmt.Errorf("decode IntroDB cache: %w", err)
	}
	if markers == nil {
		markers = []Marker{}
	}
	return markers, true, nil
}

func (service *Service) cacheIntroDBMarkers(ctx context.Context, tx playbackProfileTransaction, input MarkerInput, markers []Marker, providerFound bool) error {
	if tx == nil {
		return nil
	}
	encoded, err := json.Marshal(markers)
	if err != nil {
		return fmt.Errorf("encode IntroDB cache: %w", err)
	}
	ttl := introDBCacheTTL
	if !providerFound {
		ttl = introDBMissCacheTTL
	}
	if service.shouldPruneIntroDBCache() {
		if _, err := tx.Exec(ctx, pruneIntroDBCacheSQL, introDBCachePruneBatch); err != nil {
			return fmt.Errorf("prune IntroDB cache: %w", err)
		}
	}
	_, err = tx.Exec(ctx, storeIntroDBCacheSQL, input.IMDBID, input.Season, input.Episode, encoded, int(ttl/time.Second))
	if err != nil {
		return fmt.Errorf("write IntroDB cache: %w", err)
	}
	return nil
}

func (service *Service) shouldPruneIntroDBCache() bool {
	return service.introDBCacheStores.Add(1)%introDBCachePruneEveryStore == 1
}

func (service *Service) fetchIntroDBMarkers(ctx context.Context, input MarkerInput) ([]Marker, bool, error) {
	endpoint, err := url.Parse(service.introDBBaseURL + "/segments")
	if err != nil {
		return nil, false, fmt.Errorf("build IntroDB URL: %w", netguard.SanitizeURLError(err))
	}
	query := endpoint.Query()
	query.Set("imdb_id", input.IMDBID)
	query.Set("season", strconv.Itoa(input.Season))
	query.Set("episode", strconv.Itoa(input.Episode))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("create IntroDB request: %w", netguard.SanitizeURLError(err))
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Rivune/1")
	started := requestwork.Now()
	requestwork.BeginOutbound(ctx, started)
	response, err := service.introDBClient.Do(request)
	if err != nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
		return nil, false, fmt.Errorf("request IntroDB segments: %w", netguard.SanitizeURLError(err))
	}
	if response.Body == nil {
		requestwork.EndOutbound(ctx, requestwork.Now(), 0)
	} else {
		response.Body = requestwork.ObserveBody(ctx, response.Body)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return []Marker{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, false, fmt.Errorf("IntroDB returned status %d", response.StatusCode)
	}
	var payload introDBResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, introDBMaxBodyBytes+1))
	if err := decoder.Decode(&payload); err != nil {
		return nil, false, fmt.Errorf("decode IntroDB segments: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, false, errors.New("IntroDB response contains trailing data")
	}
	if payload.IMDBID != input.IMDBID || payload.Season != input.Season || payload.Episode != input.Episode {
		return nil, false, errors.New("IntroDB response identity mismatch")
	}
	return normalizeIntroDBMarkers(payload), true, nil
}

func normalizeIntroDBMarkers(payload introDBResponse) []Marker {
	candidates := []struct {
		markerType MarkerType
		segment    *introDBSegment
	}{
		{MarkerTypeIntro, payload.Intro},
		{MarkerTypeRecap, payload.Recap},
		{MarkerTypeOutro, payload.Outro},
	}
	markers := make([]Marker, 0, len(candidates))
	for _, candidate := range candidates {
		segment := candidate.segment
		if segment == nil || !validIntroDBSegment(*segment) {
			continue
		}
		markers = append(markers, Marker{
			Type: candidate.markerType, StartSeconds: segment.StartSeconds, EndSeconds: segment.EndSeconds,
			Confidence: segment.Confidence, SubmissionCount: segment.SubmissionCount,
		})
	}
	sort.Slice(markers, func(left, right int) bool {
		if markers[left].StartSeconds == markers[right].StartSeconds {
			return markers[left].EndSeconds < markers[right].EndSeconds
		}
		return markers[left].StartSeconds < markers[right].StartSeconds
	})
	validated := markers[:0]
	for _, marker := range markers {
		if len(validated) > 0 && marker.StartSeconds < validated[len(validated)-1].EndSeconds {
			continue
		}
		validated = append(validated, marker)
	}
	return validated
}

func validIntroDBSegment(segment introDBSegment) bool {
	if math.IsNaN(segment.StartSeconds) || math.IsInf(segment.StartSeconds, 0) || math.IsNaN(segment.EndSeconds) || math.IsInf(segment.EndSeconds, 0) ||
		segment.StartSeconds < 0 || segment.EndSeconds <= segment.StartSeconds || segment.EndSeconds > float64(introDBMaxMarkerTime) ||
		segment.Confidence < 0 || segment.Confidence > 1 || segment.SubmissionCount < 1 {
		return false
	}
	return math.Abs(float64(segment.StartMS)-segment.StartSeconds*1000) <= 1 &&
		math.Abs(float64(segment.EndMS)-segment.EndSeconds*1000) <= 1
}

func filterMarkers(markers []Marker, input MarkerInput) []Marker {
	filtered := make([]Marker, 0, len(markers))
	for _, marker := range markers {
		if marker.Type == MarkerTypeIntro && input.IncludeIntro || marker.Type == MarkerTypeRecap && input.IncludeRecap || marker.Type == MarkerTypeOutro && input.IncludeOutro {
			filtered = append(filtered, marker)
		}
	}
	return filtered
}
