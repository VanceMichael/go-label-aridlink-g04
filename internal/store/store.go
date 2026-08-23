package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) DB() DBTX { return s.pool }

func (s *Store) ExecCommitted(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if err := platform.CheckContext(ctx); err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	return tag, translate(err)
}

func (s *Store) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return s.WithTxOptions(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn)
}

func (s *Store) WithTxOptions(ctx context.Context, options pgx.TxOptions, fn func(pgx.Tx) error) error {
	if err := platform.CheckContext(ctx); err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return translate(err)
	}
	committed = true
	return nil
}

func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "40001", "40P01":
			return fmt.Errorf("%w: %s", platform.ErrConflict, pgErr.Message)
		case "23503", "23514", "23502":
			return fmt.Errorf("%w: %s", platform.ErrValidation, pgErr.Message)
		}
	}
	return err
}

func Translate(err error) error { return translate(err) }
