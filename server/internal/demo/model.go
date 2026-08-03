package demo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	CookieName = "rivune_demo"
	APIPrefix  = "/api/v1"

	AlexProfileID = "d0000000-0000-4000-8000-000000000001"
	KidsProfileID = "d0000000-0000-4000-8000-000000000002"
	DemoUserID     = "d0000000-0000-4000-8000-000000000003"
	DemoDeviceID   = "d0000000-0000-4000-8000-000000000004"

	SignalMovieID    = "d1000000-0000-4000-8000-000000000001"
	LighthouseID     = "d1000000-0000-4000-8000-000000000002"
	OpenSkiesID      = "d1000000-0000-4000-8000-000000000003"
	OrbitSeriesID    = "d2000000-0000-4000-8000-000000000001"
	OrbitSeasonOneID = "d2100000-0000-4000-8000-000000000001"
	OrbitSeasonTwoID = "d2100000-0000-4000-8000-000000000002"
	OrbitEpisodeOne  = "d2200000-0000-4000-8000-000000000001"
	OrbitEpisodeTwo  = "d2200000-0000-4000-8000-000000000002"
	OrbitEpisodeThree = "d2200000-0000-4000-8000-000000000003"
	OrbitEpisodeFour  = "d2200000-0000-4000-8000-000000000004"
	WorldNewsID      = "d3000000-0000-4000-8000-000000000001"
	CultureLiveID    = "d3000000-0000-4000-8000-000000000002"

	HomeCollectionID = "d4000000-0000-4000-8000-000000000001"
	SpotlightFolderID = "d4100000-0000-4000-8000-000000000001"
	SeriesFolderID    = "d4100000-0000-4000-8000-000000000002"
	LiveFolderID      = "d4100000-0000-4000-8000-000000000003"
)

const (
	defaultTTL         = 8 * time.Hour
	defaultMaxSessions = 64
)

// SetupAdmission is the only database-facing capability demo handlers receive.
type SetupAdmission interface {
	AcquireSetupPending(context.Context) (func(), error)
}

type Options struct {
	TTL         time.Duration
	MaxSessions int
	Now         func() time.Time
	Random      io.Reader
}

type Service struct {
	admission SetupAdmission
	ttl       time.Duration
	max       int
	now       func() time.Time
	random    io.Reader

	mu       sync.Mutex
	sessions map[[sha256.Size]byte]*session
}

type session struct {
	mu        sync.Mutex
	id        string
	createdAt time.Time
	expiresAt time.Time
	state     sessionState
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
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Service{
		admission: admission,
		ttl: options.TTL,
		max: options.MaxSessions,
		now: options.Now,
		random: options.Random,
		sessions: make(map[[sha256.Size]byte]*session),
	}
}

func (s *Service) Disable() {
	s.mu.Lock()
	clear(s.sessions)
	s.mu.Unlock()
}

func (s *Service) newSession() (string, *session, error) {
	secret := make([]byte, 32)
	identifier := make([]byte, 16)
	if _, err := io.ReadFull(s.random, secret); err != nil {
		return "", nil, fmt.Errorf("generate demo session secret: %w", err)
	}
	if _, err := io.ReadFull(s.random, identifier); err != nil {
		return "", nil, fmt.Errorf("generate demo session identifier: %w", err)
	}
	identifier[6] = identifier[6]&0x0f | 0x40
	identifier[8] = identifier[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", identifier[0:4], identifier[4:6], identifier[6:8], identifier[8:10], identifier[10:16])
	value := base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(value))
	now := s.now().UTC()
	created := &session{id: id, createdAt: now, expiresAt: now.Add(s.ttl), state: freshState(now)}

	s.mu.Lock()
	s.pruneLocked(now)
	for len(s.sessions) >= s.max {
		var oldestDigest [sha256.Size]byte
		var oldest *session
		for candidateDigest, candidate := range s.sessions {
			if oldest == nil || candidate.createdAt.Before(oldest.createdAt) {
				oldestDigest, oldest = candidateDigest, candidate
			}
		}
		delete(s.sessions, oldestDigest)
	}
	s.sessions[digest] = created
	s.mu.Unlock()
	return value, created, nil
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

func (s *Service) remove(digest [sha256.Size]byte) {
	s.mu.Lock()
	delete(s.sessions, digest)
	s.mu.Unlock()
}

func (s *Service) pruneLocked(now time.Time) {
	for digest, candidate := range s.sessions {
		if !now.Before(candidate.expiresAt) {
			delete(s.sessions, digest)
		}
	}
}
