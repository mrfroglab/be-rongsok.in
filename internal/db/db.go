// Package db: koneksi Postgres (pgx pool), helper transaksi & migrasi skema.
package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX dipenuhi *pgxpool.Pool maupun pgx.Tx — query store bisa jalan di
// dalam atau di luar transaksi.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Connect membuat pool. DATABASE_URL gaya Prisma (`?schema=public`) didukung:
// parameter `schema` diubah jadi search_path. Sesi selalu TimeZone=UTC karena
// kolom timestamp (tanpa zona) menyimpan waktu UTC.
func Connect(ctx context.Context, rawURL string) (*pgxpool.Pool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL tidak valid: %w", err)
	}
	q := u.Query()
	schema := q.Get("schema")
	q.Del("schema")
	u.RawQuery = q.Encode()

	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL tidak valid: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	if schema != "" && schema != "public" {
		cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	}
	cfg.MaxConns = 20

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Tunggu DB siap (container DB bisa start belakangan).
	var pingErr error
	for i := 0; i < 30; i++ {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		pingErr = pool.Ping(pctx)
		cancel()
		if pingErr == nil {
			return pool, nil
		}
		slog.Warn("database belum siap, mencoba lagi", "err", pingErr)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	pool.Close()
	return nil, fmt.Errorf("tidak bisa terhubung ke database: %w", pingErr)
}

// WithTx menjalankan fn dalam transaksi; commit kalau fn sukses.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op setelah commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Kode error Postgres yang ditangani khusus.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
)

// IsUniqueViolation: setara Prisma P2002.
func IsUniqueViolation(err error) bool { return hasCode(err, codeUniqueViolation) }

// IsForeignKeyViolation: setara Prisma P2003.
func IsForeignKeyViolation(err error) bool { return hasCode(err, codeForeignKeyViolation) }

func hasCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
