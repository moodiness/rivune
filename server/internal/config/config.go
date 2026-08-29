package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moodiness/rivune/server/internal/netguard"
	"github.com/moodiness/rivune/server/internal/secretcrypto"
)

const (
	defaultListenAddress           = ":8080"
	defaultDatabaseHost            = "localhost"
	defaultDatabasePort            = 5432
	defaultDatabaseName            = "rivune"
	defaultDatabaseUser            = "rivune"
	defaultAccessTokenTTL          = 15 * time.Minute
	defaultRefreshTokenTTL         = 30 * 24 * time.Hour
	defaultProfileGrantTTL         = 12 * time.Hour
	defaultRemuxConcurrency        = 4
	defaultTranscodeThreads        = 4
	defaultTranscodeMaxReadRate    = 1.5
	defaultHLSInitialBufferSeconds = 6
	defaultVideoDevice             = "/dev/dri/renderD128"
)

type Config struct {
	ListenAddress            string
	PublicURL                string
	DatabaseURL              string
	SetupToken               string
	EncryptionKeys           *secretcrypto.Keyring
	EncryptionKeysFromLegacy bool
	AccessTokenTTL           time.Duration
	RefreshTokenTTL          time.Duration
	ProfileGrantTTL          time.Duration
	TrustedProxies           []netip.Prefix
	NAT64Prefixes            []netip.Prefix
	FFmpegPath               string
	FFprobePath              string
	RemuxConcurrency         int
	TranscodeThreads         int
	TranscodeMaxReadRate     float64
	HLSInitialBufferSeconds  int
	VideoDevice              string
	MediaTempDir             string
	ArtworkCacheDir          string
	LANArtworkOrigins        []string
	SemanticOllamaURL        string
	SemanticOllamaModel      string
}

func (Config) MarshalJSON() ([]byte, error) {
	return nil, errors.New("bootstrap configuration cannot be serialized")
}

func (Config) String() string   { return "[REDACTED bootstrap configuration]" }
func (Config) GoString() string { return "[REDACTED bootstrap configuration]" }

// LegacyEnvironment is startup migration input only. Callers must never use it
// as a runtime fallback after ImportLegacyEnvironment has completed.
type LegacyEnvironment struct {
	Timezone                *string
	JellyfinEnabled         *bool
	JellyfinDebug           *bool
	HardwareAcceleration    *string
	TranscodeMaxBitrateKbps *int
	MediaMaxStorageMB       *int
	ArtworkMaxStorageMB     *int
	AllowTranscoding        *bool
	TMDBAccessToken         string
	FanartAPIKey            string
	MDBListAPIKey           string
	TVDBAPIKey              string
	TVDBPIN                 string
	TraktClientID           string
	TraktClientSecret       string
	SimklClientID           string
	invalid                 map[string]error
}

func (LegacyEnvironment) MarshalJSON() ([]byte, error) {
	return nil, errors.New("legacy environment migration input cannot be serialized")
}

func (LegacyEnvironment) String() string   { return "[REDACTED legacy environment]" }
func (LegacyEnvironment) GoString() string { return "[REDACTED legacy environment]" }

func (legacy LegacyEnvironment) ValidationError(setting string) error {
	return legacy.invalid[setting]
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:           envOrDefault("RIVUNE_LISTEN_ADDRESS", defaultListenAddress),
		PublicURL:               strings.TrimRight(strings.TrimSpace(os.Getenv("RIVUNE_PUBLIC_URL")), "/"),
		SetupToken:              strings.TrimSpace(os.Getenv("RIVUNE_SETUP_TOKEN")),
		FFmpegPath:              "ffmpeg",
		FFprobePath:             "ffprobe",
		RemuxConcurrency:        defaultRemuxConcurrency,
		TranscodeThreads:        defaultTranscodeThreads,
		TranscodeMaxReadRate:    defaultTranscodeMaxReadRate,
		HLSInitialBufferSeconds: defaultHLSInitialBufferSeconds,
		VideoDevice:             defaultVideoDevice,
		ArtworkCacheDir:         envOrDefault("RIVUNE_ARTWORK_CACHE_DIR", "/var/lib/rivune/artwork"),
		MediaTempDir:            strings.TrimSpace(os.Getenv("RIVUNE_MEDIA_TEMP_DIR")),
		SemanticOllamaURL:       strings.TrimSpace(os.Getenv("RIVUNE_SEMANTIC_OLLAMA_URL")),
		SemanticOllamaModel:     strings.TrimSpace(os.Getenv("RIVUNE_SEMANTIC_OLLAMA_MODEL")),
	}

	var err error
	cfg.DatabaseURL, err = loadDatabaseURL()
	if err != nil {
		return Config{}, err
	}
	cfg.EncryptionKeys, cfg.EncryptionKeysFromLegacy, err = loadEncryptionKeys()
	if err != nil {
		return Config{}, err
	}
	cfg.AccessTokenTTL = defaultAccessTokenTTL
	cfg.RefreshTokenTTL = defaultRefreshTokenTTL
	cfg.ProfileGrantTTL = defaultProfileGrantTTL
	cfg.TrustedProxies, err = loadTrustedProxies(os.Getenv("RIVUNE_TRUSTED_PROXIES"))
	if err != nil {
		return Config{}, err
	}
	cfg.NAT64Prefixes, err = loadNAT64Prefixes(os.Getenv("RIVUNE_NAT64_PREFIXES"))
	if err != nil {
		return Config{}, err
	}
	cfg.LANArtworkOrigins, err = loadLANArtworkOrigins(os.Getenv("RIVUNE_LAN_ARTWORK_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	if (cfg.SemanticOllamaURL == "") != (cfg.SemanticOllamaModel == "") {
		return Config{}, errors.New("RIVUNE_SEMANTIC_OLLAMA_URL and RIVUNE_SEMANTIC_OLLAMA_MODEL must be configured together")
	}

	if cfg.PublicURL != "" {
		parsed, parseErr := url.Parse(cfg.PublicURL)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" || strings.Contains(parsed.Hostname(), "%") || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, errors.New("RIVUNE_PUBLIC_URL must be an absolute HTTP(S) origin without credentials, path, query, or fragment")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Config{}, errors.New("RIVUNE_PUBLIC_URL must use http or https")
		}
		if parsed.Scheme == "http" && !isLocalHTTPHost(parsed.Hostname(), cfg.NAT64Prefixes) {
			return Config{}, errors.New("RIVUNE_PUBLIC_URL must use https unless its host is loopback or a private-network IP address")
		}
		parsed.Path = ""
		cfg.PublicURL = parsed.String()
	}

	return cfg, nil
}

func loadEncryptionKeys() (*secretcrypto.Keyring, bool, error) {
	if configured := os.Getenv("RIVUNE_ENCRYPTION_KEYS"); configured != "" {
		keyring, err := secretcrypto.ParseKeyring(configured)
		return keyring, false, err
	}
	legacy := strings.TrimSpace(os.Getenv("RIVUNE_TRACKING_ENCRYPTION_KEY"))
	if legacy == "" {
		return nil, false, errors.New("RIVUNE_ENCRYPTION_KEYS is required; for a new installation set it to 1:<64-lowercase-hex> using a newly generated 32-byte key; for a legacy upgrade restore the existing RIVUNE_TRACKING_ENCRYPTION_KEY instead")
	}
	if len(legacy) != 64 {
		return nil, false, errors.New("RIVUNE_TRACKING_ENCRYPTION_KEY must be exactly 64 hexadecimal characters (32 bytes); restore the existing key from backup and do not generate a replacement for an existing database")
	}
	decoded, err := hex.DecodeString(legacy)
	if err != nil {
		return nil, false, errors.New("RIVUNE_TRACKING_ENCRYPTION_KEY must be exactly 64 hexadecimal characters (32 bytes); restore the existing key from backup and do not generate a replacement for an existing database")
	}
	allZero := true
	for _, b := range decoded {
		allZero = allZero && b == 0
	}
	if allZero {
		return nil, false, errors.New("RIVUNE_TRACKING_ENCRYPTION_KEY must not be all zero; restore the existing key from backup and do not generate a replacement")
	}
	keyring, err := secretcrypto.NewKeyring([]secretcrypto.Key{{Version: 1, Bytes: decoded}})
	if err != nil {
		return nil, false, fmt.Errorf("initialize legacy tracking encryption key: %w", err)
	}
	return keyring, true, nil
}

func LoadLegacyEnvironment() (LegacyEnvironment, error) {
	legacy := LegacyEnvironment{
		TMDBAccessToken:   strings.TrimSpace(os.Getenv("RIVUNE_TMDB_ACCESS_TOKEN")),
		FanartAPIKey:      strings.TrimSpace(os.Getenv("RIVUNE_FANART_API_KEY")),
		MDBListAPIKey:     strings.TrimSpace(os.Getenv("RIVUNE_MDBLIST_API_KEY")),
		TVDBAPIKey:        strings.TrimSpace(os.Getenv("RIVUNE_TVDB_API_KEY")),
		TVDBPIN:           strings.TrimSpace(os.Getenv("RIVUNE_TVDB_PIN")),
		TraktClientID:     strings.TrimSpace(os.Getenv("RIVUNE_TRAKT_CLIENT_ID")),
		TraktClientSecret: strings.TrimSpace(os.Getenv("RIVUNE_TRAKT_CLIENT_SECRET")),
		SimklClientID:     strings.TrimSpace(os.Getenv("RIVUNE_SIMKL_CLIENT_ID")),
	}
	legacy.invalid = make(map[string]error)
	var err error
	if legacy.Timezone, err = loadOptionalTimezone("TZ"); err != nil {
		legacy.invalid["timezone"] = err
	}
	if legacy.JellyfinEnabled, err = loadOptionalBoolean("RIVUNE_JELLYFIN_ENABLED"); err != nil {
		legacy.invalid["jellyfinEnabled"] = err
	}
	if legacy.JellyfinDebug, err = loadOptionalBoolean("RIVUNE_JELLYFIN_DEBUG"); err != nil {
		legacy.invalid["jellyfinDebug"] = err
	}
	if legacy.AllowTranscoding, err = loadOptionalBoolean("RIVUNE_ALLOW_TRANSCODING"); err != nil {
		legacy.invalid["allowTranscoding"] = err
	}
	if legacy.HardwareAcceleration, err = loadOptionalHardwareAcceleration(); err != nil {
		legacy.invalid["hardwareAcceleration"] = err
	}
	if legacy.TranscodeMaxBitrateKbps, err = loadOptionalInteger("RIVUNE_TRANSCODE_MAX_BITRATE_KBPS", 64, 200000); err != nil {
		legacy.invalid["transcodeMaxBitrateKbps"] = err
	}
	if legacy.MediaMaxStorageMB, err = loadOptionalInteger("RIVUNE_MEDIA_MAX_STORAGE_MB", 512, 102400); err != nil {
		legacy.invalid["mediaMaxStorageMB"] = err
	}
	if legacy.ArtworkMaxStorageMB, err = loadOptionalInteger("RIVUNE_ARTWORK_MAX_STORAGE_MB", 256, 102400); err != nil {
		legacy.invalid["artworkMaxStorageMB"] = err
	}
	return legacy, nil
}

func isLocalHTTPHost(host string, nat64Prefixes []netip.Prefix) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	if address.Is4In6() {
		return address.Unmap().IsLoopback()
	}
	if address.IsLoopback() {
		return true
	}
	if !address.IsPrivate() || !netguard.IsAllowedAddress(address) {
		return false
	}
	for _, prefix := range nat64Prefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func loadDatabaseURL() (string, error) {
	if value := strings.TrimSpace(os.Getenv("RIVUNE_DATABASE_URL")); value != "" {
		return value, nil
	}

	password := strings.TrimSpace(os.Getenv("RIVUNE_DATABASE_PASSWORD"))
	if password == "" {
		return "", errors.New("set RIVUNE_DATABASE_URL or RIVUNE_DATABASE_PASSWORD")
	}

	port := defaultDatabasePort
	if rawPort := strings.TrimSpace(os.Getenv("RIVUNE_DATABASE_PORT")); rawPort != "" {
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", fmt.Errorf("invalid RIVUNE_DATABASE_PORT %q", rawPort)
		}
		port = parsedPort
	}

	host := envOrDefault("RIVUNE_DATABASE_HOST", defaultDatabaseHost)
	name := envOrDefault("RIVUNE_DATABASE_NAME", defaultDatabaseName)
	user := envOrDefault("RIVUNE_DATABASE_USER", defaultDatabaseUser)
	sslMode := strings.TrimSpace(os.Getenv("RIVUNE_DATABASE_SSLMODE"))
	if sslMode == "" {
		return "", errors.New("RIVUNE_DATABASE_SSLMODE must be set explicitly for component database configuration")
	}

	query := url.Values{"sslmode": []string{sslMode}}
	if rootCertificate := strings.TrimSpace(os.Getenv("RIVUNE_DATABASE_SSLROOTCERT")); rootCertificate != "" {
		query.Set("sslrootcert", rootCertificate)
	}
	databaseURL := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + name,
		RawQuery: query.Encode(),
	}
	return databaseURL.String(), nil
}

func loadTrustedProxies(value string) ([]netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	entries := strings.Split(value, ",")
	if len(entries) > 64 {
		return nil, errors.New("RIVUNE_TRUSTED_PROXIES must contain at most 64 addresses or networks")
	}
	proxies := make([]netip.Prefix, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, errors.New("RIVUNE_TRUSTED_PROXIES contains an empty entry")
		}
		network, err := netip.ParsePrefix(entry)
		if err != nil {
			address, addressErr := netip.ParseAddr(entry)
			if addressErr != nil {
				return nil, fmt.Errorf("RIVUNE_TRUSTED_PROXIES contains invalid address or network %q", entry)
			}
			bits := 128
			if address.Is4() {
				bits = 32
			}
			network = netip.PrefixFrom(address, bits)
		}
		proxies = append(proxies, network.Masked())
	}
	return proxies, nil
}

func loadNAT64Prefixes(value string) ([]netip.Prefix, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	entries := strings.Split(value, ",")
	if len(entries) > 16 {
		return nil, errors.New("RIVUNE_NAT64_PREFIXES must contain at most 16 networks")
	}
	prefixes := make([]netip.Prefix, 0, len(entries))
	all := []netip.Prefix{netip.MustParsePrefix("64:ff9b::/96")}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		prefix, err := netip.ParsePrefix(entry)
		if err != nil || prefix != prefix.Masked() || !prefix.Addr().Is6() || !validNAT64PrefixLength(prefix.Bits()) {
			return nil, fmt.Errorf("RIVUNE_NAT64_PREFIXES contains invalid RFC 6052 network %q", entry)
		}
		for _, existing := range all {
			if existing.Contains(prefix.Addr()) || prefix.Contains(existing.Addr()) {
				return nil, fmt.Errorf("RIVUNE_NAT64_PREFIXES contains overlapping networks %q and %q", existing, prefix)
			}
		}
		prefixes = append(prefixes, prefix)
		all = append(all, prefix)
	}
	return prefixes, nil
}

func loadLANArtworkOrigins(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	entries := strings.Split(value, ",")
	if len(entries) > 32 {
		return nil, errors.New("RIVUNE_LAN_ARTWORK_ORIGINS must contain at most 32 origins")
	}
	origins := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, errors.New("RIVUNE_LAN_ARTWORK_ORIGINS contains an empty entry")
		}
		origin, err := netguard.ParsePrivateOrigin(entry)
		if err != nil {
			return nil, fmt.Errorf("RIVUNE_LAN_ARTWORK_ORIGINS contains an invalid origin: %w", err)
		}
		if _, exists := seen[origin.Origin]; exists {
			continue
		}
		seen[origin.Origin] = struct{}{}
		origins = append(origins, origin.Origin)
	}
	return origins, nil
}

func validNAT64PrefixLength(bits int) bool {
	switch bits {
	case 32, 40, 48, 56, 64, 96:
		return true
	default:
		return false
	}
}

func loadOptionalBoolean(name string) (*bool, error) {
	raw, present := os.LookupEnv(name)
	if !present || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "true":
		result := true
		return &result, nil
	case "false":
		result := false
		return &result, nil
	default:
		return nil, fmt.Errorf("%s must be true or false", name)
	}
}

func loadOptionalInteger(name string, minimum, maximum int) (*int, error) {
	raw, present := os.LookupEnv(name)
	if !present || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < minimum || value > maximum {
		return nil, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return &value, nil
}

func loadOptionalTimezone(name string) (*string, error) {
	raw, present := os.LookupEnv(name)
	if !present || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value := strings.TrimSpace(raw)
	if _, err := time.LoadLocation(value); err != nil {
		return nil, fmt.Errorf("%s must be a valid IANA timezone", name)
	}
	return &value, nil
}

func loadOptionalHardwareAcceleration() (*string, error) {
	raw, present := os.LookupEnv("RIVUNE_HARDWARE_ACCELERATION")
	if !present || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "auto", "software", "hybrid", "vaapi", "qsv", "nvenc", "amf":
		return &value, nil
	default:
		return nil, errors.New("RIVUNE_HARDWARE_ACCELERATION must be auto, software, hybrid, vaapi, qsv, nvenc, or amf")
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
