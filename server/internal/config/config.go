package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddress        = ":8080"
	defaultDatabaseHost         = "localhost"
	defaultDatabasePort         = 5432
	defaultDatabaseName         = "rivune"
	defaultDatabaseUser         = "rivune"
	defaultAccessTokenTTL       = 15 * time.Minute
	defaultRefreshTokenTTL      = 30 * 24 * time.Hour
	defaultProfileGrantTTL      = 12 * time.Hour
	defaultMetadataCacheTTL     = 24 * time.Hour
	defaultRemuxConcurrency     = 2
	defaultTranscodeThreads     = 4
	defaultMediaStorageMB       = 20480
	defaultHardwareAcceleration = "auto"
	defaultVideoDevice          = "/dev/dri/renderD128"
)

type Config struct {
	ListenAddress        string
	PublicURL            string
	DatabaseURL          string
	SetupToken           string
	AccessTokenTTL       time.Duration
	RefreshTokenTTL      time.Duration
	ProfileGrantTTL      time.Duration
	TMDBAccessToken      string
	MetadataCacheTTL     time.Duration
	TVDBAPIKey           string
	TVDBPIN              string
	TraktClientID        string
	TrustedProxies       []netip.Prefix
	FFmpegPath           string
	FFprobePath          string
	RemuxConcurrency     int
	TranscodeThreads     int
	HardwareAcceleration string
	VideoDevice          string
	MediaTempDir         string
	MediaStorageBytes    int64
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:        envOrDefault("RIVUNE_LISTEN_ADDRESS", defaultListenAddress),
		PublicURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("RIVUNE_PUBLIC_URL")), "/"),
		SetupToken:           strings.TrimSpace(os.Getenv("RIVUNE_SETUP_TOKEN")),
		TMDBAccessToken:      strings.TrimSpace(os.Getenv("RIVUNE_TMDB_ACCESS_TOKEN")),
		TVDBAPIKey:           strings.TrimSpace(os.Getenv("RIVUNE_TVDB_API_KEY")),
		TVDBPIN:              strings.TrimSpace(os.Getenv("RIVUNE_TVDB_PIN")),
		TraktClientID:        strings.TrimSpace(os.Getenv("RIVUNE_TRAKT_CLIENT_ID")),
		FFmpegPath:           envOrDefault("RIVUNE_FFMPEG_PATH", "ffmpeg"),
		FFprobePath:          envOrDefault("RIVUNE_FFPROBE_PATH", "ffprobe"),
		MediaTempDir:         strings.TrimSpace(os.Getenv("RIVUNE_MEDIA_TEMP_DIR")),
		HardwareAcceleration: strings.ToLower(envOrDefault("RIVUNE_HARDWARE_ACCELERATION", defaultHardwareAcceleration)),
		VideoDevice:          envOrDefault("RIVUNE_VIDEO_DEVICE", defaultVideoDevice),
	}

	var err error
	cfg.DatabaseURL, err = loadDatabaseURL()
	if err != nil {
		return Config{}, err
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
	cfg.TrustedProxies, err = loadTrustedProxies(os.Getenv("RIVUNE_TRUSTED_PROXIES"))
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
	}

	return cfg, nil
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
	sslMode := envOrDefault("RIVUNE_DATABASE_SSLMODE", "disable")

	databaseURL := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, password),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + name,
		RawQuery: url.Values{"sslmode": []string{sslMode}}.Encode(),
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

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
