// Package config reads the environment variables once at boot and validates them all.
// If a required value is missing or malformed, the process must end immediately — failing fast beats failing once there are real users.
//
// Both reading and validating are the job of github.com/caarlos0/env/v11 — variable names,
// defaults, and the required/notEmpty conditions are declared in the struct tags, in one place.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// EnvDevelopment is the only APP_ENV value that turns on the developer-facing surfaces — every other value means production.
const EnvDevelopment = "development"

// Config is the whole system's configuration, read once at boot.
//
// There are two ways to add a new value — put a field with an `env` tag right here,
// or, for several values belonging to the same group, collect them into a nested struct and attach an `envPrefix`.
// A nested struct needs no `env` tag of its own, and the tags inside it spell only the trailing part of the name;
// `envPrefix:"MONGO_"` paired with `env:"URI"`, for instance, means the MONGO_URI variable.
type Config struct {
	AppEnv          string        `env:"APP_ENV"             envDefault:"development"`
	HTTPAddr        string        `env:"HTTP_ADDR"           envDefault:":8080"`
	GRPCAddr        string        `env:"GRPC_ADDR"           envDefault:":9090"`
	LogLevel        string        `env:"LOG_LEVEL"           envDefault:"info"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT"    envDefault:"15s"`
	// ShutdownDelay is how long readiness answers "draining" before the listeners close — the window in which a load
	// balancer notices and stops sending new work. Zero is allowed, for a machine with nothing in front of the process.
	ShutdownDelay time.Duration `env:"SHUTDOWN_DELAY"      envDefault:"2s"`
	CountInterval time.Duration `env:"USER_COUNT_INTERVAL" envDefault:"10s"`

	Server Server `envPrefix:"SERVER_"`
	Mongo  Mongo  `envPrefix:"MONGO_"`
	JWT    JWT    `envPrefix:"JWT_"`
	Bcrypt Bcrypt `envPrefix:"BCRYPT_"`
}

// Server holds every timeout layer of *http.Server.
// net/http's default is "no timeout at all", which leaves the door open for slowloris.
type Server struct {
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT"        envDefault:"5s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT"       envDefault:"10s"`
	// Zero means falling back to ReadTimeout, per net/http's semantics.
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"0"`
	// RPCTimeout is the ceiling on every gRPC call: a shorter deadline from the caller is honored, a longer one is capped.
	RPCTimeout time.Duration `env:"RPC_TIMEOUT" envDefault:"10s"`
}

// Mongo holds the driver's connection and pool settings.
// The field names borrow database/sql's idiom so they read familiarly, but they end up as Mongo driver options.
type Mongo struct {
	URI               string `env:"URI,required,notEmpty"`
	Database          string `env:"DATABASE"           envDefault:"userdb"`
	Collection        string `env:"COLLECTION"         envDefault:"users"`
	RefreshCollection string `env:"REFRESH_COLLECTION" envDefault:"refresh_tokens"`

	ConnTimeout time.Duration `env:"CONN_TIMEOUT" envDefault:"1m"` // → SetConnectTimeout
	// Zero means idle connections are never closed, per the driver's semantics.
	MaxConnIdleTime time.Duration `env:"MAX_CONN_IDLE_TIME" envDefault:"30m"` // → SetMaxConnIdleTime
	MaxIdleConns    uint64        `env:"MAX_IDLE_CONNS"     envDefault:"10"`  // → SetMinPoolSize
	MaxOpenConns    uint64        `env:"MAX_OPEN_CONNS"     envDefault:"10"`  // → SetMaxPoolSize
}

// MinSecretBytes is the shortest JWT_SECRET accepted — HS256 keys shorter than the hash output are trivially brute-forced.
const MinSecretBytes = 32

type JWT struct {
	Secret     []byte        `env:"SECRET,required,notEmpty"`
	Issuer     string        `env:"ISSUER"      envDefault:"user-service"`
	Audience   string        `env:"AUDIENCE"    envDefault:"user-service-api"`
	AccessTTL  time.Duration `env:"ACCESS_TTL"  envDefault:"15m"`
	RefreshTTL time.Duration `env:"REFRESH_TTL" envDefault:"168h"`
}

type Bcrypt struct {
	Cost int `env:"COST" envDefault:"12"`
}

// IsDevelopment is the one place that decides whether we are running in development mode.
// The comparison is case-insensitive, because APP_ENV=Development should not silently become production mode.
func (c Config) IsDevelopment() bool { return strings.EqualFold(c.AppEnv, EnvDevelopment) }

// MustLoad is for use at boot — a bad configuration is a failure that cannot be fixed at runtime,
// so it logs every problem and panics right away rather than letting a half-built service come up.
func MustLoad() Config {
	cfg, err := Load()
	if err != nil {
		slog.Error("failed to read configuration", slog.Any("err", err))
		panic(err)
	}
	return cfg
}

// Load reads every value according to the struct tags.
// The library already collects every error into a single batch, which beats fixing them one at a time and re-running each round.
func Load() (Config, error) {
	cfg, err := env.ParseAsWithOptions[Config](env.Options{
		// []byte is []uint8, which the library would read as a comma-separated slice.
		// The secret has to be the bytes of the string itself, so we spell out the conversion.
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeOf([]byte(nil)): func(v string) (any, error) { return []byte(v), nil },
		},
	})
	if err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// validate holds the rules a struct tag cannot express — each one names the variable so the fix is obvious.
func (c Config) validate() error {
	var problems []string

	// The worker's ticker panics if the interval is zero or negative.
	if c.CountInterval <= 0 {
		problems = append(problems, "USER_COUNT_INTERVAL must be greater than 0")
	}
	if c.ShutdownDelay < 0 {
		problems = append(problems, "SHUTDOWN_DELAY must not be negative")
	}
	if len(c.JWT.Secret) < MinSecretBytes {
		problems = append(problems, fmt.Sprintf("JWT_SECRET must be at least %d bytes", MinSecretBytes))
	}
	// The driver treats MinPoolSize above MaxPoolSize as an error at connect time; better to say so before the port opens.
	if c.Mongo.MaxIdleConns > c.Mongo.MaxOpenConns {
		problems = append(problems, "MONGO_MAX_IDLE_CONNS must not exceed MONGO_MAX_OPEN_CONNS")
	}

	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}
