package playback

import (
	"context"
	"encoding/json"
	"fmt"
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

type ActivitySession struct {
	ID         string    `json:"id"`
	TitleID    string    `json:"titleId,omitempty"`
	Title      string    `json:"title"`
	MediaType  string    `json:"mediaType"`
	Mode       string    `json:"mode"`
	Username   string    `json:"username"`
	ProfileID  string    `json:"profileId"`
	Profile    string    `json:"profile"`
	Device     string    `json:"device"`
	Platform   string    `json:"platform"`
	Processing bool      `json:"processing"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type MediaActivityJob struct {
	SessionID  string    `json:"sessionId,omitempty"`
	AssetID    string    `json:"assetId"`
	Mode       string    `json:"mode"`
	State      string    `json:"state"`
	Prewarming bool      `json:"prewarming"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
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

func (service *Service) Activity(ctx context.Context, principal auth.Principal) (Activity, error) {
	if principal.Role != "admin" {
		return Activity{}, ErrForbidden
	}
	if _, err := service.cleanupInactiveSessions(ctx); err != nil {
		return Activity{}, err
	}

	rows, err := service.pool.Query(ctx, `
		SELECT playback.id::text, COALESCE(playback.title_id, ''), COALESCE(title.display_title, ''),
		       playback.media_type, playback.assets, users.username, playback.profile_id::text,
		       profiles.name, devices.name, devices.platform, playback.created_at,
		       playback.last_seen_at, playback.expires_at
		FROM playback_sessions playback
		JOIN auth_sessions sessions ON sessions.id = playback.auth_session_id
		JOIN users ON users.id = sessions.user_id
		JOIN devices ON devices.id = sessions.device_id
		JOIN profiles ON profiles.id = playback.profile_id
		LEFT JOIN titles title ON title.id::text = playback.title_id
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
		if err := rows.Scan(
			&value.ID, &value.TitleID, &value.Title, &value.MediaType, &assetsJSON,
			&value.Username, &value.ProfileID, &value.Profile, &value.Device, &value.Platform,
			&value.CreatedAt, &value.LastSeenAt, &value.ExpiresAt,
		); err != nil {
			return Activity{}, fmt.Errorf("scan playback activity: %w", err)
		}
		value.Mode = activityMode(assetsJSON)
		if value.Title == "" {
			value.Title = value.TitleID
		}
		if value.Title == "" {
			value.Title = value.MediaType
		}
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
	if principal.Role != "admin" {
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

func (service *Service) PurgeActivity(ctx context.Context, principal auth.Principal) (PurgeResult, error) {
	if principal.Role != "admin" {
		return PurgeResult{}, ErrForbidden
	}
	return service.cleanupActivity(ctx)
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
		result = append(result, MediaActivityJob{
			SessionID: job.sessionID, AssetID: job.assetID, Mode: job.mode, State: state,
			Prewarming: job.prewarming, CreatedAt: job.createdAt, LastSeenAt: job.lastAccessed,
		})
		job.mu.RUnlock()
	}
	return result
}

func activityMode(encodedAssets []byte) string {
	var assets []storedAsset
	if json.Unmarshal(encodedAssets, &assets) != nil {
		return "unknown"
	}
	for _, asset := range assets {
		switch asset.Kind {
		case processingRemux, processingTranscodeAudio, processingTranscode:
			return asset.Kind
		case assetKindEmbeddedSubtitle, assetKindConvertedSubtitle:
			continue
		default:
			return "direct"
		}
	}
	return "unknown"
}

func intervalLiteral(duration time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(duration/time.Second))
}
