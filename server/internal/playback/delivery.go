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
	deliveryChildQueryName            = "RivuneChildId"
	maximumDeliveryChildren           = maximumPlaylistReferences
	maximumDeliveryChildrenPerProfile = maximumDeliveryChildren
	maximumDeliveryChildrenGlobal     = 4 * maximumDeliveryChildrenPerProfile
	maximumDeliveryChildIDLength      = 64
	maximumDeliveryStartTicksLength   = 32
	maximumDeliveryCompatTokenLength  = 128
	maximumDeliveryTargetLength       = 4096
	deliveryChildTTL                  = 5 * time.Minute
)

var errDeliveryChildCapacity = errors.New("playback child capability capacity exceeded")

type deliveryChildState struct {
	assetID           string
	file              string
	start             string
	target            string
	signature         string
	retainWhileActive bool // Sliding media segments leave this false so table activity cannot extend their TTL.
}

type deliveryChildEntry struct {
	state    deliveryChildState
	activeAt time.Time
}

type deliveryChildBudget struct {
	mu         sync.Mutex
	live       int
	byProfile  map[string]int
	tables     sync.Map
	globalMax  int
	profileMax int
}

type deliveryChildTable struct {
	mu         sync.Mutex
	entries    map[string]deliveryChildEntry
	byState    map[deliveryChildState]string
	now        func() time.Time
	budget     *deliveryChildBudget
	profileID  string
	activeAt   time.Time
	nextExpiry time.Time
	closed     bool
}

type deliveryLinkTemplate struct {
	path           string
	playSessionID  string
	mediaSourceID  string
	startTimeTicks string
}

type deliveryRequest struct {
	request   *http.Request
	assetID   string
	target    string
	signature string
	template  deliveryLinkTemplate
	child     bool
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
	assets       *deliveryAssetTable
}

type deliveryAssetTable struct {
	ids map[string]struct{}
}

type deliveryRequestError struct {
	cause error
}

func (err *deliveryRequestError) Error() string {
	return err.cause.Error()
}

func (err *deliveryRequestError) Unwrap() error {
	return err.cause
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

// IsTerminalDeliveryError reports whether a Serve failure proves that the
// opaque delivery handle can no longer be used. Request-scoped failures,
// including malformed child capabilities and downstream cancellation, are
// retryable and must not tear down the owning playback session.
func IsTerminalDeliveryError(err error) bool {
	if err == nil {
		return false
	}
	var requestErr *deliveryRequestError
	if errors.As(err, &requestErr) {
		return false
	}
	return errors.Is(err, ErrSessionNotFound)
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
		return &deliveryRequestError{cause: err}
	}
	buildChildURL := func(state deliveryChildState) (string, error) {
		childID, registerErr := handle.children.register(state)
		if registerErr != nil {
			return "", registerErr
		}
		return delivery.template.childURL(childID, state), nil
	}
	err = service.proxyAsset(
		w, delivery.request, handle.sessionID, delivery.assetID, handle.token,
		delivery.target, delivery.signature, buildChildURL,
	)
	return classifyDeliveryProxyError(err, delivery.child)
}

func classifyDeliveryProxyError(err error, child bool) error {
	if err == nil || !child || errors.Is(err, ErrSessionNotFound) {
		return err
	}
	return &deliveryRequestError{cause: err}
}

// ServeAsset delivers one asset that was captured inside an opaque delivery
// handle. Callers select only an asset identifier exposed by the URL-free
// Session metadata; native session credentials and provider URLs never leave
// this package.
func (service *Service) ServeAsset(w http.ResponseWriter, r *http.Request, handle DeliveryHandle, assetID string) error {
	if service == nil || w == nil || r == nil || r.URL == nil || !handle.Valid() ||
		handle.assets == nil || !handle.assets.contains(assetID) ||
		(r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return &deliveryRequestError{cause: ErrSessionNotFound}
	}
	request := r.Clone(r.Context())
	request.URL = cloneRequestURL(r)
	request.URL.RawQuery = ""
	request.Header = r.Header.Clone()
	for _, name := range []string{"Authorization", "X-Emby-Authorization", "X-Emby-Token", "X-MediaBrowser-Authorization", "X-MediaBrowser-Token"} {
		request.Header.Del(name)
	}
	return service.proxyAsset(w, request, handle.sessionID, assetID, handle.token, "", "", nil)
}

func (table *deliveryAssetTable) contains(assetID string) bool {
	if table == nil || assetID == "" || len(assetID) > 128 {
		return false
	}
	_, ok := table.ids[assetID]
	return ok
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
	isChild := false
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
		isChild = true
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
		signature: state.signature, template: template, child: isChild,
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

func newDeliveryChildBudget(globalMax, profileMax int) *deliveryChildBudget {
	return &deliveryChildBudget{
		byProfile: make(map[string]int), globalMax: globalMax, profileMax: profileMax,
	}
}

func (budget *deliveryChildBudget) reserve(profileID string) bool {
	if budget == nil {
		return true
	}
	if profileID == "" {
		return false
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	if budget.live >= budget.globalMax || budget.byProfile[profileID] >= budget.profileMax {
		return false
	}
	budget.live++
	budget.byProfile[profileID]++
	return true
}

func (budget *deliveryChildBudget) release(profileID string, count int) {
	if budget == nil || count == 0 {
		return
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	profileLive := budget.byProfile[profileID]
	if count < 0 || count > profileLive || count > budget.live {
		panic("playback delivery child budget accounting invariant violated")
	}
	budget.live -= count
	profileLive -= count
	if profileLive == 0 {
		delete(budget.byProfile, profileID)
	} else {
		budget.byProfile[profileID] = profileLive
	}
}

func (budget *deliveryChildBudget) usage(profileID string) (int, int) {
	if budget == nil {
		return 0, 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.live, budget.byProfile[profileID]
}

func (table *deliveryChildTable) bindBudget(budget *deliveryChildBudget, profileID string, now func() time.Time) {
	if table == nil || budget == nil {
		return
	}
	table.mu.Lock()
	if len(table.entries) != 0 || table.budget != nil {
		table.mu.Unlock()
		panic("playback delivery child table bound after use")
	}
	table.budget = budget
	table.profileID = profileID
	if now != nil {
		table.now = now
	}
	table.mu.Unlock()
}

func (service *Service) deliveryChildBudget() *deliveryChildBudget {
	service.deliveryChildrenMu.Lock()
	defer service.deliveryChildrenMu.Unlock()
	if service.deliveryChildren == nil {
		service.deliveryChildren = newDeliveryChildBudget(maximumDeliveryChildrenGlobal, maximumDeliveryChildrenPerProfile)
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
		if !table.budget.reserve(table.profileID) {
			table.mu.Unlock()
			return "", errDeliveryChildCapacity
		}
		table.activeAt = now
		entry := deliveryChildEntry{state: state, activeAt: now}
		table.entries[childID] = entry
		table.byState[state] = childID
		expiresAt := table.childExpiresAt(entry)
		if table.nextExpiry.IsZero() || expiresAt.Before(table.nextExpiry) {
			table.nextExpiry = expiresAt
		}
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
	entry.activeAt = now
	table.activeAt = now
	table.entries[childID] = entry
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
	table.budget.release(table.profileID, len(table.entries))
	clear(table.entries)
	clear(table.byState)
	table.activeAt = time.Time{}
	table.nextExpiry = time.Time{}
}

func (table *deliveryChildTable) expireLocked(now time.Time) {
	if len(table.entries) == 0 || !table.nextExpiry.IsZero() && table.nextExpiry.After(now) {
		return
	}
	expired := 0
	nextExpiry := time.Time{}
	for childID, entry := range table.entries {
		expiresAt := table.childExpiresAt(entry)
		if !expiresAt.After(now) {
			delete(table.entries, childID)
			delete(table.byState, entry.state)
			expired++
			continue
		}
		if nextExpiry.IsZero() || expiresAt.Before(nextExpiry) {
			nextExpiry = expiresAt
		}
	}
	table.budget.release(table.profileID, expired)
	table.nextExpiry = nextExpiry
	if len(table.entries) == 0 && table.budget != nil {
		table.budget.tables.Delete(table)
	}
}

func (table *deliveryChildTable) childExpiresAt(entry deliveryChildEntry) time.Time {
	activeAt := entry.activeAt
	if entry.state.retainWhileActive && table.activeAt.After(activeAt) {
		activeAt = table.activeAt
	}
	return activeAt.Add(deliveryChildTTL)
}

func (table *deliveryChildTable) touchExistingLocked(childID string, now time.Time) {
	entry, exists := table.entries[childID]
	if !exists {
		return
	}
	entry.activeAt = now
	table.activeAt = now
	table.entries[childID] = entry
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
	basePath, ok := deliveryVideoBasePath(requestURL.Path)
	if !ok {
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
	if candidates, found := values["api_key"]; found {
		if len(candidates) != 1 {
			return deliveryLinkTemplate{}, ErrSessionNotFound
		}
		credential := strings.TrimSpace(candidates[0])
		if credential == "" || len(credential) > maximumDeliveryCompatTokenLength || strings.ContainsAny(credential, "\r\n\t ") {
			return deliveryLinkTemplate{}, ErrSessionNotFound
		}
	}
	return deliveryLinkTemplate{
		path: basePath, playSessionID: playID, mediaSourceID: mediaID, startTimeTicks: startTimeTicks,
	}, nil
}

func (template deliveryLinkTemplate) childURL(childID string, state deliveryChildState) string {
	extension := deliveryChildExtension(state)
	query := url.Values{
		"MediaSourceId":        []string{template.mediaSourceID},
		"PlaySessionId":        []string{template.playSessionID},
		"api_key":              []string{template.playSessionID},
		deliveryChildQueryName: []string{childID},
	}
	if template.startTimeTicks != "" {
		query.Set("StartTimeTicks", template.startTimeTicks)
	}
	path := template.path + "/hls1/" + url.PathEscape(template.playSessionID) + "/" + url.PathEscape(childID) + "." + extension
	if state.file == "index.m3u8" && state.target == "" && state.signature == "" {
		path = template.path + "/main.m3u8"
	}
	return (&url.URL{Path: path, RawQuery: query.Encode()}).String()
}

func deliveryVideoBasePath(rawPath string) (string, bool) {
	parts := strings.Split(strings.Trim(rawPath, "/"), "/")
	videoIndex := 0
	if len(parts) >= 3 && strings.EqualFold(parts[0], "emby") {
		videoIndex = 1
	}
	if len(parts) <= videoIndex+1 || !strings.EqualFold(parts[videoIndex], "Videos") ||
		parts[videoIndex+1] == "" || len(parts[videoIndex+1]) > 128 {
		return "", false
	}
	if parts[videoIndex+1] == "." || parts[videoIndex+1] == ".." {
		return "", false
	}
	return "/" + strings.Join(parts[:videoIndex+2], "/"), true
}

func deliveryChildExtension(state deliveryChildState) string {
	value := state.file
	if value == "" && state.target != "" {
		if parsed, err := url.Parse(state.target); err == nil {
			value = parsed.Path
		}
	}
	extension := strings.ToLower(strings.TrimPrefix(pathExtension(value), "."))
	if len(extension) == 0 || len(extension) > 12 {
		return "bin"
	}
	for _, character := range extension {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return "bin"
		}
	}
	return extension
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
	assetIDs := make(map[string]struct{}, len(assets))
	for index := range assets {
		if assets[index].ID != "" {
			assetIDs[assets[index].ID] = struct{}{}
		}
	}
	handle := DeliveryHandle{
		sessionID: sessionID, assetID: selectedID, token: token,
		children: newDeliveryChildTable(), assets: &deliveryAssetTable{ids: assetIDs},
	}
	for index := range sources {
		source := sources[index]
		if source.ID != selectedID {
			continue
		}
		if source.Protocol == "hls" && (source.Mode == processingRemux || source.Mode == processingTranscodeAudio || source.Mode == processingTranscode) {
			handle.defaultFile = "master.m3u8"
			if assets[assetIndex].StartSeconds > 0 {
				handle.defaultStart = hlsStartKey(assets[assetIndex].StartSeconds)
			}
		}
		break
	}
	return handle
}
