package store

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolEnv returns a getenv over a fixed map; unset keys read as "".
func poolEnv(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func parsePoolTestConfig(t *testing.T) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return cfg
}

func TestConfigurePGPoolDefaults(t *testing.T) {
	cfg := parsePoolTestConfig(t)
	problems := configurePGPool(cfg, poolEnv(nil))
	if len(problems) != 0 {
		t.Fatalf("unset env problems = %v", problems)
	}
	if cfg.MaxConns != 2 {
		t.Fatalf("default MaxConns = %d, want 2", cfg.MaxConns)
	}
	if cfg.MinConns != 0 {
		t.Fatalf("MinConns = %d, want 0 (idle projects hold no connections)", cfg.MinConns)
	}
	if cfg.ConnConfig.ConnectTimeout != 0 {
		t.Fatalf("unset wait must leave ConnectTimeout alone, got %v", cfg.ConnConfig.ConnectTimeout)
	}
}

func TestConfigurePGPoolEnvOverride(t *testing.T) {
	cfg := parsePoolTestConfig(t)
	problems := configurePGPool(cfg, poolEnv(map[string]string{
		"LEANKG_PG_POOL_SIZE":    " 7 ",
		"LEANKG_PG_POOL_WAIT_MS": "5000",
	}))
	if len(problems) != 0 {
		t.Fatalf("valid env problems = %v", problems)
	}
	if cfg.MaxConns != 7 {
		t.Fatalf("MaxConns = %d, want 7", cfg.MaxConns)
	}
	if cfg.MinConns != 0 {
		t.Fatalf("MinConns = %d, want 0", cfg.MinConns)
	}
	if cfg.ConnConfig.ConnectTimeout != 5*time.Second {
		t.Fatalf("ConnectTimeout = %v, want 5s", cfg.ConnConfig.ConnectTimeout)
	}
}

func TestConfigurePGPoolInvalidFallsBackAndReports(t *testing.T) {
	cases := []struct {
		name string
		kv   map[string]string
		want []string
	}{
		{"zero size", map[string]string{"LEANKG_PG_POOL_SIZE": "0"},
			[]string{`invalid LEANKG_PG_POOL_SIZE="0"`}},
		{"non-numeric size", map[string]string{"LEANKG_PG_POOL_SIZE": "zero"},
			[]string{`invalid LEANKG_PG_POOL_SIZE="zero"`}},
		{"non-numeric wait", map[string]string{"LEANKG_PG_POOL_WAIT_MS": "fast"},
			[]string{`invalid LEANKG_PG_POOL_WAIT_MS="fast"`}},
		{"zero wait", map[string]string{"LEANKG_PG_POOL_WAIT_MS": "0"},
			[]string{`invalid LEANKG_PG_POOL_WAIT_MS="0"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parsePoolTestConfig(t)
			problems := configurePGPool(cfg, poolEnv(tc.kv))
			if len(problems) != len(tc.want) {
				t.Fatalf("problems = %v, want %v", problems, tc.want)
			}
			for i := range tc.want {
				if problems[i] != tc.want[i] {
					t.Fatalf("problem[%d] = %q, want %q", i, problems[i], tc.want[i])
				}
			}
			if cfg.MaxConns != 2 {
				t.Fatalf("invalid size must fall back to default 2, got %d", cfg.MaxConns)
			}
			if cfg.ConnConfig.ConnectTimeout != 0 {
				t.Fatalf("invalid wait must not apply, got %v", cfg.ConnConfig.ConnectTimeout)
			}
		})
	}
}
