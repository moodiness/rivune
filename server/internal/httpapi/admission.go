package httpapi

import (
	"crypto/sha256"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	credentialAdmissionGlobalConcurrency = 4
	credentialAdmissionSourceConcurrency = 1
	credentialAdmissionSourceAttempts    = 10
	credentialAdmissionTrackedSources    = 2_048
	credentialUsernameAttempts           = 10
	credentialUsernameTrackedSubjects    = 2_048
	deviceCodeAdmissionGlobalConcurrency = 8
	deviceCodeAdmissionSourceConcurrency = 2
	deviceCodeAdmissionSourceAttempts    = 12
	deviceCodeAdmissionTrackedSources    = 4_096
	publicAdmissionWindow                = time.Minute
	publicAdmissionConcurrencyRetry      = time.Second
	publicAdmissionCleanupLimit          = 64
)

type admissionSource struct {
	attempts   int
	inFlight   int
	windowEnds time.Time
}

type attemptBudget struct {
	attempts   int
	windowEnds time.Time
}

type usernameAdmission struct {
	mu              sync.Mutex
	now             func() time.Time
	window          time.Duration
	subjectAttempts int
	maximumSubjects int
	subjects        map[[sha256.Size]byte]attemptBudget
}

type requestAdmission struct {
	mu                sync.Mutex
	now               func() time.Time
	window            time.Duration
	globalConcurrency int
	sourceConcurrency int
	sourceAttempts    int
	maximumSources    int
	inFlight          int
	sources           map[string]*admissionSource
}

func newRequestAdmission(globalConcurrency, sourceConcurrency, sourceAttempts, maximumSources int, window time.Duration) *requestAdmission {
	return &requestAdmission{
		now:               time.Now,
		window:            window,
		globalConcurrency: globalConcurrency,
		sourceConcurrency: sourceConcurrency,
		sourceAttempts:    sourceAttempts,
		maximumSources:    maximumSources,
		sources:           make(map[string]*admissionSource),
	}
}

func newCredentialAdmission() *requestAdmission {
	return newRequestAdmission(
		credentialAdmissionGlobalConcurrency,
		credentialAdmissionSourceConcurrency,
		credentialAdmissionSourceAttempts,
		credentialAdmissionTrackedSources,
		publicAdmissionWindow,
	)
}

func newCredentialUsernameAdmission() *usernameAdmission {
	return newUsernameAdmission(
		credentialUsernameAttempts,
		credentialUsernameTrackedSubjects,
		publicAdmissionWindow,
	)
}

func newUsernameAdmission(subjectAttempts, maximumSubjects int, window time.Duration) *usernameAdmission {
	return &usernameAdmission{
		now:             time.Now,
		window:          window,
		subjectAttempts: subjectAttempts,
		maximumSubjects: maximumSubjects,
		subjects:        make(map[[sha256.Size]byte]attemptBudget),
	}
}

func (admission *usernameAdmission) acquire(username string) (time.Duration, bool) {
	subject := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	now := admission.now()

	admission.mu.Lock()
	defer admission.mu.Unlock()
	admission.cleanupExpired(now, publicAdmissionCleanupLimit)

	state, exists := admission.subjects[subject]
	if !exists {
		if len(admission.subjects) >= admission.maximumSubjects {
			return publicAdmissionConcurrencyRetry, false
		}
		state.windowEnds = now.Add(admission.window)
	} else if !now.Before(state.windowEnds) {
		state.attempts = 0
		state.windowEnds = now.Add(admission.window)
	}
	if state.attempts >= admission.subjectAttempts {
		retryAfter := state.windowEnds.Sub(now)
		if retryAfter <= 0 {
			retryAfter = publicAdmissionConcurrencyRetry
		}
		return retryAfter, false
	}

	state.attempts++
	admission.subjects[subject] = state
	return 0, true
}

func (admission *usernameAdmission) cleanupExpired(now time.Time, limit int) {
	checked := 0
	for subject, state := range admission.subjects {
		if checked >= limit {
			return
		}
		checked++
		if !now.Before(state.windowEnds) {
			delete(admission.subjects, subject)
		}
	}
}

func newDeviceCodeAdmission() *requestAdmission {
	return newRequestAdmission(
		deviceCodeAdmissionGlobalConcurrency,
		deviceCodeAdmissionSourceConcurrency,
		deviceCodeAdmissionSourceAttempts,
		deviceCodeAdmissionTrackedSources,
		publicAdmissionWindow,
	)
}

func networkAdmissionSource(source string) string {
	address, err := netip.ParseAddr(strings.TrimSpace(source))
	if err != nil {
		return "unknown"
	}
	address = address.Unmap().WithZone("")
	if address.Is4() {
		return address.String()
	}
	return netip.PrefixFrom(address, 64).Masked().String()
}

func (admission *requestAdmission) acquire(source string) (func(), time.Duration, bool) {
	now := admission.now()
	source = networkAdmissionSource(source)

	admission.mu.Lock()
	defer admission.mu.Unlock()
	admission.cleanupExpired(now, publicAdmissionCleanupLimit)

	if admission.inFlight >= admission.globalConcurrency {
		return nil, publicAdmissionConcurrencyRetry, false
	}
	state := admission.sources[source]
	if state == nil {
		if len(admission.sources) >= admission.maximumSources {
			return nil, publicAdmissionConcurrencyRetry, false
		}
		state = &admissionSource{windowEnds: now.Add(admission.window)}
		admission.sources[source] = state
	} else if !now.Before(state.windowEnds) && state.inFlight == 0 {
		state.attempts = 0
		state.windowEnds = now.Add(admission.window)
	}
	if state.inFlight >= admission.sourceConcurrency {
		return nil, publicAdmissionConcurrencyRetry, false
	}
	if state.attempts >= admission.sourceAttempts {
		retryAfter := state.windowEnds.Sub(now)
		if retryAfter <= 0 {
			retryAfter = publicAdmissionConcurrencyRetry
		}
		return nil, retryAfter, false
	}

	state.attempts++
	state.inFlight++
	admission.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			admission.mu.Lock()
			state.inFlight--
			admission.inFlight--
			admission.mu.Unlock()
		})
	}, 0, true
}

func (admission *requestAdmission) cleanupExpired(now time.Time, limit int) {
	removed := 0
	for source, state := range admission.sources {
		if removed >= limit {
			return
		}
		if state.inFlight == 0 && !now.Before(state.windowEnds) {
			delete(admission.sources, source)
			removed++
		}
	}
}

func writeAdmissionDenied(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int64((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests; retry later")
}
