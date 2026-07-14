// Package config loads process configuration from the environment.
//
// Every service reads the same Config, so a single .env drives the whole stack.
// Load fails loudly on a malformed value rather than silently falling back to a
// default: a typo in a DSN must not present as a connection error ten seconds later.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Config is the full configuration surface of the platform.
type Config struct {
	Env      string // development | staging | production
	LogLevel string // debug | info | warn | error

	Postgres   Postgres
	ClickHouse ClickHouse
	Redis      Redis
	S3         S3

	Gateway   Server
	API       Server
	Processor Processor
}

// Postgres is the control plane: orgs, projects, issue state, alert rules.
type Postgres struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// ClickHouse is the event plane: errors, logs, spans, metrics.
type ClickHouse struct {
	Addr     []string
	Database string
	Username string
	Password string

	DialTimeout     time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// Redis backs the ingest queue (Redis Streams) and rate limiter.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// S3 stores release artifacts — source maps today, debug files later.
type S3 struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// Server is the shared HTTP listener configuration for gateway and api.
type Server struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Processor tunes the queue consumer.
type Processor struct {
	// ConsumerGroup and ConsumerName identify this worker within the stream's
	// consumer group. ConsumerName defaults to the hostname so a restarted pod
	// reclaims its own pending messages.
	ConsumerGroup string
	ConsumerName  string
	// BatchSize is how many envelope items a worker claims per read.
	BatchSize int
	// Concurrency is the number of in-flight item processors per worker.
	Concurrency int
}

// Load reads configuration from the process environment, after merging in a
// dotenv file if one is present. The same file is read by docker compose, so a
// single .env at the repo root configures the containers and the Go services
// identically — two sources of truth for a port number is exactly how you get
// "works in compose, fails in the app".
func Load() (*Config, error) {
	if err := loadDotenv(env("SABAB_ENV_FILE", EnvFile)); err != nil {
		return nil, err
	}

	var errs []error

	cfg := &Config{
		Env:      env("SABAB_ENV", "development"),
		LogLevel: env("SABAB_LOG_LEVEL", "info"),
	}

	cfg.Postgres.DSN = env("SABAB_POSTGRES_DSN", "postgres://sabab:sabab@localhost:5432/sabab?sslmode=disable")
	cfg.Postgres.MaxConns = int32(mustInt(&errs, "SABAB_POSTGRES_MAX_CONNS", 25))
	cfg.Postgres.MinConns = int32(mustInt(&errs, "SABAB_POSTGRES_MIN_CONNS", 2))
	cfg.Postgres.MaxConnLifetime = mustDuration(&errs, "SABAB_POSTGRES_MAX_CONN_LIFETIME", time.Hour)
	cfg.Postgres.MaxConnIdleTime = mustDuration(&errs, "SABAB_POSTGRES_MAX_CONN_IDLE_TIME", 5*time.Minute)

	cfg.ClickHouse.Addr = envList("SABAB_CLICKHOUSE_ADDR", []string{"localhost:9000"})
	cfg.ClickHouse.Database = env("SABAB_CLICKHOUSE_DATABASE", "sabab")
	cfg.ClickHouse.Username = env("SABAB_CLICKHOUSE_USERNAME", "sabab")
	cfg.ClickHouse.Password = env("SABAB_CLICKHOUSE_PASSWORD", "sabab")
	cfg.ClickHouse.DialTimeout = mustDuration(&errs, "SABAB_CLICKHOUSE_DIAL_TIMEOUT", 10*time.Second)
	cfg.ClickHouse.MaxOpenConns = mustInt(&errs, "SABAB_CLICKHOUSE_MAX_OPEN_CONNS", 10)
	cfg.ClickHouse.MaxIdleConns = mustInt(&errs, "SABAB_CLICKHOUSE_MAX_IDLE_CONNS", 5)
	cfg.ClickHouse.ConnMaxLifetime = mustDuration(&errs, "SABAB_CLICKHOUSE_CONN_MAX_LIFETIME", time.Hour)

	cfg.Redis.Addr = env("SABAB_REDIS_ADDR", "localhost:6379")
	cfg.Redis.Password = env("SABAB_REDIS_PASSWORD", "")
	cfg.Redis.DB = mustInt(&errs, "SABAB_REDIS_DB", 0)

	cfg.S3.Endpoint = env("SABAB_S3_ENDPOINT", "localhost:9002")
	cfg.S3.Region = env("SABAB_S3_REGION", "us-east-1")
	cfg.S3.Bucket = env("SABAB_S3_BUCKET", "sabab-artifacts")
	cfg.S3.AccessKey = env("SABAB_S3_ACCESS_KEY", "sabab")
	cfg.S3.SecretKey = env("SABAB_S3_SECRET_KEY", "sabab-secret")
	cfg.S3.UseSSL = mustBool(&errs, "SABAB_S3_USE_SSL", false)

	cfg.Gateway = loadServer(&errs, "GATEWAY", ":8080")
	cfg.API = loadServer(&errs, "API", ":8081")

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "processor"
	}
	cfg.Processor.ConsumerGroup = env("SABAB_PROCESSOR_CONSUMER_GROUP", "processors")
	cfg.Processor.ConsumerName = env("SABAB_PROCESSOR_CONSUMER_NAME", hostname)
	cfg.Processor.BatchSize = mustInt(&errs, "SABAB_PROCESSOR_BATCH_SIZE", 128)
	cfg.Processor.Concurrency = mustInt(&errs, "SABAB_PROCESSOR_CONCURRENCY", 8)

	// Parse errors and validation errors are reported together. Short-circuiting
	// after the parse pass would hide a bad SABAB_LOG_LEVEL behind an unrelated
	// typo, turning one misconfigured deploy into several restarts. Values that
	// failed to parse fell back to their defaults, so validate never sees a
	// half-built Config.
	if err := errors.Join(append(errs, cfg.validate())...); err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadServer(errs *[]error, prefix, defaultAddr string) Server {
	return Server{
		Addr:            env("SABAB_"+prefix+"_ADDR", defaultAddr),
		ReadTimeout:     mustDuration(errs, "SABAB_"+prefix+"_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    mustDuration(errs, "SABAB_"+prefix+"_WRITE_TIMEOUT", 20*time.Second),
		IdleTimeout:     mustDuration(errs, "SABAB_"+prefix+"_IDLE_TIMEOUT", 120*time.Second),
		ShutdownTimeout: mustDuration(errs, "SABAB_"+prefix+"_SHUTDOWN_TIMEOUT", 15*time.Second),
	}
}

func (c *Config) validate() error {
	var errs []error
	if c.Postgres.DSN == "" {
		errs = append(errs, errors.New("SABAB_POSTGRES_DSN must not be empty"))
	}
	if len(c.ClickHouse.Addr) == 0 {
		errs = append(errs, errors.New("SABAB_CLICKHOUSE_ADDR must list at least one host:port"))
	}
	if c.Redis.Addr == "" {
		errs = append(errs, errors.New("SABAB_REDIS_ADDR must not be empty"))
	}
	if c.Processor.BatchSize <= 0 {
		errs = append(errs, errors.New("SABAB_PROCESSOR_BATCH_SIZE must be > 0"))
	}
	if c.Processor.Concurrency <= 0 {
		errs = append(errs, errors.New("SABAB_PROCESSOR_CONCURRENCY must be > 0"))
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("SABAB_LOG_LEVEL %q must be one of debug|info|warn|error", c.LogLevel))
	}
	return errors.Join(errs...)
}

// IsProduction reports whether the process runs with production defaults
// (JSON logs, no stack traces on API errors).
func (c *Config) IsProduction() bool { return c.Env == "production" }
