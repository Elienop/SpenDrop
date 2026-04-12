// Package config centralises all runtime-tunable knobs for SpenDrop.
//
// Every numeric limit, timeout, and rate in the backend should be read from a
// Config value rather than being hard-coded in the call site. This gives
// operators a single place to adjust behavior via environment variables and
// gives tests a hermetic way to override defaults.
//
// Defaults() returns the in-source defaults as an ordinary value. Load()
// layers environment-variable overrides on top and runs Validate() before
// returning. Changing a field after Load has been called is only supported
// from main during startup — treat the returned *Config as read-only once the
// HTTP server has started.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for SpenDrop.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port string

	// DBPath is the filesystem location of the SQLite database file.
	DBPath string

	// HTTP timeouts applied to the http.Server.
	HTTP HTTPConfig

	// Session controls session cookie lifetime, cleanup cadence, and token entropy.
	Session SessionConfig

	// RateLimit controls login/register rate limiting.
	RateLimit RateLimitConfig

	// Password controls bcrypt cost and min/max password length.
	Password PasswordConfig

	// Upload controls JSON body and file upload size limits.
	Upload UploadConfig

	// SQLite controls SQLite open-time options used to build the DSN.
	SQLite SQLiteConfig

	// ShutdownGrace is the maximum time the server waits for in-flight
	// requests to finish during graceful shutdown.
	ShutdownGrace time.Duration
}

// HTTPConfig holds HTTP server timeouts.
type HTTPConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

// SessionConfig holds session lifecycle parameters.
type SessionConfig struct {
	// TTL is how long a newly minted session is valid.
	TTL time.Duration
	// CleanupInterval is how often the background job purges expired sessions.
	CleanupInterval time.Duration
	// TokenBytes is the number of random bytes used per session token. Each
	// byte contributes 8 bits of entropy; the hex-encoded string is twice as long.
	TokenBytes int
}

// RateLimitConfig holds the counters used by the login/register rate limiter.
type RateLimitConfig struct {
	// MaxAttempts is the number of attempts allowed per client IP per window
	// before the server returns 429.
	MaxAttempts int
	// Window is how often the attempt counters are reset.
	Window time.Duration
}

// PasswordConfig holds bcrypt cost and password length policy.
type PasswordConfig struct {
	// BcryptCost is the bcrypt work factor. Must be 4-31. Higher is slower
	// but harder to brute force. 12 is the production default.
	BcryptCost int
	// MinLength is the minimum accepted password length in bytes.
	MinLength int
	// MaxLength is the maximum accepted password length in bytes. Must be
	// <= 72 because bcrypt truncates inputs longer than that.
	MaxLength int
}

// UploadConfig holds request body size limits.
type UploadConfig struct {
	// MaxJSONBytes caps the size of application/json request bodies.
	MaxJSONBytes int64
	// MaxFileBytes caps the size of multipart file uploads (xlsx import).
	MaxFileBytes int64
}

// SQLiteConfig holds SQLite open-time options used to build the DSN.
type SQLiteConfig struct {
	BusyTimeout time.Duration
	JournalMode string
	ForeignKeys bool
}

// Defaults returns the in-source default configuration. All fields are set.
// The returned value is a fresh copy — callers can mutate it without
// affecting other callers.
func Defaults() Config {
	return Config{
		Port:          "8080",
		DBPath:        "spendrop.db",
		ShutdownGrace: 10 * time.Second,
		HTTP: HTTPConfig{
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
		Session: SessionConfig{
			TTL:             30 * 24 * time.Hour,
			CleanupInterval: 1 * time.Hour,
			TokenBytes:      32,
		},
		RateLimit: RateLimitConfig{
			MaxAttempts: 10,
			Window:      time.Minute,
		},
		Password: PasswordConfig{
			BcryptCost: 12,
			MinLength:  8,
			MaxLength:  72,
		},
		Upload: UploadConfig{
			MaxJSONBytes: 1 << 20,  // 1 MiB
			MaxFileBytes: 10 << 20, // 10 MiB
		},
		SQLite: SQLiteConfig{
			BusyTimeout: 5 * time.Second,
			JournalMode: "WAL",
			ForeignKeys: true,
		},
	}
}

// Load reads configuration from environment variables on top of Defaults()
// and returns a validated Config. Unset variables keep their default. Any
// malformed value or out-of-range field returns an error.
func Load() (*Config, error) {
	cfg := Defaults()

	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}

	// HTTP timeouts
	if err := parseDuration("HTTP_READ_HEADER_TIMEOUT", &cfg.HTTP.ReadHeaderTimeout); err != nil {
		return nil, err
	}
	if err := parseDuration("HTTP_READ_TIMEOUT", &cfg.HTTP.ReadTimeout); err != nil {
		return nil, err
	}
	if err := parseDuration("HTTP_WRITE_TIMEOUT", &cfg.HTTP.WriteTimeout); err != nil {
		return nil, err
	}
	if err := parseDuration("HTTP_IDLE_TIMEOUT", &cfg.HTTP.IdleTimeout); err != nil {
		return nil, err
	}
	if err := parseDuration("SHUTDOWN_GRACE", &cfg.ShutdownGrace); err != nil {
		return nil, err
	}

	// Session
	if err := parseDuration("SESSION_TTL", &cfg.Session.TTL); err != nil {
		return nil, err
	}
	if err := parseDuration("SESSION_CLEANUP_INTERVAL", &cfg.Session.CleanupInterval); err != nil {
		return nil, err
	}
	if err := parseInt("SESSION_TOKEN_BYTES", &cfg.Session.TokenBytes); err != nil {
		return nil, err
	}

	// Rate limit
	if err := parseInt("RATE_LIMIT_MAX", &cfg.RateLimit.MaxAttempts); err != nil {
		return nil, err
	}
	if err := parseDuration("RATE_LIMIT_WINDOW", &cfg.RateLimit.Window); err != nil {
		return nil, err
	}

	// Password
	if err := parseInt("BCRYPT_COST", &cfg.Password.BcryptCost); err != nil {
		return nil, err
	}
	if err := parseInt("PASSWORD_MIN_LENGTH", &cfg.Password.MinLength); err != nil {
		return nil, err
	}
	if err := parseInt("PASSWORD_MAX_LENGTH", &cfg.Password.MaxLength); err != nil {
		return nil, err
	}

	// Upload
	if err := parseInt64("MAX_JSON_BYTES", &cfg.Upload.MaxJSONBytes); err != nil {
		return nil, err
	}
	if err := parseInt64("MAX_UPLOAD_BYTES", &cfg.Upload.MaxFileBytes); err != nil {
		return nil, err
	}

	// SQLite
	if err := parseDuration("SQLITE_BUSY_TIMEOUT", &cfg.SQLite.BusyTimeout); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks all fields for acceptable ranges. It is called by Load but
// is also exposed so callers constructing a Config by hand can verify it.
func (c *Config) Validate() error {
	if c.Port == "" {
		return fmt.Errorf("PORT must not be empty")
	}
	if c.DBPath == "" {
		return fmt.Errorf("DB_PATH must not be empty")
	}
	if c.ShutdownGrace <= 0 {
		return fmt.Errorf("SHUTDOWN_GRACE must be > 0: %s", c.ShutdownGrace)
	}

	if c.HTTP.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be > 0: %s", c.HTTP.ReadHeaderTimeout)
	}
	if c.HTTP.ReadTimeout <= 0 {
		return fmt.Errorf("HTTP_READ_TIMEOUT must be > 0: %s", c.HTTP.ReadTimeout)
	}
	if c.HTTP.WriteTimeout <= 0 {
		return fmt.Errorf("HTTP_WRITE_TIMEOUT must be > 0: %s", c.HTTP.WriteTimeout)
	}
	if c.HTTP.IdleTimeout <= 0 {
		return fmt.Errorf("HTTP_IDLE_TIMEOUT must be > 0: %s", c.HTTP.IdleTimeout)
	}

	if c.Session.TTL <= 0 {
		return fmt.Errorf("SESSION_TTL must be > 0: %s", c.Session.TTL)
	}
	if c.Session.CleanupInterval <= 0 {
		return fmt.Errorf("SESSION_CLEANUP_INTERVAL must be > 0: %s", c.Session.CleanupInterval)
	}
	if c.Session.TokenBytes < 16 {
		return fmt.Errorf("SESSION_TOKEN_BYTES must be >= 16 for sufficient entropy: %d", c.Session.TokenBytes)
	}

	if c.RateLimit.MaxAttempts < 1 {
		return fmt.Errorf("RATE_LIMIT_MAX must be >= 1: %d", c.RateLimit.MaxAttempts)
	}
	if c.RateLimit.Window <= 0 {
		return fmt.Errorf("RATE_LIMIT_WINDOW must be > 0: %s", c.RateLimit.Window)
	}

	// bcrypt's own valid range is [4,31] per golang.org/x/crypto/bcrypt.
	// Below 4 the library rejects the cost; above 31 it's impractically slow.
	if c.Password.BcryptCost < 4 || c.Password.BcryptCost > 31 {
		return fmt.Errorf("BCRYPT_COST out of range (4-31): %d", c.Password.BcryptCost)
	}
	if c.Password.MinLength < 1 {
		return fmt.Errorf("PASSWORD_MIN_LENGTH must be >= 1: %d", c.Password.MinLength)
	}
	// bcrypt silently truncates input to the first 72 bytes, so allowing
	// longer passwords would be a footgun — a user could enter 80 chars and
	// only the first 72 would authenticate.
	if c.Password.MaxLength > 72 {
		return fmt.Errorf("PASSWORD_MAX_LENGTH must be <= 72 (bcrypt input limit): %d", c.Password.MaxLength)
	}
	if c.Password.MinLength > c.Password.MaxLength {
		return fmt.Errorf("PASSWORD_MIN_LENGTH (%d) must be <= PASSWORD_MAX_LENGTH (%d)", c.Password.MinLength, c.Password.MaxLength)
	}

	if c.Upload.MaxJSONBytes < 1 {
		return fmt.Errorf("MAX_JSON_BYTES must be >= 1: %d", c.Upload.MaxJSONBytes)
	}
	if c.Upload.MaxFileBytes < 1 {
		return fmt.Errorf("MAX_UPLOAD_BYTES must be >= 1: %d", c.Upload.MaxFileBytes)
	}

	if c.SQLite.BusyTimeout <= 0 {
		return fmt.Errorf("SQLITE_BUSY_TIMEOUT must be > 0: %s", c.SQLite.BusyTimeout)
	}
	if c.SQLite.JournalMode == "" {
		return fmt.Errorf("SQLite journal mode must not be empty")
	}
	return nil
}

// SQLiteDSN returns the SQLite connection string derived from this config.
// The DSN format is "<path>?_journal_mode=<mode>&_busy_timeout=<ms>&_foreign_keys=<on|off>".
func (c *Config) SQLiteDSN() string {
	fk := "off"
	if c.SQLite.ForeignKeys {
		fk = "on"
	}
	return fmt.Sprintf(
		"%s?_journal_mode=%s&_busy_timeout=%d&_foreign_keys=%s",
		c.DBPath,
		c.SQLite.JournalMode,
		c.SQLite.BusyTimeout.Milliseconds(),
		fk,
	)
}

// parseDuration reads an env var as a time.Duration string ("5s", "10m",
// "1h30m"). If the env var is unset the destination is left alone.
func parseDuration(key string, dst *time.Duration) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = d
	return nil
}

// parseInt reads an env var as a base-10 int. If the env var is unset the
// destination is left alone.
func parseInt(key string, dst *int) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = n
	return nil
}

// parseInt64 reads an env var as a base-10 int64. If the env var is unset the
// destination is left alone.
func parseInt64(key string, dst *int64) error {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = n
	return nil
}
