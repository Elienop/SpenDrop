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
		"TRUST_PROXY_HEADERS", "TRUSTED_PROXY_HOPS", "TRUSTED_PROXY_CIDRS",
		"BCRYPT_COST", "PASSWORD_MIN_LENGTH", "PASSWORD_MAX_LENGTH",
		"MAX_JSON_BYTES", "MAX_UPLOAD_BYTES",
		"SQLITE_BUSY_TIMEOUT",
		"BACKUP_ENABLED", "BACKUP_DIR", "BACKUP_INTERVAL",
		"BACKUP_KEEP_DAILY", "BACKUP_KEEP_WEEKLY", "BACKUP_KEEP_MONTHLY",
		"BACKUP_KEEP_CORRUPT",
		"PUSH_ENABLED", "VAPID_PUBLIC_KEY", "VAPID_PRIVATE_KEY", "VAPID_SUBJECT",
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
		// The ceiling matters as much as the floor: past ~2KB the hex-encoded
		// token makes the cookie too large for browsers to store, so the
		// Set-Cookie is dropped and every login silently fails to stick.
		{"token bytes too many", func(c *Config) { c.Session.TokenBytes = 4096 }, "SESSION_TOKEN_BYTES"},
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

func TestBackupDefaults(t *testing.T) {
	d := Defaults()
	if !d.Backup.Enabled {
		t.Errorf("Backup.Enabled default = false, want true")
	}
	if d.Backup.Interval != 24*time.Hour {
		t.Errorf("Backup.Interval default = %s, want 24h", d.Backup.Interval)
	}
	if d.Backup.KeepDaily != 7 {
		t.Errorf("Backup.KeepDaily default = %d, want 7", d.Backup.KeepDaily)
	}
	if d.Backup.KeepWeekly != 4 {
		t.Errorf("Backup.KeepWeekly default = %d, want 4", d.Backup.KeepWeekly)
	}
	if d.Backup.KeepMonthly != 12 {
		t.Errorf("Backup.KeepMonthly default = %d, want 12", d.Backup.KeepMonthly)
	}
	if d.Backup.Dir == "" {
		t.Errorf("Backup.Dir default is empty")
	}
}

func TestLoad_ParsesBackupEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BACKUP_ENABLED", "false")
	t.Setenv("BACKUP_DIR", "/var/backups/spendrop")
	t.Setenv("BACKUP_INTERVAL", "6h")
	t.Setenv("BACKUP_KEEP_DAILY", "14")
	t.Setenv("BACKUP_KEEP_WEEKLY", "8")
	t.Setenv("BACKUP_KEEP_MONTHLY", "24")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Backup.Enabled {
		t.Errorf("Backup.Enabled = true, want false")
	}
	if cfg.Backup.Dir != "/var/backups/spendrop" {
		t.Errorf("Backup.Dir = %q", cfg.Backup.Dir)
	}
	if cfg.Backup.Interval != 6*time.Hour {
		t.Errorf("Backup.Interval = %s, want 6h", cfg.Backup.Interval)
	}
	if cfg.Backup.KeepDaily != 14 {
		t.Errorf("Backup.KeepDaily = %d, want 14", cfg.Backup.KeepDaily)
	}
	if cfg.Backup.KeepWeekly != 8 {
		t.Errorf("Backup.KeepWeekly = %d, want 8", cfg.Backup.KeepWeekly)
	}
	if cfg.Backup.KeepMonthly != 24 {
		t.Errorf("Backup.KeepMonthly = %d, want 24", cfg.Backup.KeepMonthly)
	}
}

// TestLoad_ParsesBoolVariants asserts the case- and synonym-tolerance of the
// boolean parser. Operators should not have to remember an exact spelling.
func TestLoad_ParsesBoolVariants(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"TRUE":  true,
		"True":  true,
		"1":     true,
		"yes":   true,
		"YES":   true,
		"false": false,
		"FALSE": false,
		"0":     false,
		"no":    false,
		"NO":    false,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("BACKUP_ENABLED", in)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Backup.Enabled != want {
				t.Errorf("Backup.Enabled = %v, want %v", cfg.Backup.Enabled, want)
			}
		})
	}
}

func TestLoad_RejectsMalformedBool(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("BACKUP_ENABLED", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("Load: want error, got nil")
	} else if !strings.Contains(err.Error(), "BACKUP_ENABLED") {
		t.Errorf("error should mention BACKUP_ENABLED, got: %v", err)
	}
}

// TestValidate_RejectsShortBackupInterval is the explicit acceptance criterion
// from the plan: BACKUP_INTERVAL=10m must be rejected with a clear error.
func TestValidate_RejectsShortBackupInterval(t *testing.T) {
	d := Defaults()
	d.Backup.Interval = 10 * time.Minute
	err := d.Validate()
	if err == nil {
		t.Fatal("Validate: want error, got nil")
	}
	if !strings.Contains(err.Error(), "BACKUP_INTERVAL") {
		t.Errorf("error should mention BACKUP_INTERVAL, got: %v", err)
	}
}

func TestValidate_RejectsNegativeBackupKeeps(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"negative daily", func(c *Config) { c.Backup.KeepDaily = -1 }},
		{"negative weekly", func(c *Config) { c.Backup.KeepWeekly = -1 }},
		{"negative monthly", func(c *Config) { c.Backup.KeepMonthly = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Defaults()
			tc.mutate(&d)
			if err := d.Validate(); err == nil {
				t.Errorf("Validate: want error, got nil")
			}
		})
	}
}

// TestValidate_RejectsAllZeroBackupKeeps asserts the all-zero retention
// guard: Prune walks each bucket in order and breaks out before adding
// anything when the keep count is zero, so a 0/0/0 config would delete
// the fresh backup on every tick. Validate must refuse the config up
// front rather than ship a scheduler that silently does nothing.
func TestValidate_RejectsAllZeroBackupKeeps(t *testing.T) {
	d := Defaults()
	d.Backup.Enabled = true
	d.Backup.KeepDaily = 0
	d.Backup.KeepWeekly = 0
	d.Backup.KeepMonthly = 0
	err := d.Validate()
	if err == nil {
		t.Fatal("Validate: want error, got nil")
	}
	if !strings.Contains(err.Error(), "BACKUP_KEEP_*") {
		t.Errorf("error should mention BACKUP_KEEP_*, got: %v", err)
	}
}

// TestValidate_AcceptsAnyNonZeroBackupKeep asserts that a single non-zero
// bucket is enough. A deployment that only wants "keep 1 daily and nothing
// else" must still pass validation.
func TestValidate_AcceptsAnyNonZeroBackupKeep(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"daily only", func(c *Config) { c.Backup.KeepDaily = 1; c.Backup.KeepWeekly = 0; c.Backup.KeepMonthly = 0 }},
		{"weekly only", func(c *Config) { c.Backup.KeepDaily = 0; c.Backup.KeepWeekly = 1; c.Backup.KeepMonthly = 0 }},
		{"monthly only", func(c *Config) { c.Backup.KeepDaily = 0; c.Backup.KeepWeekly = 0; c.Backup.KeepMonthly = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Defaults()
			d.Backup.Enabled = true
			tc.mutate(&d)
			if err := d.Validate(); err != nil {
				t.Errorf("Validate: unexpected error: %v", err)
			}
		})
	}
}

func TestValidate_RejectsEmptyBackupDirWhenEnabled(t *testing.T) {
	d := Defaults()
	d.Backup.Enabled = true
	d.Backup.Dir = ""
	if err := d.Validate(); err == nil {
		t.Fatal("Validate: want error, got nil")
	} else if !strings.Contains(err.Error(), "BACKUP_DIR") {
		t.Errorf("error should mention BACKUP_DIR, got: %v", err)
	}
}

// TestValidate_DisabledSkipsBackupChecks asserts that when Enabled=false, the
// other backup knobs are not validated — a disabled scheduler should not
// care about the interval, dir, or keep counts. This is an acceptance
// criterion in the brief.
func TestValidate_DisabledSkipsBackupChecks(t *testing.T) {
	d := Defaults()
	d.Backup.Enabled = false
	d.Backup.Dir = ""                   // would fail if Enabled
	d.Backup.Interval = time.Nanosecond // would fail if Enabled
	d.Backup.KeepDaily = -5             // would fail if Enabled
	if err := d.Validate(); err != nil {
		t.Errorf("Validate: unexpected error when disabled: %v", err)
	}
}

func TestPushDefaults_DisabledNoOp(t *testing.T) {
	d := Defaults()
	if d.Push.Enabled {
		t.Errorf("Push.Enabled default = true, want false")
	}
	// Disabled config with all VAPID fields empty must still validate.
	if err := d.Validate(); err != nil {
		t.Fatalf("disabled Push must be a no-op for Validate, got: %v", err)
	}
}

func TestPushValidate_RejectsHalfConfigWhenEnabled(t *testing.T) {
	// A real base64url keypair shape (not validated for curve correctness,
	// only for decodability + presence). These decode as base64url.
	const pub = "BNcRdreALRFXTkOOUHK1EtK2wtazFZWxRP9rsB5XF8XlS6KbBJg"
	const priv = "on6X5KGB6Xms6Abz0Tdq8h2gXZ5l9Y6Xqg0Z1aB2cd"
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing public", func(c *Config) {
			c.Push = PushConfig{Enabled: true, VAPIDPrivateKey: priv, VAPIDSubject: "mailto:a@b.c"}
		}},
		{"missing private", func(c *Config) {
			c.Push = PushConfig{Enabled: true, VAPIDPublicKey: pub, VAPIDSubject: "mailto:a@b.c"}
		}},
		{"missing subject", func(c *Config) {
			c.Push = PushConfig{Enabled: true, VAPIDPublicKey: pub, VAPIDPrivateKey: priv}
		}},
		{"bad subject scheme", func(c *Config) {
			c.Push = PushConfig{Enabled: true, VAPIDPublicKey: pub, VAPIDPrivateKey: priv, VAPIDSubject: "tel:+100"}
		}},
		{"public not base64url", func(c *Config) {
			c.Push = PushConfig{Enabled: true, VAPIDPublicKey: "not base64!!", VAPIDPrivateKey: priv, VAPIDSubject: "mailto:a@b.c"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Defaults()
			tc.mutate(&d)
			if err := d.Validate(); err == nil {
				t.Errorf("Validate: want error, got nil")
			}
		})
	}
}

func TestPushValidate_AcceptsFullConfig(t *testing.T) {
	d := Defaults()
	d.Push = PushConfig{
		Enabled:         true,
		VAPIDPublicKey:  "BNcRdreALRFXTkOOUHK1EtK2wtazFZWxRP9rsB5XF8XlS6KbBJg",
		VAPIDPrivateKey: "on6X5KGB6Xms6Abz0Tdq8h2gXZ5l9Y6Xqg0Z1aB2cd",
		VAPIDSubject:    "mailto:ops@example.com",
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("full Push config should validate, got: %v", err)
	}
}

func TestLoad_ParsesPushEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("PUSH_ENABLED", "true")
	t.Setenv("VAPID_PUBLIC_KEY", "BNcRdreALRFXTkOOUHK1EtK2wtazFZWxRP9rsB5XF8XlS6KbBJg")
	t.Setenv("VAPID_PRIVATE_KEY", "on6X5KGB6Xms6Abz0Tdq8h2gXZ5l9Y6Xqg0Z1aB2cd")
	t.Setenv("VAPID_SUBJECT", "https://example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Push.Enabled {
		t.Errorf("Push.Enabled = false, want true")
	}
	if cfg.Push.VAPIDSubject != "https://example.com" {
		t.Errorf("VAPIDSubject = %q", cfg.Push.VAPIDSubject)
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
