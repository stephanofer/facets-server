// Package config validates bounded runtime configuration without exposing secrets.
package config

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/stephanofer/facets-server/internal/access"
)

type Config struct {
	Access                                                                           access.Policy
	MailFrom                                                                         string
	Environment, Address, DiagnosticsAddress, DatabaseURL, MigrationURL, WorkerQueue string
	PoolMax                                                                          int32
	RequestTimeout, SQLTimeout, ShutdownTimeout, JobTimeout, Lease                   time.Duration
	MaxBody                                                                          int64
	Workers, QueueLimit                                                              int
}

func Load(get func(string) string) (Config, error) {
	c := Config{Environment: "development", Address: "127.0.0.1:8080", PoolMax: 8, WorkerQueue: "notifications",
		DiagnosticsAddress: "127.0.0.1:9090",
		RequestTimeout:     10 * time.Second, SQLTimeout: 5 * time.Second, ShutdownTimeout: 15 * time.Second,
		JobTimeout: 20 * time.Second, Lease: 30 * time.Second, MaxBody: 1 << 20, Workers: 2, QueueLimit: 10000}
	if s := get("FACETS_ENV"); s != "" {
		c.Environment = s
	}
	if s := get("FACETS_ADDRESS"); s != "" {
		c.Address = s
	}
	if s := get("FACETS_DIAGNOSTICS_ADDRESS"); s != "" {
		c.DiagnosticsAddress = s
	}
	diagnosticHost, _, err := net.SplitHostPort(c.DiagnosticsAddress)
	if err != nil || !net.ParseIP(diagnosticHost).IsLoopback() {
		return Config{}, errors.New("diagnostics must bind a loopback address")
	}
	c.DatabaseURL, c.MigrationURL = get("FACETS_DATABASE_URL"), get("FACETS_MIGRATION_URL")
	if s := get("FACETS_WORKER_QUEUE"); s != "" {
		c.WorkerQueue = s
	}
	if c.WorkerQueue != "notifications" && c.WorkerQueue != "heavy" {
		return Config{}, errors.New("invalid worker queue")
	}
	durations := map[string]*time.Duration{
		"FACETS_REQUEST_TIMEOUT":  &c.RequestTimeout,
		"FACETS_SQL_TIMEOUT":      &c.SQLTimeout,
		"FACETS_SHUTDOWN_TIMEOUT": &c.ShutdownTimeout,
		"FACETS_JOB_TIMEOUT":      &c.JobTimeout,
		"FACETS_JOB_LEASE":        &c.Lease,
	}
	for key, dst := range durations {
		if s := get(key); s != "" {
			d, err := time.ParseDuration(s)
			if err != nil {
				return Config{}, errors.New("invalid duration configuration: " + key)
			}
			*dst = d
		}
	}
	for key, dst := range map[string]*int{"FACETS_WORKERS": &c.Workers, "FACETS_QUEUE_LIMIT": &c.QueueLimit} {
		if s := get(key); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return Config{}, errors.New("invalid integer configuration: " + key)
			}
			*dst = n
		}
	}
	if s := get("FACETS_POOL_MAX"); s != "" {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return Config{}, errors.New("invalid pool configuration")
		}
		c.PoolMax = int32(n)
	}
	if s := get("FACETS_MAX_BODY"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Config{}, errors.New("invalid body limit")
		}
		c.MaxBody = n
	}
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "production" {
		return Config{}, errors.New("invalid environment")
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return Config{}, errors.New("invalid HTTP address")
	}
	invalidPool := c.PoolMax < 2 || c.PoolMax > 100
	invalidWorkers := c.Workers < 1 || c.Workers > 32
	invalidQueue := c.QueueLimit < 1 || c.QueueLimit > 1000000
	invalidBody := c.MaxBody < 1 || c.MaxBody > 16<<20
	if invalidPool || invalidWorkers || invalidQueue || invalidBody {
		return Config{}, errors.New("runtime limit out of range")
	}
	for _, d := range []time.Duration{c.RequestTimeout, c.SQLTimeout, c.ShutdownTimeout, c.JobTimeout, c.Lease} {
		if d < time.Millisecond || d > 5*time.Minute {
			return Config{}, errors.New("runtime timeout out of range")
		}
	}
	if c.Lease < c.JobTimeout+c.SQLTimeout || c.SQLTimeout > c.RequestTimeout {
		return Config{}, errors.New("inconsistent runtime deadlines")
	}
	if c.WorkerQueue == "notifications" && c.Lease < c.JobTimeout+2*c.SQLTimeout {
		return Config{}, errors.New("notification lease must reserve secret cleanup and completion deadlines")
	}
	for _, dsn := range []string{c.DatabaseURL, c.MigrationURL} {
		if dsn == "" {
			continue
		}
		u, err := url.Parse(dsn)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
			return Config{}, errors.New("invalid database URL")
		}
		if c.Environment == "production" && u.Query().Get("sslmode") != "verify-full" {
			return Config{}, errors.New("production database requires sslmode=verify-full")
		}
	}
	if c.Environment == "production" && c.DatabaseURL != "" && c.DatabaseURL == c.MigrationURL {
		return Config{}, errors.New("application and migration credentials must differ")
	}
	c.Access, err = access.LoadPolicy(get, c.Environment)
	if err != nil {
		return Config{}, err
	}
	c.MailFrom = get("FACETS_MAIL_FROM")
	return c, nil
}
