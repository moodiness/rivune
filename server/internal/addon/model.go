package addon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
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

type InstalledAddon struct {
	ID             string          `json:"id"`
	TransportURL   string          `json:"transportUrl"`
	Manifest       json.RawMessage `json:"manifest"`
	Position       int             `json:"position"`
	ProfileIDs     []string        `json:"profileIds"`
	InstalledAt    time.Time       `json:"installedAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	parsedManifest Manifest
}

type InstallInput struct {
	TransportURL string   `json:"transportUrl"`
	ProfileIDs   []string `json:"profileIds,omitempty"`
}

type UpdateAddonInput struct {
	TransportURL string   `json:"transportUrl"`
	ProfileIDs   []string `json:"profileIds"`
}

type ReorderInput struct {
	AddonIDs []string `json:"addonIds"`
}

type CachePolicy struct {
	MaxAgeSeconds               *int64 `json:"maxAgeSeconds,omitempty"`
	StaleWhileRevalidateSeconds *int64 `json:"staleWhileRevalidateSeconds,omitempty"`
	StaleIfErrorSeconds         *int64 `json:"staleIfErrorSeconds,omitempty"`
}

type ResourceResult struct {
	AddonID      string          `json:"addonId"`
	ManifestID   string          `json:"manifestId"`
	TransportURL string          `json:"transportUrl"`
	Resource     string          `json:"resource"`
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Payload      json.RawMessage `json:"payload"`
	Cache        CachePolicy     `json:"cache"`
	Extra        []ExtraValue    `json:"extra,omitempty"`
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
	ManifestID   string          `json:"manifestId"`
	Position     int             `json:"position"`
	Catalog      ManifestCatalog `json:"catalog"`
	AddonCatalog bool            `json:"addonCatalog"`
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
	if len(raw) == 0 || !json.Valid(raw) {
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
