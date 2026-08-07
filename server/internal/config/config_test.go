package config

import (
	"bytes"
	"encoding/xml"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesSecureTokenTTLsByDefault(t *testing.T) {
	setRequiredEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Timezone != "UTC" {
		t.Fatalf("expected UTC default timezone, got %q", cfg.Timezone)
	}
	if cfg.AccessTokenTTL != 15*time.Minute {
		t.Fatalf("expected 15 minute access token TTL, got %s", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 30*24*time.Hour {
		t.Fatalf("expected 30 day refresh token TTL, got %s", cfg.RefreshTokenTTL)
	}
	if cfg.ProfileGrantTTL != 12*time.Hour {
		t.Fatalf("expected 12 hour profile grant TTL, got %s", cfg.ProfileGrantTTL)
	}
	if cfg.MetadataCacheTTL != 24*time.Hour {
		t.Fatalf("expected 24 hour metadata cache TTL, got %s", cfg.MetadataCacheTTL)
	}
	if cfg.RemuxConcurrency != 2 || cfg.FFmpegPath != "ffmpeg" || cfg.FFprobePath != "ffprobe" {
		t.Fatalf("unexpected media processor defaults: ffmpeg=%q ffprobe=%q concurrency=%d", cfg.FFmpegPath, cfg.FFprobePath, cfg.RemuxConcurrency)
	}
	if cfg.HardwareAcceleration != "auto" || cfg.VideoDevice != "/dev/dri/renderD128" {
		t.Fatalf("unexpected hardware defaults: acceleration=%q device=%q", cfg.HardwareAcceleration, cfg.VideoDevice)
	}
	if cfg.TranscodeMaxBitrateKbps != 12000 {
		t.Fatalf("unexpected transcoding bitrate default: %d", cfg.TranscodeMaxBitrateKbps)
	}
	if cfg.ArtworkCacheDir != "/var/lib/rivune/artwork" || cfg.ArtworkStorageBytes != 20480*1024*1024 {
		t.Fatalf("unexpected artwork cache defaults: directory=%q bytes=%d", cfg.ArtworkCacheDir, cfg.ArtworkStorageBytes)
	}
	if len(cfg.LANArtworkOrigins) != 0 {
		t.Fatalf("unexpected default LAN artwork origins: %v", cfg.LANArtworkOrigins)
	}
	if cfg.JellyfinEnabled {
		t.Fatal("expected Jellyfin to default to disabled")
	}
}

func TestLoadParsesJellyfinFlag(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "true", value: "true", want: true},
		{name: "normalized true", value: "  TrUe  ", want: true},
		{name: "false", value: "false", want: false},
		{name: "normalized false", value: "  FaLsE  ", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_JELLYFIN_ENABLED", test.value)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if cfg.JellyfinEnabled != test.want {
				t.Fatalf("JellyfinEnabled = %t, want %t", cfg.JellyfinEnabled, test.want)
			}
		})
	}
}

func TestLoadDefaultsJellyfinToDisabledWhenAbsent(t *testing.T) {
	setRequiredEnvironment(t)
	if err := os.Unsetenv("RIVUNE_JELLYFIN_ENABLED"); err != nil {
		t.Fatalf("unset RIVUNE_JELLYFIN_ENABLED: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.JellyfinEnabled {
		t.Fatal("expected absent Jellyfin flag to disable Jellyfin")
	}
}

func TestLoadRejectsInvalidJellyfinFlag(t *testing.T) {
	for _, value := range []string{"1", "yes", "on", "garbage"} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_JELLYFIN_ENABLED", value)

			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "RIVUNE_JELLYFIN_ENABLED must be true or false") {
				t.Fatalf("Load() error = %v, want strict boolean error", err)
			}
		})
	}
}

func TestLoadNormalizesLANArtworkOrigins(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_LAN_ARTWORK_ORIGINS", " http://192.168.1.48:63113/ ,https://[fd12::48]:8443,http://192.168.1.48:63113 ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	want := []string{"http://192.168.1.48:63113", "https://[fd12::48]:8443"}
	if len(cfg.LANArtworkOrigins) != len(want) {
		t.Fatalf("LAN artwork origins = %v, want %v", cfg.LANArtworkOrigins, want)
	}
	for index := range want {
		if cfg.LANArtworkOrigins[index] != want[index] {
			t.Fatalf("LAN artwork origins = %v, want %v", cfg.LANArtworkOrigins, want)
		}
	}
}

func TestLoadRejectsUnsafeLANArtworkOrigins(t *testing.T) {
	for _, value := range []string{
		"https://cdn.example:443",
		"http://127.0.0.1:8080",
		"http://169.254.169.254:80",
		"http://192.0.2.1:8080",
		"http://192.168.1.48:8080/path",
		"http://192.168.1.48:8080?key=secret",
		"http://user@192.168.1.48:8080",
		"http://192.168.1.48",
	} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_LAN_ARTWORK_ORIGINS", value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe LAN artwork origin was accepted")
			} else if strings.Contains(err.Error(), "key=secret") {
				t.Fatalf("configuration error exposed an origin query: %v", err)
			}
		})
	}
}

func TestLoadUsesEnvironmentCredentials(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_DATABASE_URL", "")
	t.Setenv("RIVUNE_DATABASE_PASSWORD", "database-secret")
	t.Setenv("RIVUNE_DATABASE_SSLMODE", "disable")
	t.Setenv("RIVUNE_TMDB_ACCESS_TOKEN", "tmdb-token")
	t.Setenv("RIVUNE_FANART_API_KEY", "fanart-project")
	t.Setenv("RIVUNE_MDBLIST_API_KEY", "mdblist-key")
	t.Setenv("RIVUNE_TVDB_API_KEY", "tvdb-key")
	t.Setenv("RIVUNE_TVDB_PIN", "tvdb-pin")
	t.Setenv("RIVUNE_TRAKT_CLIENT_ID", "trakt-client")
	t.Setenv("RIVUNE_TRAKT_CLIENT_SECRET", "trakt-secret")
	t.Setenv("RIVUNE_SIMKL_CLIENT_ID", "simkl-client")
	t.Setenv("RIVUNE_TRACKING_ENCRYPTION_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	t.Setenv("TZ", "Europe/Paris")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DatabaseURL != "postgres://rivune:database-secret@localhost:5432/rivune?sslmode=disable" {
		t.Fatal("unexpected database URL")
	}
	if cfg.SetupToken != "setup-secret" || cfg.TMDBAccessToken != "tmdb-token" ||
		cfg.FanartAPIKey != "fanart-project" || cfg.MDBListAPIKey != "mdblist-key" ||
		cfg.TVDBAPIKey != "tvdb-key" || cfg.TVDBPIN != "tvdb-pin" || cfg.TraktClientID != "trakt-client" ||
		cfg.TraktClientSecret != "trakt-secret" || cfg.SimklClientID != "simkl-client" ||
		!bytes.Equal(cfg.TrackingEncryptionKey, make([]byte, 32)) || cfg.Timezone != "Europe/Paris" {
		t.Fatal("environment configuration was not loaded")
	}
}

func TestLoadRejectsNonHexTrackingEncryptionKey(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TRACKING_ENCRYPTION_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if _, err := Load(); err == nil {
		t.Fatal("expected non-hexadecimal tracking key to be rejected")
	}
}

func TestLoadDatabaseURLAddsEncodedRootCertificate(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_DATABASE_URL", "")
	t.Setenv("RIVUNE_DATABASE_PASSWORD", "database-secret")
	t.Setenv("RIVUNE_DATABASE_SSLMODE", "verify-full")
	t.Setenv("RIVUNE_DATABASE_SSLROOTCERT", "/run/rivune TLS/ca#one.crt")

	databaseURL, err := loadDatabaseURL()
	if err != nil {
		t.Fatalf("load database URL: %v", err)
	}
	const expected = "postgres://rivune:database-secret@localhost:5432/rivune?sslmode=verify-full&sslrootcert=%2Frun%2Frivune+TLS%2Fca%23one.crt"
	if databaseURL != expected {
		t.Fatalf("database URL = %q, want %q", databaseURL, expected)
	}

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	if got := parsed.Query().Get("sslmode"); got != "verify-full" {
		t.Fatalf("sslmode = %q, want verify-full", got)
	}
	if got := parsed.Query().Get("sslrootcert"); got != "/run/rivune TLS/ca#one.crt" {
		t.Fatalf("sslrootcert = %q, want decoded certificate path", got)
	}
}

func TestLoadDatabaseURLPreservesExplicitURL(t *testing.T) {
	setRequiredEnvironment(t)
	const explicit = "postgres://custom:secret@db.example/rivune?application_name=Rivune%20Primary&sslmode=require"
	t.Setenv("RIVUNE_DATABASE_URL", explicit)
	t.Setenv("RIVUNE_DATABASE_PASSWORD", "ignored")
	t.Setenv("RIVUNE_DATABASE_SSLMODE", "verify-full")
	t.Setenv("RIVUNE_DATABASE_SSLROOTCERT", "/ignored/ca.crt")

	databaseURL, err := loadDatabaseURL()
	if err != nil {
		t.Fatalf("load database URL: %v", err)
	}
	if databaseURL != explicit {
		t.Fatalf("database URL = %q, want explicit URL unchanged", databaseURL)
	}
}

func TestLoadRequiresExplicitDatabaseSSLModeForComponentConfiguration(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_DATABASE_URL", "")
	t.Setenv("RIVUNE_DATABASE_PASSWORD", "database-secret")

	if _, err := Load(); err == nil || err.Error() != "RIVUNE_DATABASE_SSLMODE must be set explicitly for component database configuration" {
		t.Fatalf("missing database SSL mode error = %v", err)
	}
}

func TestUnraidTemplateRequiresVerifiedPostgreSQLTLS(t *testing.T) {
	templatePath := filepath.Join("..", "..", "..", "templates", "unraid", "rivune.xml")
	content, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read Unraid template: %v", err)
	}

	var template struct {
		Overview string `xml:"Overview"`
		Requires string `xml:"Requires"`
		Configs  []struct {
			Name     string `xml:"Name,attr"`
			Target   string `xml:"Target,attr"`
			Default  string `xml:"Default,attr"`
			Mode     string `xml:"Mode,attr"`
			Type     string `xml:"Type,attr"`
			Required string `xml:"Required,attr"`
			Value    string `xml:",chardata"`
		} `xml:"Config"`
	}
	if err := xml.Unmarshal(content, &template); err != nil {
		t.Fatalf("parse Unraid template XML: %v", err)
	}

	configs := make(map[string]struct {
		Name     string
		Default  string
		Mode     string
		Type     string
		Required string
		Value    string
	}, len(template.Configs))
	for _, config := range template.Configs {
		if _, exists := configs[config.Target]; exists {
			t.Fatalf("duplicate Unraid config target %q", config.Target)
		}
		configs[config.Target] = struct {
			Name     string
			Default  string
			Mode     string
			Type     string
			Required string
			Value    string
		}{config.Name, config.Default, config.Mode, config.Type, config.Required, strings.TrimSpace(config.Value)}
	}

	sslMode, ok := configs["RIVUNE_DATABASE_SSLMODE"]
	if !ok {
		t.Fatal("Unraid template is missing RIVUNE_DATABASE_SSLMODE")
	}
	if sslMode.Default != "verify-full" || sslMode.Value != "verify-full" || sslMode.Type != "Variable" || sslMode.Required != "true" {
		t.Fatalf("unsafe Unraid PostgreSQL SSL mode config: %+v", sslMode)
	}

	const containerCAPath = "/run/rivune-postgres-tls/ca.crt"
	caMount, ok := configs[containerCAPath]
	if !ok {
		t.Fatalf("Unraid template is missing CA mount %q", containerCAPath)
	}
	if caMount.Type != "Path" || caMount.Mode != "ro" || caMount.Required != "true" || caMount.Default == "" || caMount.Value != caMount.Default {
		t.Fatalf("unsafe Unraid PostgreSQL CA mount config: %+v", caMount)
	}

	rootCertificate, ok := configs["RIVUNE_DATABASE_SSLROOTCERT"]
	if !ok {
		t.Fatal("Unraid template is missing RIVUNE_DATABASE_SSLROOTCERT")
	}
	if rootCertificate.Default != containerCAPath || rootCertificate.Value != containerCAPath ||
		rootCertificate.Type != "Variable" || rootCertificate.Required != "true" {
		t.Fatalf("unsafe Unraid PostgreSQL root certificate config: %+v", rootCertificate)
	}

	networkGuidance := strings.ToLower(template.Overview + " " + template.Requires)
	for _, required := range []string{"separate", "edge", "database"} {
		if !strings.Contains(networkGuidance, required) {
			t.Fatalf("Unraid network guidance does not mention %q", required)
		}
	}
	for _, unsafe := range []string{"same custom docker network", "shared custom docker network"} {
		if strings.Contains(networkGuidance, unsafe) {
			t.Fatalf("Unraid network guidance recommends unsafe shared network wording %q", unsafe)
		}
	}
}

func TestLoadRejectsInvalidTimezone(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TZ", "Mars/Olympus_Mons")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid TZ configuration to fail")
	}
}

func TestLoadAcceptsHTTPSAndLoopbackHTTPPublicURLs(t *testing.T) {
	tests := []string{
		"https://rivune.example.com",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"http://[::ffff:127.0.0.1]:8080",
	}

	for _, publicURL := range tests {
		t.Run(publicURL, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_PUBLIC_URL", publicURL)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if cfg.PublicURL != publicURL {
				t.Fatalf("public URL = %q, want %q", cfg.PublicURL, publicURL)
			}
		})
	}
}

func TestLoadRejectsNonLoopbackHTTPPublicURLs(t *testing.T) {
	for _, publicURL := range []string{
		"http://rivune.example.com",
		"http://192.168.1.10:8080",
		"http://0.0.0.0:8080",
		"http://host.docker.internal:8080",
	} {
		t.Run(publicURL, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_PUBLIC_URL", publicURL)

			_, err := Load()
			if err == nil || err.Error() != "RIVUNE_PUBLIC_URL must use https unless its host is loopback" {
				t.Fatalf("non-loopback HTTP public URL error = %v", err)
			}
		})
	}
}

func TestLoadRejectsUnsafeTokenTTLs(t *testing.T) {
	tests := []struct {
		name       string
		accessTTL  string
		refreshTTL string
	}{
		{name: "access token too short", accessTTL: "30s", refreshTTL: "720h"},
		{name: "access token too long", accessTTL: "2h", refreshTTL: "720h"},
		{name: "refresh token not longer than access token", accessTTL: "1h", refreshTTL: "1h"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_ACCESS_TOKEN_TTL", test.accessTTL)
			t.Setenv("RIVUNE_REFRESH_TOKEN_TTL", test.refreshTTL)

			if _, err := Load(); err == nil {
				t.Fatal("expected invalid token TTL configuration to fail")
			}
		})
	}
}

func TestLoadRejectsUnsafeProfileGrantTTL(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_PROFILE_GRANT_TTL", "1m")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid profile grant TTL configuration to fail")
	}
}

func TestLoadRejectsUnsafeRemuxConcurrency(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_REMUX_CONCURRENCY", "17")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid remux concurrency to fail")
	}
}
func TestLoadRejectsUnsafeTranscodeBitrate(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TRANSCODE_MAX_BITRATE_KBPS", "63")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid transcoding bitrate to fail")
	}
}

func TestLoadRejectsUnsafeArtworkStorageLimit(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_ARTWORK_MAX_STORAGE_MB", "255")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid artwork storage limit to fail")
	}
}

func TestLoadRejectsInvalidHardwareAcceleration(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_HARDWARE_ACCELERATION", "amf")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid hardware acceleration mode to fail")
	}
}

func TestLoadRejectsRelativeVideoDevice(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_VIDEO_DEVICE", "renderD128")

	if _, err := Load(); err == nil {
		t.Fatal("expected relative video device path to fail")
	}
}

func TestLoadRejectsTVDBPINWithoutAPIKey(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TVDB_PIN", "subscriber-pin")

	if _, err := Load(); err == nil {
		t.Fatal("expected a TVDB PIN without an API key to fail")
	}
}

func TestLoadParsesConfiguredNAT64Prefixes(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_NAT64_PREFIXES", "2001:db8:aa00::/40, 2001:db9::/32")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	expected := []netip.Prefix{
		netip.MustParsePrefix("2001:db8:aa00::/40"),
		netip.MustParsePrefix("2001:db9::/32"),
	}
	if len(cfg.NAT64Prefixes) != len(expected) {
		t.Fatalf("NAT64 prefixes = %v", cfg.NAT64Prefixes)
	}
	for index := range expected {
		if cfg.NAT64Prefixes[index] != expected[index] {
			t.Fatalf("NAT64 prefix %d = %s, want %s", index, cfg.NAT64Prefixes[index], expected[index])
		}
	}
}

func TestLoadRejectsInvalidNAT64Prefixes(t *testing.T) {
	for _, value := range []string{
		"192.0.2.0/24",
		"2001:db8::/72",
		"2001:db8::1/96",
		"64:ff9b::/96",
		"2001:db8::/32,2001:db8:1::/48",
	} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_NAT64_PREFIXES", value)
			if _, err := Load(); err == nil {
				t.Fatalf("accepted invalid NAT64 prefix configuration %q", value)
			}
		})
	}
}

func TestLoadParsesTrustedProxyAddressesAndNetworks(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TRUSTED_PROXIES", "127.0.0.1, 10.0.0.0/8")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 ||
		cfg.TrustedProxies[0].String() != "127.0.0.1/32" ||
		cfg.TrustedProxies[1].String() != "10.0.0.0/8" {
		t.Fatalf("unexpected trusted proxies: %v", cfg.TrustedProxies)
	}
}

func TestLoadRejectsInvalidTrustedProxy(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TRUSTED_PROXIES", "not-an-address")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid trusted proxy configuration to fail")
	}
}

func setRequiredEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("RIVUNE_DATABASE_URL", "postgres://rivune:secret@localhost/rivune")
	t.Setenv("RIVUNE_DATABASE_PASSWORD", "")
	t.Setenv("RIVUNE_SETUP_TOKEN", "setup-secret")
	t.Setenv("RIVUNE_DATABASE_SSLMODE", "")
	t.Setenv("RIVUNE_DATABASE_SSLROOTCERT", "")
	t.Setenv("RIVUNE_PUBLIC_URL", "")
	t.Setenv("TZ", "")
	t.Setenv("RIVUNE_TRUSTED_PROXIES", "")
	t.Setenv("RIVUNE_ACCESS_TOKEN_TTL", "")
	t.Setenv("RIVUNE_REFRESH_TOKEN_TTL", "")
	t.Setenv("RIVUNE_PROFILE_GRANT_TTL", "")
	t.Setenv("RIVUNE_TMDB_ACCESS_TOKEN", "")
	t.Setenv("RIVUNE_FANART_API_KEY", "")
	t.Setenv("RIVUNE_MDBLIST_API_KEY", "")
	t.Setenv("RIVUNE_TVDB_API_KEY", "")
	t.Setenv("RIVUNE_TVDB_PIN", "")
	t.Setenv("RIVUNE_TRAKT_CLIENT_ID", "")
	t.Setenv("RIVUNE_TRAKT_CLIENT_SECRET", "")
	t.Setenv("RIVUNE_SIMKL_CLIENT_ID", "")
	t.Setenv("RIVUNE_TRACKING_ENCRYPTION_KEY", "")
	t.Setenv("RIVUNE_METADATA_CACHE_TTL", "")
	t.Setenv("RIVUNE_FFMPEG_PATH", "")
	t.Setenv("RIVUNE_FFPROBE_PATH", "")
	t.Setenv("RIVUNE_REMUX_CONCURRENCY", "")
	t.Setenv("RIVUNE_TRANSCODE_THREADS", "")
	t.Setenv("RIVUNE_TRANSCODE_MAX_BITRATE_KBPS", "")
	t.Setenv("RIVUNE_HARDWARE_ACCELERATION", "")
	t.Setenv("RIVUNE_VIDEO_DEVICE", "")
	t.Setenv("RIVUNE_LAN_ARTWORK_ORIGINS", "")
	t.Setenv("RIVUNE_JELLYFIN_ENABLED", "")
}

func TestLoadAllowsCollectionOnlyTraktClientID(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TRAKT_CLIENT_ID", "client-id")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load collection-only Trakt config: %v", err)
	}
	if cfg.TraktClientID != "client-id" || cfg.TraktClientSecret != "" {
		t.Fatal("unexpected collection-only Trakt config")
	}
}

func TestLoadRejectsTraktSecretWithoutClientID(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TRAKT_CLIENT_SECRET", "client-secret")
	if _, err := Load(); err == nil {
		t.Fatal("expected Trakt secret without a client ID to be rejected")
	}
}
