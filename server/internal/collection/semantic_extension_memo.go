package collection

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

const (
	semanticExtensionMemoTTL             = 24 * time.Hour
	semanticExtensionMemoCapacity        = 1024
	semanticExtensionMemoBudget          = 10 * time.Second
	semanticExtensionMemoContractVersion = "1"
	semanticExtensionMemoBlindDomain     = "rivune:collection:semantic-extension-memo:v1"
	semanticExtensionLatencySamples      = 256
)

var (
	errInvalidSemanticExtensionSelection = errors.New("semantic extension returned an invalid selection")
	ErrSemanticExtensionBusy             = errors.New("semantic extension is busy")
)

type semanticExtensionMemoKey [sha256.Size]byte

type semanticExtensionMemoEntry struct {
	selection  []string
	expiresAt  time.Time
	sequence   uint64
	persistent bool
}

type semanticExtensionStoredEntry struct {
	key       semanticExtensionMemoKey
	selection []string
	expiresAt time.Time
	updatedAt time.Time
}

type semanticExtensionMemoStore interface {
	Load(context.Context, int, time.Time, int) ([]semanticExtensionStoredEntry, error)
	Upsert(context.Context, int, semanticExtensionMemoKey, []string, time.Time, time.Time, int) (int, error)
}

type postgresSemanticExtensionMemoStore struct{ pool *pgxpool.Pool }

func (store postgresSemanticExtensionMemoStore) Load(ctx context.Context, version int, now time.Time, limit int) ([]semanticExtensionStoredEntry, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT cache_key, selection, expires_at, updated_at
		FROM semantic_extension_memo
		WHERE key_version = $1 AND expires_at > $2
		ORDER BY updated_at DESC, cache_key
		LIMIT $3
	`, version, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]semanticExtensionStoredEntry, 0, limit)
	for rows.Next() {
		var raw []byte
		var entry semanticExtensionStoredEntry
		if err := rows.Scan(&raw, &entry.selection, &entry.expiresAt, &entry.updatedAt); err != nil {
			return nil, err
		}
		if len(raw) != sha256.Size {
			continue
		}
		copy(entry.key[:], raw)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (store postgresSemanticExtensionMemoStore) Upsert(ctx context.Context, version int, key semanticExtensionMemoKey, selection []string, expiresAt, updatedAt time.Time, capacity int) (int, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE semantic_extension_memo IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO semantic_extension_memo (key_version, cache_key, selection, expires_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (key_version, cache_key) DO UPDATE
		SET selection = EXCLUDED.selection, expires_at = EXCLUDED.expires_at, updated_at = EXCLUDED.updated_at
		WHERE semantic_extension_memo.updated_at <= EXCLUDED.updated_at
	`, version, key[:], selection, expiresAt, updatedAt); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM semantic_extension_memo WHERE expires_at <= $1`, updatedAt); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM semantic_extension_memo
		WHERE (key_version, cache_key) IN (
			SELECT key_version, cache_key FROM semantic_extension_memo
			ORDER BY updated_at DESC, key_version, cache_key OFFSET $1
		)
	`, capacity); err != nil {
		return 0, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM semantic_extension_memo WHERE key_version = $1 AND expires_at > $2`, version, updatedAt).Scan(&count); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

type semanticExtensionFlight struct {
	key        semanticExtensionMemoKey
	done       chan struct{}
	cancel     context.CancelFunc
	selection  []string
	err        error
	waiters    int
	finished   bool
	started    bool
	generation uint64
	base       context.Context
	run        func(context.Context) ([]string, error)
	cacheable  bool
	startedAt  time.Time
	warmup     bool
}

type semanticExtensionPersistJob struct {
	store      semanticExtensionMemoStore
	version    int
	key        semanticExtensionMemoKey
	selection  []string
	expiresAt  time.Time
	updatedAt  time.Time
	capacity   int
	generation uint64
}

type semanticExtensionMemoMetrics struct {
	hits, misses, coalescedWaiters                                  uint64
	executions, successes, timeouts, failures, cancellations        uint64
	busyFallbacks                                                   uint64
	latencies                                                       [semanticExtensionLatencySamples]time.Duration
	latencyCount                                                    int
	latencyNext                                                     int
}

type semanticExtensionMemo struct {
	mu                sync.Mutex
	entries           map[semanticExtensionMemoKey]semanticExtensionMemoEntry
	flights           map[semanticExtensionMemoKey]*semanticExtensionFlight
	active            *semanticExtensionFlight
	queued            *semanticExtensionFlight
	sequence          uint64
	generation        uint64
	now               func() time.Time
	ttl               time.Duration
	capacity          int
	budget            time.Duration
	warmupBudget      time.Duration
	keys              *secretcrypto.Keyring
	store             semanticExtensionMemoStore
	logger            *slog.Logger
	persistentStatus  string
	persistentEntries int
	persistQueue      chan semanticExtensionMemoKey
	persistPending    map[semanticExtensionMemoKey]semanticExtensionPersistJob
	persistCancel     context.CancelFunc
	persistDone       chan struct{}
	metrics           semanticExtensionMemoMetrics
}

func newSemanticExtensionMemo() *semanticExtensionMemo {
	return &semanticExtensionMemo{
		entries: make(map[semanticExtensionMemoKey]semanticExtensionMemoEntry),
		flights: make(map[semanticExtensionMemoKey]*semanticExtensionFlight),
		now:     time.Now, ttl: semanticExtensionMemoTTL, capacity: semanticExtensionMemoCapacity,
		budget: semanticExtensionMemoBudget, warmupBudget: semanticExtensionTimeout, logger: slog.Default(), persistentStatus: "disabled",
		persistQueue: make(chan semanticExtensionMemoKey, semanticExtensionMemoCapacity),
		persistPending: make(map[semanticExtensionMemoKey]semanticExtensionPersistJob),
	}
}

func (memo *semanticExtensionMemo) configurePersistence(ctx context.Context, keys *secretcrypto.Keyring, store semanticExtensionMemoStore) {
	if memo == nil {
		return
	}
	memo.stopPersistenceWorker()
	memo.mu.Lock()
	memo.keys, memo.store = keys, store
	memo.generation++
	generation := memo.generation
	memo.entries = make(map[semanticExtensionMemoKey]semanticExtensionMemoEntry)
	memo.sequence, memo.persistentEntries = 0, 0
	memo.persistQueue = make(chan semanticExtensionMemoKey, semanticExtensionMemoCapacity)
	memo.persistPending = make(map[semanticExtensionMemoKey]semanticExtensionPersistJob)
	if keys == nil || store == nil {
		memo.persistentStatus = "disabled"
		memo.mu.Unlock()
		return
	}
	memo.persistentStatus = "pending"
	memo.mu.Unlock()
	entries, err := store.Load(ctx, keys.ActiveVersion(), memo.nowTime(), semanticExtensionMemoCapacity)
	memo.mu.Lock()
	if memo.generation != generation || memo.keys == nil || memo.keys.ActiveVersion() != keys.ActiveVersion() {
		memo.mu.Unlock()
		return
	}
	if err != nil {
		memo.persistentStatus = "failed"
		memo.logStoreFailure(ctx, "load semantic extension memo", err)
		memo.mu.Unlock()
		return
	}
	now := memo.nowTime()
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if !now.Before(entry.expiresAt) {
			continue
		}
		if _, exists := memo.entries[entry.key]; exists {
			continue
		}
		memo.sequence++
		memo.entries[entry.key] = semanticExtensionMemoEntry{selection: slices.Clone(entry.selection), expiresAt: entry.expiresAt, sequence: memo.sequence, persistent: true}
		memo.persistentEntries++
	}
	memo.persistentStatus = "ready"
	workerContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	memo.persistCancel, memo.persistDone = cancel, done
	memo.mu.Unlock()
	go memo.runPersistenceWorker(workerContext, done)
}

func (memo *semanticExtensionMemo) resolve(ctx context.Context, extension SemanticExtension, request SemanticExtensionRequest) ([]string, error) {
	if memo == nil || ctx == nil || extension == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request = cloneSemanticExtensionRequest(request)
	key := memo.makeKey(extension, request)
	now := memo.nowTime()
	memo.mu.Lock()
	if entry, ok := memo.entries[key]; ok {
		if now.Before(entry.expiresAt) {
			selection, err := validSemanticExtensionSelection(request.Candidates, entry.selection)
			if err == nil {
				memo.metrics.hits++
				if selection == nil {
					selection = []string{}
				}
				memo.mu.Unlock()
				return selection, nil
			}
		}
		delete(memo.entries, key)
		if entry.persistent && memo.persistentEntries > 0 {
			memo.persistentEntries--
		}
	}
	if flight := memo.flights[key]; flight != nil {
		flight.waiters++
		memo.metrics.coalescedWaiters++
		memo.mu.Unlock()
		return memo.wait(ctx, key, flight)
	}
	memo.metrics.misses++
	flight := &semanticExtensionFlight{
		key: key, done: make(chan struct{}), waiters: 1, generation: memo.generation,
		base: context.WithoutCancel(ctx), cacheable: true,
		run: func(operationContext context.Context) ([]string, error) {
			selection, err := extension.Resolve(operationContext, request)
			if err == nil {
				selection, err = validSemanticExtensionSelection(request.Candidates, selection)
			}
			return selection, err
		},
	}
	memo.flights[key] = flight
	if err := memo.admitLocked(flight); err != nil {
		delete(memo.flights, key)
		memo.metrics.busyFallbacks++
		memo.mu.Unlock()
		return nil, err
	}
	memo.mu.Unlock()
	return memo.wait(ctx, key, flight)
}

func (memo *semanticExtensionMemo) runWarmup(ctx context.Context, key semanticExtensionMemoKey, run func(context.Context) error) error {
	if memo == nil || ctx == nil || run == nil {
		return ErrInvalidInput
	}
	memo.mu.Lock()
	if flight := memo.flights[key]; flight != nil {
		flight.waiters++
		memo.metrics.coalescedWaiters++
		memo.mu.Unlock()
		_, err := memo.wait(ctx, key, flight)
		return err
	}
	flight := &semanticExtensionFlight{key: key, done: make(chan struct{}), waiters: 1, generation: memo.generation, base: context.WithoutCancel(ctx), warmup: true, run: func(operationContext context.Context) ([]string, error) { return nil, run(operationContext) }}
	memo.flights[key] = flight
	if err := memo.admitLocked(flight); err != nil {
		delete(memo.flights, key)
		memo.mu.Unlock()
		return err
	}
	memo.mu.Unlock()
	_, err := memo.wait(ctx, key, flight)
	return err
}

func (memo *semanticExtensionMemo) admitLocked(flight *semanticExtensionFlight) error {
	if memo.active == nil {
		memo.startLocked(flight)
		return nil
	}
	if !flight.warmup && memo.queued != nil && memo.queued.warmup {
		memo.finishQueuedLocked(memo.queued, context.Canceled)
	}
	if memo.queued == nil {
		memo.queued = flight
		if !flight.warmup && memo.active.warmup && memo.active.cancel != nil {
			memo.active.cancel()
		}
		return nil
	}
	return ErrSemanticExtensionBusy
}

func (memo *semanticExtensionMemo) finishQueuedLocked(flight *semanticExtensionFlight, err error) {
	if flight == nil || flight.finished || flight.started {
		return
	}
	if memo.queued == flight {
		memo.queued = nil
	}
	if memo.flights[flight.key] == flight {
		delete(memo.flights, flight.key)
	}
	flight.err, flight.finished = err, true
	close(flight.done)
}

func (memo *semanticExtensionMemo) startLocked(flight *semanticExtensionFlight) {
	budget := memo.executionBudget()
	if flight.warmup {
		budget = memo.warmupExecutionBudget()
	}
	operationContext, cancel := context.WithTimeout(flight.base, budget)
	flight.cancel, flight.started, flight.startedAt = cancel, true, memo.nowTime()
	memo.active = flight
	memo.metrics.executions++
	go memo.runFlight(flight, operationContext)
}

func (memo *semanticExtensionMemo) wait(ctx context.Context, key semanticExtensionMemoKey, flight *semanticExtensionFlight) ([]string, error) {
	select {
	case <-ctx.Done():
		memo.mu.Lock()
		if !flight.finished {
			flight.waiters--
			if flight.waiters == 0 {
				if flight == memo.queued {
					memo.queued = nil
					delete(memo.flights, key)
				} else if flight == memo.active && flight.cancel != nil {
					delete(memo.flights, key)
					flight.cancel()
				}
			}
		}
		memo.mu.Unlock()
		return nil, ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return slices.Clone(flight.selection), flight.err
	}
}

func (memo *semanticExtensionMemo) runFlight(flight *semanticExtensionFlight, ctx context.Context) {
	selection, err := flight.run(ctx)
	if flight.warmup && err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	flight.cancel()
	finishedAt := memo.nowTime()
	memo.mu.Lock()
	current := memo.flights[flight.key] == flight
	if current {
		delete(memo.flights, flight.key)
	}
	flight.selection, flight.err, flight.finished = slices.Clone(selection), err, true
	if err == nil {
		memo.metrics.successes++
	} else if errors.Is(err, context.DeadlineExceeded) {
		memo.metrics.timeouts++
	} else if errors.Is(err, context.Canceled) {
		memo.metrics.cancellations++
	} else {
		memo.metrics.failures++
	}
	if !flight.warmup && !errors.Is(err, context.Canceled) {
		memo.recordLatencyLocked(finishedAt.Sub(flight.startedAt))
	}
	if current && flight.cacheable && flight.generation == memo.generation && err == nil {
		memo.storeLocked(flight.key, selection, finishedAt, false)
		memo.persistAsync(flight.key, selection, finishedAt)
	}
	close(flight.done)
	if memo.active == flight {
		memo.active = nil
	}
	if memo.active == nil && memo.queued != nil {
		next := memo.queued
		memo.queued = nil
		memo.startLocked(next)
	}
	memo.mu.Unlock()
}

func (memo *semanticExtensionMemo) persistAsync(key semanticExtensionMemoKey, selection []string, now time.Time) {
	if memo.store == nil || memo.keys == nil || memo.persistQueue == nil {
		return
	}
	job := semanticExtensionPersistJob{
		store: memo.store, version: memo.keys.ActiveVersion(), key: key, selection: slices.Clone(selection),
		expiresAt: now.Add(memo.cacheTTL()), updatedAt: now, capacity: memo.cacheCapacity(), generation: memo.generation,
	}
	if _, pending := memo.persistPending[key]; pending {
		memo.persistPending[key] = job
		return
	}
	if len(memo.persistPending) >= semanticExtensionMemoCapacity {
		memo.persistentStatus = "failed"
		return
	}
	memo.persistPending[key] = job
	select {
	case memo.persistQueue <- key:
	default:
		delete(memo.persistPending, key)
		memo.persistentStatus = "failed"
	}
}

func (memo *semanticExtensionMemo) runPersistenceWorker(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-memo.persistQueue:
			for {
				memo.mu.Lock()
				job, ok := memo.persistPending[key]
				memo.mu.Unlock()
				if !ok {
					break
				}
				operationContext, cancel := context.WithTimeout(ctx, 2*time.Second)
				count, err := job.store.Upsert(operationContext, job.version, job.key, job.selection, job.expiresAt, job.updatedAt, job.capacity)
				cancel()
				memo.mu.Lock()
				latest, stillPending := memo.persistPending[key]
				changed := stillPending && (latest.generation != job.generation || !latest.updatedAt.Equal(job.updatedAt))
				if !changed {
					delete(memo.persistPending, key)
				}
				if memo.generation == job.generation && memo.keys != nil && memo.keys.ActiveVersion() == job.version {
					if err != nil {
						memo.persistentStatus = "failed"
						memo.logStoreFailure(ctx, "persist semantic extension memo", err)
					} else {
						memo.persistentStatus, memo.persistentEntries = "ready", count
						if entry, exists := memo.entries[job.key]; exists {
							entry.persistent = true
							memo.entries[job.key] = entry
						}
					}
				}
				memo.mu.Unlock()
				if !changed {
					break
				}
			}
		}
	}
}

func (memo *semanticExtensionMemo) stopPersistenceWorker() {
	if memo == nil {
		return
	}
	memo.mu.Lock()
	cancel, done := memo.persistCancel, memo.persistDone
	memo.persistCancel, memo.persistDone = nil, nil
	memo.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (memo *semanticExtensionMemo) close() {
	memo.stopPersistenceWorker()
}

func (memo *semanticExtensionMemo) storeLocked(key semanticExtensionMemoKey, selection []string, now time.Time, persistent bool) {
	for candidateKey, entry := range memo.entries {
		if !now.Before(entry.expiresAt) {
			delete(memo.entries, candidateKey)
			if entry.persistent && memo.persistentEntries > 0 {
				memo.persistentEntries--
			}
		}
	}
	capacity := memo.cacheCapacity()
	if old, exists := memo.entries[key]; exists && old.persistent {
		persistent = true
	}
	if _, exists := memo.entries[key]; !exists && len(memo.entries) >= capacity {
		var oldestKey semanticExtensionMemoKey
		var oldest uint64
		found := false
		for candidateKey, entry := range memo.entries {
			if !found || entry.sequence < oldest {
				oldestKey, oldest, found = candidateKey, entry.sequence, true
			}
		}
		if found {
			delete(memo.entries, oldestKey)
		}
	}
	memo.sequence++
	memo.entries[key] = semanticExtensionMemoEntry{selection: slices.Clone(selection), expiresAt: now.Add(memo.cacheTTL()), sequence: memo.sequence, persistent: persistent}
}

func (memo *semanticExtensionMemo) clear() {
	if memo == nil {
		return
	}
	memo.mu.Lock()
	flights, active := memo.flights, memo.active
	memo.entries, memo.flights = make(map[semanticExtensionMemoKey]semanticExtensionMemoEntry), make(map[semanticExtensionMemoKey]*semanticExtensionFlight)
	memo.sequence, memo.persistentEntries = 0, 0
	memo.generation++
	memo.queued = nil
	for _, flight := range flights {
		if flight == active {
			if flight.cancel != nil {
				flight.cancel()
			}
			continue
		}
		if !flight.finished {
			flight.err, flight.finished = context.Canceled, true
			close(flight.done)
		}
	}
	if active == nil {
		memo.active = nil
	}
	memo.mu.Unlock()
}

func (memo *semanticExtensionMemo) recordLatencyLocked(value time.Duration) {
	if value < 0 {
		value = 0
	}
	memo.metrics.latencies[memo.metrics.latencyNext] = value
	memo.metrics.latencyNext = (memo.metrics.latencyNext + 1) % len(memo.metrics.latencies)
	if memo.metrics.latencyCount < len(memo.metrics.latencies) {
		memo.metrics.latencyCount++
	}
}

func (memo *semanticExtensionMemo) percentileLocked(percent int) int64 {
	if memo.metrics.latencyCount == 0 {
		return 0
	}
	values := append([]time.Duration(nil), memo.metrics.latencies[:memo.metrics.latencyCount]...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := (percent*len(values)+99)/100 - 1
	return values[index].Milliseconds()
}

func (memo *semanticExtensionMemo) makeKey(extension SemanticExtension, request SemanticExtensionRequest) semanticExtensionMemoKey {
	identity := "unidentified"
	if identified, ok := extension.(interface{ SemanticCacheIdentity() string }); ok {
		if value := strings.TrimSpace(identified.SemanticCacheIdentity()); value != "" {
			identity = value
		}
	}
	digest := sha256.New()
	writeSemanticExtensionMemoField(digest, semanticExtensionMemoContractVersion)
	writeSemanticExtensionMemoField(digest, identity)
	writeSemanticExtensionMemoField(digest, canonicalSemanticLanguage(request.Language))
	writeSemanticExtensionMemoField(digest, strings.Join(strings.Fields(normalizeSemanticText(request.Query)), " "))
	for _, candidate := range request.Candidates {
		writeSemanticExtensionMemoField(digest, strings.ToLower(strings.TrimSpace(candidate.ID)))
	}
	payload := digest.Sum(nil)
	memo.mu.Lock()
	keys := memo.keys
	memo.mu.Unlock()
	if keys != nil {
		blind, err := keys.BlindIndex(semanticExtensionMemoBlindDomain, payload)
		if err == nil {
			return semanticExtensionMemoKey(blind.Digest)
		}
	}
	return semanticExtensionMemoKey(sha256.Sum256(payload))
}

func makeSemanticExtensionMemoKey(request SemanticExtensionRequest) semanticExtensionMemoKey {
	return newSemanticExtensionMemo().makeKey(semanticMemoIdentity("unidentified"), request)
}

type semanticMemoIdentity string

func (semanticMemoIdentity) Resolve(context.Context, SemanticExtensionRequest) ([]string, error) {
	return nil, nil
}
func (identity semanticMemoIdentity) SemanticCacheIdentity() string { return string(identity) }

func (memo *semanticExtensionMemo) warmupKey(extension SemanticExtension) semanticExtensionMemoKey {
	request := SemanticExtensionRequest{Query: "semantic-extension-internal-warmup", Language: "und"}
	return memo.makeKey(extension, request)
}

func (memo *semanticExtensionMemo) nowTime() time.Time {
	if memo.now == nil {
		return time.Now()
	}
	return memo.now()
}
func (memo *semanticExtensionMemo) executionBudget() time.Duration {
	if memo.budget <= 0 || memo.budget > semanticExtensionMemoBudget {
		return semanticExtensionMemoBudget
	}
	return memo.budget
}
func (memo *semanticExtensionMemo) warmupExecutionBudget() time.Duration {
	if memo.warmupBudget <= 0 || memo.warmupBudget > semanticExtensionTimeout {
		return semanticExtensionTimeout
	}
	return memo.warmupBudget
}

func (memo *semanticExtensionMemo) cacheTTL() time.Duration {
	if memo.ttl <= 0 || memo.ttl > semanticExtensionMemoTTL {
		return semanticExtensionMemoTTL
	}
	return memo.ttl
}
func (memo *semanticExtensionMemo) cacheCapacity() int {
	if memo.capacity <= 0 || memo.capacity > semanticExtensionMemoCapacity {
		return semanticExtensionMemoCapacity
	}
	return memo.capacity
}
func (memo *semanticExtensionMemo) logStoreFailure(ctx context.Context, message string, err error) {
	logger := memo.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, message, "error", err)
}

func cloneSemanticExtensionRequest(request SemanticExtensionRequest) SemanticExtensionRequest {
	request.Query, request.Language = strings.Clone(request.Query), strings.Clone(request.Language)
	request.Candidates = slices.Clone(request.Candidates)
	for index := range request.Candidates {
		request.Candidates[index].ID = strings.Clone(request.Candidates[index].ID)
		request.Candidates[index].Kind = strings.Clone(request.Candidates[index].Kind)
		request.Candidates[index].Label = strings.Clone(request.Candidates[index].Label)
	}
	return request
}

func writeSemanticExtensionMemoField(digest hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write([]byte(value))
}

func validSemanticExtensionSelection(candidates []SemanticExtensionCandidate, selection []string) ([]string, error) {
	if len(selection) > maximumSemanticExtensionMatches {
		return nil, errInvalidSemanticExtensionSelection
	}
	offered := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		id := strings.ToLower(strings.TrimSpace(candidate.ID))
		if id != "" {
			offered[id] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(selection))
	seen := make(map[string]struct{}, len(selection))
	for _, raw := range selection {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			return nil, errInvalidSemanticExtensionSelection
		}
		if _, ok := offered[id]; !ok {
			return nil, errInvalidSemanticExtensionSelection
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errInvalidSemanticExtensionSelection
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized, nil
}
