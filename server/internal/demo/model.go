package demo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	CookieName = "rivune_demo"
	APIPrefix  = "/api/v1"

	AlexProfileID  = "d0000000-0000-4000-8000-000000000001"
	KidsProfileID  = "d0000000-0000-4000-8000-000000000002"
	DemoUserID     = "d0000000-0000-4000-8000-000000000003"
	DemoDeviceID   = "d0000000-0000-4000-8000-000000000004"
	DemoCategoryID = "d0000000-0000-4000-8000-000000000005"

	SignalMovieID     = "d1000000-0000-4000-8000-000000000001"
	LighthouseID      = "d1000000-0000-4000-8000-000000000002"
	OpenSkiesID       = "d1000000-0000-4000-8000-000000000003"
	OrbitSeriesID     = "d2000000-0000-4000-8000-000000000001"
	OrbitSeasonOneID  = "d2100000-0000-4000-8000-000000000001"
	OrbitSeasonTwoID  = "d2100000-0000-4000-8000-000000000002"
	OrbitEpisodeOne   = "d2200000-0000-4000-8000-000000000001"
	OrbitEpisodeTwo   = "d2200000-0000-4000-8000-000000000002"
	OrbitEpisodeThree = "d2200000-0000-4000-8000-000000000003"
	OrbitEpisodeFour  = "d2200000-0000-4000-8000-000000000004"
	WorldNewsID       = "d3000000-0000-4000-8000-000000000001"
	CultureLiveID     = "d3000000-0000-4000-8000-000000000002"

	HomeCollectionID  = "d4000000-0000-4000-8000-000000000001"
	SpotlightFolderID = "d4100000-0000-4000-8000-000000000001"
	SeriesFolderID    = "d4100000-0000-4000-8000-000000000002"
	LiveFolderID      = "d4100000-0000-4000-8000-000000000003"
)

const (
	defaultTTL                       = 8 * time.Hour
	defaultMaxSessions               = 64
	defaultMaxSessionsPerSource      = 8
	playbackStateTTL                 = 30 * time.Minute
	maxPlaybackEntriesPerSession     = 16
	maxPlaybackEntriesAcrossSessions = 256
)

// SetupAdmission is the only database-facing capability demo handlers receive.
type SetupAdmission interface {
	AcquireSetupPending(context.Context) (func(), error)
	AdmitDemoSession(context.Context, [sha256.Size]byte, string, time.Time, time.Time, int, int, func() error) (string, func(), error)
	ReleaseDemoSession(context.Context, string) (func(), error)
}

type Options struct {
	TTL                  time.Duration
	MaxSessions          int
	MaxSessionsPerSource int
	Now                  func() time.Time
	Random               io.Reader
}

type Service struct {
	admission    SetupAdmission
	ttl          time.Duration
	max          int
	maxPerSource int
	now          func() time.Time
	random       io.Reader
	randomMu     sync.Mutex

	mu            sync.Mutex
	sessions      map[[sha256.Size]byte]*session
	playbackTotal int
	disabled      bool
}

type session struct {
	mu          sync.Mutex
	id          string
	admissionID string
	createdAt   time.Time
	expiresAt   time.Time
	state       sessionState
}

type sessionState struct {
	activeProfileID string
	profiles        map[string]*profileState
	playback        map[string]playbackState
	playbackCounter uint64
}

type profileState struct {
	library   map[string]bool
	progress  map[string]progressState
	dismissed map[string]bool
}

type progressState struct {
	PositionSeconds int       `json:"positionSeconds"`
	DurationSeconds int       `json:"durationSeconds"`
	Completed       bool      `json:"completed"`
	Version         int64     `json:"version"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type playbackState struct {
	TitleID   string
	CreatedAt time.Time
}

func New(admission SetupAdmission, options Options) *Service {
	if options.TTL <= 0 {
		options.TTL = defaultTTL
	}
	if options.MaxSessions <= 0 {
		options.MaxSessions = defaultMaxSessions
	}
	if options.MaxSessionsPerSource <= 0 {
		options.MaxSessionsPerSource = defaultMaxSessionsPerSource
	}
	if options.MaxSessionsPerSource > options.MaxSessions {
		options.MaxSessionsPerSource = options.MaxSessions
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Service{
		admission:    admission,
		ttl:          options.TTL,
		max:          options.MaxSessions,
		maxPerSource: options.MaxSessionsPerSource,
		now:          options.Now,
		random:       options.Random,
		sessions:     make(map[[sha256.Size]byte]*session),
	}
}

func (s *Service) isDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled
}

func (s *Service) Disable() {
	s.mu.Lock()
	s.disabled = true
	for digest := range s.sessions {
		s.removeLocked(digest)
	}
	s.mu.Unlock()
}

func (s *Service) newSession(
	ctx context.Context,
	sourceHash [sha256.Size]byte,
	replacedValue string,
) (string, *session, func(), error) {
	now := s.now().UTC()
	expiresAt := now.Add(s.ttl)
	var replaced *session
	var replacedDigest [sha256.Size]byte
	if replacedValue != "" {
		replacedDigest = sha256.Sum256([]byte(replacedValue))
		s.mu.Lock()
		s.pruneLocked(now)
		replaced = s.sessions[replacedDigest]
		s.mu.Unlock()
	}

	var value string
	var digest [sha256.Size]byte
	var created *session
	replacedAdmissionID := ""
	if replaced != nil {
		replacedAdmissionID = replaced.admissionID
	}
	admissionID, release, err := s.admission.AdmitDemoSession(
		ctx,
		sourceHash,
		replacedAdmissionID,
		now,
		expiresAt,
		s.max,
		s.maxPerSource,
		func() error {
			var secret [32]byte
			var identifier [16]byte
			s.randomMu.Lock()
			defer s.randomMu.Unlock()
			if _, err := io.ReadFull(s.random, secret[:]); err != nil {
				return fmt.Errorf("generate demo session secret: %w", err)
			}
			if _, err := io.ReadFull(s.random, identifier[:]); err != nil {
				return fmt.Errorf("generate demo session identifier: %w", err)
			}
			identifier[6] = identifier[6]&0x0f | 0x40
			identifier[8] = identifier[8]&0x3f | 0x80
			id := fmt.Sprintf("%x-%x-%x-%x-%x", identifier[0:4], identifier[4:6], identifier[6:8], identifier[8:10], identifier[10:16])
			value = base64.RawURLEncoding.EncodeToString(secret[:])
			digest = sha256.Sum256([]byte(value))
			created = &session{id: id, createdAt: now, expiresAt: expiresAt, state: freshState(now)}

			s.mu.Lock()
			collision := s.sessions[digest] != nil && s.sessions[digest] != replaced
			s.mu.Unlock()
			if collision {
				return errors.New("generated duplicate demo session secret")
			}
			return nil
		},
	)
	if err != nil {
		return "", nil, nil, err
	}
	created.admissionID = admissionID

	if replaced != nil {
		replaced.mu.Lock()
	}
	s.mu.Lock()
	s.pruneLocked(now)
	if replaced != nil && s.sessions[replacedDigest] == replaced {
		s.removeLocked(replacedDigest)
	}
	s.sessions[digest] = created
	s.mu.Unlock()
	if replaced != nil {
		replaced.mu.Unlock()
	}
	return value, created, release, nil
}

func (s *Service) session(value string) (*session, [sha256.Size]byte) {
	digest := sha256.Sum256([]byte(value))
	now := s.now().UTC()
	s.mu.Lock()
	s.pruneLocked(now)
	found := s.sessions[digest]
	s.mu.Unlock()
	return found, digest
}

func (s *Service) releaseSession(ctx context.Context, digest [sha256.Size]byte, current *session) (func(), error) {
	release, err := s.admission.ReleaseDemoSession(ctx, current.admissionID)
	if err != nil {
		return nil, err
	}
	current.mu.Lock()
	s.mu.Lock()
	if s.sessions[digest] == current {
		s.removeLocked(digest)
	}
	s.mu.Unlock()
	current.mu.Unlock()
	return release, nil
}

func (s *Service) removeLocked(digest [sha256.Size]byte) {
	current := s.sessions[digest]
	if current == nil {
		return
	}
	s.playbackTotal -= len(current.state.playback)
	clear(current.state.playback)
	delete(s.sessions, digest)
}

func (s *Service) pruneLocked(now time.Time) {
	for digest, candidate := range s.sessions {
		if !now.Before(candidate.expiresAt) {
			s.removeLocked(digest)
		}
	}
}

func (s *Service) resetStateLocked(current *session, now time.Time) {
	s.mu.Lock()
	s.playbackTotal -= len(current.state.playback)
	current.state = freshState(now)
	s.mu.Unlock()
}

func (s *Service) allocatePlaybackLocked(current *session, titleID string, now time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneLocked(now)
	s.prunePlaybackLocked(now)
	active := false
	for _, candidate := range s.sessions {
		if candidate == current {
			active = true
			break
		}
	}
	if !active || len(current.state.playback) >= maxPlaybackEntriesPerSession ||
		s.playbackTotal >= maxPlaybackEntriesAcrossSessions {
		return "", false
	}

	current.state.playbackCounter++
	id := fmt.Sprintf("d7000000-0000-4000-8000-%012d", current.state.playbackCounter)
	current.state.playback[id] = playbackState{TitleID: titleID, CreatedAt: now}
	s.playbackTotal++
	return id, true
}

func (s *Service) deletePlaybackLocked(current *session, id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pruneLocked(now)
	s.prunePlaybackLocked(now)
	if _, ok := current.state.playback[id]; !ok {
		return false
	}
	delete(current.state.playback, id)
	s.playbackTotal--
	return true
}

func (s *Service) prunePlaybackLocked(now time.Time) {
	oldestRetained := now.Add(-playbackStateTTL)
	for _, current := range s.sessions {
		for id, state := range current.state.playback {
			if !state.CreatedAt.After(oldestRetained) {
				delete(current.state.playback, id)
				s.playbackTotal--
			}
		}
	}
}
