// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright © 2026 D4ND3R and the Contest-Management-System-Enchanced contributors

// Package testutil gives tests real, isolated infrastructure:
//
//   - DB(t) returns a pool on a fresh database cloned from a migrated
//     template, dropped when the test ends;
//   - Redis(t) returns a client whose keys live under a unique prefix,
//     deleted when the test ends.
//
// `make test` starts disposable PostgreSQL and Valkey servers
// (scripts/infra.sh) and exports CMS_TEST_DATABASE_URL and
// CMS_TEST_REDIS_URL. Without them these helpers skip the test, unless
// CMS_TEST_REQUIRE_INFRA=1 (set by make test and CI), where they fail.
package testutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/d4nd3r/contest-management-system-enchanced/internal/config"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/db"
	"github.com/d4nd3r/contest-management-system-enchanced/internal/kv"
)

// Environment variables read by the helpers.
const (
	EnvDatabaseURL  = "CMS_TEST_DATABASE_URL"
	EnvRedisURL     = "CMS_TEST_REDIS_URL"
	EnvRequireInfra = "CMS_TEST_REQUIRE_INFRA"
)

func missing(t testing.TB, what, env string) {
	t.Helper()
	msg := fmt.Sprintf("%s not available: %s is not set (run `make test`, which starts it)", what, env)
	if os.Getenv(EnvRequireInfra) == "1" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func randomSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

// withDBName returns base (a postgres:// URL) pointing at database name.
func withDBName(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("%s must be a postgres:// URL", EnvDatabaseURL)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// ensureTemplate creates (once per test binary, and once per schema
// version across parallel packages) a migrated template database.
func ensureTemplate(ctx context.Context, admin *pgx.Conn) (string, error) {
	templateOnce.Do(func() {
		migs, err := db.Migrations()
		if err != nil {
			templateErr = err
			return
		}
		// The name depends on the migrations, so a changed schema gets a
		// new template and stale ones are never reused.
		h := sha256.New()
		for _, m := range migs {
			h.Write([]byte(m.Checksum))
		}
		name := fmt.Sprintf("cms_tmpl_%d_%x", len(migs), h.Sum(nil)[:6])

		// Serialise template creation across test processes.
		if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(4242)`); err != nil {
			templateErr = err
			return
		}
		defer func() { _, _ = admin.Exec(context.Background(), `SELECT pg_advisory_unlock(4242)`) }()

		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
			templateErr = err
			return
		}
		if !exists {
			building := name + "_building"
			_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+building+` WITH (FORCE)`)
			if _, err := admin.Exec(ctx, `CREATE DATABASE `+building); err != nil {
				templateErr = err
				return
			}
			u, err := withDBName(os.Getenv(EnvDatabaseURL), building)
			if err != nil {
				templateErr = err
				return
			}
			pool, err := db.Open(ctx, config.Database{URL: u, MaxConns: 2})
			if err != nil {
				templateErr = err
				return
			}
			_, err = db.Migrate(ctx, pool, nil)
			pool.Close()
			if err != nil {
				templateErr = fmt.Errorf("migrating the template: %w", err)
				return
			}
			if _, err := admin.Exec(ctx, `ALTER DATABASE `+building+` RENAME TO `+name); err != nil {
				templateErr = err
				return
			}
		}
		templateName = name
	})
	return templateName, templateErr
}

// DB returns a pool on a new migrated database that only this test uses.
func DB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if os.Getenv(EnvDatabaseURL) == "" {
		missing(t, "PostgreSQL", EnvDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, os.Getenv(EnvDatabaseURL))
	if err != nil {
		t.Fatalf("connecting to the test PostgreSQL: %v", err)
	}
	defer admin.Close(context.Background())

	tmpl, err := ensureTemplate(ctx, admin)
	if err != nil {
		t.Fatalf("preparing the template database: %v", err)
	}
	name := "cms_test_" + randomSuffix()
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE `+tmpl); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	u, err := withDBName(os.Getenv(EnvDatabaseURL), name)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, config.Database{URL: u, MaxConns: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropDatabase(t, name)
	})
	return pool
}

// EmptyDB returns a pool on a new database with no migrations applied,
// for tests of the migration runner itself.
func EmptyDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, config.Database{URL: EmptyDatabaseURL(t), MaxConns: 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// EmptyDatabaseURL creates a new empty database, dropped when the test
// ends, and returns its URL (for tests that run whole commands).
func EmptyDatabaseURL(t testing.TB) string {
	t.Helper()
	if os.Getenv(EnvDatabaseURL) == "" {
		missing(t, "PostgreSQL", EnvDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, os.Getenv(EnvDatabaseURL))
	if err != nil {
		t.Fatalf("connecting to the test PostgreSQL: %v", err)
	}
	defer admin.Close(context.Background())
	name := "cms_test_" + randomSuffix()
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := withDBName(os.Getenv(EnvDatabaseURL), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dropDatabase(t, name) })
	return u
}

func dropDatabase(t testing.TB, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, os.Getenv(EnvDatabaseURL))
	if err != nil {
		t.Logf("dropping %s: %v", name, err)
		return
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
		t.Logf("dropping %s: %v", name, err)
	}
}

// RedisURL returns the URL of the test Valkey (tests that run whole
// commands isolate themselves with CMS_REDIS_KEY_PREFIX).
func RedisURL(t testing.TB) string {
	t.Helper()
	u := os.Getenv(EnvRedisURL)
	if u == "" {
		missing(t, "Valkey", EnvRedisURL)
	}
	return u
}

// Redis returns a client whose keys are isolated under a unique prefix.
func Redis(t testing.TB) *kv.Client {
	t.Helper()
	url := os.Getenv(EnvRedisURL)
	if url == "" {
		missing(t, "Valkey", EnvRedisURL)
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("%s: %v", EnvRedisURL, err)
	}
	c := kv.New(redis.NewClient(opt), "test:"+randomSuffix()+":")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("connecting to the test Valkey: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := c.DeletePrefix(ctx); err != nil {
			t.Logf("cleaning Valkey keys: %v", err)
		}
		_ = c.Close()
	})
	return c
}
