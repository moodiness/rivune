package playback

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

const (
	playbackSessionIdleTTL     = 30 * time.Minute
	cleanupInactiveSessionsSQL = `
		DELETE FROM playback_sessions
		WHERE expires_at <= now() OR last_seen_at <= now() - $1::interval
		RETURNING id::text
	`
	activePlaybackSessionsSQL = `
		SELECT id::text
		FROM playback_sessions
		WHERE expires_at > now() AND last_seen_at > now() - $1::interval
	`
)

type Activity struct {
	Summary     ActivitySummary    `json:"summary"`
	Diagnostics MediaDiagnostics   `json:"diagnostics"`
	Sessions    []ActivitySession  `json:"sessions"`
	Jobs        []MediaActivityJob `json:"jobs"`
}

type ActivitySummary struct {
	ActiveSessions    int   `json:"activeSessions"`
	ActiveJobs        int   `json:"activeJobs"`
	ProcessingSlots   int   `json:"processingSlots"`
	ProcessingLimit   int   `json:"processingLimit"`
	StorageBytes      int64 `json:"storageBytes"`
	StorageLimitBytes int64 `json:"storageLimitBytes"`
}

type MediaDiagnostics struct {
	VideoEncoder    string `json:"videoEncoder"`
	HardwareToneMap bool   `json:"hardwareToneMap"`
}

type ActivityExternalIDs struct {
	IMDb string `json:"imdb,omitempty"`
	TMDB string `json:"tmdb,omitempty"`
	TVDB string `json:"tvdb,omitempty"`
}
type ActivityExternalIDMediaTypes struct {
	IMDb string `json:"imdb,omitempty"`
	TMDB string `json:"tmdb,omitempty"`
	TVDB string `json:"tvdb,omitempty"`
}

type ActivitySession struct {
	ID                   string                       `json:"id"`
	TitleID              string                       `json:"titleId,omitempty"`
	ArtworkURL           string                       `json:"artworkUrl,omitempty"`
	ExternalIDs          ActivityExternalIDs          `json:"externalIds"`
	ExternalIDMediaTypes ActivityExternalIDMediaTypes `json:"externalIdMediaTypes,omitempty"`
	Title                string                       `json:"title"`
	MediaType            string                       `json:"mediaType"`
	Mode                 string                       `json:"mode"`
	Decision             *PlaybackDecision            `json:"decision,omitempty"`
	Username             string                       `json:"username"`
	ProfileID            string                       `json:"profileId"`
	Profile              string                       `json:"profile"`
	Device               string                       `json:"device"`
	Platform             string                       `json:"platform"`
	Processing           bool                         `json:"processing"`
	PositionSeconds      int                          `json:"positionSeconds"`
	DurationSeconds      int                          `json:"durationSeconds"`
	CreatedAt            time.Time                    `json:"createdAt"`
	LastSeenAt           time.Time                    `json:"lastSeenAt"`
	ExpiresAt            time.Time                    `json:"expiresAt"`
}

type MediaActivityJob struct {
	SessionID       string    `json:"sessionId,omitempty"`
	AssetID         string    `json:"assetId"`
	Mode            string    `json:"mode"`
	State           string    `json:"state"`
	Prewarming      bool      `json:"prewarming"`
	ProgressPercent *float64  `json:"progressPercent,omitempty"`
	Speed           *float64  `json:"speed,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	LastSeenAt      time.Time `json:"lastSeenAt"`
}

type PurgeResult struct {
	SessionsRemoved int   `json:"sessionsRemoved"`
	JobsStopped     int   `json:"jobsStopped"`
	StorageBytes    int64 `json:"storageBytes"`
}

type mediaDiagnosticsProvider interface {
	VideoEncoder() string
	HardwareToneMap() bool
	ActiveProcesses() int
	ProcessLimit() int
}

func activityArtworkURL(candidates *[6]string) string {
	for _, candidate := range candidates {
		if validMediaURL(candidate) {
			return candidate
		}
	}
	return ""
}

func formatActivityTitle(
	mediaType, storedTitle, parentMediaType, parentTitle, ancestorTitle string,
	seasonOrdinal, episodeOrdinal *int,
) string {
	if mediaType != "episode" {
		if strings.TrimSpace(storedTitle) != "" {
			return storedTitle
		}
		switch mediaType {
		case "movie":
			return "Movie"
		case "series":
			return "Series"
		case "season":
			return "Season"
		default:
			return "Media"
		}
	}

	seriesTitle := strings.TrimSpace(ancestorTitle)
	if parentMediaType == "series" {
		seriesTitle = strings.TrimSpace(parentTitle)
	}
	parts := make([]string, 0, 3)
	if seriesTitle != "" {
		parts = append(parts, seriesTitle)
	}
	switch {
	case seasonOrdinal != nil && *seasonOrdinal >= 0 && episodeOrdinal != nil && *episodeOrdinal >= 0:
		parts = append(parts, fmt.Sprintf("S%02dE%02d", *seasonOrdinal, *episodeOrdinal))
	case seasonOrdinal != nil && *seasonOrdinal >= 0:
		parts = append(parts, fmt.Sprintf("S%02d", *seasonOrdinal))
	case episodeOrdinal != nil && *episodeOrdinal >= 0:
		parts = append(parts, fmt.Sprintf("E%02d", *episodeOrdinal))
	}
	if episodeTitle := strings.TrimSpace(storedTitle); episodeTitle != "" {
		parts = append(parts, episodeTitle)
	}
	if len(parts) == 0 {
		return "Episode"
	}
	return strings.Join(parts, " · ")
}

func (service *Service) Activity(ctx context.Context, principal auth.Principal) (Activity, error) {
	if !principal.IsGlobalAdministrator() {
		return Activity{}, ErrForbidden
	}
	if _, err := service.cleanupInactiveSessions(ctx); err != nil {
		return Activity{}, err
	}

	rows, err := service.pool.Query(ctx, `
		SELECT playback.id::text, COALESCE(playback.title_id, ''), COALESCE(title.display_title, ''),
		       COALESCE(parent.media_type, ''), COALESCE(parent.display_title, ''),
		       COALESCE(ancestor.display_title, ''), parent.ordinal, title.ordinal,
		       COALESCE(title.poster_url, ''), COALESCE(title.background_url, ''),
		       COALESCE(parent.poster_url, ''), COALESCE(parent.background_url, ''),
		       COALESCE(ancestor.poster_url, ''), COALESCE(ancestor.background_url, ''),
		       COALESCE(canonical_identities.imdb, identities.imdb, ''),
		       COALESCE(canonical_identities.tmdb, identities.tmdb, ''),
		       COALESCE(identities.tvdb, ''),
		       COALESCE(canonical_identities.imdb_namespace, CASE WHEN identities.imdb IS NOT NULL THEN title.media_type END, ''),
		       COALESCE(canonical_identities.tmdb_namespace, CASE WHEN identities.tmdb IS NOT NULL THEN title.media_type END, ''),
		       CASE WHEN identities.tvdb IS NOT NULL THEN title.media_type ELSE '' END,
		       playback.media_type, playback.assets,
		       COALESCE(progress.position_seconds, 0),
		       COALESCE(progress.duration_seconds, floor(NULLIF(playback.assets->0->>'durationSeconds', '')::double precision)::integer, 0),
		       users.username, playback.profile_id::text, profiles.name, devices.name,
		       devices.platform, playback.created_at, playback.last_seen_at, playback.expires_at
		FROM playback_sessions playback
		JOIN auth_sessions sessions ON sessions.id = playback.auth_session_id
		JOIN users ON users.id = sessions.user_id
		JOIN devices ON devices.id = sessions.device_id
		JOIN profiles ON profiles.id = playback.profile_id
		LEFT JOIN titles title ON title.id::text = playback.title_id
		LEFT JOIN titles parent ON parent.id = title.parent_id
		LEFT JOIN titles ancestor ON ancestor.id = parent.parent_id
		LEFT JOIN profile_progress progress
		       ON progress.profile_id = playback.profile_id
		      AND progress.title_id::text = playback.title_id
		LEFT JOIN LATERAL (
			SELECT max(external_id) FILTER (WHERE provider = 'imdb') AS imdb,
			       max(external_id) FILTER (WHERE provider = 'tmdb') AS tmdb,
			       max(external_id) FILTER (WHERE provider = 'tvdb') AS tvdb
			FROM title_external_ids
			WHERE title_id = title.id
			  AND namespace = title.media_type
			  AND provider IN ('imdb', 'tmdb', 'tvdb')
		) identities ON true
		LEFT JOIN LATERAL (
			SELECT max(external_id) FILTER (WHERE provider = 'imdb') AS imdb,
			       max(external_id) FILTER (WHERE provider = 'tmdb') AS tmdb,
			       max(namespace) FILTER (WHERE provider = 'imdb') AS imdb_namespace,
			       max(namespace) FILTER (WHERE provider = 'tmdb') AS tmdb_namespace
			FROM title_external_ids
			WHERE title_id = CASE
				WHEN title.media_type IN ('movie', 'series') THEN title.id
				WHEN parent.media_type IN ('movie', 'series') THEN parent.id
				WHEN ancestor.media_type IN ('movie', 'series') THEN ancestor.id
			END
			  AND provider IN ('imdb', 'tmdb')
			  AND namespace = CASE
				WHEN title.media_type IN ('movie', 'series') THEN title.media_type
				WHEN parent.media_type IN ('movie', 'series') THEN parent.media_type
				WHEN ancestor.media_type IN ('movie', 'series') THEN ancestor.media_type
			  END
		) canonical_identities ON true
		WHERE playback.expires_at > now()
		  AND playback.last_seen_at > now() - $1::interval
		ORDER BY playback.last_seen_at DESC, playback.created_at DESC
	`, intervalLiteral(playbackSessionIdleTTL))
	if err != nil {
		return Activity{}, fmt.Errorf("query playback activity: %w", err)
	}
	defer rows.Close()

	sessions := make([]ActivitySession, 0)
	for rows.Next() {
		var value ActivitySession
		var assetsJSON []byte
		var artworkCandidates [6]string
		var parentMediaType, parentTitle, ancestorTitle string
		var seasonOrdinal, episodeOrdinal *int
		if err := rows.Scan(
			&value.ID, &value.TitleID, &value.Title,
			&parentMediaType, &parentTitle, &ancestorTitle, &seasonOrdinal, &episodeOrdinal,
			&artworkCandidates[0], &artworkCandidates[1], &artworkCandidates[2],
			&artworkCandidates[3], &artworkCandidates[4], &artworkCandidates[5],
			&value.ExternalIDs.IMDb, &value.ExternalIDs.TMDB, &value.ExternalIDs.TVDB,
			&value.ExternalIDMediaTypes.IMDb, &value.ExternalIDMediaTypes.TMDB, &value.ExternalIDMediaTypes.TVDB,
			&value.MediaType, &assetsJSON, &value.PositionSeconds, &value.DurationSeconds,
			&value.Username, &value.ProfileID, &value.Profile, &value.Device, &value.Platform,
			&value.CreatedAt, &value.LastSeenAt, &value.ExpiresAt,
		); err != nil {
			return Activity{}, fmt.Errorf("scan playback activity: %w", err)
		}
		value.Mode, value.Decision = activityPlaybackDetails(assetsJSON)
		value.ArtworkURL = activityArtworkURL(&artworkCandidates)
		value.Title = formatActivityTitle(
			value.MediaType, value.Title, parentMediaType, parentTitle, ancestorTitle,
			seasonOrdinal, episodeOrdinal,
		)
		sessions = append(sessions, value)
	}
	if err := rows.Err(); err != nil {
		return Activity{}, fmt.Errorf("iterate playback activity: %w", err)
	}

	jobs := service.activityJobs()
	processingSessions := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		if job.SessionID != "" {
			processingSessions[job.SessionID] = struct{}{}
		}
	}
	for index := range sessions {
		_, sessions[index].Processing = processingSessions[sessions[index].ID]
	}

	diagnostics := MediaDiagnostics{VideoEncoder: "unknown"}
	processingSlots := len(jobs)
	processingLimit := 0
	if provider, ok := service.processor.(mediaDiagnosticsProvider); ok {
		diagnostics.VideoEncoder = provider.VideoEncoder()
		diagnostics.HardwareToneMap = provider.HardwareToneMap()
		processingSlots = provider.ActiveProcesses()
		processingLimit = provider.ProcessLimit()
	}
	return Activity{
		Summary: ActivitySummary{
			ActiveSessions: len(sessions), ActiveJobs: len(jobs), ProcessingSlots: processingSlots,
			ProcessingLimit: processingLimit, StorageBytes: directorySize(service.mediaOptions.TempDirectory),
			StorageLimitBytes: service.mediaOptions.MaxStorageBytes,
		},
		Diagnostics: diagnostics,
		Sessions:    sessions,
		Jobs:        jobs,
	}, nil
}

func (service *Service) StopActivitySession(ctx context.Context, principal auth.Principal, sessionID string) error {
	if !principal.IsGlobalAdministrator() {
		return ErrForbidden
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}
	command, err := service.pool.Exec(ctx, "DELETE FROM playback_sessions WHERE id::text = $1", sessionID)
	if err != nil {
		return fmt.Errorf("delete managed playback session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	service.stopHLSSession(sessionID)
	return nil
}

// Cleanup removes inactive sessions and cancels HLS work with no active session.
// It is safe to invoke repeatedly or concurrently.
func (service *Service) Cleanup(ctx context.Context) error {
	_, err := service.cleanupActivity(ctx)
	return err
}

// RunHousekeeping performs the normal stale playback cleanup and returns its
// aggregate result for the trusted operations service.
func (service *Service) RunHousekeeping(ctx context.Context) (PurgeResult, error) {
	return service.cleanupActivity(ctx)
}

func (service *Service) PurgeActivity(ctx context.Context, principal auth.Principal) (PurgeResult, error) {
	if !principal.IsGlobalAdministrator() {
		return PurgeResult{}, ErrForbidden
	}
	return service.cleanupActivity(ctx)
}

// ResetCache removes every persisted playback session, stops all processing
// jobs, clears ephemeral playback decisions, and recreates the media workspace.
// Authorization is enforced by the operations service that exposes this
// trusted maintenance primitive.
func (service *Service) ResetCache(ctx context.Context) (PurgeResult, error) {
	command, err := service.pool.Exec(ctx, "DELETE FROM playback_sessions")
	if err != nil {
		return PurgeResult{}, fmt.Errorf("delete playback sessions: %w", err)
	}
	result := PurgeResult{SessionsRemoved: int(command.RowsAffected())}
	result.JobsStopped = service.stopAllHLSJobs()
	service.references.clear()
	service.probes.clear()
	service.preparations.clear()
	service.pruneTrickplayImages(true)
	if err := os.RemoveAll(service.mediaOptions.TempDirectory); err != nil {
		return result, fmt.Errorf("clear media workspace: %w", err)
	}
	if err := os.MkdirAll(service.mediaOptions.TempDirectory, 0o700); err != nil {
		return result, fmt.Errorf("recreate media workspace: %w", err)
	}
	result.StorageBytes = directorySize(service.mediaOptions.TempDirectory)
	return result, nil
}

func (service *Service) stopAllHLSJobs() int {
	service.hlsMu.Lock()
	keys := make([]string, 0, len(service.hlsJobs))
	for key := range service.hlsJobs {
		keys = append(keys, key)
	}
	service.hlsMu.Unlock()
	for _, key := range keys {
		service.stopHLSJob(key)
	}
	return len(keys)
}

func (service *Service) cleanupActivity(ctx context.Context) (PurgeResult, error) {
	sessionsRemoved, err := service.cleanupInactiveSessions(ctx)
	if err != nil {
		return PurgeResult{}, err
	}
	jobsStopped, err := service.stopOrphanedHLSJobs(ctx)
	if err != nil {
		return PurgeResult{}, err
	}
	service.pruneTrickplayImages(false)
	return PurgeResult{
		SessionsRemoved: sessionsRemoved,
		JobsStopped:     jobsStopped,
		StorageBytes:    directorySize(service.mediaOptions.TempDirectory),
	}, nil
}

func (service *Service) cleanupInactiveSessions(ctx context.Context) (int, error) {
	rows, err := service.pool.Query(ctx, cleanupInactiveSessionsSQL, intervalLiteral(playbackSessionIdleTTL))
	if err != nil {
		return 0, fmt.Errorf("clean inactive playback sessions: %w", err)
	}
	defer rows.Close()
	identifiers := make([]string, 0)
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return 0, fmt.Errorf("scan inactive playback session: %w", err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate inactive playback sessions: %w", err)
	}
	for _, identifier := range identifiers {
		service.stopHLSSession(identifier)
	}
	return len(identifiers), nil
}

func (service *Service) stopOrphanedHLSJobs(ctx context.Context) (int, error) {
	rows, err := service.pool.Query(ctx, activePlaybackSessionsSQL, intervalLiteral(playbackSessionIdleTTL))
	if err != nil {
		return 0, fmt.Errorf("query active playback sessions: %w", err)
	}
	defer rows.Close()
	active := make(map[string]struct{})
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return 0, fmt.Errorf("scan active playback session: %w", err)
		}
		active[identifier] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate active playback sessions: %w", err)
	}
	service.hlsMu.Lock()
	keys := make([]string, 0)
	for key, job := range service.hlsJobs {
		if job.prewarming {
			continue
		}
		if _, exists := active[job.sessionID]; !exists {
			keys = append(keys, key)
		}
	}
	service.hlsMu.Unlock()
	for _, key := range keys {
		service.stopHLSJob(key)
	}
	return len(keys), nil
}

func (service *Service) activityJobs() []MediaActivityJob {
	service.hlsMu.Lock()
	jobs := make([]*hlsJob, 0, len(service.hlsJobs))
	for _, job := range service.hlsJobs {
		jobs = append(jobs, job)
	}
	service.hlsMu.Unlock()

	now := service.currentTime()
	result := make([]MediaActivityJob, 0, len(jobs))
	for _, job := range jobs {
		job.mu.RLock()
		state := "processing"
		if job.err != nil {
			state = "failed"
		} else {
			select {
			case <-job.done:
				state = "complete"
			default:
			}
		}
		activityJob := MediaActivityJob{
			SessionID: job.sessionID, AssetID: job.assetID, Mode: job.mode, State: state,
			Prewarming: job.prewarming, CreatedAt: job.createdAt, LastSeenAt: job.lastAccessed,
		}
		directory := job.directory
		durationSeconds := job.sourceDurationSeconds
		startSeconds := job.startOffsetSeconds
		job.mu.RUnlock()

		if encodedSeconds, ok := hlsPlaylistEncodedSeconds(directory); ok {
			remainingSeconds := math.Max(durationSeconds-startSeconds, 0)
			if remainingSeconds > 0 && !math.IsInf(remainingSeconds, 0) {
				progressPercent := math.Min(100, encodedSeconds/remainingSeconds*100)
				if !math.IsNaN(progressPercent) && !math.IsInf(progressPercent, 0) {
					activityJob.ProgressPercent = &progressPercent
				}
			}
			if encodedSeconds > 0 {
				elapsedSeconds := math.Max(now.Sub(activityJob.CreatedAt).Seconds(), 0)
				if elapsedSeconds > 0 {
					speed := encodedSeconds / elapsedSeconds
					if !math.IsNaN(speed) && !math.IsInf(speed, 0) {
						activityJob.Speed = &speed
					}
				}
			}
		}
		result = append(result, activityJob)
	}
	return result
}

func activityMode(encodedAssets []byte) string {
	mode, _ := activityPlaybackDetails(encodedAssets)
	return mode
}

func activityPlaybackDetails(encodedAssets []byte) (string, *PlaybackDecision) {
	var assets []storedAsset
	if json.Unmarshal(encodedAssets, &assets) != nil {
		return "unknown", nil
	}
	for _, asset := range assets {
		switch asset.Kind {
		case processingRemux, processingTranscodeAudio, processingTranscode:
			return asset.Kind, clonePlaybackDecision(asset.Decision)
		case assetKindEmbeddedSubtitle, assetKindConvertedSubtitle, assetKindBitmapSubtitle:
			continue
		default:
			return "direct", clonePlaybackDecision(asset.Decision)
		}
	}
	return "unknown", nil
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(duration/time.Second))
}
