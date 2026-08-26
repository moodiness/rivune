package collection

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

type semanticMemoExtensionFunc func(context.Context, SemanticExtensionRequest) ([]string, error)

func (function semanticMemoExtensionFunc) Resolve(ctx context.Context, request SemanticExtensionRequest) ([]string, error) {
	return function(ctx, request)
}

func (semanticMemoExtensionFunc) SemanticCacheIdentity() string { return "unidentified" }

type identifiedSemanticMemoExtension struct {
	identity string
	resolve  semanticMemoExtensionFunc
}

func (extension identifiedSemanticMemoExtension) Resolve(ctx context.Context, request SemanticExtensionRequest) ([]string, error) {
	return extension.resolve(ctx, request)
}

func (extension identifiedSemanticMemoExtension) SemanticCacheIdentity() string {
	return extension.identity
}

type memorySemanticMemoStore struct {
	mu        sync.Mutex
	entries   map[int]map[semanticExtensionMemoKey]semanticExtensionStoredEntry
	loadErr   error
	upsertErr error
	upserted  chan struct{}
}

func (store *memorySemanticMemoStore) Load(_ context.Context, version int, now time.Time, limit int) ([]semanticExtensionStoredEntry, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.loadErr != nil {
		return nil, store.loadErr
	}
	var result []semanticExtensionStoredEntry
	for _, entry := range store.entries[version] {
		if now.Before(entry.expiresAt) {
			result = append(result, entry)
		}
	}
	slices.SortFunc(result, func(a, b semanticExtensionStoredEntry) int { return b.updatedAt.Compare(a.updatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (store *memorySemanticMemoStore) Upsert(_ context.Context, version int, key semanticExtensionMemoKey, selection []string, expiresAt, updatedAt time.Time, capacity int) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.upsertErr != nil {
		return 0, store.upsertErr
	}
	if store.entries == nil {
		store.entries = make(map[int]map[semanticExtensionMemoKey]semanticExtensionStoredEntry)
	}
	if store.entries[version] == nil {
		store.entries[version] = make(map[semanticExtensionMemoKey]semanticExtensionStoredEntry)
	}
	store.entries[version][key] = semanticExtensionStoredEntry{key: key, selection: slices.Clone(selection), expiresAt: expiresAt, updatedAt: updatedAt}
	for candidateKey, entry := range store.entries[version] {
		if !updatedAt.Before(entry.expiresAt) {
			delete(store.entries[version], candidateKey)
		}
	}
	for len(store.entries[version]) > capacity {
		var oldestKey semanticExtensionMemoKey
		var oldest time.Time
		first := true
		for candidateKey, entry := range store.entries[version] {
			if first || entry.updatedAt.Before(oldest) {
				oldestKey, oldest, first = candidateKey, entry.updatedAt, false
			}
		}
		delete(store.entries[version], oldestKey)
	}
	if store.upserted != nil {
		select {
		case store.upserted <- struct{}{}:
		default:
		}
	}
	return len(store.entries[version]), nil
}

type blockingSemanticMemoStore struct {
	mu         sync.Mutex
	active     int
	maxActive  int
	calls      int
	started    chan struct{}
	release    chan struct{}
	selections map[semanticExtensionMemoKey][]string
}

func (*blockingSemanticMemoStore) Load(context.Context, int, time.Time, int) ([]semanticExtensionStoredEntry, error) {
	return nil, nil
}

func (store *blockingSemanticMemoStore) Upsert(ctx context.Context, _ int, key semanticExtensionMemoKey, selection []string, _ time.Time, _ time.Time, _ int) (int, error) {
	store.mu.Lock()
	store.active++
	store.calls++
	if store.active > store.maxActive {
		store.maxActive = store.active
	}
	store.mu.Unlock()
	select {
	case store.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		store.mu.Lock()
		store.active--
		store.mu.Unlock()
		return 0, ctx.Err()
	case <-store.release:
	}
	store.mu.Lock()
	store.active--
	if store.selections == nil {
		store.selections = make(map[semanticExtensionMemoKey][]string)
	}
	store.selections[key] = slices.Clone(selection)
	count := len(store.selections)
	store.mu.Unlock()
	return count, nil
}

func semanticMemoRequest(query string, candidates ...string) SemanticExtensionRequest {
	request := SemanticExtensionRequest{Query: query, Language: "en-US"}
	for _, id := range candidates {
		request.Candidates = append(request.Candidates, SemanticExtensionCandidate{ID: id, Kind: "theme", Label: id})
	}
	return request
}

func TestSemanticExtensionMemoPersistenceWorkerBoundsAndCoalescesBurst(t *testing.T) {
	keys, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x71}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	store := &blockingSemanticMemoStore{started: make(chan struct{}, 1), release: make(chan struct{}, 2048)}
	memo := newSemanticExtensionMemo()
	memo.configurePersistence(context.Background(), keys, store)
	now := time.Unix(50_000, 0)
	memo.mu.Lock()
	for index := range 1024 {
		key := semanticExtensionMemoKey{byte(index), byte(index >> 8)}
		memo.persistAsync(key, []string{"old"}, now)
		memo.persistAsync(key, []string{"latest"}, now.Add(time.Second))
	}
	memo.mu.Unlock()
	for range 1024 {
		store.release <- struct{}{}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		memo.mu.Lock()
		pending := len(memo.persistPending)
		memo.mu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("persistence queue retained %d jobs", pending)
		}
		time.Sleep(time.Millisecond)
	}
	memo.close()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.maxActive != 1 || store.calls != 1024 || len(store.selections) != 1024 {
		t.Fatalf("persistence worker active=%d calls=%d selections=%d", store.maxActive, store.calls, len(store.selections))
	}
	for _, selection := range store.selections {
		if !slices.Equal(selection, []string{"latest"}) {
			t.Fatalf("coalesced selection = %v", selection)
		}
	}
}

func TestSemanticExtensionMemoPersistenceWorkerStopsCleanly(t *testing.T) {
	keys, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x72}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	store := &blockingSemanticMemoStore{started: make(chan struct{}, 1), release: make(chan struct{})}
	memo := newSemanticExtensionMemo()
	memo.configurePersistence(context.Background(), keys, store)
	memo.mu.Lock()
	memo.persistAsync(semanticExtensionMemoKey{1}, []string{"value"}, time.Now())
	memo.mu.Unlock()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("persistence worker did not start")
	}
	done := make(chan struct{})
	go func() { memo.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("persistence worker did not stop")
	}
}
func TestSemanticExtensionMemoCachesNormalizedSuccessfulAndEmptySelections(t *testing.T) {
	memo := newSemanticExtensionMemo()
	var calls atomic.Int32
	extension := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) {
		calls.Add(1)
		return []string{"theme:space"}, nil
	})

	first, err := memo.resolve(context.Background(), extension, semanticMemoRequest("  SCI-FI   Café ", "theme:space"))
	if err != nil || !slices.Equal(first, []string{"theme:space"}) {
		t.Fatalf("first selection = %v, error %v", first, err)
	}
	secondRequest := semanticMemoRequest("sci fi cafe", "theme:space")
	secondRequest.Language = "EN-us"
	second, err := memo.resolve(context.Background(), extension, secondRequest)
	if err != nil || !slices.Equal(second, first) || calls.Load() != 1 {
		t.Fatalf("normalized cache selection = %v, calls = %d, error %v", second, calls.Load(), err)
	}

	emptyCalls := atomic.Int32{}
	empty := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) {
		emptyCalls.Add(1)
		return []string{}, nil
	})
	emptyRequest := semanticMemoRequest("nothing ambiguous", "theme:space")
	for range 2 {
		selection, resolveErr := memo.resolve(context.Background(), empty, emptyRequest)
		if resolveErr != nil || selection == nil || len(selection) != 0 {
			t.Fatalf("empty selection = %#v, error %v", selection, resolveErr)
		}
	}
	if emptyCalls.Load() != 1 {
		t.Fatalf("empty selection extension calls = %d, want 1", emptyCalls.Load())
	}
}

func TestSemanticExtensionMemoKeyIncludesOrderedCandidates(t *testing.T) {
	memo := newSemanticExtensionMemo()
	var calls atomic.Int32
	extension := semanticMemoExtensionFunc(func(_ context.Context, request SemanticExtensionRequest) ([]string, error) {
		calls.Add(1)
		return []string{request.Candidates[0].ID}, nil
	})

	for _, request := range []SemanticExtensionRequest{
		semanticMemoRequest("space", "theme:space", "theme:future"),
		semanticMemoRequest("space", "theme:future", "theme:space"),
		semanticMemoRequest("space", "theme:space", "theme:future"),
	} {
		if _, err := memo.resolve(context.Background(), extension, request); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("candidate-sensitive extension calls = %d, want 2", calls.Load())
	}
}

func TestSemanticExtensionMemoCoalescesAndIsolatesSlices(t *testing.T) {
	memo := newSemanticExtensionMemo()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	extension := semanticMemoExtensionFunc(func(_ context.Context, request SemanticExtensionRequest) ([]string, error) {
		calls.Add(1)
		close(started)
		<-release
		if request.Candidates[0].ID != "theme:space" {
			return nil, errors.New("request candidate mutated")
		}
		return []string{"theme:space"}, nil
	})
	request := semanticMemoRequest("space", "theme:space")
	type result struct {
		selection []string
		err       error
	}
	results := make(chan result, 2)
	go func() {
		selection, err := memo.resolve(context.Background(), extension, request)
		results <- result{selection, err}
	}()
	<-started
	request.Candidates[0].ID = "theme:mutated"
	go func() {
		selection, err := memo.resolve(context.Background(), extension, semanticMemoRequest("space", "theme:space"))
		results <- result{selection, err}
	}()
	waitForSemanticMemoWaiters(t, memo, semanticMemoRequest("space", "theme:space"), 2)
	close(release)
	for range 2 {
		result := <-results
		if result.err != nil || !slices.Equal(result.selection, []string{"theme:space"}) {
			t.Fatalf("coalesced selection = %v, error %v", result.selection, result.err)
		}
		result.selection[0] = "theme:changed"
	}
	cached, err := memo.resolve(context.Background(), extension, semanticMemoRequest("space", "theme:space"))
	if err != nil || !slices.Equal(cached, []string{"theme:space"}) || calls.Load() != 1 {
		t.Fatalf("isolated cached selection = %v, calls = %d, error %v", cached, calls.Load(), err)
	}
}

func TestSemanticExtensionMemoWaiterCancellationIsIndependent(t *testing.T) {
	memo := newSemanticExtensionMemo()
	started := make(chan struct{})
	release := make(chan struct{})
	operationCanceled := make(chan struct{})
	extension := semanticMemoExtensionFunc(func(ctx context.Context, _ SemanticExtensionRequest) ([]string, error) {
		close(started)
		select {
		case <-release:
			return []string{"theme:space"}, nil
		case <-ctx.Done():
			close(operationCanceled)
			return nil, ctx.Err()
		}
	})
	request := semanticMemoRequest("space", "theme:space")
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, err := memo.resolve(firstContext, extension, request)
		firstResult <- err
	}()
	<-started
	secondResult := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, request)
		secondResult <- err
	}()
	waitForSemanticMemoWaiters(t, memo, request, 2)
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v", err)
	}
	select {
	case <-operationCanceled:
		t.Fatal("one canceled waiter canceled shared operation")
	default:
	}
	close(release)
	if err := <-secondResult; err != nil {
		t.Fatalf("remaining waiter error = %v", err)
	}
}

func TestSemanticExtensionMemoCancelsOperationAfterLastWaiterLeaves(t *testing.T) {
	memo := newSemanticExtensionMemo()
	started := make(chan struct{})
	stopped := make(chan struct{})
	extension := semanticMemoExtensionFunc(func(ctx context.Context, _ SemanticExtensionRequest) ([]string, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := memo.resolve(ctx, extension, semanticMemoRequest("space", "theme:space"))
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("last waiter error = %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shared operation remained active after its last waiter canceled")
	}
}

func TestSemanticExtensionMemoEnforcesTimeoutAndDoesNotCacheErrorsOrInvalidOutput(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.budget = 20 * time.Millisecond
	var timeoutCalls atomic.Int32
	timeoutExtension := semanticMemoExtensionFunc(func(ctx context.Context, _ SemanticExtensionRequest) ([]string, error) {
		timeoutCalls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	request := semanticMemoRequest("space", "theme:space")
	for range 2 {
		started := time.Now()
		_, err := memo.resolve(context.Background(), timeoutExtension, request)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("bounded operation took %v", elapsed)
		}
	}
	if timeoutCalls.Load() != 2 {
		t.Fatalf("timed-out extension calls = %d, want 2", timeoutCalls.Load())
	}
	if semanticExtensionMemoBudget != 10*time.Second {
		t.Fatalf("production semantic extension budget = %v", semanticExtensionMemoBudget)
	}

	var invalidCalls atomic.Int32
	invalid := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) {
		invalidCalls.Add(1)
		return []string{"theme:unknown"}, nil
	})
	for range 2 {
		if _, err := memo.resolve(context.Background(), invalid, request); !errors.Is(err, errInvalidSemanticExtensionSelection) {
			t.Fatalf("invalid output error = %v", err)
		}
	}
	if invalidCalls.Load() != 2 {
		t.Fatalf("invalid extension calls = %d, want 2", invalidCalls.Load())
	}
}

func TestSemanticExtensionMemoEvictsOldestEntryAndExpiresEntries(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.capacity = 2
	memo.ttl = time.Hour
	now := time.Unix(1000, 0)
	memo.now = func() time.Time { return now }
	var mu sync.Mutex
	calls := make(map[string]int)
	extension := semanticMemoExtensionFunc(func(_ context.Context, request SemanticExtensionRequest) ([]string, error) {
		mu.Lock()
		calls[request.Query]++
		mu.Unlock()
		return []string{request.Candidates[0].ID}, nil
	})
	resolve := func(query string) {
		t.Helper()
		if _, err := memo.resolve(context.Background(), extension, semanticMemoRequest(query, "theme:space")); err != nil {
			t.Fatal(err)
		}
	}
	resolve("oldest")
	resolve("middle")
	resolve("newest")
	resolve("middle")
	resolve("oldest")
	mu.Lock()
	if calls["oldest"] != 2 || calls["middle"] != 1 || calls["newest"] != 1 {
		t.Fatalf("eviction calls = %v", calls)
	}
	mu.Unlock()

	now = now.Add(time.Hour)
	resolve("newest")
	mu.Lock()
	defer mu.Unlock()
	if calls["newest"] != 2 {
		t.Fatalf("expired entry calls = %d, want 2", calls["newest"])
	}
}

func TestSemanticExtensionMemoClearInvalidatesCacheAndInFlightCompletion(t *testing.T) {
	memo := newSemanticExtensionMemo()
	request := semanticMemoRequest("space", "theme:space")
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	extension := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return []string{"theme:space"}, nil
	})
	first := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, request)
		first <- err
	}()
	<-started
	memo.clear()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if _, err := memo.resolve(context.Background(), extension, request); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("extension calls after clear = %d, want 2", calls.Load())
	}
	memo.clear()
	if _, err := memo.resolve(context.Background(), extension, request); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("extension calls after cached clear = %d, want 3", calls.Load())
	}
}

func TestSemanticExtensionMemoClearDetachesOldFlight(t *testing.T) {
	memo := newSemanticExtensionMemo()
	request := semanticMemoRequest("space", "theme:space")
	oldStarted := make(chan struct{})
	oldStopped := make(chan struct{})
	old := semanticMemoExtensionFunc(func(ctx context.Context, _ SemanticExtensionRequest) ([]string, error) {
		close(oldStarted)
		<-ctx.Done()
		close(oldStopped)
		return nil, ctx.Err()
	})
	oldResult := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), old, request)
		oldResult <- err
	}()
	<-oldStarted

	memo.clear()
	newCalls := atomic.Int32{}
	replacement := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) {
		newCalls.Add(1)
		return []string{"theme:space"}, nil
	})
	selection, err := memo.resolve(context.Background(), replacement, request)
	if err != nil || !slices.Equal(selection, []string{"theme:space"}) || newCalls.Load() != 1 {
		t.Fatalf("replacement selection = %v, calls = %d, error %v", selection, newCalls.Load(), err)
	}
	select {
	case <-oldStopped:
	case <-time.After(time.Second):
		t.Fatal("clear did not cancel the detached old flight")
	}
	if err := <-oldResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("old flight error = %v", err)
	}
}

func TestSemanticExtensionMemoSharedContextRetainsValues(t *testing.T) {
	type contextKey struct{}
	memo := newSemanticExtensionMemo()
	extension := semanticMemoExtensionFunc(func(ctx context.Context, _ SemanticExtensionRequest) ([]string, error) {
		if got := ctx.Value(contextKey{}); got != "retained" {
			return nil, errors.New("context value was not retained")
		}
		return []string{"theme:space"}, nil
	})
	ctx := context.WithValue(context.Background(), contextKey{}, "retained")
	if _, err := memo.resolve(ctx, extension, semanticMemoRequest("space", "theme:space")); err != nil {
		t.Fatal(err)
	}
}

func TestSemanticExtensionMemoKeyIsBlindedVersionedAndIdentitySensitive(t *testing.T) {
	request := semanticMemoRequest("  SCI-FI Café ", "theme:space", "theme:future")
	keysOne, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x31}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	keysTwo, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 2, Bytes: bytes.Repeat([]byte{0x32}, 32)}, {Version: 1, Bytes: bytes.Repeat([]byte{0x31}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	first := newSemanticExtensionMemo()
	first.keys = keysOne
	second := newSemanticExtensionMemo()
	second.keys = keysTwo
	modelA := identifiedSemanticMemoExtension{identity: "ollama:model-a", resolve: func(context.Context, SemanticExtensionRequest) ([]string, error) { return nil, nil }}
	modelB := identifiedSemanticMemoExtension{identity: "ollama:model-b", resolve: modelA.resolve}
	key := first.makeKey(modelA, request)
	normalized := semanticMemoRequest("sci fi cafe", "theme:space", "theme:future")
	if key != first.makeKey(modelA, normalized) {
		t.Fatal("equivalent normalized request produced another blind index")
	}
	if key == first.makeKey(modelB, request) || key == first.makeKey(modelA, semanticMemoRequest("sci fi cafe", "theme:future", "theme:space")) {
		t.Fatal("model identity or candidate order was omitted from memo key")
	}
	if key == second.makeKey(modelA, request) {
		t.Fatal("key rotation did not make the semantic memo cold")
	}
	if bytes.Contains(key[:], []byte("sci")) || bytes.Contains(key[:], []byte("space")) {
		t.Fatal("blind memo key contains user input")
	}
}

func TestSemanticExtensionMemoPersistsReloadsExpiresAndIgnoresStoreFailure(t *testing.T) {
	now := time.Unix(10_000, 0)
	store := &memorySemanticMemoStore{upserted: make(chan struct{}, 1)}
	keys, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x41}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	calls := atomic.Int32{}
	extension := identifiedSemanticMemoExtension{identity: "ollama:model", resolve: func(context.Context, SemanticExtensionRequest) ([]string, error) {
		calls.Add(1)
		return []string{}, nil
	}}
	first := newSemanticExtensionMemo()
	first.now = func() time.Time { return now }
	first.configurePersistence(context.Background(), keys, store)
	selection, err := first.resolve(context.Background(), extension, semanticMemoRequest("nothing", "theme:space"))
	if err != nil || selection == nil {
		t.Fatalf("persistent resolve = %#v, %v", selection, err)
	}
	select {
	case <-store.upserted:
	case <-time.After(time.Second):
		t.Fatal("successful empty selection was not persisted")
	}
	second := newSemanticExtensionMemo()
	second.now = func() time.Time { return now.Add(time.Minute) }
	second.configurePersistence(context.Background(), keys, store)
	selection, err = second.resolve(context.Background(), extension, semanticMemoRequest("nothing", "theme:space"))
	if err != nil || selection == nil || calls.Load() != 1 {
		t.Fatalf("reloaded selection = %#v calls=%d err=%v", selection, calls.Load(), err)
	}
	rotatedKeys, rotateErr := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 2, Bytes: bytes.Repeat([]byte{0x42}, 32)}, {Version: 1, Bytes: bytes.Repeat([]byte{0x41}, 32)}})
	if rotateErr != nil {
		t.Fatal(rotateErr)
	}
	rotated := newSemanticExtensionMemo()
	rotated.now = func() time.Time { return now.Add(time.Minute) }
	rotated.configurePersistence(context.Background(), rotatedKeys, store)
	if _, err := rotated.resolve(context.Background(), extension, semanticMemoRequest("nothing", "theme:space")); err != nil || calls.Load() != 2 {
		t.Fatalf("rotated persistence was not cold: calls=%d err=%v", calls.Load(), err)
	}
	third := newSemanticExtensionMemo()
	third.now = func() time.Time { return now.Add(semanticExtensionMemoTTL) }
	third.configurePersistence(context.Background(), keys, store)
	if _, err := third.resolve(context.Background(), extension, semanticMemoRequest("nothing", "theme:space")); err != nil || calls.Load() != 3 {
		t.Fatalf("expired persistence calls=%d err=%v", calls.Load(), err)
	}
	failing := &memorySemanticMemoStore{upsertErr: errors.New("database unavailable")}
	degraded := newSemanticExtensionMemo()
	degraded.now = func() time.Time { return now }
	degraded.configurePersistence(context.Background(), keys, failing)
	if _, err := degraded.resolve(context.Background(), extension, semanticMemoRequest("degraded", "theme:space")); err != nil {
		t.Fatalf("database failure blocked semantic success: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		degraded.mu.Lock()
		status := degraded.persistentStatus
		degraded.mu.Unlock()
		if status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("persistent status = %q, want failed", status)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestSemanticExtensionMemoNeverPersistsFailures(t *testing.T) {
	now := time.Unix(10_000, 0)
	keys, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x61}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	failureStore := &memorySemanticMemoStore{upserted: make(chan struct{}, 1)}
	failureMemo := newSemanticExtensionMemo()
	failureMemo.now = func() time.Time { return now }
	failureMemo.configurePersistence(context.Background(), keys, failureStore)
	failingExtension := identifiedSemanticMemoExtension{identity: "ollama:failing", resolve: func(context.Context, SemanticExtensionRequest) ([]string, error) {
		return nil, errors.New("model failure")
	}}
	if _, err := failureMemo.resolve(context.Background(), failingExtension, semanticMemoRequest("failure", "theme:space")); err == nil {
		t.Fatal("model failure was hidden")
	}
	select {
	case <-failureStore.upserted:
		t.Fatal("failed model response was persisted")
	case <-time.After(10 * time.Millisecond):
	}
}

func TestSemanticExtensionMemoLoadFailureDegradesWithoutBlocking(t *testing.T) {
	keys, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: bytes.Repeat([]byte{0x51}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	memo := newSemanticExtensionMemo()
	memo.configurePersistence(context.Background(), keys, &memorySemanticMemoStore{loadErr: errors.New("database unavailable")})
	if memo.persistentStatus != "failed" {
		t.Fatalf("persistent status = %q, want failed", memo.persistentStatus)
	}
	extension := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) { return []string{"theme:space"}, nil })
	if selection, err := memo.resolve(context.Background(), extension, semanticMemoRequest("space", "theme:space")); err != nil || !slices.Equal(selection, []string{"theme:space"}) {
		t.Fatalf("degraded selection = %v, error %v", selection, err)
	}
}

func TestSemanticExtensionMemoAdmitsOneActiveOneQueuedAndCoalescesFullKey(t *testing.T) {
	memo := newSemanticExtensionMemo()
	activeStarted, releaseActive := make(chan struct{}), make(chan struct{})
	queuedStarted := make(chan struct{})
	extension := semanticMemoExtensionFunc(func(_ context.Context, request SemanticExtensionRequest) ([]string, error) {
		switch request.Query {
		case "active":
			close(activeStarted)
			<-releaseActive
		case "queued":
			close(queuedStarted)
		}
		return []string{"theme:space"}, nil
	})
	results := make(chan error, 3)
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("active", "theme:space"))
		results <- err
	}()
	<-activeStarted
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("queued", "theme:space"))
		results <- err
	}()
	waitForSemanticMemoGauges(t, memo, 1, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("queued", "theme:space"))
		results <- err
	}()
	waitForSemanticMemoWaiters(t, memo, semanticMemoRequest("queued", "theme:space"), 2)
	if _, err := memo.resolve(context.Background(), extension, semanticMemoRequest("third", "theme:space")); !errors.Is(err, ErrSemanticExtensionBusy) {
		t.Fatalf("third distinct request error = %v, want busy", err)
	}
	select {
	case <-queuedStarted:
		t.Fatal("queued execution started before promotion")
	default:
	}
	close(releaseActive)
	select {
	case <-queuedStarted:
	case <-time.After(time.Second):
		t.Fatal("queued execution was not promoted")
	}
	for range 3 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	status := (&Service{semanticExtension: extension, semanticMemo: memo, semanticWarmupStatus: "pending"}).SemanticExtensionOperationsStatus()
	if status.Executions != 2 || status.Successes != 2 || status.CoalescedWaiters != 1 || status.BusyFallbacks != 1 || status.Misses != 3 || status.Active != 0 || status.Queued != 0 {
		t.Fatalf("admission metrics = %+v", status)
	}
}

func TestSemanticExtensionOperationsTerminalBucketsIncludeCancellation(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.mu.Lock()
	memo.metrics.executions = 7
	memo.metrics.successes = 3
	memo.metrics.timeouts = 1
	memo.metrics.failures = 2
	memo.metrics.cancellations = 1
	memo.mu.Unlock()
	status := (&Service{semanticMemo: memo}).SemanticExtensionOperationsStatus()
	if status.Cancellations != 1 || status.Executions != status.Successes+status.Timeouts+status.Failures+status.Cancellations {
		t.Fatalf("semantic terminal buckets = %+v", status)
	}
}

func TestSemanticExtensionMemoQueuedBudgetStartsAtPromotionAndLastWaiterRemovesQueue(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.budget = 20 * time.Millisecond
	activeStarted, releaseActive := make(chan struct{}), make(chan struct{})
	extension := semanticMemoExtensionFunc(func(ctx context.Context, request SemanticExtensionRequest) ([]string, error) {
		if request.Query == "active" {
			close(activeStarted)
			<-releaseActive
			return []string{"theme:space"}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	go func() {
		_, _ = memo.resolve(context.Background(), extension, semanticMemoRequest("active", "theme:space"))
	}()
	<-activeStarted
	queuedContext, cancelQueued := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() {
		_, err := memo.resolve(queuedContext, extension, semanticMemoRequest("queued", "theme:space"))
		queuedDone <- err
	}()
	waitForSemanticMemoGauges(t, memo, 1, 1)
	time.Sleep(2 * memo.budget)
	cancelQueued()
	if err := <-queuedDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation = %v", err)
	}
	waitForSemanticMemoGauges(t, memo, 1, 0)
	promotedDone := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("promoted", "theme:space"))
		promotedDone <- err
	}()
	waitForSemanticMemoGauges(t, memo, 1, 1)
	promotedAt := time.Now()
	close(releaseActive)
	if err := <-promotedDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("promoted timeout = %v", err)
	}
	if elapsed := time.Since(promotedAt); elapsed < memo.budget/2 {
		t.Fatalf("queued budget was spent before promotion: %v", elapsed)
	}
}

func TestSemanticExtensionMemoWarmupUsesIndependentBudget(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.budget, memo.warmupBudget = 5*time.Millisecond, 100*time.Millisecond
	started := time.Now()
	err := memo.runWarmup(context.Background(), memo.warmupKey(semanticMemoIdentity("warmup-budget")), func(ctx context.Context) error {
		select {
		case <-time.After(25 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("warmup beyond interactive budget failed: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("warmup returned before intended work: %v", elapsed)
	}
	if memo.executionBudget() != 5*time.Millisecond || memo.warmupExecutionBudget() != 100*time.Millisecond {
		t.Fatalf("budgets search=%v warmup=%v", memo.executionBudget(), memo.warmupExecutionBudget())
	}
}

func TestSemanticExtensionMemoSearchPreemptsActiveWarmupWithoutOverlap(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.warmupBudget = time.Second
	warmStarted, warmCanceled, allowWarmExit := make(chan struct{}), make(chan struct{}), make(chan struct{})
	warmResult := make(chan error, 1)
	go func() {
		warmResult <- memo.runWarmup(context.Background(), memo.warmupKey(semanticMemoIdentity("preempted-warmup")), func(ctx context.Context) error {
			close(warmStarted)
			<-ctx.Done()
			close(warmCanceled)
			<-allowWarmExit
			return ctx.Err()
		})
	}()
	<-warmStarted
	searchStarted := make(chan struct{})
	extension := semanticMemoExtensionFunc(func(context.Context, SemanticExtensionRequest) ([]string, error) {
		close(searchStarted)
		return []string{"theme:space"}, nil
	})
	searchResult := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("search", "theme:space"))
		searchResult <- err
	}()
	select {
	case <-warmCanceled:
	case <-time.After(time.Second):
		t.Fatal("search did not cancel active warmup")
	}
	waitForSemanticMemoGauges(t, memo, 1, 1)
	select {
	case <-searchStarted:
		t.Fatal("search overlapped canceled warmup")
	default:
	}
	close(allowWarmExit)
	if err := <-warmResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("preempted warmup error = %v", err)
	}
	select {
	case <-searchStarted:
	case <-time.After(time.Second):
		t.Fatal("search was not promoted after warmup exit")
	}
	if err := <-searchResult; err != nil {
		t.Fatal(err)
	}
}

func TestSemanticExtensionMemoSearchReplacesQueuedWarmup(t *testing.T) {
	memo := newSemanticExtensionMemo()
	activeStarted, releaseActive := make(chan struct{}), make(chan struct{})
	searchStarted := make(chan struct{})
	extension := semanticMemoExtensionFunc(func(_ context.Context, request SemanticExtensionRequest) ([]string, error) {
		if request.Query == "active" {
			close(activeStarted)
			<-releaseActive
		} else {
			close(searchStarted)
		}
		return []string{"theme:space"}, nil
	})
	activeResult := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("active", "theme:space"))
		activeResult <- err
	}()
	<-activeStarted
	warmStarted, warmResult := make(chan struct{}), make(chan error, 1)
	go func() {
		warmResult <- memo.runWarmup(context.Background(), memo.warmupKey(semanticMemoIdentity("queued-warmup")), func(context.Context) error { close(warmStarted); return nil })
	}()
	waitForSemanticMemoGauges(t, memo, 1, 1)
	searchResult := make(chan error, 1)
	go func() {
		_, err := memo.resolve(context.Background(), extension, semanticMemoRequest("replacement", "theme:space"))
		searchResult <- err
	}()
	if err := <-warmResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("replaced warmup error = %v", err)
	}
	select {
	case <-warmStarted:
		t.Fatal("replaced queued warmup executed")
	default:
	}
	waitForSemanticMemoGauges(t, memo, 1, 1)
	close(releaseActive)
	if err := <-activeResult; err != nil {
		t.Fatal(err)
	}
	select {
	case <-searchStarted:
	case <-time.After(time.Second):
		t.Fatal("replacement search was not promoted")
	}
	if err := <-searchResult; err != nil {
		t.Fatal(err)
	}
}

func TestSemanticExtensionMemoLatencyPercentilesUseBoundedRing(t *testing.T) {
	memo := newSemanticExtensionMemo()
	memo.mu.Lock()
	for value := 1; value <= semanticExtensionLatencySamples+10; value++ {
		memo.recordLatencyLocked(time.Duration(value) * time.Millisecond)
	}
	p50, p95 := memo.percentileLocked(50), memo.percentileLocked(95)
	memo.mu.Unlock()
	if p50 != 138 || p95 != 254 {
		t.Fatalf("latency percentiles = p50 %d p95 %d", p50, p95)
	}
}

func TestSemanticExtensionMemoExcludesCanceledAndWarmupLatency(t *testing.T) {
	memo := newSemanticExtensionMemo()
	now := time.Unix(0, 0)
	memo.now = func() time.Time { return now }
	canceled := &semanticExtensionFlight{key: semanticExtensionMemoKey{1}, done: make(chan struct{}), waiters: 1, started: true, startedAt: now, cancel: func() {}, run: func(context.Context) ([]string, error) {
		now = now.Add(500 * time.Millisecond)
		return nil, context.Canceled
	}}
	memo.flights[canceled.key], memo.active = canceled, canceled
	memo.runFlight(canceled, context.Background())
	warmup := &semanticExtensionFlight{key: semanticExtensionMemoKey{2}, done: make(chan struct{}), waiters: 1, started: true, startedAt: now, cancel: func() {}, warmup: true, run: func(context.Context) ([]string, error) {
		now = now.Add(time.Second)
		return nil, nil
	}}
	memo.flights[warmup.key], memo.active = warmup, warmup
	memo.runFlight(warmup, context.Background())
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.metrics.cancellations != 1 || memo.metrics.latencyCount != 0 {
		t.Fatalf("cancellation/latency metrics = %+v", memo.metrics)
	}
}

func waitForSemanticMemoGauges(t *testing.T, memo *semanticExtensionMemo, active, queued int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		memo.mu.Lock()
		gotActive, gotQueued := 0, 0
		if memo.active != nil {
			gotActive = 1
		}
		if memo.queued != nil {
			gotQueued = 1
		}
		memo.mu.Unlock()
		if gotActive == active && gotQueued == queued {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("semantic memo gauges active=%d queued=%d, want %d/%d", gotActive, gotQueued, active, queued)
		}
		time.Sleep(time.Millisecond)
	}
}

type warmableSemanticMemoExtension struct {
	resolveStarted chan struct{}
	releaseResolve chan struct{}
	warmStarted    chan struct{}
}

func (extension *warmableSemanticMemoExtension) Resolve(context.Context, SemanticExtensionRequest) ([]string, error) {
	close(extension.resolveStarted)
	<-extension.releaseResolve
	return []string{"theme:space"}, nil
}

func (*warmableSemanticMemoExtension) SemanticCacheIdentity() string { return "warmable:model" }
func (extension *warmableSemanticMemoExtension) Warmup(context.Context) error {
	close(extension.warmStarted)
	return nil
}

func TestServiceWarmSemanticExtensionSharesGateWithoutCaching(t *testing.T) {
	extension := &warmableSemanticMemoExtension{resolveStarted: make(chan struct{}), releaseResolve: make(chan struct{}), warmStarted: make(chan struct{})}
	service := &Service{semanticExtension: extension, semanticMemo: newSemanticExtensionMemo(), semanticWarmupStatus: "pending"}
	resolved := make(chan error, 1)
	go func() {
		_, err := service.semanticMemo.resolve(context.Background(), extension, semanticMemoRequest("active", "theme:space"))
		resolved <- err
	}()
	<-extension.resolveStarted
	warmed := make(chan error, 1)
	go func() { warmed <- service.WarmSemanticExtension(context.Background()) }()
	waitForSemanticMemoGauges(t, service.semanticMemo, 1, 1)
	if status := service.SemanticExtensionOperationsStatus(); status.WarmupStatus != "pending" || status.MemoryEntries != 0 {
		t.Fatalf("warmup pending snapshot = %+v", status)
	}
	select {
	case <-extension.warmStarted:
		t.Fatal("warmup bypassed semantic admission gate")
	default:
	}
	close(extension.releaseResolve)
	if err := <-resolved; err != nil {
		t.Fatal(err)
	}
	if err := <-warmed; err != nil {
		t.Fatal(err)
	}
	status := service.SemanticExtensionOperationsStatus()
	if status.WarmupStatus != "ready" || status.MemoryEntries != 1 || status.Executions != 2 || status.Successes != 2 {
		t.Fatalf("warmup ready snapshot = %+v", status)
	}
}

func waitForSemanticMemoWaiters(t *testing.T, memo *semanticExtensionMemo, request SemanticExtensionRequest, want int) {
	t.Helper()
	key := makeSemanticExtensionMemoKey(request)
	deadline := time.Now().Add(time.Second)
	for {
		memo.mu.Lock()
		flight := memo.flights[key]
		got := 0
		if flight != nil {
			got = flight.waiters
		}
		memo.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("semantic extension flight waiters = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}
