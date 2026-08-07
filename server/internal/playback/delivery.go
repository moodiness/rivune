package playback

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moodiness/rivune/server/internal/auth"
)

// Aggregate limits admit one maximum-sized playlist for any user while
// bounding the service to four such live child sets across all open handles.
const (
	deliveryChildQueryName           = "RivuneChildId"
	maximumDeliveryChildren          = maximumPlaylistReferences
	maximumDeliveryChildrenPerUser   = maximumDeliveryChildren
	maximumDeliveryChildrenGlobal    = 4 * maximumDeliveryChildrenPerUser
	maximumDeliveryChildIDLength     = 64
	maximumDeliveryStartTicksLength  = 32
	maximumDeliveryCompatTokenLength = 128
	maximumDeliveryTargetLength      = 4096
	deliveryChildTTL                 = 5 * time.Minute
)

var errDeliveryChildCapacity = errors.New("playback child capability capacity exceeded")

type deliveryChildState struct {
	assetID   string
	file      string
	start     string
	target    string
	signature string
}

type deliveryChildEntry struct {
	state        deliveryChildState
	advertisedAt time.Time
	expiresAt    time.Time
	resolved     bool
	previousID   string
	nextID       string
}

type deliveryChildBudget struct {
	mu        sync.Mutex
	live      int
	byUser    map[string]int
	tables    sync.Map
	globalMax int
	userMax   int
}

type deliveryChildTable struct {
	mu      sync.Mutex
	entries map[string]deliveryChildEntry
	byState map[deliveryChildState]string
	headID  string
	tailID  string
	now     func() time.Time
	budget  *deliveryChildBudget
	userID  string
	closed  bool
}

type deliveryLinkTemplate struct {
	path            string
	playSessionID   string
	mediaSourceID   string
	startTimeTicks  string
	queryCredential string
}

type deliveryRequest struct {
	request   *http.Request
	assetID   string
	target    string
	signature string
	template  deliveryLinkTemplate
}

// Delivery contains playback metadata that is safe for transport adapters and
// the in-process capability required to serve its selected asset. Handle must
// stay server-side; it is intentionally omitted from JSON.
type Delivery struct {
	Session Session        `json:"session"`
	Handle  DeliveryHandle `json:"-"`
}

// DeliveryHandle is an opaque, in-process playback capability. Its fields are
// deliberately private so transport adapters cannot inspect or expose native
// playback credentials.
type DeliveryHandle struct {
	sessionID    string
	assetID      string
	token        string
	defaultFile  string
	defaultStart string
	children     *deliveryChildTable
}

// MarshalJSON prevents the capability contents from entering an adapter DTO if
// a handle is accidentally marshaled on its own.
func (DeliveryHandle) MarshalJSON() ([]byte, error) {
	return []byte("{}"), nil
}

// Format keeps opaque capability contents out of structured and printf-style
// logs, including verbose formatting.
func (DeliveryHandle) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "playback.DeliveryHandle(opaque)")
}

// Valid reports whether the opaque capability can be passed to Serve or Close.
func (handle DeliveryHandle) Valid() bool {
	return handle.sessionID != "" && handle.assetID != "" && handle.token != ""
}

// Open resolves a source through the native playback pipeline while returning
// a URL-free session view and an opaque delivery capability.
func (service *Service) Open(ctx context.Context, principal auth.Principal, input ResolveInput) (Delivery, error) {
	var handle DeliveryHandle
	session, err := service.resolve(ctx, principal, input, &handle)
	if err != nil {
		return Delivery{}, err
	}
	if !handle.Valid() {
		if stopErr := service.Stop(ctx, principal, session.ID); stopErr != nil {
			return Delivery{}, fmt.Errorf("discard unsupported playback delivery: %w", stopErr)
		}
		return Delivery{}, ErrUnsupportedSource
	}
	return Delivery{Session: deliverySafeSession(session), Handle: handle}, nil
}

// Serve delivers the selected asset through ProxyAsset. Compat child links are
// adapter-owned URLs backed by reusable, short-lived capabilities in the
// opaque handle; native playback credentials never enter those URLs.
func (service *Service) Serve(w http.ResponseWriter, r *http.Request, handle DeliveryHandle) error {
	if !handle.Valid() || handle.children == nil {
		return ErrSessionNotFound
	}
	delivery, err := requestForDelivery(r, handle)
	if err != nil {
		return err
	}
	buildChildURL := func(state deliveryChildState) (string, error) {
		childID, registerErr := handle.children.register(state)
		if registerErr != nil {
			return "", registerErr
		}
		return delivery.template.childURL(childID), nil
	}
	return service.proxyAsset(
		w, delivery.request, handle.sessionID, delivery.assetID, handle.token,
		delivery.target, delivery.signature, buildChildURL,
	)
}

func requestForDelivery(request *http.Request, handle DeliveryHandle) (deliveryRequest, error) {
	template, err := newDeliveryLinkTemplate(request.URL)
	if err != nil {
		return deliveryRequest{}, ErrSessionNotFound
	}
	state := deliveryChildState{
		assetID: handle.assetID,
		file:    handle.defaultFile,
		start:   handle.defaultStart,
	}
	if childID, found, childErr := deliveryQueryScalar(request.URL.Query(), deliveryChildQueryName); childErr != nil {
		return deliveryRequest{}, ErrSessionNotFound
	} else if found {
		if handle.children == nil || len(childID) == 0 || len(childID) > maximumDeliveryChildIDLength {
			return deliveryRequest{}, ErrSessionNotFound
		}
		resolved, ok := handle.children.resolve(childID)
		state = resolved
		if !ok {
			return deliveryRequest{}, ErrSessionNotFound
		}
	}
	cloned := request.Clone(request.Context())
	cloned.URL = cloneRequestURL(request)
	query := make(url.Values)
	if state.file != "" {
		query.Set("file", state.file)
	}
	if state.start != "" {
		query.Set("start", state.start)
	}
	cloned.URL.RawQuery = query.Encode()
	return deliveryRequest{
		request: cloned, assetID: state.assetID, target: state.target,
		signature: state.signature, template: template,
	}, nil
}

// Close terminates a delivery through the same profile-bound Stop path used by
// native playback and discards all outstanding child capabilities.
func (service *Service) Close(ctx context.Context, principal auth.Principal, handle DeliveryHandle) error {
	if service == nil || !handle.Valid() {
		return ErrSessionNotFound
	}
	if handle.children != nil {
		handle.children.clear()
	}
	return service.closeDeliverySession(ctx, principal, handle)
}

func cloneRequestURL(request *http.Request) *url.URL {
	cloned := *request.URL
	return &cloned
}

func newDeliveryChildBudget(globalMax, userMax int) *deliveryChildBudget {
	return &deliveryChildBudget{
		byUser: make(map[string]int), globalMax: globalMax, userMax: userMax,
	}
}

func (budget *deliveryChildBudget) reserve(userID string) bool {
	if budget == nil {
		return true
	}
	if userID == "" {
		return false
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.live >= budget.globalMax || budget.byUser[userID] >= budget.userMax {
		return false
	}
	budget.live++
	budget.byUser[userID]++
	return true
}

func (budget *deliveryChildBudget) release(userID string, count int) {
	if budget == nil || count == 0 {
		return
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	userLive := budget.byUser[userID]
	if count < 0 || count > userLive || count > budget.live {
		panic("playback delivery child budget accounting invariant violated")
	}
	budget.live -= count
	userLive -= count
	if userLive == 0 {
		delete(budget.byUser, userID)
	} else {
		budget.byUser[userID] = userLive
	}
}

func (budget *deliveryChildBudget) usage(userID string) (int, int) {
	if budget == nil {
		return 0, 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.live, budget.byUser[userID]
}

func (table *deliveryChildTable) bindBudget(budget *deliveryChildBudget, userID string, now func() time.Time) {
	if table == nil || budget == nil {
		return
	}
	table.mu.Lock()
	if len(table.entries) != 0 || table.budget != nil {
		table.mu.Unlock()
		panic("playback delivery child table bound after use")
	}
	table.budget = budget
	table.userID = userID
	if now != nil {
		table.now = now
	}
	table.mu.Unlock()
}

func (service *Service) deliveryChildBudget() *deliveryChildBudget {
	service.deliveryChildrenMu.Lock()
	defer service.deliveryChildrenMu.Unlock()
	if service.deliveryChildren == nil {
		service.deliveryChildren = newDeliveryChildBudget(maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerUser)
	}
	return service.deliveryChildren
}

// PruneDeliveryHandles expires unused compatibility child capabilities across
// all open deliveries without disclosing or accepting any capability value.
func (service *Service) PruneDeliveryHandles() {
	if service == nil {
		return
	}
	service.deliveryChildrenMu.Lock()
	budget := service.deliveryChildren
	service.deliveryChildrenMu.Unlock()
	if budget == nil {
		return
	}
	budget.tables.Range(func(candidate, _ any) bool {
		candidate.(*deliveryChildTable).prune()
		return true
	})
}

func (table *deliveryChildTable) prune() {
	table.mu.Lock()
	if !table.closed {
		table.expireLocked(table.now())
	}
	table.mu.Unlock()
}

func newDeliveryChildTable() *deliveryChildTable {
	return &deliveryChildTable{
		entries: make(map[string]deliveryChildEntry),
		byState: make(map[deliveryChildState]string),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (table *deliveryChildTable) register(state deliveryChildState) (string, error) {
	if table == nil || !validDeliveryChildState(state) {
		return "", ErrSessionNotFound
	}
	table.mu.Lock()
	if table.closed {
		table.mu.Unlock()
		return "", ErrSessionNotFound
	}
	now := table.now()
	table.expireLocked(now)
	if existingID, exists := table.byState[state]; exists {
		table.touchExistingLocked(existingID, now)
		table.mu.Unlock()
		return existingID, nil
	}
	table.mu.Unlock()

	for range 4 {
		var random [24]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", fmt.Errorf("generate playback child capability: %w", err)
		}
		childID := base64.RawURLEncoding.EncodeToString(random[:])
		table.mu.Lock()
		if table.closed {
			table.mu.Unlock()
			return "", ErrSessionNotFound
		}
		now = table.now()
		table.expireLocked(now)
		if existingID, exists := table.byState[state]; exists {
			table.touchExistingLocked(existingID, now)
			table.mu.Unlock()
			return existingID, nil
		}
		if len(table.entries) >= maximumDeliveryChildren {
			table.mu.Unlock()
			return "", errDeliveryChildCapacity
		}
		if _, exists := table.entries[childID]; exists {
			table.mu.Unlock()
			continue
		}
		if !table.budget.reserve(table.userID) {
			table.mu.Unlock()
			return "", errDeliveryChildCapacity
		}
		entry := deliveryChildEntry{
			state: state, advertisedAt: now, expiresAt: now.Add(deliveryChildTTL),
		}
		table.appendLocked(childID, entry)
		table.byState[state] = childID
		if table.budget != nil {
			table.budget.tables.Store(table, struct{}{})
		}
		table.mu.Unlock()
		return childID, nil
	}
	return "", errors.New("generate unique playback child capability")
}

func (table *deliveryChildTable) resolve(childID string) (deliveryChildState, bool) {
	if table == nil || len(childID) == 0 || len(childID) > maximumDeliveryChildIDLength {
		return deliveryChildState{}, false
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	now := table.now()
	table.expireLocked(now)
	entry, ok := table.entries[childID]
	if !ok {
		return deliveryChildState{}, false
	}
	entry.resolved = true
	table.refreshLocked(childID, entry, now)
	return entry.state, true
}

func (table *deliveryChildTable) clear() {
	if table == nil {
		return
	}
	table.mu.Lock()
	budget := table.budget
	table.clearLocked()
	table.closed = true
	table.mu.Unlock()
	if budget != nil {
		budget.tables.Delete(table)
	}
}

func (table *deliveryChildTable) clearLocked() {
	table.budget.release(table.userID, len(table.entries))
	clear(table.entries)
	clear(table.byState)
	table.headID = ""
	table.tailID = ""
}

func (table *deliveryChildTable) expireLocked(now time.Time) {
	for table.headID != "" {
		childID := table.headID
		entry, exists := table.entries[childID]
		if !exists {
			table.headID = ""
			table.tailID = ""
			return
		}
		if entry.expiresAt.After(now) {
			return
		}
		table.removeLocked(childID, entry)
	}
}

func (table *deliveryChildTable) appendLocked(childID string, entry deliveryChildEntry) {
	entry.previousID = table.tailID
	entry.nextID = ""
	if table.tailID != "" {
		tail := table.entries[table.tailID]
		tail.nextID = childID
		table.entries[table.tailID] = tail
	} else {
		table.headID = childID
	}
	table.tailID = childID
	table.entries[childID] = entry
}

func (table *deliveryChildTable) refreshLocked(childID string, entry deliveryChildEntry, now time.Time) {
	table.unlinkLocked(entry)
	entry.expiresAt = now.Add(deliveryChildTTL)
	table.appendLocked(childID, entry)
}

func (table *deliveryChildTable) removeLocked(childID string, entry deliveryChildEntry) {
	table.unlinkLocked(entry)
	delete(table.entries, childID)
	if table.byState[entry.state] == childID {
		delete(table.byState, entry.state)
	}
	table.budget.release(table.userID, 1)
	if len(table.entries) == 0 && table.budget != nil {
		table.budget.tables.Delete(table)
	}
}

func (table *deliveryChildTable) unlinkLocked(entry deliveryChildEntry) {
	if entry.previousID != "" {
		previous := table.entries[entry.previousID]
		previous.nextID = entry.nextID
		table.entries[entry.previousID] = previous
	} else {
		table.headID = entry.nextID
	}
	if entry.nextID != "" {
		next := table.entries[entry.nextID]
		next.previousID = entry.previousID
		table.entries[entry.nextID] = next
	} else {
		table.tailID = entry.previousID
	}
}

func (table *deliveryChildTable) touchExistingLocked(childID string, now time.Time) {
	entry, exists := table.entries[childID]
	if !exists {
		return
	}
	if !entry.resolved {
		entry.advertisedAt = now
	}
	table.refreshLocked(childID, entry, now)
}

func validDeliveryChildState(state deliveryChildState) bool {
	if state.assetID == "" || len(state.assetID) > 128 || len(state.file) > 128 ||
		len(state.start) > 32 || len(state.target) > maximumDeliveryTargetLength || len(state.signature) > 128 {
		return false
	}
	if state.target != "" || state.signature != "" {
		return state.target != "" && state.signature != "" && state.file == "" && state.start == ""
	}
	if state.file == "" || !localMediaName.MatchString(state.file) {
		return false
	}
	_, err := processedMediaStart(state.start)
	return err == nil
}

func newDeliveryLinkTemplate(requestURL *url.URL) (deliveryLinkTemplate, error) {
	if requestURL == nil || requestURL.Path == "" || len(requestURL.Path) > 2048 {
		return deliveryLinkTemplate{}, ErrSessionNotFound
	}
	values := requestURL.Query()
	playID, playFound, err := deliveryQueryScalar(values, "PlaySessionId")
	if err != nil || !playFound || playID == "" || len(playID) > 128 {
		return deliveryLinkTemplate{}, ErrSessionNotFound
	}
	mediaID, mediaFound, err := deliveryQueryScalar(values, "MediaSourceId")
	if err != nil || !mediaFound || mediaID == "" || len(mediaID) > 128 {
		return deliveryLinkTemplate{}, ErrSessionNotFound
	}
	startTimeTicks, ticksFound, err := deliveryQueryScalar(values, "StartTimeTicks")
	if err != nil {
		return deliveryLinkTemplate{}, ErrSessionNotFound
	}
	if ticksFound {
		if startTimeTicks == "" || len(startTimeTicks) > maximumDeliveryStartTicksLength {
			return deliveryLinkTemplate{}, ErrSessionNotFound
		}
		parsed, parseErr := strconv.ParseInt(startTimeTicks, 10, 64)
		if parseErr != nil || parsed < 0 {
			return deliveryLinkTemplate{}, ErrSessionNotFound
		}
		startTimeTicks = strconv.FormatInt(parsed, 10)
	}
	var queryCredential string
	if candidates, found := values["api_key"]; found {
		if len(candidates) != 1 {
			return deliveryLinkTemplate{}, ErrSessionNotFound
		}
		queryCredential = strings.TrimSpace(candidates[0])
		if queryCredential == "" || len(queryCredential) > maximumDeliveryCompatTokenLength || strings.ContainsAny(queryCredential, "\r\n\t ") {
			return deliveryLinkTemplate{}, ErrSessionNotFound
		}
	}
	return deliveryLinkTemplate{
		path: requestURL.Path, playSessionID: playID, mediaSourceID: mediaID,
		startTimeTicks: startTimeTicks, queryCredential: queryCredential,
	}, nil
}

func (template deliveryLinkTemplate) childURL(childID string) string {
	query := url.Values{
		"PlaySessionId":        []string{template.playSessionID},
		"MediaSourceId":        []string{template.mediaSourceID},
		deliveryChildQueryName: []string{childID},
	}
	if template.startTimeTicks != "" {
		query.Set("StartTimeTicks", template.startTimeTicks)
	}
	if template.queryCredential != "" {
		query.Set("api_key", template.queryCredential)
	}
	return (&url.URL{Path: template.path, RawQuery: query.Encode()}).String()
}

func deliveryQueryScalar(values url.Values, name string) (string, bool, error) {
	var found []string
	for actual, candidates := range values {
		if strings.EqualFold(actual, name) {
			found = append(found, candidates...)
		}
	}
	if len(found) == 0 {
		return "", false, nil
	}
	if len(found) != 1 {
		return "", true, ErrSessionNotFound
	}
	return found[0], true, nil
}

func deliverySafeSession(session Session) Session {
	safe := session
	safe.Sources = append([]Source(nil), session.Sources...)
	for index := range safe.Sources {
		safe.Sources[index].URL = ""
	}
	safe.Subtitles = append([]Subtitle(nil), session.Subtitles...)
	for index := range safe.Subtitles {
		safe.Subtitles[index].URL = ""
	}
	return safe
}

func deliveryHandleForSession(sessionID, token string, sources []Source, assets []storedAsset) DeliveryHandle {
	selectedID := firstCompatibleSource(sources)
	assetIndex := storedAssetIndex(assets, selectedID)
	if selectedID == "" || assetIndex < 0 {
		return DeliveryHandle{}
	}
	handle := DeliveryHandle{sessionID: sessionID, assetID: selectedID, token: token, children: newDeliveryChildTable()}
	for index := range sources {
		source := sources[index]
		if source.ID != selectedID {
			continue
		}
		if source.Protocol == "hls" && (source.Mode == processingRemux || source.Mode == processingTranscodeAudio || source.Mode == processingTranscode) {
			handle.defaultFile = "index.m3u8"
			if assets[assetIndex].StartSeconds > 0 {
				handle.defaultStart = hlsStartKey(assets[assetIndex].StartSeconds)
			}
		}
		break
	}
	return handle
}
