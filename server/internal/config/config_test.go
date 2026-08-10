package config

import (
	"encoding/xml"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesBootstrapAndCompiledDefaults(t *testing.T) {
	setRequiredEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.AccessTokenTTL != 15*time.Minute || cfg.RefreshTokenTTL != 30*24*time.Hour || cfg.ProfileGrantTTL != 12*time.Hour {
		t.Fatalf("unexpected fixed token lifetimes: access=%s refresh=%s profile=%s", cfg.AccessTokenTTL, cfg.RefreshTokenTTL, cfg.ProfileGrantTTL)
	}
	if cfg.FFmpegPath != "ffmpeg" || cfg.FFprobePath != "ffprobe" || cfg.RemuxConcurrency != 4 || cfg.TranscodeThreads != 4 || cfg.TranscodeMaxReadRate != 1.5 || cfg.HLSInitialBufferSeconds != 6 {
		t.Fatalf("unexpected compiled media defaults: %+v", cfg)
	}
	if cfg.EncryptionKeys == nil || cfg.EncryptionKeys.ActiveVersion() != 2 || cfg.EncryptionKeysFromLegacy {
		t.Fatal("versioned encryption keyring was not loaded")
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

func TestLoadLegacyEnvironmentSeparatesMigrationInput(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TZ", "Europe/Paris")
	t.Setenv("RIVUNE_JELLYFIN_ENABLED", "true")
	t.Setenv("RIVUNE_ALLOW_TRANSCODING", "false")
	t.Setenv("RIVUNE_TRANSCODE_MAX_BITRATE_KBPS", "18000")
	t.Setenv("RIVUNE_TMDB_ACCESS_TOKEN", "tmdb-token")
	t.Setenv("RIVUNE_TVDB_API_KEY", "tvdb-key")
	t.Setenv("RIVUNE_TVDB_PIN", "tvdb-pin")
	t.Setenv("RIVUNE_TRAKT_CLIENT_ID", "trakt-client")
	t.Setenv("RIVUNE_TRAKT_CLIENT_SECRET", "trakt-secret")
	legacy, err := LoadLegacyEnvironment()
	if err != nil {
		t.Fatalf("load legacy environment: %v", err)
	}
	if legacy.Timezone == nil || *legacy.Timezone != "Europe/Paris" || legacy.JellyfinEnabled == nil || !*legacy.JellyfinEnabled || legacy.AllowTranscoding == nil || *legacy.AllowTranscoding || legacy.TranscodeMaxBitrateKbps == nil || *legacy.TranscodeMaxBitrateKbps != 18000 {
		t.Fatalf("legacy runtime migration input = %+v", legacy)
	}
	if legacy.TMDBAccessToken != "tmdb-token" || legacy.TVDBPIN != "tvdb-pin" || legacy.TraktClientSecret != "trakt-secret" {
		t.Fatal("legacy credential migration input was not loaded")
	}
}

func TestLoadRequiresStrictVersionedEncryptionKeys(t *testing.T) {
	setRequiredEnvironment(t)
	for _, value := range []string{"", "2:" + strings.Repeat("0", 64), "2:" + strings.Repeat("AA", 32), "2:" + strings.Repeat("12", 32) + ",2:" + strings.Repeat("34", 32)} {
		t.Setenv("RIVUNE_ENCRYPTION_KEYS", value)
		t.Setenv("RIVUNE_TRACKING_ENCRYPTION_KEY", "")
		if _, err := Load(); err == nil {
			t.Fatalf("unsafe keyring %q was accepted", value)
		}
	}
}

func TestLoadUsesLegacyTrackingKeyOnlyAsVersionOneKeyring(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_ENCRYPTION_KEYS", "")
	t.Setenv("RIVUNE_TRACKING_ENCRYPTION_KEY", strings.Repeat("42", 32))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EncryptionKeysFromLegacy || cfg.EncryptionKeys.ActiveVersion() != 1 {
		t.Fatal("legacy key did not initialize version one keyring")
	}
}

func TestLoadExplainsLegacyTrackingKeyRecovery(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "missing", want: "for a legacy upgrade restore the existing RIVUNE_TRACKING_ENCRYPTION_KEY"},
		{name: "invalid", value: strings.Repeat("z", 64), want: "restore the existing key from backup"},
		{name: "all zero", value: strings.Repeat("0", 64), want: "must not be all zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setRequiredEnvironment(t)
			t.Setenv("RIVUNE_ENCRYPTION_KEYS", "")
			t.Setenv("RIVUNE_TRACKING_ENCRYPTION_KEY", test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want text %q", err, test.want)
			}
			if test.value != "" && strings.Contains(err.Error(), test.value) {
				t.Fatal("Load error exposed legacy key material")
			}
		})
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

func TestUnraidTemplateSupportsCommonAndHardenedDeploymentModes(t *testing.T) {
	templatePath := filepath.Join("..", "..", "..", "templates", "unraid", "rivune.xml")
	content, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read Unraid template: %v", err)
	}

	type configSpec struct {
		Name        string
		Description string
		Default     string
		Display     string
		Mode        string
		Type        string
		Required    string
		Value       string
	}
	var template struct {
		Overview string `xml:"Overview"`
		Requires string `xml:"Requires"`
		Configs  []struct {
			Name        string `xml:"Name,attr"`
			Target      string `xml:"Target,attr"`
			Description string `xml:"Description,attr"`
			Default     string `xml:"Default,attr"`
			Display     string `xml:"Display,attr"`
			Mode        string `xml:"Mode,attr"`
			Type        string `xml:"Type,attr"`
			Required    string `xml:"Required,attr"`
			Value       string `xml:",chardata"`
		} `xml:"Config"`
	}
	if err := xml.Unmarshal(content, &template); err != nil {
		t.Fatalf("parse Unraid template XML: %v", err)
	}

	configs := make(map[string]configSpec, len(template.Configs))
	for _, config := range template.Configs {
		if _, exists := configs[config.Target]; exists {
			t.Fatalf("duplicate Unraid config target %q", config.Target)
		}
		configs[config.Target] = configSpec{
			Name:        config.Name,
			Description: config.Description,
			Default:     config.Default,
			Display:     config.Display,
			Mode:        config.Mode,
			Type:        config.Type,
			Required:    config.Required,
			Value:       strings.TrimSpace(config.Value),
		}
	}

	sslMode := configs["RIVUNE_DATABASE_SSLMODE"]
	if sslMode.Default != "disable|verify-full" || sslMode.Value != "disable" || sslMode.Type != "Variable" || sslMode.Required != "true" || !strings.Contains(sslMode.Description, "verify-full") {
		t.Fatalf("unexpected Unraid PostgreSQL SSL mode config: %+v", sslMode)
	}
	databasePort := configs["RIVUNE_DATABASE_PORT"]
	if databasePort.Type != "Variable" || databasePort.Required != "false" || databasePort.Default != "" || databasePort.Value != "" {
		t.Fatalf("PostgreSQL port does not use its safe default: %+v", databasePort)
	}

	const containerCAPath = "/run/rivune-postgres-tls/ca.crt"
	caMount := configs[containerCAPath]
	if caMount.Type != "Path" || caMount.Mode != "ro" || caMount.Required != "false" || caMount.Default != "" || caMount.Value != "" {
		t.Fatalf("PostgreSQL CA mount is not optional: %+v", caMount)
	}
	rootCertificate := configs["RIVUNE_DATABASE_SSLROOTCERT"]
	if rootCertificate.Type != "Variable" || rootCertificate.Required != "false" || rootCertificate.Default != "" || rootCertificate.Value != "" || !strings.Contains(rootCertificate.Description, containerCAPath) {
		t.Fatalf("PostgreSQL root certificate is not optional: %+v", rootCertificate)
	}

	if _, exists := configs["8080"]; exists {
		t.Fatal("an empty optional Unraid Port would publish a random host port")
	}
	if !strings.Contains(template.Overview, "manually add a TCP Port mapping") {
		t.Fatal("Unraid Pangolin fallback does not explain manual host port mapping")
	}

	if _, exists := configs["/dev/dri/renderD128"]; exists {
		t.Fatal("an empty optional Unraid Device would produce an invalid Docker argument")
	}
	videoGroup := configs["RIVUNE_VIDEO_GROUP_ID"]
	if videoGroup.Type != "Variable" || videoGroup.Required != "false" || videoGroup.Default != "" || videoGroup.Value != "" {
		t.Fatalf("AMD/Intel render group is not optional: %+v", videoGroup)
	}

	keyring := configs["RIVUNE_ENCRYPTION_KEYS"]
	for _, guidance := range []string{"openssl rand -hex 32", "Legacy upgrade", "never generate a replacement"} {
		if !strings.Contains(keyring.Description, guidance) {
			t.Fatalf("Unraid keyring guidance does not mention %q", guidance)
		}
	}
	if keyring.Required != "true" || keyring.Type != "Variable" {
		t.Fatalf("unexpected Unraid keyring config: %+v", keyring)
	}

	trustedProxies := configs["RIVUNE_TRUSTED_PROXIES"]
	if trustedProxies.Required != "false" || !strings.Contains(trustedProxies.Description, "Newt") {
		t.Fatalf("unexpected trusted proxy config: %+v", trustedProxies)
	}

	networkGuidance := strings.ToLower(template.Overview + " " + template.Requires)
	for _, required := range []string{"database-only", "pangolin", "optional", "verify-full"} {
		if !strings.Contains(networkGuidance, required) {
			t.Fatalf("Unraid guidance does not mention %q", required)
		}
	}
	for _, unsafe := range []string{"same custom docker network", "shared custom docker network", "0.0.0.0/0"} {
		if strings.Contains(networkGuidance, unsafe) {
			t.Fatalf("Unraid guidance contains unsafe network wording %q", unsafe)
		}
	}
}

func TestLoadLegacyEnvironmentCapturesInvalidTimezone(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TZ", "Mars/Olympus_Mons")
	legacy, err := LoadLegacyEnvironment()
	if err != nil || legacy.ValidationError("timezone") == nil {
		t.Fatal("invalid legacy timezone was not captured")
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

func TestLoadIgnoresLegacyMediaRuntimeEnvironment(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_FFMPEG_PATH", "/untrusted/ffmpeg")
	t.Setenv("RIVUNE_REMUX_CONCURRENCY", "99")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.FFmpegPath != "ffmpeg" || cfg.RemuxConcurrency != 4 {
		t.Fatalf("legacy runtime environment affected bootstrap config: %+v", cfg)
	}
}

func TestLoadLegacyEnvironmentCapturesUnsafeRuntimeValues(t *testing.T) {
	for environment, setting := range map[string]string{"RIVUNE_TRANSCODE_MAX_BITRATE_KBPS": "transcodeMaxBitrateKbps", "RIVUNE_ARTWORK_MAX_STORAGE_MB": "artworkMaxStorageMB", "RIVUNE_HARDWARE_ACCELERATION": "hardwareAcceleration"} {
		t.Run(environment, func(t *testing.T) {
			setRequiredEnvironment(t)
			value := map[string]string{"RIVUNE_TRANSCODE_MAX_BITRATE_KBPS": "63", "RIVUNE_ARTWORK_MAX_STORAGE_MB": "255", "RIVUNE_HARDWARE_ACCELERATION": "amf"}[environment]
			t.Setenv(environment, value)
			legacy, err := LoadLegacyEnvironment()
			if err != nil || legacy.ValidationError(setting) == nil {
				t.Fatal("unsafe legacy runtime value was not captured")
			}
		})
	}
}

func TestLoadLegacyEnvironmentDefersCredentialDependenciesToPersistence(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_TVDB_PIN", "subscriber-pin")
	legacy, err := LoadLegacyEnvironment()
	if err != nil || legacy.TVDBPIN != "subscriber-pin" {
		t.Fatalf("isolated migration input = %s, %v", legacy, err)
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
	t.Setenv("RIVUNE_ENCRYPTION_KEYS", "2:"+strings.Repeat("12", 32)+",1:"+strings.Repeat("34", 32))
	t.Setenv("RIVUNE_METADATA_CACHE_TTL", "")
	t.Setenv("RIVUNE_FFMPEG_PATH", "")
	t.Setenv("RIVUNE_FFPROBE_PATH", "")
	t.Setenv("RIVUNE_REMUX_CONCURRENCY", "")
	t.Setenv("RIVUNE_TRANSCODE_THREADS", "")
	t.Setenv("RIVUNE_TRANSCODE_MAX_BITRATE_KBPS", "")
	t.Setenv("RIVUNE_TRANSCODE_MAX_READ_RATE", "")
	t.Setenv("RIVUNE_HLS_INITIAL_BUFFER_SECONDS", "")
	t.Setenv("RIVUNE_MEDIA_TEMP_DIR", "")
	t.Setenv("RIVUNE_MEDIA_MAX_STORAGE_MB", "")
	t.Setenv("RIVUNE_ARTWORK_MAX_STORAGE_MB", "")
	t.Setenv("RIVUNE_HARDWARE_ACCELERATION", "")
	t.Setenv("RIVUNE_VIDEO_DEVICE", "")
	t.Setenv("RIVUNE_LAN_ARTWORK_ORIGINS", "")
	t.Setenv("RIVUNE_JELLYFIN_ENABLED", "")
	t.Setenv("RIVUNE_JELLYFIN_DEBUG", "")
	t.Setenv("RIVUNE_ALLOW_TRANSCODING", "")
}
