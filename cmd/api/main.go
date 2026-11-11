// Command rongsokin-api: server backend Rongsok.in.
//
//	rongsokin-api               migrasi → seed (bila DB kosong) → jalankan server
//	rongsokin-api migrate       hanya migrasi skema
//	rongsokin-api seed --force  HAPUS semua data lalu isi data demo
//	rongsokin-api healthcheck   cek GET /health (dipakai healthcheck Docker)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrfroglab/be-rongsok.in/internal/app"
	"github.com/mrfroglab/be-rongsok.in/internal/config"
	"github.com/mrfroglab/be-rongsok.in/internal/db"
	"github.com/mrfroglab/be-rongsok.in/internal/seed"
	"github.com/mrfroglab/be-rongsok.in/migrations"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if cmd == "healthcheck" {
		os.Exit(healthcheck())
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("konfigurasi tidak valid", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	switch cmd {
	case "migrate":
		if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
			slog.Error("migrasi gagal", "err", err)
			os.Exit(1)
		}
	case "seed":
		if len(os.Args) < 3 || os.Args[2] != "--force" {
			fmt.Fprintln(os.Stderr, "seed menghapus SEMUA data. Jalankan: rongsokin-api seed --force")
			os.Exit(2)
		}
		if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
			slog.Error("migrasi gagal", "err", err)
			os.Exit(1)
		}
		if err := seed.Force(ctx, pool, cfg.SeedAdminPassword); err != nil {
			slog.Error("seed gagal", "err", err)
			os.Exit(1)
		}
	case "", "serve":
		if err := serve(ctx, cfg, pool); err != nil {
			slog.Error("server berhenti dengan error", "err", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "perintah tidak dikenal: %s\n", cmd)
		os.Exit(2)
	}
}

func serve(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool) error {
	if err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		return fmt.Errorf("migrasi gagal: %w", err)
	}
	if err := seed.IfEmpty(ctx, pool, cfg.SeedAdminPassword); err != nil {
		slog.Error("seeding gagal / dilewati", "err", err)
	}

	a := app.New(cfg, pool)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go a.Sweeper.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server berjalan", "port", cfg.Port, "api", "/api/v1", "docs", "/api-docs")
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	slog.Info("mematikan server…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
