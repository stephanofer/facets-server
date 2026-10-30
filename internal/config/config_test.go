package config_test

import (
	"github.com/stephanofer/facets-server/internal/config"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		env     map[string]string
		invalid bool
	}{
		{"defaults", nil, false},
		{"invalid duration", map[string]string{"FACETS_SQL_TIMEOUT": "forever"}, true},
		{"zero timeout", map[string]string{"FACETS_SQL_TIMEOUT": "0s"}, true},
		{"pool overflow", map[string]string{"FACETS_POOL_MAX": "2147483648"}, true},
		{"unbounded workers", map[string]string{"FACETS_WORKERS": "0"}, true},
		{"public diagnostics", map[string]string{"FACETS_DIAGNOSTICS_ADDRESS": "0.0.0.0:9090"}, true},
		{"lease shorter than execution", map[string]string{"FACETS_JOB_LEASE": "1s"}, true},
		{"insecure production", map[string]string{"FACETS_ENV": "production", "FACETS_DATABASE_URL": "postgres://app@localhost/db?sslmode=disable"}, true},
		{"secure production", map[string]string{"FACETS_ENV": "production", "FACETS_DATABASE_URL": "postgres://app@db/db?sslmode=verify-full"}, false},
		{"invalid URL", map[string]string{"FACETS_DATABASE_URL": "secret-not-a-url"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load(func(k string) string { return tc.env[k] })
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v, invalid = %v", err, tc.invalid)
			}
		})
	}
}
