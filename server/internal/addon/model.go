package addon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

var (
	ErrActiveProfileRequired = errors.New("an active profile is required")
	ErrInvalidTransportURL   = errors.New("invalid addon transport URL")
	ErrInvalidManifest       = errors.New("invalid addon manifest")
	ErrAlreadyInstalled      = errors.New("addon is already installed")
	ErrNotFound              = errors.New("addon not found")
	ErrForbidden             = errors.New("addon operation forbidden")
	ErrUnsupportedResource   = errors.New("addon does not support the resource request")
	ErrProviderUnavailable   = errors.New("addon provider unavailable")
	ErrInvalidResponse       = errors.New("invalid addon response")
	ErrInvalidInput          = errors.New("invalid addon input")
)

const (
	maxManifestBytes          = 4 << 20
	maxManifestTypes          = 128
	maxManifestResources      = 128
	maxManifestCatalogs       = 256
	maxManifestConfigEntries  = 128
	maxManifestListEntries    = 256
	maxManifestCatalogExtras  = 64
	maxManifestComplexity     = 4096
	maxPlannedRequests        = 256
	maxConcurrentRequests     = 8
	maxAggregateResponseBytes = 32 << 20

	MaximumProviderStreams   = 256
	MaximumProviderSubtitles = 512
)

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

type Manifest struct {
	ID            string                `json:"id"`
	Version       string                `json:"version"`
	Name          string                `json:"name"`
	Description   string                `json:"description,omitempty"`
	ContactEmail  string                `json:"contactEmail,omitempty"`
	Logo          string                `json:"logo,omitempty"`
	Background    string                `json:"background,omitempty"`
	Types         []string              `json:"types"`
	Resources     []ManifestResource    `json:"resources"`
	IDPrefixes    *[]string             `json:"idPrefixes,omitempty"`
	Catalogs      []ManifestCatalog     `json:"catalogs"`
	AddonCatalogs []ManifestCatalog     `json:"addonCatalogs,omitempty"`
	Config        []json.RawMessage     `json:"config,omitempty"`
	BehaviorHints ManifestBehaviorHints `json:"behaviorHints,omitempty"`
}

type ManifestBehaviorHints struct {
	Adult                 bool `json:"adult,omitempty"`
	P2P                   bool `json:"p2p,omitempty"`
	Configurable          bool `json:"configurable,omitempty"`
	ConfigurationRequired bool `json:"configurationRequired,omitempty"`
}

type ManifestResource struct {
	Name       string
	Short      bool
	Types      *[]string
	IDPrefixes *[]string
}

func (resource *ManifestResource) UnmarshalJSON(data []byte) error {
	var short string
	if err := json.Unmarshal(data, &short); err == nil {
		resource.Name = short
		resource.Short = true
		resource.Types = nil
		resource.IDPrefixes = nil
		return nil
	}
	var full struct {
		Name       string    `json:"name"`
		Types      *[]string `json:"types"`
		IDPrefixes *[]string `json:"idPrefixes"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		return fmt.Errorf("resource must be a string or object: %w", err)
	}
	resource.Name = full.Name
	resource.Short = false
	resource.Types = full.Types
	resource.IDPrefixes = full.IDPrefixes
	return nil
}

func (resource ManifestResource) MarshalJSON() ([]byte, error) {
	if resource.Short {
		return json.Marshal(resource.Name)
	}
	return json.Marshal(struct {
		Name       string    `json:"name"`
		Types      *[]string `json:"types,omitempty"`
		IDPrefixes *[]string `json:"idPrefixes,omitempty"`
	}{Name: resource.Name, Types: resource.Types, IDPrefixes: resource.IDPrefixes})
}

type ManifestCatalog struct {
	Type           string      `json:"type"`
	ID             string      `json:"id"`
	Name           string      `json:"name,omitempty"`
	Genres         []string    `json:"genres,omitempty"`
	Extra          []ExtraProp `json:"extra,omitempty"`
	ExtraRequired  []string    `json:"extraRequired,omitempty"`
	ExtraSupported []string    `json:"extraSupported,omitempty"`
}

type ExtraProp struct {
	Name         string   `json:"name"`
	IsRequired   bool     `json:"isRequired,omitempty"`
	Default      string   `json:"default,omitempty"`
	Options      []string `json:"options,omitempty"`
	OptionsLimit int      `json:"optionsLimit,omitempty"`
}

type ExtraValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CatalogSearchInput struct {
	Search string
	Skip   int
	Limit  int
	Extra  []ExtraValue
}

type ResourcePath struct {
	Resource string       `json:"resource"`
	Type     string       `json:"type"`
	ID       string       `json:"id"`
	Extra    []ExtraValue `json:"extra,omitempty"`
}

func IsExposableResource(resource string) bool {
	switch resource {
	case "catalog", "addon_catalog", "meta":
		return true
	default:
		return false
	}
}

func isPlaybackResource(resource string) bool {
	switch resource {
	case "stream", "subtitles":
		return true
	default:
		return false
	}
}

type InstalledAddon struct {
	ID          string          `json:"id"`
	Manifest    json.RawMessage `json:"manifest"`
	Enabled     bool            `json:"enabled"`
	Position    int             `json:"position"`
	ProfileIDs  []string        `json:"profileIds"`
	CategoryIDs []string        `json:"categoryIds"`
	InstalledAt time.Time       `json:"installedAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`

	transportURL   string
	parsedManifest Manifest
}

// ManagedAddon is returned only by addon management mutations and lookups.
// TransportURL is omitted unless the caller is a global administrator.
type ManagedAddon struct {
	InstalledAddon
	TransportURL string `json:"transportUrl,omitempty"`
}

func managedAddon(installed InstalledAddon, revealTransport bool) ManagedAddon {
	managed := ManagedAddon{InstalledAddon: installed}
	if revealTransport {
		managed.TransportURL = installed.transportURL
	}
	return managed
}

type InstallInput struct {
	TransportURL string   `json:"transportUrl"`
	ProfileIDs   []string `json:"profileIds,omitempty"`
	CategoryIDs  []string `json:"categoryIds,omitempty"`
}

type AddonPreview struct {
	Manifest     Manifest          `json:"manifest"`
	Capabilities AddonCapabilities `json:"capabilities"`
	ProfileIDs   []string          `json:"profileIds"`
	CategoryIDs  []string          `json:"categoryIds"`
}

type UpdateAddonInput struct {
	TransportURL *string  `json:"transportUrl,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
	ProfileIDs   []string `json:"profileIds"`
	CategoryIDs  []string `json:"categoryIds"`
}

type ReorderInput struct {
	AddonIDs []string `json:"addonIds"`
}

type CachePolicy struct {
	MaxAgeSeconds               *int64 `json:"maxAgeSeconds,omitempty"`
	StaleWhileRevalidateSeconds *int64 `json:"staleWhileRevalidateSeconds,omitempty"`
	StaleIfErrorSeconds         *int64 `json:"staleIfErrorSeconds,omitempty"`
}

type ProviderStreamResponse struct {
	Streams []ProviderStream `json:"streams"`
}

type ProviderStream struct {
	Name          string                      `json:"name"`
	Title         string                      `json:"title"`
	URL           string                      `json:"url"`
	Description   string                      `json:"description"`
	YTID          string                      `json:"ytId"`
	ExternalURL   string                      `json:"externalUrl"`
	InfoHash      string                      `json:"infoHash"`
	FileIndex     *int                        `json:"fileIdx"`
	BehaviorHints ProviderStreamBehaviorHints `json:"behaviorHints"`
}

type ProviderStreamBehaviorHints struct {
	NotWebReady  bool   `json:"notWebReady"`
	Filename     string `json:"filename"`
	ProxyHeaders struct {
		Request map[string]string `json:"request"`
	} `json:"proxyHeaders"`
}

type ProviderSubtitleResponse struct {
	Subtitles []ProviderSubtitle `json:"subtitles"`
}

type ProviderSubtitle struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Language string `json:"lang"`
	Forced   bool   `json:"forced"`
}

func ParseProviderStreamResponse(payload []byte) (ProviderStreamResponse, error) {
	var envelope struct {
		Streams json.RawMessage `json:"streams"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ProviderStreamResponse{}, fmt.Errorf("%w: decode stream response", ErrInvalidResponse)
	}
	streams, err := decodeProviderItems(envelope.Streams, "streams", MaximumProviderStreams, validateProviderStream)
	if err != nil {
		return ProviderStreamResponse{}, err
	}
	return ProviderStreamResponse{Streams: streams}, nil
}

func ParseProviderSubtitleResponse(payload []byte) (ProviderSubtitleResponse, error) {
	var envelope struct {
		Subtitles json.RawMessage `json:"subtitles"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ProviderSubtitleResponse{}, fmt.Errorf("%w: decode subtitles response", ErrInvalidResponse)
	}
	subtitles, err := decodeProviderItems(envelope.Subtitles, "subtitles", MaximumProviderSubtitles, validateProviderSubtitle)
	if err != nil {
		return ProviderSubtitleResponse{}, err
	}
	return ProviderSubtitleResponse{Subtitles: subtitles}, nil
}

func decodeProviderItems[T any](payload json.RawMessage, field string, maximum int, validate func(T) error) ([]T, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("%w: provider response requires %q array", ErrInvalidResponse, field)
	}
	items := make([]T, 0)
	for decoder.More() {
		if len(items) == maximum {
			return nil, fmt.Errorf("%w: provider response %q exceeds %d items", ErrInvalidResponse, field, maximum)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || firstJSONToken(raw) != '{' {
			return nil, fmt.Errorf("%w: provider response %q items must be objects", ErrInvalidResponse, field)
		}
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("%w: decode provider response %q item", ErrInvalidResponse, field)
		}
		if err := validate(item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%w: decode provider response %q", ErrInvalidResponse, field)
	}
	return items, nil
}

func validateProviderStream(stream ProviderStream) error {
	for _, value := range []struct {
		value   string
		maximum int
	}{
		{stream.Name, 4096},
		{stream.Title, 4096},
		{stream.Description, 8192},
	} {
		if invalidProviderDisplayText(value.value, value.maximum) {
			return fmt.Errorf("%w: invalid provider stream field", ErrInvalidResponse)
		}
	}
	for _, value := range []struct {
		value   string
		maximum int
	}{
		{stream.URL, 8192},
		{stream.ExternalURL, 8192},
		{stream.YTID, 1024},
		{stream.InfoHash, 1024},
		{stream.BehaviorHints.Filename, 4096},
	} {
		if invalidValue(value.value, value.maximum) {
			return fmt.Errorf("%w: invalid provider stream field", ErrInvalidResponse)
		}
	}
	headers := stream.BehaviorHints.ProxyHeaders.Request
	if len(headers) > 64 {
		return fmt.Errorf("%w: provider stream request headers exceed 64 entries", ErrInvalidResponse)
	}
	totalHeaderBytes := 0
	canonicalHeaderNames := make(map[string]struct{}, len(headers))
	for name, value := range headers {
		if invalidToken(name, 256) || !httpguts.ValidHeaderFieldName(name) || invalidValue(value, 8192) {
			return fmt.Errorf("%w: invalid provider stream request header", ErrInvalidResponse)
		}
		canonicalName := strings.ToLower(name)
		if _, duplicate := canonicalHeaderNames[canonicalName]; duplicate {
			return fmt.Errorf("%w: duplicate provider stream request header", ErrInvalidResponse)
		}
		canonicalHeaderNames[canonicalName] = struct{}{}
		if len(name) > 32<<10-totalHeaderBytes || len(value) > 32<<10-totalHeaderBytes-len(name) {
			return fmt.Errorf("%w: provider stream request headers exceed 32768 bytes", ErrInvalidResponse)
		}
		totalHeaderBytes += len(name) + len(value)
	}
	return nil
}

func validateProviderSubtitle(subtitle ProviderSubtitle) error {
	if invalidValue(subtitle.ID, 1024) || invalidValue(subtitle.URL, 8192) || invalidValue(subtitle.Language, 128) {
		return fmt.Errorf("%w: invalid provider subtitle field", ErrInvalidResponse)
	}
	return nil
}

type ResourceResult struct {
	AddonID    string          `json:"addonId"`
	ManifestID string          `json:"manifestId"`
	AddonName  string          `json:"-"`
	Resource   string          `json:"resource"`
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Payload    json.RawMessage `json:"payload"`
	Cache      CachePolicy     `json:"cache"`
	Extra      []ExtraValue    `json:"extra,omitempty"`
}

func (result ResourceResult) MarshalJSON() ([]byte, error) {
	type resourceResult ResourceResult
	safe := resourceResult(result)
	if !IsExposableResource(result.Resource) {
		safe.Payload = json.RawMessage("null")
		return json.Marshal(safe)
	}
	payload, err := SanitizeExposablePayload(result.Payload)
	if err != nil {
		return nil, err
	}
	safe.Payload = payload
	return json.Marshal(safe)
}

func SanitizeExposablePayload(payload json.RawMessage) (json.RawMessage, error) {
	if err := validateExposablePayloadComplexity(context.Background(), payload); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("sanitize addon resource payload: %w", err)
	}
	safe, ok := sanitizeExposableValue(value, "")
	if !ok {
		safe = map[string]any{}
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return nil, fmt.Errorf("encode sanitized addon resource payload: %w", err)
	}
	return encoded, nil
}

func sanitizeExposableValue(value any, field string) (any, bool) {
	normalizedField := normalizedSensitiveField(field)
	if sensitiveProviderField(normalizedField) {
		return nil, false
	}
	if providerURLField(normalizedField) {
		text, ok := value.(string)
		if !ok || !strings.HasPrefix(text, "/api/v1/artwork/") {
			return nil, false
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		safe := make(map[string]any, len(typed))
		for name, child := range typed {
			sanitized, ok := sanitizeExposableValue(child, name)
			if ok {
				safe[name] = sanitized
			}
		}
		return safe, true
	case []any:
		safe := make([]any, 0, len(typed))
		for _, child := range typed {
			sanitized, ok := sanitizeExposableValue(child, "")
			if ok {
				safe = append(safe, sanitized)
			}
		}
		return safe, true
	case string:
		if providerURLField(normalizedField) {
			return typed, true
		}
		trimmed := strings.TrimSpace(typed)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "://") || strings.Contains(lower, "magnet:") {
			return nil, false
		}
		if !strings.HasSuffix(normalizedField, "id") {
			parsed, err := url.Parse(trimmed)
			if err == nil && (parsed.IsAbs() || parsed.Host != "") {
				return nil, false
			}
		}
		return typed, true
	default:
		return value, true
	}
}

func normalizedSensitiveField(field string) string {
	var normalized strings.Builder
	normalized.Grow(len(field))
	for _, character := range strings.ToLower(field) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func providerURLField(field string) bool {
	if strings.Contains(field, "url") {
		return true
	}
	switch field {
	case "uri", "href", "src", "thumbnail", "thumbnailurl":
		return true
	default:
		return false
	}
}

func sensitiveProviderField(field string) bool {
	for _, fragment := range []string{"header", "cookie", "authorization", "credential", "password", "secret", "token", "apikey", "proxy", "signature"} {
		if strings.Contains(field, fragment) {
			return true
		}
	}
	return field == "transporturl"
}

type ResourceFailure struct {
	AddonID    string `json:"addonId"`
	ManifestID string `json:"manifestId"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

type ResourceBatch struct {
	Results []ResourceResult  `json:"results"`
	Errors  []ResourceFailure `json:"errors"`
}

type CatalogDescriptor struct {
	AddonID      string          `json:"addonId"`
	AddonName    string          `json:"addonName,omitempty"`
	AddonLogoURL string          `json:"addonLogoUrl,omitempty"`
	ManifestID   string          `json:"manifestId"`
	Position     int             `json:"position"`
	Catalog      ManifestCatalog `json:"catalog"`
	AddonCatalog bool            `json:"addonCatalog"`
	Searchable   bool            `json:"searchable"`
}

func NormalizeTransportURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 8192 {
		return "", ErrInvalidTransportURL
	}
	if strings.HasPrefix(strings.ToLower(raw), "stremio://") {
		raw = "https://" + raw[len("stremio://"):]
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", ErrInvalidTransportURL
	}
	if strings.HasSuffix(parsed.Path, "/stremio/v1") {
		return "", fmt.Errorf("%w: legacy transports are not supported", ErrInvalidTransportURL)
	}
	if !strings.HasSuffix(parsed.Path, "/manifest.json") {
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/manifest.json"
		parsed.RawPath = ""
	}
	return parsed.String(), nil
}

func ParseManifest(raw []byte) (Manifest, json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes || !json.Valid(raw) {
		return Manifest{}, nil, ErrInvalidManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, nil, err
	}
	compact := bytes.NewBuffer(make([]byte, 0, len(raw)))
	if err := json.Compact(compact, raw); err != nil {
		return Manifest{}, nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	return manifest, json.RawMessage(compact.Bytes()), nil
}

func (manifest Manifest) Validate() error {
	if err := validateManifestCardinality(manifest); err != nil {
		return err
	}
	if invalidToken(manifest.ID, 512) || invalidToken(manifest.Name, 512) || !semanticVersionPattern.MatchString(manifest.Version) {
		return ErrInvalidManifest
	}
	if len(manifest.Resources) == 0 {
		return fmt.Errorf("%w: at least one resource is required", ErrInvalidManifest)
	}
	for _, contentType := range manifest.Types {
		if invalidToken(contentType, 256) {
			return fmt.Errorf("%w: invalid content type", ErrInvalidManifest)
		}
	}
	if manifest.IDPrefixes != nil {
		for _, prefix := range *manifest.IDPrefixes {
			if invalidValue(prefix, 1024) {
				return fmt.Errorf("%w: invalid ID prefix", ErrInvalidManifest)
			}
		}
	}
	for _, resource := range manifest.Resources {
		if invalidToken(resource.Name, 256) {
			return fmt.Errorf("%w: invalid resource", ErrInvalidManifest)
		}
		if resource.Types != nil {
			for _, contentType := range *resource.Types {
				if invalidToken(contentType, 256) {
					return fmt.Errorf("%w: invalid resource type", ErrInvalidManifest)
				}
			}
		}
		if resource.IDPrefixes != nil {
			for _, prefix := range *resource.IDPrefixes {
				if invalidValue(prefix, 1024) {
					return fmt.Errorf("%w: invalid resource ID prefix", ErrInvalidManifest)
				}
			}
		}
	}
	seen := make(map[string]struct{}, len(manifest.Catalogs)+len(manifest.AddonCatalogs))
	for _, group := range []struct {
		name     string
		catalogs []ManifestCatalog
	}{{"catalog", manifest.Catalogs}, {"addon catalog", manifest.AddonCatalogs}} {
		for _, catalog := range group.catalogs {
			if invalidToken(catalog.Type, 256) || invalidValue(catalog.ID, 2048) {
				return fmt.Errorf("%w: invalid %s", ErrInvalidManifest, group.name)
			}
			key := group.name + "\x00" + catalog.Type + "\x00" + catalog.ID
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%w: duplicate %s", ErrInvalidManifest, group.name)
			}
			seen[key] = struct{}{}
			if err := validateExtras(catalog); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateManifestCardinality(manifest Manifest) error {
	if len(manifest.Types) > maxManifestTypes {
		return fmt.Errorf("%w: types exceeds limit of %d", ErrInvalidManifest, maxManifestTypes)
	}
	if len(manifest.Resources) > maxManifestResources {
		return fmt.Errorf("%w: resources exceeds limit of %d", ErrInvalidManifest, maxManifestResources)
	}
	if len(manifest.Catalogs)+len(manifest.AddonCatalogs) > maxManifestCatalogs {
		return fmt.Errorf("%w: catalogs exceeds limit of %d", ErrInvalidManifest, maxManifestCatalogs)
	}
	if len(manifest.Config) > maxManifestConfigEntries {
		return fmt.Errorf("%w: config exceeds limit of %d", ErrInvalidManifest, maxManifestConfigEntries)
	}

	complexity := len(manifest.Types) + len(manifest.Resources) + len(manifest.Catalogs) + len(manifest.AddonCatalogs) + len(manifest.Config)
	addList := func(name string, size int) error {
		if size > maxManifestListEntries {
			return fmt.Errorf("%w: %s exceeds limit of %d", ErrInvalidManifest, name, maxManifestListEntries)
		}
		complexity += size
		if complexity > maxManifestComplexity {
			return fmt.Errorf("%w: manifest complexity exceeds limit of %d", ErrInvalidManifest, maxManifestComplexity)
		}
		return nil
	}
	if manifest.IDPrefixes != nil {
		if err := addList("idPrefixes", len(*manifest.IDPrefixes)); err != nil {
			return err
		}
	}
	for _, resource := range manifest.Resources {
		if resource.Types != nil {
			if err := addList("resource types", len(*resource.Types)); err != nil {
				return err
			}
		}
		if resource.IDPrefixes != nil {
			if err := addList("resource idPrefixes", len(*resource.IDPrefixes)); err != nil {
				return err
			}
		}
	}
	validateCatalog := func(catalog ManifestCatalog) error {
		if len(catalog.Extra) > maxManifestCatalogExtras {
			return fmt.Errorf("%w: catalog extras exceeds limit of %d", ErrInvalidManifest, maxManifestCatalogExtras)
		}
		for _, list := range []struct {
			name string
			size int
		}{
			{"catalog genres", len(catalog.Genres)},
			{"catalog extras", len(catalog.Extra)},
			{"catalog extraRequired", len(catalog.ExtraRequired)},
			{"catalog extraSupported", len(catalog.ExtraSupported)},
		} {
			if err := addList(list.name, list.size); err != nil {
				return err
			}
		}
		for _, extra := range catalog.Extra {
			if err := addList("catalog extra options", len(extra.Options)); err != nil {
				return err
			}
		}
		return nil
	}
	for _, catalogs := range [][]ManifestCatalog{manifest.Catalogs, manifest.AddonCatalogs} {
		for _, catalog := range catalogs {
			if err := validateCatalog(catalog); err != nil {
				return err
			}
		}
	}
	return nil
}

func (catalog ManifestCatalog) SupportsSearch() bool {
	return catalog.DeclaresExtra("search")
}

func (catalog ManifestCatalog) DeclaresExtra(name string) bool {
	return hasExtra(catalog.extraProps(), name)
}

func (manifest Manifest) Supports(path ResourcePath) bool {
	if path.Resource == "catalog" || path.Resource == "addon_catalog" {
		if !contains(manifest.Types, path.Type) || !manifest.supportsResourceType(path.Resource, path.Type) {
			return false
		}
		catalogs := manifest.Catalogs
		if path.Resource == "addon_catalog" {
			catalogs = manifest.AddonCatalogs
		}
		catalog, ok := findCatalog(catalogs, path.Type, path.ID)
		return ok && catalog.SupportsExtra(path.Extra)
	}
	for _, resource := range manifest.Resources {
		if resource.Name != path.Resource {
			continue
		}
		var types []string
		var prefixes *[]string
		if resource.Short {
			types = manifest.Types
			prefixes = manifest.IDPrefixes
		} else {
			if resource.Types == nil {
				continue
			}
			types = *resource.Types
			prefixes = resource.IDPrefixes
		}
		if !contains(types, path.Type) {
			continue
		}
		if prefixes == nil || len(*prefixes) == 0 || hasAnyPrefix(path.ID, *prefixes) {
			return true
		}
	}
	return false
}

func (manifest Manifest) supportsResourceType(name string, contentType string) bool {
	for _, resource := range manifest.Resources {
		if resource.Name != name {
			continue
		}
		if resource.Short {
			if contains(manifest.Types, contentType) {
				return true
			}
			continue
		}
		if resource.Types != nil && contains(*resource.Types, contentType) {
			return true
		}
	}
	return false
}

func (manifest Manifest) ApplyCatalogDefaults(path ResourcePath) ResourcePath {
	var catalogs []ManifestCatalog
	switch path.Resource {
	case "catalog":
		catalogs = manifest.Catalogs
	case "addon_catalog":
		catalogs = manifest.AddonCatalogs
	default:
		return path
	}
	catalog, ok := findCatalog(catalogs, path.Type, path.ID)
	if !ok {
		return path
	}
	for _, prop := range catalog.extraProps() {
		if prop.Default == "" || hasExtraValue(path.Extra, prop.Name) {
			continue
		}
		if len(path.Extra) == 0 {
			path.Extra = []ExtraValue{{Name: prop.Name, Value: prop.Default}}
		} else {
			path.Extra = append(append([]ExtraValue(nil), path.Extra...), ExtraValue{Name: prop.Name, Value: prop.Default})
		}
	}
	return path
}

func (catalog ManifestCatalog) SupportsExtra(values []ExtraValue) bool {
	props := catalog.extraProps()
	for _, value := range values {
		if !hasExtra(props, value.Name) {
			return false
		}
	}
	for _, prop := range props {
		if prop.IsRequired && !hasExtraValue(values, prop.Name) {
			return false
		}
	}
	return true
}

func (catalog ManifestCatalog) extraProps() []ExtraProp {
	if catalog.Extra != nil {
		return catalog.Extra
	}
	props := make([]ExtraProp, 0, len(catalog.ExtraSupported))
	for _, name := range catalog.ExtraSupported {
		props = append(props, ExtraProp{Name: name, IsRequired: contains(catalog.ExtraRequired, name), OptionsLimit: 1})
	}
	return props
}

func findCatalog(catalogs []ManifestCatalog, contentType, id string) (ManifestCatalog, bool) {
	for _, catalog := range catalogs {
		if catalog.Type == contentType && catalog.ID == id {
			return catalog, true
		}
	}
	return ManifestCatalog{}, false
}

func validateExtras(catalog ManifestCatalog) error {
	seen := make(map[string]struct{})
	for _, prop := range catalog.extraProps() {
		if invalidToken(prop.Name, 256) || invalidValue(prop.Default, 2048) || prop.OptionsLimit < 0 {
			return fmt.Errorf("%w: invalid catalog extra", ErrInvalidManifest)
		}
		if _, duplicate := seen[prop.Name]; duplicate {
			return fmt.Errorf("%w: duplicate catalog extra", ErrInvalidManifest)
		}
		seen[prop.Name] = struct{}{}
	}
	return nil
}

func invalidToken(value string, maximum int) bool {
	return strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || invalidValue(value, maximum)
}

func invalidValue(value string, maximum int) bool {
	if len(value) > maximum || strings.ContainsRune(value, '\x00') {
		return true
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func invalidProviderDisplayText(value string, maximum int) bool {
	if len(value) > maximum || strings.ContainsRune(value, '\x00') {
		return true
	}
	for _, character := range value {
		if character < 0x20 && character != '\t' && character != '\n' && character != '\r' || character == 0x7f {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func hasExtra(values []ExtraProp, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func hasExtraValue(values []ExtraValue, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}
