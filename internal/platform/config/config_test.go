package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nthw-dev/user-management-api/internal/platform/config"
)

const validSecret = "a-secret-that-is-definitely-longer-than-32-bytes"

func setValid(t *testing.T) {
	t.Helper()
	t.Setenv("MONGO_URI", "mongodb://localhost:27017")
	t.Setenv("JWT_SECRET", validSecret)
}

func TestLoad_Defaults(t *testing.T) {
	setValid(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "development", cfg.AppEnv)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, ":9090", cfg.GRPCAddr)
	require.Equal(t, "userdb", cfg.Mongo.Database)
	require.Equal(t, "users", cfg.Mongo.Collection)
	require.Equal(t, "refresh_tokens", cfg.Mongo.RefreshCollection)
	require.Equal(t, time.Minute, cfg.Mongo.ConnTimeout)
	require.Equal(t, 30*time.Minute, cfg.Mongo.MaxConnIdleTime)
	require.Equal(t, uint64(10), cfg.Mongo.MaxIdleConns)
	require.Equal(t, uint64(10), cfg.Mongo.MaxOpenConns)
	require.Equal(t, 15*time.Minute, cfg.JWT.AccessTTL)
	require.Equal(t, 168*time.Hour, cfg.JWT.RefreshTTL)
	require.Equal(t, 12, cfg.Bcrypt.Cost)
	require.Equal(t, 10*time.Second, cfg.CountInterval, "the brief specifies a 10-second interval")
	require.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
	require.Equal(t, 2*time.Second, cfg.ShutdownDelay)
	require.Equal(t, 5*time.Second, cfg.Server.ReadHeaderTimeout)
	require.Equal(t, 5*time.Second, cfg.Server.ReadTimeout)
	require.Equal(t, 10*time.Second, cfg.Server.WriteTimeout)
	require.Zero(t, cfg.Server.IdleTimeout, "zero means falling back to ReadTimeout, per net/http's semantics")
	require.Equal(t, 10*time.Second, cfg.Server.RPCTimeout)
	require.True(t, cfg.IsDevelopment())
}

// The docs promise the app refuses to boot on a short secret — this is what keeps that promise.
func TestLoad_RejectsShortSecret(t *testing.T) {
	setValid(t)
	t.Setenv("JWT_SECRET", "too-short")

	_, err := config.Load()

	require.ErrorContains(t, err, "JWT_SECRET must be at least 32 bytes")
}

func TestLoad_RejectsIdleConnsAboveOpenConns(t *testing.T) {
	setValid(t)
	t.Setenv("MONGO_MAX_IDLE_CONNS", "20")
	t.Setenv("MONGO_MAX_OPEN_CONNS", "10")

	_, err := config.Load()

	require.ErrorContains(t, err, "MONGO_MAX_IDLE_CONNS must not exceed MONGO_MAX_OPEN_CONNS")
}

// Zero is a legitimate SHUTDOWN_DELAY (nothing in front of the process to warn); a negative one is not.
func TestLoad_ShutdownDelay(t *testing.T) {
	t.Run("zero is allowed", func(t *testing.T) {
		setValid(t)
		t.Setenv("SHUTDOWN_DELAY", "0s")

		cfg, err := config.Load()

		require.NoError(t, err)
		require.Zero(t, cfg.ShutdownDelay)
	})

	t.Run("negative is refused", func(t *testing.T) {
		setValid(t)
		t.Setenv("SHUTDOWN_DELAY", "-1s")

		_, err := config.Load()

		require.ErrorContains(t, err, "SHUTDOWN_DELAY must not be negative")
	})
}

func TestLoad_RejectsNonPositiveCountInterval(t *testing.T) {
	setValid(t)
	t.Setenv("USER_COUNT_INTERVAL", "0")

	_, err := config.Load()

	require.ErrorContains(t, err, "USER_COUNT_INTERVAL")
}

// A required value that is missing or set to empty — `required,notEmpty` in the tag must complain, naming the variable.
func TestLoad_RejectsEmpty(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "MONGO_URI missing", key: "MONGO_URI"},
		{name: "JWT_SECRET missing", key: "JWT_SECRET"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValid(t)
			t.Setenv(tt.key, "")

			_, err := config.Load()

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.key)
		})
	}
}

// A malformed value must fail at read time, rather than letting a zero slip through into the system.
func TestLoad_RejectsMalformed(t *testing.T) {
	// The library's message cites the Go field name, not the environment variable name.
	tests := []struct {
		name  string
		key   string
		value string
		field string
	}{
		{name: "malformed duration", key: "JWT_ACCESS_TTL", value: "fifteen-minutes", field: "AccessTTL"},
		{name: "malformed integer", key: "BCRYPT_COST", value: "twelve", field: "Cost"},
		{name: "a negative value for an unsigned integer", key: "MONGO_MAX_IDLE_CONNS", value: "-1", field: "MaxIdleConns"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValid(t)
			t.Setenv(tt.key, tt.value)

			_, err := config.Load()

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.field)
		})
	}
}

// A value with an envDefault falls back to that default when set to empty, rather than becoming the empty value.
func TestLoad_EmptyFallsBackToDefault(t *testing.T) {
	setValid(t)
	t.Setenv("LOG_LEVEL", "")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, "info", cfg.LogLevel)
}

// SERVER_IDLE_TIMEOUT differs from the other timeouts in that zero is a deliberate setting, not an error.
func TestLoad_AllowsZeroIdleTimeout(t *testing.T) {
	setValid(t)
	t.Setenv("SERVER_IDLE_TIMEOUT", "0s")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Zero(t, cfg.Server.IdleTimeout)
}

// For MONGO_MAX_CONN_IDLE_TIME, zero means idle connections are never closed — not an error.
func TestLoad_AllowsZeroMongoIdleTime(t *testing.T) {
	setValid(t)
	t.Setenv("MONGO_MAX_CONN_IDLE_TIME", "0s")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Zero(t, cfg.Mongo.MaxConnIdleTime)
}

// APP_ENV=Development must still be development mode, not fall through to production over a single capital letter.
func TestIsDevelopment_IgnoresCase(t *testing.T) {
	for _, v := range []string{"development", "Development", "DEVELOPMENT"} {
		t.Run(v, func(t *testing.T) {
			setValid(t)
			t.Setenv("APP_ENV", v)

			cfg, err := config.Load()

			require.NoError(t, err)
			require.True(t, cfg.IsDevelopment())
		})
	}

	t.Run("production is not development mode", func(t *testing.T) {
		setValid(t)
		t.Setenv("APP_ENV", "production")

		cfg, err := config.Load()

		require.NoError(t, err)
		require.False(t, cfg.IsDevelopment())
	})
}

// The secret must be the bytes of the string itself, not the comma-separated uint8 slice the library reads []byte as by default.
func TestLoad_SecretIsRawBytes(t *testing.T) {
	setValid(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Equal(t, validSecret, string(cfg.JWT.Secret))
}

// A bad configuration cannot be fixed at runtime — it must fail before the port opens, rather than coming up half-built.
func TestMustLoad_Panics(t *testing.T) {
	setValid(t)
	t.Setenv("MONGO_URI", "")

	require.PanicsWithError(
		t,
		`invalid configuration: env: environment variable "MONGO_URI" should not be empty`,
		func() { config.MustLoad() },
	)
}

// Collecting every error into one batch beats fixing them one at a time and re-running each round.
func TestLoad_ReportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("MONGO_URI", "")
	t.Setenv("JWT_SECRET", "")
	t.Setenv("BCRYPT_COST", "twelve")

	_, err := config.Load()

	require.Error(t, err)
	require.Contains(t, err.Error(), "MONGO_URI")
	require.Contains(t, err.Error(), "JWT_SECRET")
	require.Contains(t, err.Error(), "Cost")
}

// A value that fails to parse must be reported once, not repeated until the root cause is unfindable.
func TestLoad_DoesNotDoubleReportOneVariable(t *testing.T) {
	setValid(t)
	t.Setenv("JWT_ACCESS_TTL", "fifteen-minutes")

	_, err := config.Load()

	require.Error(t, err)
	require.Equal(t, 1, strings.Count(err.Error(), "AccessTTL"))
}
