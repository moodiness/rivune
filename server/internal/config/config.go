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
)

const (
	defaultListenAddress           = ":8080"
	defaultTimezone                = "UTC"
	defaultDatabaseHost            = "localhost"
	defaultDatabasePort            = 5432
	defaultDatabaseName            = "rivune"
	defaultDatabaseUser            = "rivune"
	defaultAccessTokenTTL          = 15 * time.Minute
	defaultRefreshTokenTTL         = 30 * 24 * time.Hour
	defaultProfileGrantTTL         = 12 * time.Hour
	defaultMetadataCacheTTL        = 24 * time.Hour
	defaultRemuxConcurrency        = 2
	defaultTranscodeThreads        = 4
	defaultTranscodeMaxBitrateKbps = 12000
	defaultMediaStorageMB          = 20480
	defaultArtworkStorageMB        = 20480
	defaultHardwareAcceleration    = "auto"
	defaultVideoDevice             = "/dev/dri/renderD128"
)

type Config struct {
	ListenAddress           string
	PublicURL               string
	DatabaseURL             string
	Timezone                string
	SetupToken              string
	JellyfinEnabled         bool
	JellyfinDebug           bool
	AccessTokenTTL          time.Duration
	RefreshTokenTTL         time.Duration
	ProfileGrantTTL         time.Duration
	TMDBAccessToken         string
	FanartAPIKey            string
	MDBListAPIKey           string
	MetadataCacheTTL        time.Duration
	TVDBAPIKey              string
	TVDBPIN                 string
	TraktClientID           string
	TraktClientSecret       string
	SimklClientID           string
	TrackingEncryptionKey   []byte
	TrustedProxies          []netip.Prefix
	NAT64Prefixes           []netip.Prefix
	FFmpegPath              string
	FFprobePath             string
	RemuxConcurrency        int
	TranscodeThreads        int
	TranscodeMaxBitrateKbps int
	HardwareAcceleration    string
	VideoDevice             string
	MediaTempDir            string
	ArtworkCacheDir         string
	MediaStorageBytes       int64
	ArtworkStorageBytes     int64
	LANArtworkOrigins       []string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:        envOrDefault("RIVUNE_LISTEN_ADDRESS", defaultListenAddress),
		PublicURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("RIVUNE_PUBLIC_URL")), "/"),
		Timezone:             envOrDefault("TZ", defaultTimezone),
		SetupToken:           strings.TrimSpace(os.Getenv("RIVUNE_SETUP_TOKEN")),
		TMDBAccessToken:      strings.TrimSpace(os.Getenv("RIVUNE_TMDB_ACCESS_TOKEN")),
		FanartAPIKey:         strings.TrimSpace(os.Getenv("RIVUNE_FANART_API_KEY")),
		MDBListAPIKey:        strings.TrimSpace(os.Getenv("RIVUNE_MDBLIST_API_KEY")),
		TVDBAPIKey:           strings.TrimSpace(os.Getenv("RIVUNE_TVDB_API_KEY")),
		TVDBPIN:              strings.TrimSpace(os.Getenv("RIVUNE_TVDB_PIN")),
		TraktClientID:        strings.TrimSpace(os.Getenv("RIVUNE_TRAKT_CLIENT_ID")),
		TraktClientSecret:    strings.TrimSpace(os.Getenv("RIVUNE_TRAKT_CLIENT_SECRET")),
		SimklClientID:        strings.TrimSpace(os.Getenv("RIVUNE_SIMKL_CLIENT_ID")),
		FFmpegPath:           envOrDefault("RIVUNE_FFMPEG_PATH", "ffmpeg"),
		FFprobePath:          envOrDefault("RIVUNE_FFPROBE_PATH", "ffprobe"),
		MediaTempDir:         strings.TrimSpace(os.Getenv("RIVUNE_MEDIA_TEMP_DIR")),
		ArtworkCacheDir:      envOrDefault("RIVUNE_ARTWORK_CACHE_DIR", "/var/lib/rivune/artwork"),
		HardwareAcceleration: strings.ToLower(envOrDefault("RIVUNE_HARDWARE_ACCELERATION", defaultHardwareAcceleration)),
		VideoDevice:          envOrDefault("RIVUNE_VIDEO_DEVICE", defaultVideoDevice),
	}

	trackingKey := strings.TrimSpace(os.Getenv("RIVUNE_TRACKING_ENCRYPTION_KEY"))

	var err error
	cfg.JellyfinEnabled, err = loadStrictBoolean("RIVUNE_JELLYFIN_ENABLED")
	if err != nil {
		return Config{}, err
	}
	cfg.JellyfinDebug, err = loadStrictBoolean("RIVUNE_JELLYFIN_DEBUG")
	if err != nil {
		return Config{}, err
	}
	cfg.DatabaseURL, err = loadDatabaseURL()
	if err != nil {
		return Config{}, err
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("TZ must be a valid IANA timezone: %w", err)
	}

	cfg.AccessTokenTTL, err = loadDuration("RIVUNE_ACCESS_TOKEN_TTL", defaultAccessTokenTTL, time.Minute, time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.RefreshTokenTTL, err = loadDuration("RIVUNE_REFRESH_TOKEN_TTL", defaultRefreshTokenTTL, time.Hour, 365*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	if cfg.RefreshTokenTTL <= cfg.AccessTokenTTL {
		return Config{}, errors.New("RIVUNE_REFRESH_TOKEN_TTL must be longer than RIVUNE_ACCESS_TOKEN_TTL")
	}
	cfg.ProfileGrantTTL, err = loadDuration("RIVUNE_PROFILE_GRANT_TTL", defaultProfileGrantTTL, 5*time.Minute, 7*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	if cfg.TVDBPIN != "" && cfg.TVDBAPIKey == "" {
		return Config{}, errors.New("RIVUNE_TVDB_PIN requires RIVUNE_TVDB_API_KEY")
	}
	if cfg.TraktClientSecret != "" && cfg.TraktClientID == "" {
		return Config{}, errors.New("RIVUNE_TRAKT_CLIENT_SECRET requires RIVUNE_TRAKT_CLIENT_ID")
	}
	if trackingKey != "" {
		cfg.TrackingEncryptionKey, err = hex.DecodeString(trackingKey)
		if err != nil || len(cfg.TrackingEncryptionKey) != 32 {
			return Config{}, errors.New("RIVUNE_TRACKING_ENCRYPTION_KEY must be 64 hexadecimal characters encoding exactly 32 bytes")
		}
	} else {
		cfg.TrackingEncryptionKey = make([]byte, 32)
		if cfg.TraktClientID != "" && cfg.TraktClientSecret != "" || cfg.SimklClientID != "" {
			return Config{}, errors.New("RIVUNE_TRACKING_ENCRYPTION_KEY is required when account tracking is configured")
		}
	}
	cfg.MetadataCacheTTL, err = loadDuration("RIVUNE_METADATA_CACHE_TTL", defaultMetadataCacheTTL, time.Hour, 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.RemuxConcurrency, err = loadInteger("RIVUNE_REMUX_CONCURRENCY", defaultRemuxConcurrency, 1, 16)
	if err != nil {
		return Config{}, err
	}
	cfg.TranscodeThreads, err = loadInteger("RIVUNE_TRANSCODE_THREADS", defaultTranscodeThreads, 1, 32)
	if err != nil {
		return Config{}, err
	}
	cfg.TranscodeMaxBitrateKbps, err = loadInteger("RIVUNE_TRANSCODE_MAX_BITRATE_KBPS", defaultTranscodeMaxBitrateKbps, 64, 200000)
	if err != nil {
		return Config{}, err
	}
	switch cfg.HardwareAcceleration {
	case "auto", "software", "vaapi", "qsv", "nvenc":
	default:
		return Config{}, errors.New("RIVUNE_HARDWARE_ACCELERATION must be auto, software, vaapi, qsv, or nvenc")
	}
	if !strings.HasPrefix(cfg.VideoDevice, "/") {
		return Config{}, errors.New("RIVUNE_VIDEO_DEVICE must be an absolute container path")
	}
	mediaStorageMB, err := loadInteger("RIVUNE_MEDIA_MAX_STORAGE_MB", defaultMediaStorageMB, 512, 102400)
	if err != nil {
		return Config{}, err
	}
	cfg.MediaStorageBytes = int64(mediaStorageMB) * 1024 * 1024
	artworkStorageMB, err := loadInteger("RIVUNE_ARTWORK_MAX_STORAGE_MB", defaultArtworkStorageMB, 256, 102400)
	if err != nil {
		return Config{}, err
	}
	cfg.ArtworkStorageBytes = int64(artworkStorageMB) * 1024 * 1024
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

	if cfg.PublicURL != "" {
		parsed, parseErr := url.Parse(cfg.PublicURL)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, errors.New("RIVUNE_PUBLIC_URL must be an absolute HTTP(S) URL without a query or fragment")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Config{}, errors.New("RIVUNE_PUBLIC_URL must use http or https")
		}
		if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
			return Config{}, errors.New("RIVUNE_PUBLIC_URL must use https unless its host is loopback")
		}
	}

	return cfg, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.Unmap().IsLoopback()
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

func loadDuration(name string, fallback, minimum, maximum time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < minimum || duration > maximum {
		return 0, fmt.Errorf("%s must be a duration between %s and %s", name, minimum, maximum)
	}
	return duration, nil
}

func loadInteger(name string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum || number > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return number, nil
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
func loadStrictBoolean(name string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
