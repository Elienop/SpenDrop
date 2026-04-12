package config

import (
	"strings"
	"testing"
	"time"
)

// clearConfigEnv resets every env var Load reads so each test sees a clean
// slate. Using t.Setenv with an empty string works on all platforms because
// Load treats "" the same as unset.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "DB_PATH", "SHUTDOWN_GRACE",
		"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT",
		"HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT",
		"SESSION_TTL", "SESSION_CLEANUP_INTERVAL", "SESSION_TOKEN_BYTES",
		"RATE_LIMIT_MAX", "RATE_LIMIT_WINDOW",
		"BCRYPT_COST", "PASSWORD_MIN_LENGTH", "PASSWORD_MAX_LENGTH",
		"MAX_JSON_BYTES", "MAX_UPLOAD_BYTES",
		"SQLITE_BUSY_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
}

func TestDefaults_Valid(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("Defaults() did not pass Validate: %v", err)
	}
}

func TestLoad_NoEnv_ReturnsDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Defaults()
	if cfg.Port != want.Port {
		t.Errorf("Port = %q, want %q", cfg.Port, want.Port)
	}
	if cfg.DBPath != want.DBPath {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want.DBPath)
	}
	if cfg.HTTP.ReadHeaderTimeout != want.HTTP.ReadHeaderTimeout {
		t.Errorf("HTTP.ReadHeaderTimeout = %s, want %s", cfg.HTTP.ReadHeaderTimeout, want.HTTP.ReadHeaderTimeout)
	}
	if cfg.Session.TTL != want.Session.TTL {
		t.Errorf("Session.TTL = %s, want %s", cfg.Session.TTL, want.Session.TTL)
	}
	if cfg.Password.BcryptCost != want.Password.BcryptCost {
		t.Errorf("Password.BcryptCost = %d, want %d", cfg.Password.BcryptCost, want.Password.BcryptCost)
	}
	if cfg.Upload.MaxJSONBytes != want.Upload.MaxJSONBytes {
		t.Errorf("Upload.MaxJSONBytes = %d, want %d", cfg.Upload.MaxJSONBytes, want.Upload.MaxJSONBytes)
	}
}

func TestLoad_ParsesEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("DB_PATH", "/var/lib/spendrop.db")
	t.Setenv("HTTP_READ_TIMEOUT", "20s")
	t.Setenv("SESSION_TTL", "24h")
	t.Setenv("SESSION_TOKEN_BYTES", "48")
	t.Setenv("RATE_LIMIT_MAX", "5")
	t.Setenv("RATE_LIMIT_WINDOW", "30s")
	t.Setenv("BCRYPT_COST", "10")
	t.Setenv("PASSWORD_MIN_LENGTH", "12")
	t.Setenv("MAX_JSON_BYTES", "2097152")    // 2 MiB
	t.Setenv("MAX_UPLOAD_BYTES", "52428800") // 50 MiB
	t.Setenv("SQLITE_BUSY_TIMEOUT", "10s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want 9090", cfg.Port)
	}
	if cfg.DBPath != "/var/lib/spendrop.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.HTTP.ReadTimeout != 20*time.Second {
		t.Errorf("HTTP.ReadTimeout = %s, want 20s", cfg.HTTP.ReadTimeout)
	}
	if cfg.Session.TTL != 24*time.Hour {
		t.Errorf("Session.TTL = %s, want 24h", cfg.Session.TTL)
	}
	if cfg.Session.TokenBytes != 48 {
		t.Errorf("Session.TokenBytes = %d, want 48", cfg.Session.TokenBytes)
	}
	if cfg.RateLimit.MaxAttempts != 5 {
		t.Errorf("RateLimit.MaxAttempts = %d, want 5", cfg.RateLimit.MaxAttempts)
	}
	if cfg.RateLimit.Window != 30*time.Second {
		t.Errorf("RateLimit.Window = %s, want 30s", cfg.RateLimit.Window)
	}
	if cfg.Password.BcryptCost != 10 {
		t.Errorf("Password.BcryptCost = %d, want 10", cfg.Password.BcryptCost)
	}
	if cfg.Password.MinLength != 12 {
		t.Errorf("Password.MinLength = %d, want 12", cfg.Password.MinLength)
	}
	if cfg.Upload.MaxJSONBytes != 2<<20 {
		t.Errorf("Upload.MaxJSONBytes = %d, want %d", cfg.Upload.MaxJSONBytes, 2<<20)
	}
	if cfg.Upload.MaxFileBytes != 50<<20 {
		t.Errorf("Upload.MaxFileBytes = %d, want %d", cfg.Upload.MaxFileBytes, 50<<20)
	}
	if cfg.SQLite.BusyTimeout != 10*time.Second {
		t.Errorf("SQLite.BusyTimeout = %s, want 10s", cfg.SQLite.BusyTimeout)
	}
}

func TestLoad_MalformedValues_ReturnError(t *testing.T) {
	cases := map[string]string{
		"HTTP_READ_TIMEOUT":   "not-a-duration",
		"SESSION_TOKEN_BYTES": "abc",
		"BCRYPT_COST":         "xyz",
		"MAX_JSON_BYTES":      "infinity",
		"RATE_LIMIT_WINDOW":   "1 week", // not a valid Go duration
	}
	for envKey, badVal := range cases {
		t.Run(envKey, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(envKey, badVal)
			if _, err := Load(); err == nil {
				t.Errorf("Load(%s=%q): want error, got nil", envKey, badVal)
			} else if !strings.Contains(err.Error(), envKey) {
				t.Errorf("Load error should mention %q, got: %v", envKey, err)
			}
		})
	}
}

func TestValidate_RejectsOutOfRangeValues(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{"bcrypt too low", func(c *Config) { c.Password.BcryptCost = 3 }, "BCRYPT_COST"},
		{"bcrypt too high", func(c *Config) { c.Password.BcryptCost = 32 }, "BCRYPT_COST"},
		{"password max above bcrypt limit", func(c *Config) { c.Password.MaxLength = 100 }, "PASSWORD_MAX_LENGTH"},
		{"min > max", func(c *Config) {
			c.Password.MinLength = 50
			c.Password.MaxLength = 20
		}, "PASSWORD_MIN_LENGTH"},
		{"token bytes too few", func(c *Config) { c.Session.TokenBytes = 8 }, "SESSION_TOKEN_BYTES"},
		{"rate limit zero", func(c *Config) { c.RateLimit.MaxAttempts = 0 }, "RATE_LIMIT_MAX"},
		{"rate window zero", func(c *Config) { c.RateLimit.Window = 0 }, "RATE_LIMIT_WINDOW"},
		{"json bytes zero", func(c *Config) { c.Upload.MaxJSONBytes = 0 }, "MAX_JSON_BYTES"},
		{"upload bytes zero", func(c *Config) { c.Upload.MaxFileBytes = 0 }, "MAX_UPLOAD_BYTES"},
		{"empty port", func(c *Config) { c.Port = "" }, "PORT"},
		{"empty db path", func(c *Config) { c.DBPath = "" }, "DB_PATH"},
		{"zero shutdown grace", func(c *Config) { c.ShutdownGrace = 0 }, "SHUTDOWN_GRACE"},
		{"zero read timeout", func(c *Config) { c.HTTP.ReadTimeout = 0 }, "HTTP_READ_TIMEOUT"},
		{"zero session TTL", func(c *Config) { c.Session.TTL = 0 }, "SESSION_TTL"},
		{"zero sqlite busy timeout", func(c *Config) { c.SQLite.BusyTimeout = 0 }, "SQLITE_BUSY_TIMEOUT"},
		{"empty journal mode", func(c *Config) { c.SQLite.JournalMode = "" }, "journal mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Defaults()
			tc.mutate(&d)
			err := d.Validate()
			if err == nil {
				t.Fatalf("Validate: want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("Validate error %q should contain %q", err, tc.wantSub)
			}
		})
	}
}

func TestSQLiteDSN(t *testing.T) {
	cfg := Defaults()
	cfg.DBPath = "test.db"
	cfg.SQLite.BusyTimeout = 5 * time.Second
	cfg.SQLite.JournalMode = "WAL"
	cfg.SQLite.ForeignKeys = true

	got := cfg.SQLiteDSN()
	want := "test.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	if got != want {
		t.Errorf("SQLiteDSN = %q, want %q", got, want)
	}

	// With foreign keys disabled
	cfg.SQLite.ForeignKeys = false
	got = cfg.SQLiteDSN()
	want = "test.db?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=off"
	if got != want {
		t.Errorf("SQLiteDSN (fk off) = %q, want %q", got, want)
	}
}

func TestLoad_TrimsWhitespaceInNumericEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BCRYPT_COST", "  10  ")
	t.Setenv("SESSION_TOKEN_BYTES", "\t48\n")
	t.Setenv("MAX_JSON_BYTES", " 2097152 ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Password.BcryptCost != 10 {
		t.Errorf("BcryptCost = %d, want 10", cfg.Password.BcryptCost)
	}
	if cfg.Session.TokenBytes != 48 {
		t.Errorf("TokenBytes = %d, want 48", cfg.Session.TokenBytes)
	}
	if cfg.Upload.MaxJSONBytes != 2<<20 {
		t.Errorf("MaxJSONBytes = %d", cfg.Upload.MaxJSONBytes)
	}
}
