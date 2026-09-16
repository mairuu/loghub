package pg

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

type Config struct {
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	ConnectTimeout  time.Duration
}

func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.Internal("database URL is not parseable").Wrapping(err)
	}

	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		pcfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}
	if cfg.ConnectTimeout > 0 {
		pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, errors.Internal("cannot create database pool").Wrapping(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.Unavailable("database_unavailable", "cannot reach the database").Wrapping(err)
	}

	return pool, nil
}

func Check(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return errors.Unavailable("database_unavailable", "cannot reach the database").Wrapping(err)
	}
	return nil
}

// Beginner is a pool, a connection or an open transaction. Begin on a
// transaction starts a savepoint.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTx runs fn inside a transaction, committing on success and rolling back on
// error or panic. Given an open transaction, it runs fn in a savepoint, so a
// failure rolls back only fn's work.
func InTx(ctx context.Context, db Beginner, fn func(pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return Wrap(err, "cannot begin transaction")
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return Wrap(err, "cannot commit transaction")
	}
	return nil
}

// Wrap classifies a database failure.
func Wrap(err error, message string) *errors.Error {
	if unreachable(err) {
		return errors.Unavailable("database_unavailable", "cannot reach the database").
			With("during", message).Wrapping(err)
	}
	return errors.Internal(message).Wrapping(err)
}

func unreachable(err error) bool {
	var connectErr *pgconn.ConnectError
	var netErr net.Error
	if errors.As(err, &connectErr) || errors.As(err, &netErr) || pgconn.Timeout(err) {
		return true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch {
	case strings.HasPrefix(pgErr.Code, "08"): // connection exception
		return true
	case strings.HasPrefix(pgErr.Code, "53"): // insufficient resources: disk full, too many connections
		return true
	case strings.HasPrefix(pgErr.Code, "57P"): // server shutting down or starting up
		return true
	}
	return false
}

// EnsureLoginRole creates role with LOGIN and the given password, or brings an
// existing role in line with them. The connecting user needs CREATEROLE.
func EnsureLoginRole(ctx context.Context, databaseURL, role, password string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return errors.Unavailable("database_unavailable", "cannot reach the database").Wrapping(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// Role statements don't take bind parameters, so let the server quote the values.
	var stmt string
	err = conn.QueryRow(ctx, `
		SELECT format(
		  CASE WHEN EXISTS (SELECT FROM pg_roles WHERE rolname = $1::text)
		    THEN 'ALTER ROLE %I WITH LOGIN PASSWORD %L'
		    ELSE 'CREATE ROLE %I WITH LOGIN PASSWORD %L'
		  END,
		  $1::text, $2::text)`, role, password).Scan(&stmt)
	if err != nil {
		return errors.Internal("cannot build role statement").With("role", role).Wrapping(err)
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		return errors.Internal("cannot create or update database role").With("role", role).Wrapping(err)
	}
	return nil
}

// Migrate applies goose migrations from an embedded filesystem.
func Migrate(ctx context.Context, databaseURL string, fsys fs.FS, dir string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return errors.Internal("cannot open database for migration").Wrapping(err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(fsys)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect("postgres"); err != nil {
		return errors.Internal("cannot set goose dialect").Wrapping(err)
	}
	if err := goose.UpContext(ctx, db, dir); err != nil {
		return errors.Internal("migration failed").Wrapping(err)
	}
	return nil
}

// MigrationStatus prints applied and pending migrations.
func MigrationStatus(ctx context.Context, databaseURL string, fsys fs.FS, dir string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return errors.Internal("cannot open database for migration").Wrapping(err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(fsys)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect("postgres"); err != nil {
		return errors.Internal("cannot set goose dialect").Wrapping(err)
	}
	return goose.StatusContext(ctx, db, dir)
}

var _ = stdlib.GetDefaultDriver
