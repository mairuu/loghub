// Package storetest gives tests a real, migrated database.
//
// Set TEST_DATABASE_URL to a postgres:// URL for a user who may create
// databases and roles; `make test-db` points it at the dev Postgres. Each test
// package gets its own scratch database, dropped when the package's tests end.
// Without the variable, tests that need a database are skipped.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/migrations"
)

const envURL = "TEST_DATABASE_URL"

// appRole matches the role cmd/loghub connects as.
const appRole = "loghub_app"

type DB struct {
	App   *pgxpool.Pool
	Owner *pgxpool.Pool
}

var shared *DB

// Main runs a package's tests against a scratch database. Call it from
// TestMain.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	adminURL := os.Getenv(envURL)
	if adminURL == "" {
		return m.Run()
	}

	ctx := context.Background()
	db, drop, err := create(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storetest: %v\n", err)
		return 1
	}
	defer drop()

	shared = db
	return m.Run()
}

// Open returns the package's database, or skips the test when there is none.
func Open(t testing.TB) *DB {
	t.Helper()
	if shared != nil {
		return shared
	}
	if os.Getenv(envURL) == "" {
		t.Skip(envURL + " is not set; run make test-db")
	}
	t.Fatal("storetest.Main is not called from TestMain")
	return nil
}

// Tenant creates a tenant with a fresh ID, so tests never see each other's
// events.
func (db *DB) Tenant(t testing.TB) string {
	t.Helper()
	id := "t_" + randomHex(6)
	err := store.NewTenantRepo(store.New(db.Owner)).CreateIfMissing(t.Context(), store.NewTenantParams{ID: id, Name: id})
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	return id
}

func create(ctx context.Context, adminURL string) (_ *DB, _ func(), err error) {
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w", envURL, err)
	}

	name := "loghub_test_" + randomHex(4)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		return nil, nil, fmt.Errorf("create database: %w", err)
	}
	db := &DB{}
	drop := func() {
		if db.App != nil {
			db.App.Close()
		}
		if db.Owner != nil {
			db.Owner.Close()
		}
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			fmt.Fprintf(os.Stderr, "storetest: drop database %s: %v\n", name, err)
		}
		admin.Close(ctx)
	}
	defer func() {
		if err != nil {
			drop()
		}
	}()

	// The migrations grant privileges to the app role. The dev role already
	// exists; leave it and its password alone. On a fresh server, another
	// test package may be creating it at the same moment, and the loser of
	// that race gets unique_violation rather than duplicate_object.
	if _, err := admin.Exec(ctx, "CREATE ROLE "+appRole); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || (pgErr.Code != "42710" && pgErr.Code != "23505") {
			return nil, nil, fmt.Errorf("create role: %w", err)
		}
	}

	u, err := url.Parse(adminURL)
	if err != nil {
		return nil, nil, fmt.Errorf("%s must be a postgres:// URL: %w", envURL, err)
	}
	u.Path = "/" + name
	dbURL := u.String()

	goose.SetLogger(goose.NopLogger())
	if err := pg.Migrate(ctx, dbURL, migrations.FS, migrations.Dir); err != nil {
		return nil, nil, err
	}

	if db.Owner, err = pgxpool.New(ctx, dbURL); err != nil {
		return nil, nil, err
	}

	// Connecting as the owner and switching role tests the same privileges
	// and policies as logging in as loghub_app, without its password.
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, nil, err
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+appRole)
		return err
	}
	if db.App, err = pgxpool.NewWithConfig(ctx, cfg); err != nil {
		return nil, nil, err
	}
	return db, drop, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
