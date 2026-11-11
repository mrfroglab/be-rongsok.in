package db

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Versi baseline = skema produksi per 2026-11-10 (dulu `prisma db push`).
const baselineVersion = "20261110000000_baseline"

// Kunci advisory supaya dua instance tidak migrasi bersamaan.
const migrateLockID = 727274

// Migrate menerapkan file *.sql di fsys yang belum tercatat di schema_migrations.
//
// Adopsi DB lama otomatis:
//   - Ada tabel _prisma_migrations → semua migrasi yang sukses di sana dianggap
//     sudah diterapkan (nama versinya identik dengan file di sini).
//   - Tidak ada riwayat sama sekali tapi tabel "User" sudah ada (era db push) →
//     baseline dianggap sudah diterapkan.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockID); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrateLockID) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, conn.Conn())
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		if err := adoptLegacyHistory(ctx, conn.Conn()); err != nil {
			return err
		}
		if applied, err = appliedVersions(ctx, conn.Conn()); err != nil {
			return err
		}
	}

	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, f := range files {
		version := strings.TrimSuffix(f, ".sql")
		if applied[version] {
			continue
		}
		sqlBytes, err := fs.ReadFile(fsys, f)
		if err != nil {
			return err
		}
		err = pgx.BeginFunc(ctx, conn.Conn(), func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
			return err
		})
		if err != nil {
			return fmt.Errorf("migrasi %s gagal: %w", version, err)
		}
		slog.Info("migrasi diterapkan", "version", version)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(versions))
	for _, v := range versions {
		m[v] = true
	}
	return m, nil
}

func adoptLegacyHistory(ctx context.Context, conn *pgx.Conn) error {
	var hasPrisma, hasUser bool
	if err := conn.QueryRow(ctx,
		`SELECT to_regclass('_prisma_migrations') IS NOT NULL, to_regclass('"User"') IS NOT NULL`,
	).Scan(&hasPrisma, &hasUser); err != nil {
		return err
	}
	switch {
	case hasPrisma:
		tag, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version)
			SELECT migration_name FROM _prisma_migrations
			WHERE finished_at IS NOT NULL AND rolled_back_at IS NULL
			ON CONFLICT DO NOTHING`)
		if err != nil {
			return err
		}
		slog.Info("riwayat migrasi Prisma diadopsi", "versions", tag.RowsAffected())
	case hasUser:
		if _, err := conn.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", baselineVersion); err != nil {
			return err
		}
		slog.Info("DB lama tanpa riwayat migrasi → baseline ditandai sudah diterapkan")
	}
	return nil
}
