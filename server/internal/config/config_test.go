package config

import (
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
}

func TestLoadUsesEnvironmentCredentials(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("RIVUNE_DATABASE_URL", "")
	t.Setenv("RIVUNE_DATABASE_PASSWORD", "database-secret")
	t.Setenv("RIVUNE_TMDB_ACCESS_TOKEN", "tmdb-token")
	t.Setenv("RIVUNE_TVDB_API_KEY", "tvdb-key")
	t.Setenv("RIVUNE_TVDB_PIN", "tvdb-pin")
	t.Setenv("RIVUNE_TRAKT_CLIENT_ID", "trakt-client")
	t.Setenv("TZ", "Europe/Paris")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DatabaseURL != "postgres://rivune:database-secret@localhost:5432/rivune?sslmode=disable" {
		t.Fatalf("unexpected database URL: %q", cfg.DatabaseURL)
	}
	if cfg.SetupToken != "setup-secret" || cfg.TMDBAccessToken != "tmdb-token" ||
		cfg.TVDBAPIKey != "tvdb-key" || cfg.TVDBPIN != "tvdb-pin" || cfg.TraktClientID != "trakt-client" ||
		cfg.Timezone != "Europe/Paris" {
		t.Fatalf("environment configuration was not loaded: %+v", cfg)
	}
}

func TestLoadRejectsInvalidTimezone(t *testing.T) {
	setRequiredEnvironment(t)
	t.Setenv("TZ", "Mars/Olympus_Mons")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid TZ configuration to fail")
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
	t.Setenv("RIVUNE_PUBLIC_URL", "")
	t.Setenv("TZ", "")
	t.Setenv("RIVUNE_TRUSTED_PROXIES", "")
	t.Setenv("RIVUNE_ACCESS_TOKEN_TTL", "")
	t.Setenv("RIVUNE_REFRESH_TOKEN_TTL", "")
	t.Setenv("RIVUNE_PROFILE_GRANT_TTL", "")
	t.Setenv("RIVUNE_TMDB_ACCESS_TOKEN", "")
	t.Setenv("RIVUNE_TVDB_API_KEY", "")
	t.Setenv("RIVUNE_TVDB_PIN", "")
	t.Setenv("RIVUNE_TRAKT_CLIENT_ID", "")
	t.Setenv("RIVUNE_METADATA_CACHE_TTL", "")
	t.Setenv("RIVUNE_FFMPEG_PATH", "")
	t.Setenv("RIVUNE_FFPROBE_PATH", "")
	t.Setenv("RIVUNE_REMUX_CONCURRENCY", "")
	t.Setenv("RIVUNE_TRANSCODE_THREADS", "")
	t.Setenv("RIVUNE_HARDWARE_ACCELERATION", "")
	t.Setenv("RIVUNE_VIDEO_DEVICE", "")
}
